package mux

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/b-nnett/codex-subscription-router/internal/protocol"
)

const chatGPTWebModelPrefix = "chatgpt-web/"

var errChatGPTWebThread = errors.New("ChatGPT Web chat stays on its ChatGPT Web route")

func isChatGPTWebModel(model string) bool {
	return strings.HasPrefix(strings.TrimSpace(model), chatGPTWebModelPrefix)
}

func modelFromParams(params json.RawMessage) (string, bool) {
	if len(params) == 0 {
		return "", false
	}
	var payload struct {
		Model *string `json:"model"`
	}
	if json.Unmarshal(params, &payload) != nil || payload.Model == nil {
		return "", false
	}
	model := strings.TrimSpace(*payload.Model)
	if model == "" {
		return "", false
	}
	return model, true
}

func requestExplicitlyUsesNativeModel(message protocol.Message) bool {
	model, ok := modelFromParams(message.Params)
	return ok && !isChatGPTWebModel(model)
}

func (m *Multiplexer) rememberThreadModelFamily(threadID string, web bool) {
	if threadID == "" {
		return
	}
	m.threadModelMu.Lock()
	if m.threadModelFamilies == nil {
		m.threadModelFamilies = make(map[string]bool)
	}
	m.threadModelFamilies[threadID] = web
	m.threadModelMu.Unlock()
}

func (m *Multiplexer) knownThreadModelFamily(threadID string) (bool, bool) {
	m.threadModelMu.RLock()
	web, ok := m.threadModelFamilies[threadID]
	m.threadModelMu.RUnlock()
	return web, ok
}

func (m *Multiplexer) requestUsesChatGPTWeb(message protocol.Message, threadID, accountID string) bool {
	if model, ok := modelFromParams(message.Params); ok {
		web := isChatGPTWebModel(model)
		m.rememberThreadModelFamily(threadID, web)
		return web
	}
	return m.threadUsesChatGPTWeb(threadID, accountID)
}

func (m *Multiplexer) threadUsesChatGPTWeb(threadID, accountID string) bool {
	if threadID == "" {
		return false
	}
	if web, ok := m.knownThreadModelFamily(threadID); ok {
		return web
	}
	if accountID == "" {
		if owner, ok := m.store.ThreadOwner(threadID); ok {
			accountID = owner
		} else if controller, ok := m.store.Controller(); ok {
			accountID = controller.ID
		}
	}
	account, ok := m.store.Account(accountID)
	if !ok || account.CodexHome == "" {
		return false
	}
	path, _, _, err := findThreadRolloutForMigration(account.CodexHome, threadID)
	if err != nil {
		return false
	}
	model, err := latestThreadModelFromRollout(path)
	if err != nil || model == "" {
		return false
	}
	web := isChatGPTWebModel(model)
	m.rememberThreadModelFamily(threadID, web)
	return web
}

func latestThreadModelFromRollout(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	model := ""
	for {
		line, readErr := reader.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) != 0 {
			var entry struct {
				Type    string          `json:"type"`
				Payload json.RawMessage `json:"payload"`
			}
			if json.Unmarshal(line, &entry) == nil {
				switch entry.Type {
				case "session_meta", "turn_context":
					var payload struct {
						Model string `json:"model"`
					}
					if json.Unmarshal(entry.Payload, &payload) == nil && strings.TrimSpace(payload.Model) != "" {
						model = strings.TrimSpace(payload.Model)
					}
				case "event_msg":
					var payload struct {
						Type           string `json:"type"`
						ThreadSettings struct {
							Model string `json:"model"`
						} `json:"thread_settings"`
					}
					if json.Unmarshal(entry.Payload, &payload) == nil && payload.Type == "thread_settings_applied" && strings.TrimSpace(payload.ThreadSettings.Model) != "" {
						model = strings.TrimSpace(payload.ThreadSettings.Model)
					}
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return model, nil
}
