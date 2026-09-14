package mux

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/b-nnett/codex-subscription-router/internal/protocol"
	"github.com/b-nnett/codex-subscription-router/internal/state"
)

type requestCall struct {
	method string
	params json.RawMessage
}

type fakeRequester struct {
	calls   []requestCall
	request func(string, json.RawMessage) (protocol.Message, error)
}

func (f *fakeRequester) Request(_ context.Context, method string, params json.RawMessage) (protocol.Message, error) {
	f.calls = append(f.calls, requestCall{method: method, params: append(json.RawMessage(nil), params...)})
	return f.request(method, params)
}

func TestRefreshSubscriptionUsesManagedRefreshAndRejectsSignedOut(t *testing.T) {
	for _, account := range []string{`{"type":"chatgpt","email":"test@example.invalid"}`, `null`, ``} {
		child := &fakeRequester{request: func(method string, params json.RawMessage) (protocol.Message, error) {
			if method != "account/read" || string(params) != `{"refreshToken":true}` {
				t.Fatalf("unexpected refresh request: %s %s", method, params)
			}
			if account == "" {
				return protocol.Message{Result: json.RawMessage(`{}`)}, nil
			}
			return protocol.Message{Result: json.RawMessage(`{"account":` + account + `}`)}, nil
		}}
		err := refreshSignedInSubscription(context.Background(), child)
		if (err == nil) != (account != "null" && account != "") {
			t.Fatalf("unexpected refresh result for %q: %v", account, err)
		}
	}
	child := &fakeRequester{request: func(string, json.RawMessage) (protocol.Message, error) {
		return protocol.Message{}, errors.New("refresh failed")
	}}
	if err := refreshSignedInSubscription(context.Background(), child); err == nil {
		t.Fatal("ignored refresh failure")
	}
}

func TestWithImmediateThreadUnload(t *testing.T) {
	input := []string{"-c", "features.code_mode_host=true", "app-server", "--analytics-default-enabled"}
	want := []string{"-c", "features.code_mode_host=true", "-c", "thread_unload_delay_secs=0", "app-server", "--analytics-default-enabled"}
	if got := withImmediateThreadUnload(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("withImmediateThreadUnload() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(input, []string{"-c", "features.code_mode_host=true", "app-server", "--analytics-default-enabled"}) {
		t.Fatal("withImmediateThreadUnload mutated the caller's arguments")
	}
}

func TestRelativePathInsideAcceptsWindowsExtendedPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics")
	}
	root := `C:\Users\KingG\.codex`
	path := `\\?\C:\Users\KingG\.codex\sessions\2026\09\12\rollout.jsonl`
	want := `sessions\2026\09\12\rollout.jsonl`
	got, err := relativePathInside(root, path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("relativePathInside() = %q, want %q", got, want)
	}
}

func TestRelativePathInsideRejectsWindowsExtendedPathOutsideRoot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics")
	}
	root := `C:\Users\KingG\.codex`
	path := `\\?\C:\Users\KingG\.codex-mux\accounts\other\rollout.jsonl`
	if _, err := relativePathInside(root, path); err == nil {
		t.Fatal("expected extended path outside the source home to be rejected")
	}
}

func TestRelativePathInsideAcceptsWindowsExtendedUNCPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics")
	}
	root := `\\server\share\.codex`
	path := `\\?\UNC\server\share\.codex\sessions\2026\09\12\rollout.jsonl`
	want := `sessions\2026\09\12\rollout.jsonl`
	got, err := relativePathInside(root, path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("relativePathInside() = %q, want %q", got, want)
	}
}

func TestCopyThreadRolloutLineagePreservesPaginatedHistory(t *testing.T) {
	sourceHome := filepath.Join(t.TempDir(), "primary")
	targetHome := filepath.Join(t.TempDir(), "secondary")
	threadID := "01a09619-9a93-7980-8096-80a3541cee68"
	sessionDir := filepath.Join(sourceHome, "sessions", "2026", "09", "12")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(sessionDir, "rollout-2026-09-12T09-50-55-"+threadID+".jsonl")
	currentPath := filepath.Join(sessionDir, "rollout-2026-09-12T12-32-55-"+threadID+"_01a096ad-ed38-7e23-a1bf-76bc62ee5c8a.jsonl")
	baseBytes := []byte(`{"type":"session_meta","payload":{"id":"page-1","session_id":"` + threadID + `"}}` + "\n" + `{"type":"response_item","payload":{"text":"old history"}}` + "\n")
	currentBytes := []byte(`{"type":"session_meta","payload":{"id":"page-2","session_id":"` + threadID + `"}}` + "\n" + `{"type":"turn_context","payload":{"history_mode":"paginated"}}` + "\n")
	for path, data := range map[string][]byte{basePath: baseBytes, currentPath: currentBytes} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	targetPath, err := copyThreadRolloutLineage(sourceHome, targetHome, threadID, currentPath)
	if err != nil {
		t.Fatal(err)
	}
	wantTargetPath := filepath.Join(targetHome, "sessions", "2026", "09", "12", filepath.Base(currentPath))
	if targetPath != wantTargetPath {
		t.Fatalf("target path = %q, want %q", targetPath, wantTargetPath)
	}
	for sourcePath, wantBytes := range map[string][]byte{basePath: baseBytes, currentPath: currentBytes} {
		relative, err := filepath.Rel(sourceHome, sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(targetHome, relative))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, wantBytes) {
			t.Fatalf("copied rollout %q changed bytes", relative)
		}
	}
}

func TestResumeThreadBetweenAccountsUsesTargetLocalPathAndUnloadsStaleTarget(t *testing.T) {
	sourceHome := filepath.Join(t.TempDir(), "primary")
	targetHome := filepath.Join(t.TempDir(), "secondary")
	threadID := "01a09619-9a93-7980-8096-80a3541cee68"
	sourcePath := filepath.Join(sourceHome, "sessions", "2026", "09", "12", "rollout-"+threadID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	rollout := []byte(`{"type":"session_meta","payload":{"id":"` + threadID + `","session_id":"` + threadID + `"}}` + "\n")
	if err := os.WriteFile(sourcePath, rollout, 0o600); err != nil {
		t.Fatal(err)
	}

	source := &fakeRequester{}
	source.request = func(method string, _ json.RawMessage) (protocol.Message, error) {
		switch method {
		case "thread/read":
			result, _ := json.Marshal(map[string]any{"thread": map[string]any{
				"id": threadID, "path": sourcePath, "cwd": `C:\\work`, "modelProvider": "openai",
			}})
			return protocol.Message{Result: result}, nil
		case "thread/loaded/list":
			return protocol.Message{Result: json.RawMessage(`{"data":[]}`)}, nil
		default:
			return protocol.Message{}, errors.New("unexpected source request: " + method)
		}
	}

	targetLoaded := true
	var resumePath string
	target := &fakeRequester{}
	target.request = func(method string, params json.RawMessage) (protocol.Message, error) {
		switch method {
		case "thread/loaded/list":
			if targetLoaded {
				result, _ := json.Marshal(map[string]any{"data": []string{threadID}})
				return protocol.Message{Result: result}, nil
			}
			return protocol.Message{Result: json.RawMessage(`{"data":[]}`)}, nil
		case "thread/unsubscribe":
			targetLoaded = false
			return protocol.Message{Result: json.RawMessage(`{"status":"unsubscribed"}`)}, nil
		case "thread/resume":
			var decoded struct {
				ThreadID string `json:"threadId"`
				Path     string `json:"path"`
			}
			if err := json.Unmarshal(params, &decoded); err != nil {
				return protocol.Message{}, err
			}
			if decoded.ThreadID != threadID {
				return protocol.Message{}, errors.New("wrong thread id")
			}
			resumePath = decoded.Path
			return protocol.Message{Result: json.RawMessage(`{"thread":{"id":"` + threadID + `"}}`)}, nil
		default:
			return protocol.Message{}, errors.New("unexpected target request: " + method)
		}
	}

	if err := resumeThreadBetweenAccounts(context.Background(), threadID, sourceHome, targetHome, source, target); err != nil {
		t.Fatal(err)
	}
	if len(source.calls) != 1 || source.calls[0].method != "thread/read" {
		t.Fatal("successful migration must not wait for source runtime cleanup")
	}
	wantPath := filepath.Join(targetHome, "sessions", "2026", "09", "12", filepath.Base(sourcePath))
	if resumePath != wantPath {
		t.Fatalf("thread/resume path = %q, want target-local %q", resumePath, wantPath)
	}
	got, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, rollout) {
		t.Fatal("target-local rollout differs from source")
	}
	methods := make([]string, 0, len(target.calls))
	for _, call := range target.calls {
		methods = append(methods, call.method)
	}
	wantMethods := []string{"thread/loaded/list", "thread/unsubscribe", "thread/loaded/list", "thread/resume"}
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Fatalf("target requests = %#v, want %#v", methods, wantMethods)
	}
}

func TestResumeThreadBetweenAccountsAcceptsNotSubscribedWhileTargetUnloads(t *testing.T) {
	sourceHome := filepath.Join(t.TempDir(), "primary")
	targetHome := filepath.Join(t.TempDir(), "secondary")
	threadID := "01a09619-9a93-7980-8096-80a3541cee68"
	sourcePath := filepath.Join(sourceHome, "sessions", "2026", "09", "12", "rollout-"+threadID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	rollout := []byte(`{"type":"session_meta","payload":{"id":"` + threadID + `","session_id":"` + threadID + `"}}` + "\n")
	if err := os.WriteFile(sourcePath, rollout, 0o600); err != nil {
		t.Fatal(err)
	}

	source := &fakeRequester{}
	source.request = func(method string, _ json.RawMessage) (protocol.Message, error) {
		switch method {
		case "thread/read":
			result, _ := json.Marshal(map[string]any{"thread": map[string]any{
				"id": threadID, "path": sourcePath, "cwd": `C:\\work`, "modelProvider": "openai",
			}})
			return protocol.Message{Result: result}, nil
		case "thread/loaded/list":
			return protocol.Message{Result: json.RawMessage(`{"data":[]}`)}, nil
		default:
			return protocol.Message{}, errors.New("unexpected source request: " + method)
		}
	}

	loadedListCalls := 0
	attached := false
	var resumePath string
	target := &fakeRequester{}
	target.request = func(method string, params json.RawMessage) (protocol.Message, error) {
		switch method {
		case "thread/loaded/list":
			loadedListCalls++
			if !attached || loadedListCalls <= 2 {
				result, _ := json.Marshal(map[string]any{"data": []string{threadID}})
				return protocol.Message{Result: result}, nil
			}
			return protocol.Message{Result: json.RawMessage(`{"data":[]}`)}, nil
		case "thread/unsubscribe":
			if attached {
				return protocol.Message{Result: json.RawMessage(`{"status":"unsubscribed"}`)}, nil
			}
			return protocol.Message{Result: json.RawMessage(`{"status":"notSubscribed"}`)}, nil
		case "thread/read":
			return protocol.Message{Result: json.RawMessage(`{"thread":{"id":"` + threadID + `","status":{"type":"idle"}}}`)}, nil
		case "thread/resume":
			if !attached {
				attached = true
				return protocol.Message{Result: json.RawMessage(`{}`)}, nil
			}
			var decoded struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(params, &decoded); err != nil {
				return protocol.Message{}, err
			}
			resumePath = decoded.Path
			return protocol.Message{Result: json.RawMessage(`{"thread":{"id":"` + threadID + `"}}`)}, nil
		default:
			return protocol.Message{}, errors.New("unexpected target request: " + method)
		}
	}

	if err := resumeThreadBetweenAccounts(context.Background(), threadID, sourceHome, targetHome, source, target); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(targetHome, "sessions", "2026", "09", "12", filepath.Base(sourcePath))
	if resumePath != wantPath {
		t.Fatalf("thread/resume path = %q, want target-local %q", resumePath, wantPath)
	}
	methods := make([]string, 0, len(target.calls))
	for _, call := range target.calls {
		methods = append(methods, call.method)
	}
	wantMethods := []string{"thread/loaded/list", "thread/unsubscribe", "thread/read", "thread/resume", "thread/unsubscribe", "thread/loaded/list", "thread/loaded/list", "thread/resume"}
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Fatalf("target requests = %#v, want %#v", methods, wantMethods)
	}
}

func TestAttachIdleThreadRejectsActiveAndUnknownState(t *testing.T) {
	for _, status := range []string{"active", "", "notLoaded"} {
		t.Run(status, func(t *testing.T) {
			child := &fakeRequester{request: func(method string, _ json.RawMessage) (protocol.Message, error) {
				if method != "thread/read" {
					t.Fatalf("must not reattach %q thread: %s", status, method)
				}
				result, _ := json.Marshal(map[string]any{"thread": map[string]any{"id": "chat", "status": map[string]any{"type": status}}})
				return protocol.Message{Result: result}, nil
			}}
			if err := attachIdleThread(context.Background(), child, "chat"); err == nil {
				t.Fatal("expected unsafe reattachment to be rejected")
			}
		})
	}
}

func TestMoveThreadToAccountChangesOwnerOnlyAfterSuccessfulResume(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	threadID := "thread-1"
	if err := store.SetThreadOwner(threadID, "primary"); err != nil {
		t.Fatal(err)
	}
	m := &Multiplexer{store: store}

	resumeFailure := func(context.Context, string, string, string) error { return errors.New("resume failed") }
	if err := m.moveThreadToAccountWithResume(context.Background(), threadID, "primary", secondary.ID, resumeFailure); err == nil {
		t.Fatal("expected failed resume to abort migration")
	}
	if owner, _ := store.ThreadOwner(threadID); owner != "primary" {
		t.Fatalf("owner changed after failed resume: %q", owner)
	}

	resumeSuccess := func(context.Context, string, string, string) error { return nil }
	if err := m.moveThreadToAccountWithResume(context.Background(), threadID, "primary", secondary.ID, resumeSuccess); err != nil {
		t.Fatal(err)
	}
	if owner, _ := store.ThreadOwner(threadID); owner != secondary.ID {
		t.Fatalf("owner after successful resume = %q, want %q", owner, secondary.ID)
	}
}

func TestColdChatMigrationRecoversSavedHistory(t *testing.T) {
	sourceHome, targetHome := t.TempDir(), t.TempDir()
	id := "cold-chat"
	dir := filepath.Join(sourceHome, "sessions", "2026", "09", "12")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rollout-01-cold-chat.jsonl", "rollout-02-cold-chat.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"type":"session_meta","payload":{"session_id":"cold-chat","cwd":"work","model_provider":"openai"}}`+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source := &fakeRequester{request: func(method string, _ json.RawMessage) (protocol.Message, error) {
		if method == "thread/read" {
			return protocol.Message{}, errors.New("thread/read: thread not loaded: cold-chat")
		}
		return protocol.Message{Result: json.RawMessage(`{"data":[]}`)}, nil
	}}
	resumed := false
	target := &fakeRequester{request: func(method string, params json.RawMessage) (protocol.Message, error) {
		if method == "thread/loaded/list" {
			return protocol.Message{Result: json.RawMessage(`{"data":[]}`)}, nil
		}
		if method != "thread/resume" {
			t.Fatalf("unexpected request %s", method)
		}
		var p struct {
			Path    string `json:"path"`
			Exclude bool   `json:"excludeTurns"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			t.Fatal(err)
		}
		if filepath.Base(p.Path) != "rollout-02-cold-chat.jsonl" || !p.Exclude {
			t.Fatal("wrong current page or unnecessary history response")
		}
		for _, name := range []string{"rollout-01-cold-chat.jsonl", "rollout-02-cold-chat.jsonl"} {
			if _, err := os.Stat(filepath.Join(targetHome, "sessions", "2026", "09", "12", name)); err != nil {
				t.Fatal(err)
			}
		}
		resumed = true
		return protocol.Message{Result: json.RawMessage(`{}`)}, nil
	}}
	if err := resumeThreadBetweenAccounts(context.Background(), id, sourceHome, targetHome, source, target); err != nil {
		t.Fatal(err)
	}
	if !resumed {
		t.Fatal("cold chat not resumed")
	}
}

func TestResumeCopiedThreadStalePathRecovery(t *testing.T) {
	for _, scenario := range []string{"matching", "different", "loaded", "unrelated"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			expected := filepath.Join(home, "sessions", "new.jsonl")
			resumes := 0
			child := &fakeRequester{request: func(method string, params json.RawMessage) (protocol.Message, error) {
				if method == "thread/loaded/list" {
					if scenario == "loaded" {
						return protocol.Message{Result: json.RawMessage(`{"data":["chat"]}`)}, nil
					}
					return protocol.Message{Result: json.RawMessage(`{"data":[]}`)}, nil
				}
				resumes++
				if resumes == 1 {
					if scenario == "unrelated" {
						return protocol.Message{}, errors.New("authentication failed")
					}
					return protocol.Message{}, errors.New("cannot resume paginated thread chat with stale path")
				}
				var decoded map[string]any
				json.Unmarshal(params, &decoded)
				if _, exists := decoded["path"]; exists {
					t.Fatal("retry retained stale path")
				}
				if decoded["threadId"] != "chat" {
					t.Fatal("retry changed thread ID")
				}
				resolved := expected
				if scenario == "different" {
					resolved = filepath.Join(home, "sessions", "old.jsonl")
				}
				result, _ := json.Marshal(map[string]any{"thread": map[string]any{"id": "chat", "path": resolved}})
				return protocol.Message{Result: result}, nil
			}}
			params, _ := json.Marshal(map[string]any{"threadId": "chat", "path": expected, "excludeTurns": true})
			err := resumeCopiedThread(context.Background(), child, "chat", home, expected, params)
			if (err == nil) != (scenario == "matching") {
				t.Fatalf("unexpected recovery result: %v", err)
			}
			if (scenario == "loaded" || scenario == "unrelated") && resumes != 1 {
				t.Fatal("unsafe retry")
			}
		})
	}
}

func TestCompareRetainedHistoryContent(t *testing.T) {
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	for _, pair := range [][2]string{{"same", "same"}, {"aaaa", "bbbb"}, {"a", "longer"}, {"", ""}} {
		os.WriteFile(a, []byte(pair[0]), 0600)
		os.WriteFile(b, []byte(pair[1]), 0600)
		same, err := sameRegularFileContents(a, b)
		if err != nil || same != (pair[0] == pair[1]) {
			t.Fatalf("comparison failed: %v %v", same, err)
		}
	}
}
