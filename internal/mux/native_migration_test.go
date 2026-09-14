package mux

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/b-nnett/codex-subscription-router/internal/backend"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in integration check: uses disposable homes and never sends turn/start.
func TestNativeMigration(t *testing.T) {
	exe := os.Getenv("CODEX_ROUTER_TEST_EXECUTABLE")
	if exe == "" {
		t.Skip("native app-server not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sourceHome, targetHome := t.TempDir(), t.TempDir()
	inbound := make(chan backend.Inbound, 4096)
	children := make([]*backend.Child, 0, 2)
	for _, home := range []string{sourceHome, targetHome} {
		child, err := backend.Start("test", home, exe, withImmediateThreadUnload([]string{"app-server"}), os.Environ(), inbound)
		if err != nil {
			t.Fatal(err)
		}
		children = append(children, child)
		t.Cleanup(func() { child.Close() })
		if _, err = child.Request(ctx, "initialize", json.RawMessage(`{"clientInfo":{"name":"router-test","version":"1"},"capabilities":{"experimentalApi":true}}`)); err != nil {
			t.Fatal(err)
		}
	}
	source, target := children[0], children[1]
	result, err := source.Request(ctx, "thread/start", json.RawMessage(`{"ephemeral":false}`))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err = json.Unmarshal(result.Result, &created); err != nil {
		t.Fatal(err)
	}
	id := created.Thread.ID
	if id == "" {
		t.Fatal("missing thread id")
	}
	params, _ := json.Marshal(map[string]any{"threadId": id, "objective": "Migration validation only", "status": "paused", "tokenBudget": 100})
	if _, err = source.Request(ctx, "thread/goal/set", params); err != nil {
		t.Fatal(err)
	}
	if err = ensureThreadUnloaded(ctx, source, id); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", databaseURI(filepath.Join(sourceHome, "goals_1.sqlite"), "rw"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE thread_goals SET tokens_used=42,time_used_seconds=17 WHERE thread_id=?", id); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = resumeThreadBetweenAccounts(ctx, id, sourceHome, targetHome, source, target); err != nil {
		t.Fatal(err)
	}
	if err = resumeThreadBetweenAccounts(ctx, id, targetHome, sourceHome, target, source); err != nil {
		t.Fatalf("move back: %v", err)
	}
	if err = ensureThreadUnloaded(ctx, source, id); err != nil {
		t.Fatal(err)
	}
	stateDB, err := sql.Open("sqlite", databaseURI(filepath.Join(sourceHome, "state_5.sqlite"), "rw"))
	if err != nil {
		t.Fatal(err)
	}
	var oldHead string
	if err = stateDB.QueryRow("SELECT rollout_path FROM threads WHERE id=?", id).Scan(&oldHead); err != nil {
		t.Fatal(err)
	}
	newHead := strings.TrimSuffix(oldHead, ".jsonl") + "_01a09eaa-68d3-7750-9d8b-7f542c72bfef.jsonl"
	content, err := os.ReadFile(oldHead)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(newHead, content, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = stateDB.Exec("UPDATE threads SET rollout_path=?,history_mode='paginated' WHERE id=?", newHead, id); err != nil {
		t.Fatal(err)
	}
	stateDB.Close()
	if err = resumeThreadBetweenAccounts(ctx, id, sourceHome, targetHome, source, target); err != nil {
		t.Fatalf("repeat move: %v", err)
	}
	goalParams, _ := json.Marshal(map[string]any{"threadId": id})
	goalResponse, err := target.Request(ctx, "thread/goal/get", goalParams)
	if err != nil {
		t.Fatal(err)
	}
	var restored struct {
		Goal struct {
			Tokens  int `json:"tokensUsed"`
			Seconds int `json:"timeUsedSeconds"`
		} `json:"goal"`
	}
	if err = json.Unmarshal(goalResponse.Result, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Goal.Tokens != 42 || restored.Goal.Seconds != 17 {
		t.Fatalf("native counters lost: %+v", restored)
	}
	status, err := goalStatus(ctx, target, id)
	if err != nil || status != "paused" {
		t.Fatalf("goal not preserved: %s %v", status, err)
	}
}
