package mux

import (
	"context"
	"encoding/json"
	"github.com/b-nnett/codex-subscription-router/internal/protocol"
	"testing"
)

func TestGoalControlRouting(t *testing.T) {
	for _, tc := range []struct {
		params string
		want   bool
	}{
		{`{"objective":"work"}`, true}, {`{"status":"active"}`, true},
		{`{"status":"paused","objective":"work"}`, false}, {`{"status":"complete"}`, false},
		{`{"tokenBudget":100}`, false}, {`{`, false},
	} {
		if got := startsGoal(protocol.Message{Method: "thread/goal/set", Params: json.RawMessage(tc.params)}); got != tc.want {
			t.Fatalf("%s: %v", tc.params, got)
		}
	}
}

func TestGoalSnapshotRestoresCompleteLiveDefinition(t *testing.T) {
	threadID := "goal-chat"
	source := &fakeRequester{request: func(method string, params json.RawMessage) (protocol.Message, error) {
		if method != "thread/goal/get" {
			t.Fatalf("unexpected source request: %s", method)
		}
		var request struct {
			ThreadID string `json:"threadId"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			t.Fatal(err)
		}
		if request.ThreadID != threadID {
			t.Fatalf("thread id = %q, want %q", request.ThreadID, threadID)
		}
		return protocol.Message{Result: json.RawMessage(`{"goal":{"threadId":"goal-chat","objective":"Finish the migration","status":"usageLimited","tokensUsed":42,"timeUsedSeconds":17,"tokenBudget":500,"createdAt":1,"updatedAt":2}}`)}, nil
	}}

	goal, err := readGoalSnapshot(context.Background(), source, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if goal == nil || goal.Objective != "Finish the migration" || goal.Status != "usageLimited" || goal.TokenBudget == nil || *goal.TokenBudget != 500 {
		t.Fatalf("unexpected goal snapshot: %+v", goal)
	}

	target := &fakeRequester{request: func(method string, params json.RawMessage) (protocol.Message, error) {
		if method != "thread/goal/set" {
			t.Fatalf("unexpected target request: %s", method)
		}
		var request struct {
			ThreadID    string `json:"threadId"`
			Objective   string `json:"objective"`
			Status      string `json:"status"`
			TokenBudget *int64 `json:"tokenBudget"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			t.Fatal(err)
		}
		if request.ThreadID != threadID || request.Objective != goal.Objective || request.Status != "active" || request.TokenBudget == nil || *request.TokenBudget != 500 {
			t.Fatalf("incomplete live goal restore: %+v", request)
		}
		return protocol.Message{Result: json.RawMessage(`{"goal":{"threadId":"goal-chat","objective":"Finish the migration","status":"active","tokensUsed":42,"timeUsedSeconds":17,"tokenBudget":500,"createdAt":1,"updatedAt":3}}`)}, nil
	}}
	if err := restoreGoalSnapshot(context.Background(), target, threadID, goal, "active"); err != nil {
		t.Fatal(err)
	}
}
