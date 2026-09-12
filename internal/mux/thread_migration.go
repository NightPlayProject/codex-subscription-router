package mux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/b-nnett/codex-subscription-router/internal/protocol"
)

const sessionMetadataReadLimit = 16 << 20

const (
	threadUnloadPollInterval = 25 * time.Millisecond
	threadUnloadWaitLimit    = 2 * time.Second
)

type appServerRequester interface {
	Request(context.Context, string, json.RawMessage) (protocol.Message, error)
}

func resumeThreadBetweenAccounts(
	ctx context.Context,
	threadID string,
	sourceHome string,
	targetHome string,
	source appServerRequester,
	target appServerRequester,
) error {
	readParams, _ := json.Marshal(map[string]any{"threadId": threadID, "includeTurns": false})
	readResponse, err := source.Request(ctx, "thread/read", readParams)
	if err != nil {
		return fmt.Errorf("read existing chat: %w", err)
	}
	var readResult struct {
		Thread struct {
			ID            string `json:"id"`
			Path          string `json:"path"`
			CWD           string `json:"cwd"`
			ModelProvider string `json:"modelProvider"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(readResponse.Result, &readResult); err != nil {
		return fmt.Errorf("decode existing chat: %w", err)
	}
	if readResult.Thread.ID == "" || readResult.Thread.Path == "" {
		return errors.New("existing chat has no resumable history path")
	}
	if err := ensureThreadUnloaded(ctx, target, threadID); err != nil {
		return fmt.Errorf("prepare target chat: %w", err)
	}
	targetPath, err := copyThreadRolloutLineage(sourceHome, targetHome, threadID, readResult.Thread.Path)
	if err != nil {
		return fmt.Errorf("copy existing chat history: %w", err)
	}
	resumeParams, _ := json.Marshal(map[string]any{
		"threadId":      threadID,
		"history":       nil,
		"path":          targetPath,
		"cwd":           readResult.Thread.CWD,
		"model":         nil,
		"modelProvider": readResult.Thread.ModelProvider,
	})
	if _, err := target.Request(ctx, "thread/resume", resumeParams); err != nil {
		return fmt.Errorf("resume existing chat: %w", err)
	}
	if err := ensureThreadUnloaded(ctx, source, threadID); err != nil {
		return fmt.Errorf("release source chat: %w", err)
	}
	return nil
}

func withImmediateThreadUnload(args []string) []string {
	result := append([]string(nil), args...)
	for index, argument := range result {
		if argument != "app-server" {
			continue
		}
		updated := make([]string, 0, len(result)+2)
		updated = append(updated, result[:index]...)
		updated = append(updated, "-c", "thread_unload_delay_secs=0")
		updated = append(updated, result[index:]...)
		return updated
	}
	return result
}

func ensureThreadUnloaded(ctx context.Context, child appServerRequester, threadID string) error {
	loaded, err := loadedThreadIDs(ctx, child)
	if err != nil {
		return err
	}
	if !containsThreadID(loaded, threadID) {
		return nil
	}
	params, _ := json.Marshal(map[string]any{"threadId": threadID})
	response, err := child.Request(ctx, "thread/unsubscribe", params)
	if err != nil {
		return fmt.Errorf("unsubscribe loaded chat: %w", err)
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return fmt.Errorf("decode unsubscribe response: %w", err)
	}
	switch result.Status {
	case "notLoaded":
		return nil
	case "notSubscribed", "unsubscribed":
		// Both statuses can race the app-server's unload task. The router starts
		// child app-servers with thread_unload_delay_secs=0, so wait for the
		// loaded-thread registry to observe the unload before touching rollout
		// files. Resuming while the old thread is still loaded would rejoin its
		// stale in-memory state instead of rebuilding from the copied history.
	default:
		return fmt.Errorf("loaded chat could not be unsubscribed: %s", result.Status)
	}
	return waitForThreadUnloaded(ctx, child, threadID)
}

func waitForThreadUnloaded(ctx context.Context, child appServerRequester, threadID string) error {
	deadline := time.Now().Add(threadUnloadWaitLimit)
	for {
		loaded, err := loadedThreadIDs(ctx, child)
		if err != nil {
			return err
		}
		if !containsThreadID(loaded, threadID) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return errors.New("chat remained loaded after unsubscribe")
		}
		timer := time.NewTimer(threadUnloadPollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func loadedThreadIDs(ctx context.Context, child appServerRequester) ([]string, error) {
	response, err := child.Request(ctx, "thread/loaded/list", json.RawMessage(`{}`))
	if err != nil {
		return nil, fmt.Errorf("list loaded chats: %w", err)
	}
	var result struct {
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return nil, fmt.Errorf("decode loaded chats: %w", err)
	}
	return result.Data, nil
}

func containsThreadID(ids []string, threadID string) bool {
	for _, id := range ids {
		if id == threadID {
			return true
		}
	}
	return false
}

func copyThreadRolloutLineage(sourceHome, targetHome, threadID, currentPath string) (string, error) {
	if sourceHome == "" || targetHome == "" || threadID == "" || currentPath == "" {
		return "", errors.New("source home, target home, thread id, and rollout path are required")
	}
	currentRelative, err := relativePathInside(sourceHome, currentPath)
	if err != nil {
		return "", fmt.Errorf("validate current rollout path: %w", err)
	}
	if firstPathComponent(currentRelative) != "sessions" {
		return "", errors.New("current rollout path is outside the sessions directory")
	}
	belongs, err := rolloutBelongsToThread(currentPath, threadID)
	if err != nil {
		return "", fmt.Errorf("read current rollout metadata: %w", err)
	}
	if !belongs {
		return "", errors.New("current rollout does not belong to the requested chat")
	}

	sessionsRoot := filepath.Join(sourceHome, "sessions")
	paths := map[string]struct{}{filepath.Clean(currentPath): {}}
	err = filepath.WalkDir(sessionsRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
			return nil
		}
		if !strings.Contains(strings.ToLower(entry.Name()), strings.ToLower(threadID)) {
			return nil
		}
		belongs, metaErr := rolloutBelongsToThread(path, threadID)
		if metaErr != nil {
			return fmt.Errorf("inspect rollout %q: %w", path, metaErr)
		}
		if belongs {
			paths[filepath.Clean(path)] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("discover paginated rollout lineage: %w", err)
	}

	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	for _, sourcePath := range ordered {
		relative, relErr := relativePathInside(sourceHome, sourcePath)
		if relErr != nil {
			return "", relErr
		}
		destinationPath := filepath.Join(targetHome, relative)
		if err := copyRegularFileReplacing(sourcePath, destinationPath); err != nil {
			return "", fmt.Errorf("copy rollout %q: %w", relative, err)
		}
	}
	return filepath.Join(targetHome, currentRelative), nil
}

func rolloutBelongsToThread(path, threadID string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	var meta struct {
		Type    string `json:"type"`
		Payload struct {
			ID        string `json:"id"`
			SessionID string `json:"session_id"`
		} `json:"payload"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, sessionMetadataReadLimit))
	if err := decoder.Decode(&meta); err != nil {
		return false, err
	}
	if meta.Type != "session_meta" {
		return false, nil
	}
	return meta.Payload.ID == threadID || meta.Payload.SessionID == threadID, nil
}

func relativePathInside(root, path string) (string, error) {
	rootForRel := normalizeWindowsExtendedPath(root)
	pathForRel := normalizeWindowsExtendedPath(path)
	rootAbsolute, err := filepath.Abs(rootForRel)
	if err != nil {
		return "", err
	}
	pathAbsolute, err := filepath.Abs(pathForRel)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbsolute, pathAbsolute)
	if err != nil {
		return "", err
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("path %q is outside %q", path, root)
	}
	return relative, nil
}

func normalizeWindowsExtendedPath(path string) string {
	if runtime.GOOS != "windows" {
		return path
	}
	const extendedPrefix = `\\?\`
	const extendedUNCPrefix = `\\?\UNC\`
	if len(path) >= len(extendedUNCPrefix) && strings.EqualFold(path[:len(extendedUNCPrefix)], extendedUNCPrefix) {
		return `\\` + path[len(extendedUNCPrefix):]
	}
	if strings.HasPrefix(path, extendedPrefix) {
		return path[len(extendedPrefix):]
	}
	return path
}

func firstPathComponent(path string) string {
	clean := filepath.Clean(path)
	if separator := strings.IndexRune(clean, os.PathSeparator); separator >= 0 {
		return clean[:separator]
	}
	return clean
}

func copyRegularFileReplacing(sourcePath, destinationPath string) error {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("source rollout is not a regular file")
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
		return err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	temporary, err := os.CreateTemp(filepath.Dir(destinationPath), ".codex-mux-rollout-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := io.Copy(temporary, source); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destinationPath); err != nil {
		if removeErr := os.Remove(destinationPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("replace old rollout: %w", removeErr)
		}
		if renameErr := os.Rename(temporaryPath, destinationPath); renameErr != nil {
			return renameErr
		}
	}
	cleanup = false
	return nil
}
