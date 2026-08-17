package externallease

import (
	"context"
	"errors"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestValidateRequestFencesRunHostConversationAndOperation(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ExternalChatRun{})
	now := time.Now().UTC()
	run := orm.ExternalChatRun{
		ID: "run-1", RequestID: "request-1", ConversationID: "conversation-1", HistoryID: "history-1",
		Provider: "codex", ActorUserID: "user-1", Status: "running", HostID: "host-1", LeaseToken: "lease-1",
		LeaseExpiresAt: timePointer(now.Add(time.Minute)), CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	valid := Request{
		Owner: "user-1", RunID: "run-1", LeaseToken: "lease-1", HostID: "host-1",
		ConversationID: "conversation-1", Operation: OperationCapabilityRead,
	}
	if err := ValidateRequest(context.Background(), db.DB, valid, now); err != nil {
		t.Fatalf("valid run capability rejected: %v", err)
	}
	invalid := []Request{
		func() Request { value := valid; value.Owner = "user-2"; return value }(),
		func() Request { value := valid; value.RunID = "run-2"; return value }(),
		func() Request { value := valid; value.HostID = "host-2"; return value }(),
		func() Request { value := valid; value.ConversationID = "conversation-2"; return value }(),
		func() Request { value := valid; value.Operation = "conversation.write"; return value }(),
		func() Request { value := valid; value.LeaseToken = ""; return value }(),
	}
	for _, request := range invalid {
		if err := ValidateRequest(context.Background(), db.DB, request, now); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid run capability accepted: %#v err=%v", request, err)
		}
	}
	if err := ValidateRequest(context.Background(), db.DB, valid, now.Add(2*time.Minute)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expired run capability accepted: %v", err)
	}
	if err := db.Model(&orm.ExternalChatRun{}).Where("id = ?", run.ID).Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidateRequest(context.Background(), db.DB, valid, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("terminal run capability accepted: %v", err)
	}
	if err := ValidateRequest(context.Background(), db.DB, Request{}, now); err != nil {
		t.Fatalf("ordinary request rejected: %v", err)
	}
}

func timePointer(value time.Time) *time.Time { return &value }
