package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	root      string
	current   string
	data      string
	checked   time.Time
	status    updateStatus
	client    *http.Client
	url       string
	commitURL string
	branchURL string
}

func newUpdateManager() *updateManager {
	m := &updateManager{
		client:    &http.Client{Timeout: 8 * time.Second},
		url:       "https://api.github.com/repos/NightPlayProject/codex-subscription-router/releases/latest",
		commitURL: "https://api.github.com/repos/NightPlayProject/codex-subscription-router/commits/",
		branchURL: "https://api.github.com/repos/NightPlayProject/codex-subscription-router/commits/main",
	}
	root := resolveInstallRoot()
	local := os.Getenv("LOCALAPPDATA")
	if root == "" || local == "" {
		return m
	}
	m.root = root
	m.data = filepath.Join(local, "Codex Subscription Router")
	// build-info.json is the install identity. The routerVersion field is
	// present in current installs, while VERSION/package.json cover older and
	// development layouts that do not have the sidecar.
	raw, err := os.ReadFile(filepath.Join(root, "build-info.json"))
	var info struct {
		Revision      string `json:"revision"`
		RouterVersion string `json:"routerVersion"`
	}
	if err == nil && json.Unmarshal(raw, &info) == nil {
		if revisionPattern.MatchString(info.Revision) {
			m.current = info.Revision
		}
		m.status.Version = strings.TrimSpace(info.RouterVersion)
	}
	if m.status.Version == "" {
		if versionRaw, versionErr := os.ReadFile(filepath.Join(root, "VERSION")); versionErr == nil {
			m.status.Version = strings.TrimSpace(string(versionRaw))
		}
	}
	if m.status.Version == "" {
		if packageRaw, packageErr := os.ReadFile(filepath.Join(root, "package.json")); packageErr == nil {
			var packageInfo struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(packageRaw, &packageInfo) == nil {
				m.status.Version = strings.TrimSpace(packageInfo.Version)
			}
		}
	}
	return m
}

func resolveInstallRoot() string {
	candidates := make([]string, 0, 8)
	if configured := strings.TrimSpace(os.Getenv("CODEX_ROUTER_INSTALL_ROOT")); configured != "" {
		candidates = append(candidates, configured)
	}
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		launcherConfig := filepath.Join(local, "Codex Subscription Router", "Launcher", "install.json")
		if raw, err := os.ReadFile(launcherConfig); err == nil {
			var config struct {
				Destination string `json:"destination"`
			}
			if json.Unmarshal(raw, &config) == nil && strings.TrimSpace(config.Destination) != "" {
				candidates = append(candidates, config.Destination)
			}
		}
	}
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Dir(executable)
		for index := 0; index < 5 && candidate != ""; index++ {
			candidates = append(candidates, candidate)
			next := filepath.Dir(candidate)
			if next == candidate {
				break
			}
			candidate = next
		}
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if candidate == "." {
			continue
		}
		key := strings.ToLower(candidate)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if info, err := os.Stat(filepath.Join(candidate, "build-info.json")); err == nil && !info.IsDir() {
			return candidate
		}
		if info, err := os.Stat(filepath.Join(candidate, "Launch-CodexSubscriptionRouter.ps1")); err == nil && !info.IsDir() {
			return candidate
		}
	}
	// Keep a configured destination even when a legacy install has no metadata;
	// its update check can still queue the latest verified source revision.
	if len(candidates) > 0 && strings.TrimSpace(candidates[0]) != "" {
		return filepath.Clean(candidates[0])
	}
	return ""
}

func (m *updateManager) check(ctx context.Context, force bool) updateStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == "" {
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
					if err == nil {
						release.Target, err = m.resolveReleaseRevision(ctx, release.Target, release.SHA, release.Tag)
					}
					if err == nil {
						m.status.Latest = release.Target
						m.status.Available = m.current == "" || release.Target != m.current
					}
				}
			}
		}
		// A release may not exist yet when a new router build has been pushed.
		// Keep Check for updates useful by falling back to the tracked branch
		// commit instead of leaving installed users stuck on the previous build.
		if m.branchURL != "" && (err != nil || !revisionPattern.MatchString(m.status.Latest)) {
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
							m.status.Available = m.current == "" || branch.SHA != m.current
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

func (m *updateManager) resolveReleaseRevision(ctx context.Context, target, sha, tag string) (string, error) {
	for _, candidate := range []string{target, sha} {
		if revisionPattern.MatchString(candidate) {
			return candidate, nil
		}
	}
	if m.commitURL == "" || strings.TrimSpace(tag) == "" {
		return "", fmt.Errorf("invalid update revision")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.commitURL+url.PathEscape(tag), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Codex-Subscription-Router")
	res, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release revision lookup returned HTTP %d", res.StatusCode)
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1024*1024)).Decode(&commit); err != nil {
		return "", err
	}
	if !revisionPattern.MatchString(commit.SHA) {
		return "", fmt.Errorf("invalid update revision")
	}
	return commit.SHA, nil
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
