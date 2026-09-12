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
	var resumePath string
	target := &fakeRequester{}
	target.request = func(method string, params json.RawMessage) (protocol.Message, error) {
		switch method {
		case "thread/loaded/list":
			loadedListCalls++
			if loadedListCalls <= 2 {
				result, _ := json.Marshal(map[string]any{"data": []string{threadID}})
				return protocol.Message{Result: result}, nil
			}
			return protocol.Message{Result: json.RawMessage(`{"data":[]}`)}, nil
		case "thread/unsubscribe":
			return protocol.Message{Result: json.RawMessage(`{"status":"notSubscribed"}`)}, nil
		case "thread/resume":
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
	wantMethods := []string{"thread/loaded/list", "thread/unsubscribe", "thread/loaded/list", "thread/loaded/list", "thread/resume"}
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Fatalf("target requests = %#v, want %#v", methods, wantMethods)
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
