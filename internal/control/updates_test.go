package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdatesPinRevisionAndCacheChecks(t *testing.T) {
	hits := 0
	revision := strings.Repeat("b", 40)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		json.NewEncoder(w).Encode(map[string]string{"sha": revision})
	}))
	defer remote.Close()
	m := &updateManager{current: strings.Repeat("a", 40), data: t.TempDir(), client: remote.Client(), url: remote.URL}
	if err := m.queue(); err == nil {
		t.Fatal("queued before check")
	}
	if result := m.check(context.Background(), false); !result.Available || !result.Supported {
		t.Fatalf("unexpected status: %+v", result)
	}
	if err := m.queue(); err != nil {
		t.Fatal(err)
	}
	result := m.check(context.Background(), false)
	if !result.Queued || hits != 1 {
		t.Fatalf("not cached/queued: %+v; hits %d", result, hits)
	}
	revision = strings.Repeat("c", 40)
	if fresh := m.check(context.Background(), true); fresh.Latest != revision || hits != 2 {
		t.Fatalf("forced check did not refresh: %+v; hits %d", fresh, hits)
	}
	revision = strings.Repeat("b", 40)
	raw, err := os.ReadFile(filepath.Join(m.data, "update-request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]string
	if json.Unmarshal(raw, &request) != nil || request["revision"] != revision {
		t.Fatalf("invalid pinned update: %s", raw)
	}
}

func TestUpdatesRejectInvalidRevisionAndUnauthenticatedQueue(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"sha":"../../evil"}`)) }))
	defer remote.Close()
	m := &updateManager{current: strings.Repeat("a", 40), data: t.TempDir(), client: remote.Client(), url: remote.URL}
	if result := m.check(context.Background(), false); result.Error == "" || result.Available {
		t.Fatalf("invalid revision accepted: %+v", result)
	}
	if err := m.queue(); err == nil {
		t.Fatal("invalid revision queued")
	}
	s := &Server{token: "secret", updater: m}
	w := httptest.NewRecorder()
	s.updates(w, httptest.NewRequest(http.MethodPost, "/v1/updates", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
}

func TestNewUpdateManagerReadsRouterVersionFromBuildInfo(t *testing.T) {
	root := t.TempDir()
	local := t.TempDir()
	revision := strings.Repeat("d", 40)
	raw := []byte(`{"revision":"` + revision + `","routerVersion":"26.908.9136.0"}`)
	if err := os.WriteFile(filepath.Join(root, "build-info.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_ROUTER_INSTALL_ROOT", root)
	t.Setenv("LOCALAPPDATA", local)

	m := newUpdateManager()
	if m.root != root || m.current != revision {
		t.Fatalf("install identity was not recovered: root=%q current=%q", m.root, m.current)
	}
	if m.status.Version != "26.908.9136.0" {
		t.Fatalf("router version = %q", m.status.Version)
	}
}

func TestUpdatesCanQueueLegacyInstallWithoutBuildInfo(t *testing.T) {
	remoteRevision := strings.Repeat("e", 40)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"target_commitish": remoteRevision})
	}))
	defer remote.Close()
	m := &updateManager{
		root:   t.TempDir(),
		data:   t.TempDir(),
		client: remote.Client(),
		url:    remote.URL,
	}
	result := m.check(context.Background(), true)
	if !result.Supported || !result.Available || result.Latest != remoteRevision {
		t.Fatalf("legacy install was not offered an update: %+v", result)
	}
	if err := m.queue(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.data, "update-request.json")); err != nil {
		t.Fatalf("legacy update was not queued: %v", err)
	}
}

func TestUpdatesResolveReleaseTagWhenGitHubReturnsBranchTarget(t *testing.T) {
	revision := strings.Repeat("f", 40)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			json.NewEncoder(w).Encode(map[string]string{"target_commitish": "main", "tag_name": "v0.1.1"})
		case "/commits/v0.1.1":
			json.NewEncoder(w).Encode(map[string]string{"sha": revision})
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()
	m := &updateManager{
		root:      t.TempDir(),
		current:   strings.Repeat("a", 40),
		data:      t.TempDir(),
		client:    remote.Client(),
		url:       remote.URL + "/release",
		commitURL: remote.URL + "/commits/",
	}
	result := m.check(context.Background(), true)
	if result.Latest != revision || !result.Available || result.Error != "" {
		t.Fatalf("tag revision was not resolved: %+v", result)
	}
}
