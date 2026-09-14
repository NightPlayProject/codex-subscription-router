package mux

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Only thread-scoped state is transferred. Authentication, other threads, and
// global application settings never leave their account home.
func copyThreadDatabases(ctx context.Context, sourceHome, targetHome, threadID, targetPath string) error {
	for _, spec := range []struct {
		name   string
		tables []string
	}{
		{"thread_history_1.sqlite", []string{"thread_turns", "thread_items", "thread_history_projection_state", "thread_realtime_items"}},
		{"goals_1.sqlite", []string{"thread_goals", "thread_goal_continuation_deferrals"}},
		{"state_5.sqlite", []string{"threads", "thread_dynamic_tools", "thread_artifacts"}},
	} {
		source := filepath.Join(sourceHome, spec.name)
		if _, err := os.Stat(source); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		target := filepath.Join(targetHome, spec.name)
		if _, err := os.Stat(target); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := copyThreadDatabase(ctx, source, target, spec.tables, threadID, targetPath); err != nil {
			return fmt.Errorf("transfer %s: %w", spec.name, err)
		}
	}
	return nil
}
func databaseURI(path, mode string) string {
	path, _ = filepath.Abs(path)
	slashPath := filepath.ToSlash(path)
	if !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	u := url.URL{Scheme: "file", Path: slashPath}
	q := u.Query()
	q.Set("mode", mode)
	u.RawQuery = q.Encode()
	return u.String()
}
func copyThreadDatabase(ctx context.Context, source, target string, tables []string, id, head string) error {
	db, err := sql.Open("sqlite", databaseURI(target, "rwc"))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, "PRAGMA busy_timeout=5000"); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, "ATTACH DATABASE ? AS source", databaseURI(source, "ro")); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var tableCount int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM main.sqlite_master WHERE type='table'").Scan(&tableCount); err != nil {
		return err
	}
	if tableCount == 0 {
		rows, err := tx.QueryContext(ctx, "SELECT sql FROM source.sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY CASE type WHEN 'table' THEN 0 ELSE 1 END")
		if err != nil {
			return err
		}
		var definitions []string
		for rows.Next() {
			var definition string
			if err = rows.Scan(&definition); err != nil {
				rows.Close()
				return err
			}
			definitions = append(definitions, definition)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, definition := range definitions {
			if _, err = tx.ExecContext(ctx, definition); err != nil {
				return err
			}
		}
		var migrations int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM source.sqlite_master WHERE name='_sqlx_migrations'").Scan(&migrations); err != nil {
			return err
		}
		if migrations > 0 {
			if _, err = tx.ExecContext(ctx, "INSERT INTO main._sqlx_migrations SELECT * FROM source._sqlx_migrations"); err != nil {
				return err
			}
		}
	}
	for _, table := range tables {
		var sourceSQL, targetSQL string
		err = tx.QueryRowContext(ctx, "SELECT sql FROM source.sqlite_master WHERE type='table' AND name=?", table).Scan(&sourceSQL)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, "SELECT sql FROM main.sqlite_master WHERE type='table' AND name=?", table).Scan(&targetSQL); err != nil {
			return err
		}
		if sourceSQL != targetSQL {
			return fmt.Errorf("incompatible schema for %s", table)
		}
		key := "thread_id"
		if table == "threads" {
			key = "id"
		}
		rows, err := tx.QueryContext(ctx, "SELECT * FROM source."+table+" WHERE "+key+"=?", id)
		if err != nil {
			return err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return err
		}
		var records [][]any
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				rows.Close()
				return err
			}
			for i, col := range columns {
				if table == "threads" && col == "rollout_path" {
					values[i] = head
				}
				// These IDs refer to account-local UI grouping tables.
				if table == "threads" && (col == "project_id" || col == "thread_section_id") {
					values[i] = nil
				}
				// A copied goal must not start itself before router ownership commits.
				if table == "thread_goals" && col == "status" && (values[i] == "active" || values[i] == "usage_limited") {
					values[i] = "paused"
				}
			}
			records = append(records, values)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM main."+table+" WHERE "+key+"=?", id); err != nil {
			return err
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
		quoted := make([]string, len(columns))
		for i, c := range columns {
			quoted[i] = `"` + strings.ReplaceAll(c, `"`, `""`) + `"`
		}
		for _, values := range records {
			if _, err = tx.ExecContext(ctx, "INSERT INTO main."+table+" ("+strings.Join(quoted, ",")+") VALUES ("+marks+")", values...); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
