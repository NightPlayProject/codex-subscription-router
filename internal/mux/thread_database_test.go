package mux

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestThreadDatabaseTransferPreservesCountersAndOtherThreads(t *testing.T) {
	source, target := filepath.Join(t.TempDir(), "source.db"), filepath.Join(t.TempDir(), "target.db")
	schema := `CREATE TABLE thread_goals(thread_id TEXT PRIMARY KEY,status TEXT,tokens_used INTEGER,time_used_seconds INTEGER); CREATE TABLE threads(id TEXT PRIMARY KEY,rollout_path TEXT,project_id TEXT)`
	for _, p := range []string{source, target} {
		db, err := sql.Open("sqlite", p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(schema); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
	src, _ := sql.Open("sqlite", source)
	defer src.Close()
	src.Exec(`INSERT INTO thread_goals VALUES ('chat','usage_limited',1234,567)`)
	src.Exec(`INSERT INTO threads VALUES ('chat','old','source-project')`)
	dst, _ := sql.Open("sqlite", target)
	defer dst.Close()
	dst.Exec(`INSERT INTO thread_goals VALUES ('other','active',99,10)`)
	if err := copyThreadDatabase(context.Background(), source, target, []string{"thread_goals", "threads"}, "chat", "new"); err != nil {
		t.Fatal(err)
	}
	var status string
	var tokens, seconds int
	if err := dst.QueryRow(`SELECT status,tokens_used,time_used_seconds FROM thread_goals WHERE thread_id='chat'`).Scan(&status, &tokens, &seconds); err != nil {
		t.Fatal(err)
	}
	if status != "paused" || tokens != 1234 || seconds != 567 {
		t.Fatalf("state lost: %s %d %d", status, tokens, seconds)
	}
	var path string
	var project any
	dst.QueryRow(`SELECT rollout_path,project_id FROM threads WHERE id='chat'`).Scan(&path, &project)
	if path != "new" || project != nil {
		t.Fatal("path or account-local grouping not remapped")
	}
	dst.QueryRow(`SELECT tokens_used FROM thread_goals WHERE thread_id='other'`).Scan(&tokens)
	if tokens != 99 {
		t.Fatal("other thread changed")
	}
	src.QueryRow(`SELECT status FROM thread_goals WHERE thread_id='chat'`).Scan(&status)
	if status != "usage_limited" {
		t.Fatal("source changed")
	}
}
func TestThreadDatabaseSchemaMismatchRollsBack(t *testing.T) {
	source, target := filepath.Join(t.TempDir(), "source.db"), filepath.Join(t.TempDir(), "target.db")
	for i, p := range []string{source, target} {
		db, _ := sql.Open("sqlite", p)
		db.Exec(`CREATE TABLE thread_goals(thread_id TEXT PRIMARY KEY,status TEXT)`)
		db.Exec(`INSERT INTO thread_goals VALUES ('chat','active')`)
		if i == 0 {
			db.Exec(`CREATE TABLE threads(id TEXT PRIMARY KEY)`)
		} else {
			db.Exec(`CREATE TABLE threads(id TEXT PRIMARY KEY,extra TEXT)`)
		}
		db.Close()
	}
	if err := copyThreadDatabase(context.Background(), source, target, []string{"thread_goals", "threads"}, "chat", "new"); err == nil {
		t.Fatal("accepted incompatible schema")
	}
	dst, _ := sql.Open("sqlite", target)
	defer dst.Close()
	var status string
	dst.QueryRow(`SELECT status FROM thread_goals WHERE thread_id='chat'`).Scan(&status)
	if status != "active" {
		t.Fatal("partial transfer committed")
	}
}
