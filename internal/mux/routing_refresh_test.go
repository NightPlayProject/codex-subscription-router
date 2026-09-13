package mux

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/b-nnett/codex-subscription-router/internal/backend"
	"github.com/b-nnett/codex-subscription-router/internal/protocol"
	"github.com/b-nnett/codex-subscription-router/internal/state"
)

func TestActiveSourceIsNotCopiedOrUnsubscribed(t *testing.T) {
	source := &fakeRequester{request: func(method string, _ json.RawMessage) (protocol.Message, error) {
		if method != "thread/read" {
			t.Fatalf("active source received %s", method)
		}
		return protocol.Message{Result: json.RawMessage(`{"thread":{"id":"chat","path":"unused.jsonl","status":{"type":"active"}}}`)}, nil
	}}
	target := &fakeRequester{request: func(method string, _ json.RawMessage) (protocol.Message, error) {
		t.Fatalf("active chat migration touched target: %s", method)
		return protocol.Message{}, nil
	}}
	err := resumeThreadBetweenAccounts(context.Background(), "chat", t.TempDir(), t.TempDir(), source, target)
	if !errors.Is(err, errChatActive) {
		t.Fatalf("got %v, expected active chat deferral", err)
	}
}

func TestMigrationNotificationsDoNotChangeOwnerOrCloseDesktopChat(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "mux"), filepath.Join(t.TempDir(), "primary"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetThreadOwner("chat", "primary"); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	m := &Multiplexer{store: store, output: &output, migrating: map[string]bool{"chat": true}}
	for _, msg := range []protocol.Message{
		{Method: "thread/started", Params: json.RawMessage(`{"thread":{"id":"chat"}}`)},
		{Method: "thread/closed", Params: json.RawMessage(`{"threadId":"chat"}`)},
		{Method: "thread/status/changed", Params: json.RawMessage(`{"threadId":"chat","status":{"type":"notLoaded"}}`)},
	} {
		m.handleInbound(backend.Inbound{AccountID: "secondary", Message: msg, Raw: []byte("unexpected")})
	}
	if output.Len() != 0 {
		t.Fatal("migration leaked internal lifecycle events")
	}
	if owner, _ := store.ThreadOwner("chat"); owner != "primary" {
		t.Fatal("internal resume changed ownership before migration committed")
	}
}

func TestDiscoveryCannotUndoMigration(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "mux"), filepath.Join(t.TempDir(), "primary"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two", "three"} {
		if err := store.LearnThreadOwner(id, "primary"); err != nil {
			t.Fatal(err)
		}
		if err := store.SetThreadOwner(id, "secondary"); err != nil {
			t.Fatal(err)
		}
		if err := store.LearnThreadOwner(id, "primary"); err != nil {
			t.Fatal(err)
		}
		if owner, _ := store.ThreadOwner(id); owner != "secondary" {
			t.Fatalf("stale discovery moved %s back", id)
		}
	}
	snapshot := store.ThreadOwners()
	snapshot["one"] = "primary"
	if owner, _ := store.ThreadOwner("one"); owner != "secondary" {
		t.Fatal("snapshot mutated store")
	}
}

func TestPreparationIncludesEveryKnownChatAndAutomaticClearsProgress(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "mux"), filepath.Join(t.TempDir(), "primary"))
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.AddAccount("Work")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two", "three"} {
		if err := store.SetThreadOwner(id, "primary"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetPreferredNewThreadAccountID(account.ID); err != nil {
		t.Fatal(err)
	}
	m := &Multiplexer{store: store}
	m.PrepareExistingChats(account.ID)
	deadline := time.Now().Add(time.Second)
	for m.RoutingStatus().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	status := m.RoutingStatus()
	// An unavailable child is deferred, never counted as successfully migrated.
	if status.Running || status.Total != 3 || status.Deferred != 3 || status.Ready != 0 {
		t.Fatalf("incorrect global preparation status: %+v", status)
	}
	m.PrepareExistingChats("")
	if status := m.RoutingStatus(); status != (RoutingStatus{}) {
		t.Fatalf("Automatic retained stale progress: %+v", status)
	}
}
