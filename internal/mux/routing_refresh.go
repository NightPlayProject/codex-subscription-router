package mux

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// RoutingStatus reports preparation separately from message submission.
type RoutingStatus struct {
	Running  bool   `json:"running"`
	Total    int    `json:"total"`
	Ready    int    `json:"ready"`
	Deferred int    `json:"deferred"`
	Failed   int    `json:"failed"`
	Error    string `json:"error,omitempty"`
}

func (m *Multiplexer) RoutingStatus() RoutingStatus {
	m.batchMu.Lock()
	defer m.batchMu.Unlock()
	return m.batchStatus
}

func (m *Multiplexer) PrepareExistingChats(accountID string) {
	owners := m.store.ThreadOwners()
	ids := make([]string, 0, len(owners))
	for id, owner := range owners {
		if owner != accountID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	m.batchMu.Lock()
	m.batchGeneration++
	generation := m.batchGeneration
	m.batchStatus = RoutingStatus{Running: accountID != "" && len(ids) > 0, Total: len(ids)}
	if accountID == "" {
		m.batchStatus = RoutingStatus{}
	}
	m.batchMu.Unlock()
	if accountID == "" {
		return
	}
	go func() {
		for _, id := range ids {
			m.batchMu.Lock()
			current := m.batchGeneration == generation
			m.batchMu.Unlock()
			if !current {
				return
			}
			unlock := m.lockThreadRoute(id)
			if m.store.PreferredNewThreadAccountID() != accountID {
				unlock()
				return
			}
			owner, ok := m.store.ThreadOwner(id)
			deferred := false
			var err error
			if ok && owner != accountID {
				ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
				child, exists := m.child(owner)
				if !exists {
					deferred = true
				} else {
					params, _ := json.Marshal(map[string]any{"threadId": id, "includeTurns": false})
					response, readErr := child.Request(ctx, "thread/read", params)
					var result struct {
						Thread struct {
							Status struct {
								Type string `json:"type"`
							} `json:"status"`
						} `json:"thread"`
					}
					err = readErr
					if err == nil {
						err = json.Unmarshal(response.Result, &result)
					}
					if err == nil {
						switch result.Thread.Status.Type {
						case "idle":
							err = m.moveThreadToAccount(ctx, id, owner, accountID)
						default:
							// Cold chats switch on resume. Do not load an entire
							// history library and start tools just to change routing.
							deferred = true
						}
					}
				}
				cancel()
			}
			unlock()
			m.batchMu.Lock()
			if m.batchGeneration != generation {
				m.batchMu.Unlock()
				return
			}
			if err != nil {
				m.batchStatus.Failed++
				m.batchStatus.Error = err.Error()
			} else if deferred {
				m.batchStatus.Deferred++
			} else {
				m.batchStatus.Ready++
			}
			m.batchMu.Unlock()
			// Yield between chats so account controls and foreground requests stay responsive.
			time.Sleep(time.Millisecond)
		}
		m.batchMu.Lock()
		if m.batchGeneration == generation {
			m.batchStatus.Running = false
		}
		m.batchMu.Unlock()
	}()
}
