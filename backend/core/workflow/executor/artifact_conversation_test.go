package executor

import (
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
)

func TestArtifactPublicationExposesExternalResultsWithoutChatTrace(t *testing.T) {
	for _, host := range []string{"external-agent", "lazymind"} {
		t.Run(host, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{},
				&orm.WorkflowSlotOrder{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
				&orm.WorkflowInputBinding{}, &orm.Conversation{}, &orm.TaskCenterTask{}, &orm.ChatHistory{})
			now := time.Now().UTC()
			session := orm.WorkflowSession{ID: "run", OriginHost: host, ControllerHost: host, OriginRef: "independent-agent",
				WorkflowID: "ppt", CreateUserID: "owner", IntentContext: `{"text":"论文四页总结"}`, CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&session).Error; err != nil {
				t.Fatal(err)
			}
			sink := DBArtifactSink{DB: db.DB}
			attempt := AttemptContext{AttemptID: "attempt", SessionID: session.ID, StepID: "outline", AttemptNo: 1,
				OutputCardinality: map[string]string{"outline": "single"}}
			artifact := Artifact{Slot: "outline", Seq: 1, ContentType: "text", Value: json.RawMessage(`{"text":"# Overview"}`)}
			for range 2 {
				if err := sink.Save(t.Context(), attempt, artifact); err != nil {
					t.Fatal(err)
				}
			}
			var conversations []orm.Conversation
			if err := db.Find(&conversations).Error; err != nil {
				t.Fatal(err)
			}
			if host == "lazymind" {
				if len(conversations) != 0 {
					t.Fatal("native runtime created an external result conversation")
				}
			} else if len(conversations) != 1 || taskcenter.ExternalWorkflowSessionID(conversations[0]) != session.ID {
				t.Fatalf("published artifact did not expose one result conversation: %+v", conversations)
			}
			var count int64
			if err := db.Model(&orm.ChatHistory{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("artifact publication created chat trace: count=%d err=%v", count, err)
			}
			if err := db.First(&session, "id = ?", session.ID).Error; err != nil || session.ConversationID != "" {
				t.Fatalf("publication changed runtime conversation scope: %+v err=%v", session, err)
			}
		})
	}
}
