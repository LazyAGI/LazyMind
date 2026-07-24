package chat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpstreamStreamChunkPreservesToolLimitPending(t *testing.T) {
	pending := &ToolLimitPendingEvent{
		DecisionID:        "decision-1",
		UsedRounds:        21,
		RoundLimit:        21,
		ExpandedMaxRounds: 200,
		TimeoutSeconds:    120,
	}

	chunk := upstreamStreamChunkFromData(LazyChatData{ToolLimitPending: pending})

	if chunk.ToolLimitPending != pending {
		t.Fatalf("tool-limit event was dropped during upstream conversion: %#v", chunk)
	}
}

func TestStreamChatUpstreamForwardsToolLimitPending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(w, `{"code":200,"msg":"success","data":{"tool_limit_pending":{"decision_id":"decision-2","used_rounds":21,"round_limit":21,"expanded_max_rounds":200,"timeout_seconds":120}}}`)
	}))
	defer server.Close()

	stream, err := StreamChatUpstream(context.Background(), server.URL, map[string]any{"query": "test"})
	if err != nil {
		t.Fatalf("start upstream stream: %v", err)
	}
	chunk, ok := <-stream
	if !ok || chunk.ToolLimitPending == nil {
		t.Fatalf("tool-limit event was not forwarded: %#v", chunk)
	}
	if chunk.ToolLimitPending.DecisionID != "decision-2" {
		t.Fatalf("unexpected decision id: %#v", chunk.ToolLimitPending)
	}
}
