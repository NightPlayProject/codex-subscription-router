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
