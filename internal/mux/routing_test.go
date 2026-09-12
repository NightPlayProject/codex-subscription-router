package mux

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/b-nnett/codex-subscription-router/internal/protocol"
	"github.com/b-nnett/codex-subscription-router/internal/state"
)

func TestPreferredThreadAccountAppliesOnlyExplicitEnabledSelection(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	mux := &Multiplexer{store: store}

	if _, ok := mux.preferredThreadAccount("primary"); ok {
		t.Fatal("automatic routing must keep existing thread ownership")
	}
	if err := store.SetPreferredNewThreadAccountID(secondary.ID); err != nil {
		t.Fatal(err)
	}
	target, ok := mux.preferredThreadAccount("primary")
	if !ok || target.ID != secondary.ID {
		t.Fatalf("expected existing thread to target selected subscription, got %#v ok=%v", target, ok)
	}
	if _, ok := mux.preferredThreadAccount(secondary.ID); ok {
		t.Fatal("thread already on selected subscription must not migrate")
	}
	disabled := false
	if _, err := store.UpdateAccount(secondary.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	if _, ok := mux.preferredThreadAccount("primary"); ok {
		t.Fatal("disabled selected subscription must not be a migration target")
	}
}

func TestIsUsageLimitResponseRecognizesStructuredError(t *testing.T) {
	message := protocol.Message{Error: &protocol.RPCError{
		Code:    -32000,
		Message: "turn failed",
		Data:    json.RawMessage(`{"codexErrorInfo":"usage_limit_exceeded"}`),
	}}
	if !isUsageLimitResponse(message) {
		t.Fatal("expected usage-limit error to be recognized")
	}
}

func TestIsUsageLimitResponseIgnoresUnrelatedError(t *testing.T) {
	message := protocol.Message{Error: &protocol.RPCError{
		Code:    -32000,
		Message: "workspace folder is unavailable",
	}}
	if isUsageLimitResponse(message) {
		t.Fatal("unrelated error was misclassified as a usage limit")
	}
}

func TestAllSubscriptionsDepletedUsesActionableMessage(t *testing.T) {
	message := allSubscriptionsDepleted(json.RawMessage(`7`), nil)
	if message.Error == nil || message.Error.Code != -32026 {
		t.Fatalf("unexpected error response: %#v", message)
	}
	if message.Error.Message != "All connected subscriptions are depleted. Add another subscription or wait for usage to reset." {
		t.Fatalf("unexpected depletion message: %q", message.Error.Message)
	}
}

func TestAllSubscriptionsDepletedShowsKnownResetTime(t *testing.T) {
	reset := time.Date(2026, time.August, 16, 10, 30, 0, 0, time.Local).Unix()
	message := allSubscriptionsDepleted(json.RawMessage(`7`), &reset)
	if message.Error == nil {
		t.Fatal("expected an error response")
	}
	want := "All connected subscriptions are depleted. Usage resets on Sunday, 16 August at 10:30 AM."
	if message.Error.Message != want {
		t.Fatalf("unexpected reset message: %q", message.Error.Message)
	}
}
