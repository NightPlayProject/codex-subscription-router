package mux

import (
	"context"
	"errors"
	"sort"
)

// RoutingStatus reports preparation separately from message submission.
type RoutingStatus struct {
	Loading  int    `json:"loading"`
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
	status := m.batchStatus
	m.migrationMu.RLock()
	status.Loading = len(m.migrating)
	m.migrationMu.RUnlock()
	return status
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
		loadedByOwner := make(map[string]map[string]struct{})
		unavailableOwners := make(map[string]struct{})
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
				if _, unavailable := unavailableOwners[owner]; unavailable {
					deferred = true
				} else {
					loaded, cached := loadedByOwner[owner]
					if !cached {
						ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
						child, exists := m.child(owner)
						if !exists {
							unavailableOwners[owner] = struct{}{}
							deferred = true
						} else {
							loadedIDs, listErr := loadedThreadIDs(ctx, child)
							if listErr != nil {
								unavailableOwners[owner] = struct{}{}
								deferred = true
							} else {
								loaded = make(map[string]struct{}, len(loadedIDs))
								for _, loadedID := range loadedIDs {
									loaded[loadedID] = struct{}{}
								}
								loadedByOwner[owner] = loaded
							}
						}
						cancel()
					}
					if !deferred {
						if _, isLoaded := loaded[id]; !isLoaded {
							deferred = true
						} else {
							ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
							err = m.moveThreadToAccount(ctx, id, owner, accountID)
							cancel()
							if errors.Is(err, errChatActive) || errors.Is(err, errThreadUnloading) {
								err = nil
								deferred = true
							}
						}
					}
				}
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
		}
		m.batchMu.Lock()
		if m.batchGeneration == generation {
			m.batchStatus.Running = false
		}
		m.batchMu.Unlock()
	}()
}
