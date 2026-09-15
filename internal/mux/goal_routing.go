package mux

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/b-nnett/codex-subscription-router/internal/protocol"
	"os"
	"path/filepath"
	"time"
)

type threadGoalSnapshot struct {
	Objective   string `json:"objective"`
	Status      string `json:"status"`
	TokenBudget *int64 `json:"tokenBudget"`
}

func readGoalSnapshot(ctx context.Context, child appServerRequester, id string) (*threadGoalSnapshot, error) {
	params, _ := json.Marshal(map[string]any{"threadId": id})
	response, err := child.Request(ctx, "thread/goal/get", params)
	if err != nil {
		return nil, err
	}
	var result struct {
		Goal *threadGoalSnapshot `json:"goal"`
	}
	if err = json.Unmarshal(response.Result, &result); err != nil {
		return nil, err
	}
	return result.Goal, nil
}

func goalStatus(ctx context.Context, child appServerRequester, id string) (string, error) {
	goal, err := readGoalSnapshot(ctx, child, id)
	if err != nil {
		return "", err
	}
	if goal == nil {
		return "", nil
	}
	return goal.Status, nil
}

func setGoalStatus(ctx context.Context, child appServerRequester, id, status string) error {
	params, _ := json.Marshal(map[string]any{"threadId": id, "status": status})
	_, err := child.Request(ctx, "thread/goal/set", params)
	return err
}

func restoreGoalSnapshot(ctx context.Context, child appServerRequester, id string, goal *threadGoalSnapshot, status string) error {
	params, _ := json.Marshal(struct {
		ThreadID    string `json:"threadId"`
		Objective   string `json:"objective"`
		Status      string `json:"status"`
		TokenBudget *int64 `json:"tokenBudget"`
	}{
		ThreadID:    id,
		Objective:   goal.Objective,
		Status:      status,
		TokenBudget: goal.TokenBudget,
	})
	_, err := child.Request(ctx, "thread/goal/set", params)
	return err
}

func (m *Multiplexer) moveThreadWithGoal(ctx context.Context, id, sourceID, targetID string) error {
	return m.moveThreadWithGoalOptions(ctx, id, sourceID, targetID, false)
}

func (m *Multiplexer) moveThreadWithGoalOptions(ctx context.Context, id, sourceID, targetID string, allowChatGPTWebSource bool) error {
	return m.moveThreadWithGoalMigrationOptions(ctx, id, sourceID, targetID, allowChatGPTWebSource, false)
}

func (m *Multiplexer) moveThreadWithGoalMigrationOptions(ctx context.Context, id, sourceID, targetID string, allowChatGPTWebSource, waitForSourceIdle bool) error {
	if sourceID == targetID {
		return nil
	}
	if m.threadUsesChatGPTWeb(id, sourceID) && !allowChatGPTWebSource {
		return errChatGPTWebThread
	}
	source, ok := m.child(sourceID)
	if !ok {
		return fmt.Errorf("source subscription unavailable")
	}
	target, ok := m.child(targetID)
	if !ok {
		return fmt.Errorf("target subscription unavailable")
	}
	account, ok := m.store.Account(sourceID)
	if !ok {
		return fmt.Errorf("source metadata unavailable")
	}
	var goal *threadGoalSnapshot
	if _, err := os.Stat(filepath.Join(account.CodexHome, "goals_1.sqlite")); err == nil {
		var readErr error
		goal, readErr = readGoalSnapshot(ctx, source, id)
		if readErr != nil {
			return readErr
		}
	}
	status := ""
	if goal != nil {
		status = goal.Status
	}
	resumeGoal := status == "active" || status == "usageLimited"
	if status == "active" {
		if err := setGoalStatus(ctx, source, id, "paused"); err != nil {
			return err
		}
		waitForSourceIdle = true
	}
	if status == "usageLimited" {
		waitForSourceIdle = true
	}
	resume := func(ctx context.Context, threadID, sourceAccountID, targetAccountID string) error {
		return m.resumeThreadOnAccountWithOptions(ctx, threadID, sourceAccountID, targetAccountID, allowChatGPTWebSource, waitForSourceIdle)
	}
	err := m.moveThreadToAccountWithResume(ctx, id, sourceID, targetID, resume)
	if err != nil {
		if status == "active" {
			if restoreErr := setGoalStatus(ctx, source, id, "active"); restoreErr != nil {
				return fmt.Errorf("%w; restore source goal: %v", err, restoreErr)
			}
		}
		return err
	}
	if resumeGoal {
		// Copying goals_1.sqlite preserves counters and history, but a running
		// app-server keeps scheduler state in memory. Re-submit the complete goal
		// definition after thread/resume so the target scheduler registers it now
		// instead of only discovering the copied row after a Codex restart.
		if err := restoreGoalSnapshot(ctx, target, id, goal, "active"); err != nil {
			return fmt.Errorf("chat moved but goal remains paused: %w", err)
		}
	}
	return nil
}

// Goal continuations originate inside app-server and do not send turn/start.
func (m *Multiplexer) routeLimitedGoal(accountID, id string) bool {
	if m.threadUsesChatGPTWeb(id, accountID) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()
	unlock := m.lockThreadRoute(id)
	defer unlock()
	if owner, ok := m.store.ThreadOwner(id); !ok || owner != accountID {
		return false
	}
	child, ok := m.child(accountID)
	if !ok {
		return false
	}
	status, err := goalStatus(ctx, child, id)
	if err != nil || status != "usageLimited" {
		return false
	}
	m.migrationMu.Lock()
	if m.goalExclusions == nil {
		m.goalExclusions = make(map[string]map[string]time.Time)
	}
	if m.goalExclusions[id] == nil {
		m.goalExclusions[id] = make(map[string]time.Time)
	}
	m.goalExclusions[id][accountID] = time.Now()
	excluded := make(map[string]struct{})
	for account, at := range m.goalExclusions[id] {
		if time.Since(at) < 5*time.Minute {
			excluded[account] = struct{}{}
		} else {
			delete(m.goalExclusions[id], account)
		}
	}
	m.migrationMu.Unlock()
	fallback, _, err := m.chooseAccountExcluding(ctx, excluded)
	if err == nil {
		err = m.moveThreadToAccount(ctx, id, accountID, fallback.ID)
	}
	if err != nil {
		m.publish(Event{Type: "goal-failover-failed", AccountID: accountID, Message: err.Error()})
		return false
	}
	m.publish(Event{Type: "thread-failed-over", AccountID: fallback.ID, Message: "Goal continued on another subscription", Data: map[string]any{"threadId": id}})
	return true
}

// Apply a manual selection at a turn boundary, including autonomous goal turns.
func (m *Multiplexer) routeGoalAtTurnBoundary(accountID, id string) {
	if id == "" {
		return
	}
	if m.threadUsesChatGPTWeb(id, accountID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()
	unlock := m.lockThreadRoute(id)
	defer unlock()
	if owner, ok := m.store.ThreadOwner(id); !ok || owner != accountID {
		return
	}
	preferred, ok := m.preferredThreadAccount(accountID)
	if !ok {
		if m.store.PreferredNewThreadAccountID() != "" {
			return
		}
		current, err := m.accountSnapshotWithProfile(ctx, accountID, false)
		if err != nil || accountHasCapacity(current) {
			return
		}
		fallback, _, err := m.chooseAccountExcluding(ctx, map[string]struct{}{accountID: {}})
		if err != nil {
			m.publish(Event{Type: "goal-failover-failed", AccountID: accountID, Message: err.Error()})
			return
		}
		preferred = fallback
	}
	snapshot, err := m.accountSnapshotWithProfile(ctx, preferred.ID, false)
	if err != nil || !accountHasCapacity(snapshot) {
		return
	}
	if err = m.moveThreadToAccount(ctx, id, accountID, preferred.ID); err != nil {
		m.publish(Event{Type: "routing-preference-unavailable", AccountID: preferred.ID, Message: err.Error()})
	}
}

func startsGoal(message protocol.Message) bool {
	if message.Method != "thread/goal/set" {
		return false
	}
	var params struct {
		Status    *string `json:"status"`
		Objective *string `json:"objective"`
	}
	if json.Unmarshal(message.Params, &params) != nil {
		return false
	}
	return (params.Status != nil && *params.Status == "active") || (params.Status == nil && params.Objective != nil)
}
