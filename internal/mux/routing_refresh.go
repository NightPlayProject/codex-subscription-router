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

type routingPreparationCandidate struct {
	threadID        string
	sourceAccountID string
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
	m.batchMu.Lock()
	m.batchGeneration++
	generation := m.batchGeneration
	m.batchStatus = RoutingStatus{Running: accountID != ""}
	m.batchMu.Unlock()
	if accountID == "" {
		return
	}
	go func() {
		owners := m.store.ThreadOwners()
		sourceAccounts := make(map[string]struct{})
		for _, owner := range owners {
			if owner != "" && owner != accountID {
				sourceAccounts[owner] = struct{}{}
			}
		}
		sourceIDs := make([]string, 0, len(sourceAccounts))
		for owner := range sourceAccounts {
			sourceIDs = append(sourceIDs, owner)
		}
		sort.Strings(sourceIDs)
		loadedByOwner := make(map[string][]string, len(sourceIDs))
		for _, owner := range sourceIDs {
			if !m.preparationCurrent(generation, accountID) {
				return
			}
			child, exists := m.child(owner)
			if !exists {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
			loadedIDs, err := loadedThreadIDs(ctx, child)
			cancel()
			if err != nil {
				// This pass is only an optimization for open chats. A chat whose
				// source process is unavailable still switches lazily on resume or
				// its next turn, so do not make saved history look like failed work.
				continue
			}
			loadedByOwner[owner] = loadedIDs
		}

		candidates := preparationCandidates(accountID, owners, loadedByOwner, m.threadUsesChatGPTWeb)
		m.batchMu.Lock()
		if m.batchGeneration != generation {
			m.batchMu.Unlock()
			return
		}
		m.batchStatus.Total = len(candidates)
		m.batchStatus.Running = len(candidates) > 0
		m.batchMu.Unlock()
		if len(candidates) == 0 {
			return
		}

		for _, candidate := range candidates {
			if !m.preparationCurrent(generation, accountID) {
				return
			}
			unlock := m.lockThreadRoute(candidate.threadID)
			owner, ok := m.store.ThreadOwner(candidate.threadID)
			deferred := false
			skipped := false
			var err error
			switch {
			case !ok:
				skipped = true
			case owner == accountID:
				// A foreground route may have completed while this background pass
				// was waiting. Treat it as ready without touching the thread again.
			case owner != candidate.sourceAccountID:
				deferred = true
			default:
				ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
				err = m.moveThreadToAccount(ctx, candidate.threadID, owner, accountID)
				cancel()
				if errors.Is(err, errChatGPTWebThread) {
					err = nil
					skipped = true
				} else if errors.Is(err, errChatActive) || errors.Is(err, errThreadUnloading) {
					err = nil
					deferred = true
				}
			}
			unlock()
			m.batchMu.Lock()
			if m.batchGeneration != generation {
				m.batchMu.Unlock()
				return
			}
			if skipped {
				if m.batchStatus.Total > 0 {
					m.batchStatus.Total--
				}
			} else if err != nil {
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

func (m *Multiplexer) preparationCurrent(generation uint64, accountID string) bool {
	m.batchMu.Lock()
	current := m.batchGeneration == generation
	m.batchMu.Unlock()
	return current && m.store.PreferredNewThreadAccountID() == accountID
}

func preparationCandidates(
	accountID string,
	owners map[string]string,
	loadedByOwner map[string][]string,
	isWebThread func(string, string) bool,
) []routingPreparationCandidate {
	seen := make(map[string]struct{})
	candidates := make([]routingPreparationCandidate, 0)
	for sourceAccountID, loadedIDs := range loadedByOwner {
		if sourceAccountID == "" || sourceAccountID == accountID {
			continue
		}
		for _, threadID := range loadedIDs {
			if threadID == "" || owners[threadID] != sourceAccountID {
				continue
			}
			if _, exists := seen[threadID]; exists {
				continue
			}
			if isWebThread(threadID, sourceAccountID) {
				continue
			}
			seen[threadID] = struct{}{}
			candidates = append(candidates, routingPreparationCandidate{threadID: threadID, sourceAccountID: sourceAccountID})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].threadID == candidates[j].threadID {
			return candidates[i].sourceAccountID < candidates[j].sourceAccountID
		}
		return candidates[i].threadID < candidates[j].threadID
	})
	return candidates
}
