package mux

import (
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
