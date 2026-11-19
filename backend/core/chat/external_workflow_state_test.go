package chat

import (
	"context"
	"encoding/json"
	"testing"
)

func TestExternalTerminalProjectionRefreshesWorkflowAfterCommit(t *testing.T) {
	for _, terminal := range []string{"completed", "failed"} {
		t.Run(terminal, func(t *testing.T) {
			ctx := context.Background()
			app, db := newExternalChatTestApplication(t)
			createExternalChatTestRun(t, app, "run-workflow-refresh")
			job, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-1")
			if err != nil || job == nil {
				t.Fatalf("claim: job=%#v err=%v", job, err)
			}
			cache := newRunDecisionTestStore(t)
			if err := projectExternalChatRunCache(ctx, db, cache, "user-1", job.RunID); err != nil {
				t.Fatal(err)
			}
			events, err := cache.LRange(ctx, convEventsKey("conversation-1"), 0, -1)
			if err != nil || len(events) != 0 {
				t.Fatalf("active driver should not notify completion: events=%v err=%v", events, err)
			}
			if _, err := app.appendEvent(ctx, "user-1", job.RunID, "host-1", job.LeaseToken,
				externalChatEvent{EventID: "terminal", Type: terminal}); err != nil {
				t.Fatal(err)
			}
			if err := projectExternalChatRunCache(ctx, db, cache, "user-1", job.RunID); err != nil {
				t.Fatal(err)
			}
			events, err = cache.LRange(ctx, convEventsKey("conversation-1"), 0, -1)
			if err != nil || len(events) != 1 {
				t.Fatalf("terminal must refresh workflow: events=%v err=%v", events, err)
			}
			var notice struct {
				Type    string            `json:"type"`
				Payload map[string]string `json:"payload"`
			}
			if err := json.Unmarshal([]byte(events[0]), &notice); err != nil {
				t.Fatal(err)
			}
			if notice.Type != "workflow_runtime_updated" || notice.Payload["run_id"] != job.RunID ||
				notice.Payload["conversation_id"] != "conversation-1" {
				t.Fatalf("unexpected workflow invalidation: %+v", notice)
			}
		})
	}
}
