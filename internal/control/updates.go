package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

type updateStatus struct {
	Supported bool   `json:"supported"`
	Current   string `json:"current,omitempty"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	Queued    bool   `json:"queued"`
	Error     string `json:"error,omitempty"`
}

type updateManager struct {
	mu      sync.Mutex
	current string
	data    string
	checked time.Time
	status  updateStatus
	client  *http.Client
	url     string
}

func newUpdateManager() *updateManager {
	m := &updateManager{client: &http.Client{Timeout: 8 * time.Second}, url: "https://api.github.com/repos/NightPlayProject/codex-subscription-router/commits/main"}
	root := os.Getenv("CODEX_ROUTER_INSTALL_ROOT")
	local := os.Getenv("LOCALAPPDATA")
	if root == "" {
		if exe, err := os.Executable(); err == nil {
			root = filepath.Clean(filepath.Join(filepath.Dir(exe), "..", ".."))
		}
	}
	if root == "" || local == "" {
		return m
	}
	raw, err := os.ReadFile(filepath.Join(root, "build-info.json"))
	if err != nil {
		return m
	}
	var info struct {
		Revision string `json:"revision"`
	}
	if json.Unmarshal(raw, &info) != nil || !revisionPattern.MatchString(info.Revision) {
		return m
	}
	m.current = info.Revision
	m.data = filepath.Join(local, "Codex Subscription Router")
	return m
}

func (m *updateManager) check(ctx context.Context) updateStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == "" {
		return updateStatus{}
	}
	if m.checked.IsZero() || time.Since(m.checked) >= time.Hour {
		m.checked = time.Now()
		m.status = updateStatus{Supported: true, Current: m.current}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.url, nil)
		if err == nil {
			req.Header.Set("Accept", "application/vnd.github+json")
			req.Header.Set("User-Agent", "Codex-Subscription-Router")
			var res *http.Response
			res, err = m.client.Do(req)
			if err == nil {
				defer res.Body.Close()
				if res.StatusCode != http.StatusOK {
					err = fmt.Errorf("update check returned HTTP %d", res.StatusCode)
				} else {
					var commit struct {
						SHA string `json:"sha"`
					}
					err = json.NewDecoder(io.LimitReader(res.Body, 1024*1024)).Decode(&commit)
					if err == nil && !revisionPattern.MatchString(commit.SHA) {
						err = fmt.Errorf("invalid update revision")
					}
					if err == nil {
						m.status.Latest = commit.SHA
						m.status.Available = commit.SHA != m.current
					}
				}
			}
		}
		if err != nil {
			m.status.Error = err.Error()
			m.checked = time.Now().Add(-55 * time.Minute)
		}
	}
	_, err := os.Stat(filepath.Join(m.data, "update-request.json"))
	m.status.Queued = err == nil
	return m.status
}

func (m *updateManager) queue() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.status.Available || !revisionPattern.MatchString(m.status.Latest) || m.data == "" {
		return fmt.Errorf("check for an available update first")
	}
	if err := os.MkdirAll(m.data, 0700); err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]string{"revision": m.status.Latest})
	// No commands or caller-supplied paths are accepted. The launcher consumes
	// this fixed revision only after all staged application processes exit.
	file := filepath.Join(m.data, "update-request.json")
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		os.Remove(file)
		return err
	}
	return f.Close()
}

func (s *Server) updates(response http.ResponseWriter, request *http.Request) {
	if !s.authorized(request) {
		writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodPost {
		methodNotAllowed(response)
		return
	}
	if request.Method == http.MethodPost {
		if err := s.updater.queue(); err != nil {
			writeJSON(response, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(response, http.StatusOK, s.updater.check(request.Context()))
}
