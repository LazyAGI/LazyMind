package chat

import (
	"context"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/tasktiming"
)

func TestChatTimingPreservesRequestTimeAndSettlesSingleAndDual(t *testing.T) {
	for _, dual := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "dual"}[dual], func(t *testing.T) {
			_, db := newExternalChatTestApplication(t)
			if err := db.AutoMigrate(&orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{}, &orm.MultiAnswersChatHistory{}); err != nil {
				t.Fatal(err)
			}
			start := time.Now().UTC().Add(-time.Minute)
			ctx := tasktiming.WithRequestTime(context.Background(), start)
			if err := beginChatTiming(ctx, db, "conversation-1", "history", "round"); err != nil {
				t.Fatal(err)
			}
			root, err := tasktiming.Snapshot(ctx, db, "user-1", "conversation-1", "history", start.Add(10*time.Second))
			if err != nil || root == nil || *root.Timing.ElapsedMS != 10000 {
				t.Fatalf("lost request time: %+v %v", root, err)
			}
			if dual {
				err = db.Create(&orm.MultiAnswersChatHistory{ID: "history", ConversationID: "conversation-1", RunID: "round", RunStatus: "completed"}).Error
			} else {
				err = db.Create(&orm.ChatHistory{ID: "history", ConversationID: "conversation-1", RunID: "round", RunStatus: "completed"}).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			settleChatTiming(ctx, db, "conversation-1", "history", "round", dual)
			root, err = tasktiming.Snapshot(ctx, db, "user-1", "conversation-1", "history", time.Now().UTC().Add(time.Hour))
			if err != nil || root.Status != "completed" || root.Timing.FinishedAt == nil || *root.Timing.ElapsedMS > 70000 {
				t.Fatalf("unsettled run: %+v %v", root, err)
			}
		})
	}
}
