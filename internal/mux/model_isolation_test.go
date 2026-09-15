package mux

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/b-nnett/codex-subscription-router/internal/backend"
	"github.com/b-nnett/codex-subscription-router/internal/protocol"
	"github.com/b-nnett/codex-subscription-router/internal/state"
)

func TestExplicitModelSelectionSwitchesRoutingFamiliesIndependently(t *testing.T) {
	m := &Multiplexer{threadModelFamilies: make(map[string]bool)}
	threadID := "chat"

	web := protocol.Message{Params: json.RawMessage(`{"threadId":"chat","model":"chatgpt-web/high"}`)}
	if !m.requestUsesChatGPTWeb(web, threadID, "primary") {
		t.Fatal("ChatGPT Web model was not kept on the Web route")
	}
	if cached, ok := m.knownThreadModelFamily(threadID); !ok || !cached {
		t.Fatal("Web model family was not cached for the thread")
	}

	native := protocol.Message{Params: json.RawMessage(`{"threadId":"chat","model":"gpt-5.6-codex"}`)}
	if m.requestUsesChatGPTWeb(native, threadID, "primary") {
		t.Fatal("native subscription model was incorrectly routed to ChatGPT Web")
	}
	if cached, ok := m.knownThreadModelFamily(threadID); !ok || cached {
		t.Fatal("explicit native selection did not move the thread back to native routing")
	}
}

func TestAutomaticTurnWithoutModelUsesNativeSubscriptionModel(t *testing.T) {
	message := protocol.Message{
		Method: "turn/start",
		Params: json.RawMessage(`{"threadId":"chat"}`),
	}
	updated := ensureAutomaticSubscriptionModel(message)
	model, ok := modelFromParams(updated.Params)
	if !ok || model != automaticSubscriptionModel {
		t.Fatalf("automatic model = %q, want %q", model, automaticSubscriptionModel)
	}
}

func TestExplicitWebTurnKeepsWebModel(t *testing.T) {
	message := protocol.Message{
		Method: "turn/start",
		Params: json.RawMessage(`{"threadId":"chat","model":"chatgpt-web/high"}`),
	}
	updated := ensureAutomaticSubscriptionModel(message)
	model, ok := modelFromParams(updated.Params)
	if !ok || model != "chatgpt-web/high" {
		t.Fatalf("explicit Web model changed to %q", model)
	}
}

func TestLatestThreadModelFromRolloutReadsPersistedWebSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := []byte(
		`{"type":"session_meta","payload":{"model":"gpt-5.6-codex"}}` + "\n" +
			`{"type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"model":"chatgpt-web/high"}}}` + "\n",
	)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	model, err := latestThreadModelFromRollout(path)
	if err != nil {
		t.Fatal(err)
	}
	if model != "chatgpt-web/high" {
		t.Fatalf("persisted model = %q, want chatgpt-web/high", model)
	}
}

func TestWebThreadMigrationIsRejectedBeforeTargetAccess(t *testing.T) {
	threadID := "web-chat"
	sourcePath := filepath.Join(t.TempDir(), "rollout.jsonl")
	source := &fakeRequester{request: func(method string, _ json.RawMessage) (protocol.Message, error) {
		if method != "thread/read" {
			t.Fatalf("unexpected source request: %s", method)
		}
		result, _ := json.Marshal(map[string]any{"thread": map[string]any{
			"id": threadID, "path": sourcePath, "model": "chatgpt-web/high", "status": map[string]any{"type": "idle"},
		}})
		return protocol.Message{Result: result}, nil
	}}
	target := &fakeRequester{request: func(method string, _ json.RawMessage) (protocol.Message, error) {
		t.Fatalf("Web thread migration reached target request %s", method)
		return protocol.Message{}, nil
	}}

	err := resumeThreadBetweenAccounts(context.Background(), threadID, t.TempDir(), t.TempDir(), source, target)
	if !errors.Is(err, errChatGPTWebThread) {
		t.Fatalf("migration error = %v, want errChatGPTWebThread", err)
	}
}

func TestExplicitNativeModelCanMoveWebHistoryIntoNativeSubscription(t *testing.T) {
	sourceHome := filepath.Join(t.TempDir(), "primary")
	targetHome := filepath.Join(t.TempDir(), "secondary")
	threadID := "web-to-native-chat"
	sourcePath := filepath.Join(sourceHome, "sessions", "2026", "09", "14", "rollout-"+threadID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	rollout := []byte(`{"type":"session_meta","payload":{"id":"` + threadID + `","session_id":"` + threadID + `","model":"chatgpt-web/high"}}` + "\n")
	if err := os.WriteFile(sourcePath, rollout, 0o600); err != nil {
		t.Fatal(err)
	}

	nativeRequest := protocol.Message{Params: json.RawMessage(`{"threadId":"web-to-native-chat","model":"gpt-5.6-codex"}`)}
	if !requestExplicitlyUsesNativeModel(nativeRequest) {
		t.Fatal("explicit native model was not recognized as a native transition")
	}
	webRequest := protocol.Message{Params: json.RawMessage(`{"threadId":"web-to-native-chat","model":"chatgpt-web/high"}`)}
	if requestExplicitlyUsesNativeModel(webRequest) {
		t.Fatal("ChatGPT Web model was incorrectly recognized as a native transition")
	}

	source := &fakeRequester{request: func(method string, _ json.RawMessage) (protocol.Message, error) {
		if method != "thread/read" {
			return protocol.Message{}, errors.New("unexpected source request: " + method)
		}
		result, _ := json.Marshal(map[string]any{"thread": map[string]any{
			"id": threadID, "path": sourcePath, "cwd": `C:\\work`, "model": "chatgpt-web/high", "modelProvider": "openai", "status": map[string]any{"type": "idle"},
		}})
		return protocol.Message{Result: result}, nil
	}}
	resumed := false
	target := &fakeRequester{request: func(method string, _ json.RawMessage) (protocol.Message, error) {
		switch method {
		case "thread/loaded/list":
			return protocol.Message{Result: json.RawMessage(`{"data":[]}`)}, nil
		case "thread/resume":
			resumed = true
			return protocol.Message{Result: json.RawMessage(`{"thread":{"id":"web-to-native-chat"}}`)}, nil
		default:
			return protocol.Message{}, errors.New("unexpected target request: " + method)
		}
	}}

	err := resumeThreadBetweenAccountsWithOptions(
		context.Background(),
		threadID,
		sourceHome,
		targetHome,
		source,
		target,
		threadMigrationOptions{allowChatGPTWebSource: true},
	)
	if err != nil {
		t.Fatalf("explicit native transition failed: %v", err)
	}
	if !resumed {
		t.Fatal("explicit native transition never resumed the copied chat on the native subscription")
	}
}

func TestWebGoalRoutingNeverMovesSubscription(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	threadID := "web-goal"
	if err := store.SetThreadOwner(threadID, "primary"); err != nil {
		t.Fatal(err)
	}
	m := &Multiplexer{
		store:               store,
		threadModelFamilies: map[string]bool{threadID: true},
	}

	if err := m.moveThreadWithGoal(context.Background(), threadID, "primary", secondary.ID); !errors.Is(err, errChatGPTWebThread) {
		t.Fatalf("goal migration error = %v, want errChatGPTWebThread", err)
	}
	if m.routeLimitedGoal("primary", threadID) {
		t.Fatal("Web goal entered native automatic failover")
	}
	if owner, _ := store.ThreadOwner(threadID); owner != "primary" {
		t.Fatalf("Web goal owner changed to %q", owner)
	}
}

func TestWebUsageLimitResponseDoesNotEnterNativeFailover(t *testing.T) {
	var output bytes.Buffer
	id := json.RawMessage(`7`)
	key := protocol.RequestIDKey(id)
	m := &Multiplexer{
		output: &output,
		externalRoutes: map[string]externalRoute{
			key: {
				accountID:  "primary",
				method:     "turn/start",
				message:    protocol.Message{ID: id, Method: "turn/start", Params: json.RawMessage(`{"threadId":"web-chat","model":"chatgpt-web/high"}`)},
				chatGPTWeb: true,
			},
		},
	}
	response := protocol.Message{ID: id, Error: &protocol.RPCError{
		Code: -32000, Message: "usage limit", Data: json.RawMessage(`{"codexErrorInfo":"usage_limit_exceeded"}`),
	}}
	raw, _ := json.Marshal(response)
	m.handleInbound(backend.Inbound{AccountID: "primary", Message: response, Raw: raw})

	if output.Len() == 0 {
		t.Fatal("Web usage-limit response was swallowed by native failover")
	}
	if _, ok := m.externalRoutes[key]; ok {
		t.Fatal("completed Web request route was not cleared")
	}
}
