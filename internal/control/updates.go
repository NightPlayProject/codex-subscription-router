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
	Version   string `json:"version,omitempty"`
	Current   string `json:"current,omitempty"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	Queued    bool   `json:"queued"`
	Error     string `json:"error,omitempty"`
}
type updateManager struct {
	mu        sync.Mutex
	current   string
	data      string
	checked   time.Time
	status    updateStatus
	client    *http.Client
	url       string
	branchURL string
}

func newUpdateManager() *updateManager {
	m := &updateManager{
		client:    &http.Client{Timeout: 8 * time.Second},
		url:       "https://api.github.com/repos/NightPlayProject/codex-subscription-router/releases/latest",
		branchURL: "https://api.github.com/repos/NightPlayProject/codex-subscription-router/commits/main",
	}
	root := os.Getenv("CODEX_ROUTER_INSTALL_ROOT")
	local := os.Getenv("LOCALAPPDATA")
	// Keep the visible version available even when build-info.json is missing.
	// Older installations can have a valid staged VERSION file before the
	// revision metadata migration has completed.
	if root == "" {
		if exe, err := os.Executable(); err == nil {
			root = filepath.Clean(filepath.Join(filepath.Dir(exe), "..", ".."))
		}
	}
	if m.status.Version == "" {
		// Development checkouts and older staged installs do not always carry the
		// VERSION sidecar. Keep the UI useful by exposing the application version
		// from the bundled package metadata path when available.
		if packageRaw, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
			var packageInfo struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(packageRaw, &packageInfo) == nil {
				m.status.Version = packageInfo.Version
			}
		}
	}
	if root != "" {
		if versionRaw, err := os.ReadFile(filepath.Join(root, "VERSION")); err == nil {
			m.status.Version = string(versionRaw)
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
func (m *updateManager) check(ctx context.Context, force bool) updateStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == "" {
		return updateStatus{}
	}
	if force || m.checked.IsZero() || time.Since(m.checked) >= time.Hour {
		m.checked = time.Now()
		m.status = updateStatus{Supported: true, Current: m.current, Version: m.status.Version}
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
					var release struct {
						Target string `json:"target_commitish"`
						SHA    string `json:"sha"`
						Tag    string `json:"tag_name"`
					}
					err = json.NewDecoder(io.LimitReader(res.Body, 1024*1024)).Decode(&release)
					if release.Target == "" {
						release.Target = release.SHA
					}
					if err == nil && !revisionPattern.MatchString(release.Target) {
						err = fmt.Errorf("invalid update revision")
					}
					if err == nil {
						m.status.Latest = release.Target
						m.status.Available = release.Target != m.current
					}
				}
			}
		}
		// A release may not exist yet when a new router build has been pushed.
		// Keep Check for updates useful by falling back to the tracked branch
		// commit instead of leaving installed users stuck on the previous build.
		if err != nil || !revisionPattern.MatchString(m.status.Latest) {
			req, branchErr := http.NewRequestWithContext(ctx, http.MethodGet, m.branchURL, nil)
			if branchErr == nil {
				req.Header.Set("Accept", "application/vnd.github+json")
				req.Header.Set("User-Agent", "Codex-Subscription-Router")
				res, branchErr := m.client.Do(req)
				if branchErr == nil {
					defer res.Body.Close()
					if res.StatusCode == http.StatusOK {
						var branch struct {
							SHA string `json:"sha"`
						}
						branchErr = json.NewDecoder(io.LimitReader(res.Body, 1024*1024)).Decode(&branch)
						if branchErr == nil && revisionPattern.MatchString(branch.SHA) {
							m.status.Latest = branch.SHA
							m.status.Available = branch.SHA != m.current
							err = nil
						}
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
	writeJSON(response, http.StatusOK, s.updater.check(request.Context(), request.Method == http.MethodGet))
}
