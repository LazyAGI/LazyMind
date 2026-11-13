package taskcenter

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestExternalWorkflowConversationRecoveryIsOwnerScopedAndPreservesRuntime(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.TaskCenterTask{}, &orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}, &orm.ChatHistory{})
	now := time.Now().UTC().Add(-time.Hour)
	for _, scenario := range []string{"one-page", "four-page", "foreign", "native", "linked", "dismissed", "no-output", "removed-task"} {
		session := orm.WorkflowSession{ID: scenario, OriginHost: "external-agent", ControllerHost: "external-agent",
			OriginRef: "third-party-worker", WorkflowID: "ppt", IntentContext: `{"text":"论文总结\nsource.pdf"}`,
			CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}
		switch scenario {
		case "foreign":
			session.CreateUserID = "other-owner"
		case "native":
			session.OriginHost, session.ControllerHost = "lazymind", "lazymind"
		case "linked":
			session.ConversationID = "existing-conversation"
			if err := db.Create(&orm.Conversation{ID: session.ConversationID, DisplayName: "论文总结", BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
				t.Fatal(err)
			}
		case "dismissed":
			session.Dismissed = true
		}
		if err := db.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
		if scenario != "no-output" {
			if err := db.Create(&orm.WorkflowSlotRevision{ID: scenario + "-output", SessionID: scenario,
				SlotID: "outline", Slot: "outline", Revision: 1, Selected: true, Validity: "effective", CreatedAt: now.Add(time.Minute)}).Error; err != nil {
				t.Fatal(err)
			}
		}
		if scenario == "removed-task" {
			if err := db.Create(&orm.TaskCenterTask{ID: "removed", UserID: "owner", WorkflowSessionID: &session.ID,
				TaskType: "workflow_run", ArchivedAt: &now, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	for range 2 {
		if err := RecoverExternalWorkflowConversations(t.Context(), db.DB, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	var conversations []orm.Conversation
	if err := db.Where("source_type = ?", ExternalWorkflowConversationSource).Find(&conversations).Error; err != nil || len(conversations) != 3 {
		t.Fatalf("projections=%+v error=%v", conversations, err)
	}
	for _, conversation := range conversations {
		sessionID := ExternalWorkflowSessionID(conversation)
		if sessionID != "one-page" && sessionID != "four-page" && sessionID != "linked" {
			t.Fatalf("unexpected projected session %q", sessionID)
		}
		if conversation.DisplayName != "论文总结" || conversation.Model != "" || string(conversation.Models) != "[]" ||
			conversation.ChatModelID != nil || conversation.ChatTimes != 0 || conversation.IsTaskConv || conversation.IsEphemeral {
			t.Fatalf("result conversation started chat or lost request title: %+v", conversation)
		}
		var session orm.WorkflowSession
		if err := db.First(&session, "id = ?", sessionID).Error; err != nil || (sessionID == "linked" && session.ConversationID != "existing-conversation" || sessionID != "linked" && session.ConversationID != "") || session.OriginRef != "third-party-worker" {
			t.Fatalf("runtime identity changed: %+v error=%v", session, err)
		}
		var task orm.TaskCenterTask
		if err := db.Where("plugin_session_id = ?", sessionID).Take(&task).Error; err != nil || task.ConversationID != conversation.ID {
			t.Fatalf("missing task presentation link: %+v error=%v", task, err)
		}
	}
	var historyCount int64
	if err := db.Model(&orm.ChatHistory{}).Count(&historyCount).Error; err != nil || historyCount != 0 {
		t.Fatalf("copied traces into chat: count=%d err=%v", historyCount, err)
	}
}

func TestExternalWorkflowConversationDoesNotReviveUserHiddenResults(t *testing.T) {
	for _, lifecycle := range []string{"renamed", "archived", "trashed", "purged", "bound-renamed", "bound-archived", "bound-trashed", "bound-purged"} {
		t.Run(lifecycle, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.TaskCenterTask{}, &orm.WorkflowSession{}, &orm.WorkflowSlotRevision{})
			now := time.Now().UTC().Add(-time.Hour).Truncate(time.Second).Add(123456789 * time.Nanosecond)
			session := orm.WorkflowSession{ID: "run", CreateUserID: "owner", OriginHost: "external-agent", ControllerHost: "external-agent",
				WorkflowID: "ppt", CreatedAt: now, UpdatedAt: now}
			if strings.HasPrefix(lifecycle, "bound-") {
				session.ConversationID = "native-conversation"
				if err := db.Create(&orm.Conversation{ID: session.ConversationID, BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
					t.Fatal(err)
				}
				lifecycle = strings.TrimPrefix(lifecycle, "bound-")
			}
			if err := db.Create(&session).Error; err != nil {
				t.Fatal(err)
			}
			publish := func(at time.Time) error {
				return db.Transaction(func(tx *gorm.DB) error { return EnsureExternalWorkflowConversation(t.Context(), tx, session, at) })
			}
			if err := publish(now); err != nil {
				t.Fatal(err)
			}
			var conversation orm.Conversation
			if err := db.Where("source_type = ?", ExternalWorkflowConversationSource).First(&conversation).Error; err != nil {
				t.Fatal(err)
			}
			query := db.Model(&orm.Conversation{}).Where("id = ?", conversation.ID)
			var err error
			switch lifecycle {
			case "renamed":
				err = query.UpdateColumn("display_name", "My title").Error
			case "archived":
				err = query.UpdateColumn("archived_at", now).Error
			case "trashed":
				err = query.UpdateColumn("deleted_at", now).Error
			case "purged":
				err = query.Delete(&orm.Conversation{}).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := publish(now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := RecoverExternalWorkflowConversations(t.Context(), db.DB, "owner"); err != nil {
				t.Fatal(err)
			}
			var records []orm.Conversation
			if err := db.Where("source_type = ?", ExternalWorkflowConversationSource).Find(&records).Error; err != nil {
				t.Fatal(err)
			}
			if lifecycle == "purged" {
				if len(records) != 0 {
					t.Fatal("publication recreated purged conversation")
				}
				return
			}
			if len(records) != 1 || (lifecycle == "archived" && records[0].ArchivedAt == nil) ||
				(lifecycle == "trashed" && records[0].DeletedAt == nil) || (lifecycle == "renamed" && records[0].DisplayName != "My title") {
				t.Fatalf("user presentation changed: %+v", records)
			}
			if lifecycle != "renamed" && !records[0].UpdatedAt.Equal(conversation.UpdatedAt) {
				t.Fatal("hidden result activity was changed")
			}
		})
	}
}

func TestExternalWorkflowConversationRejectsUnrelatedMetadata(t *testing.T) {
	ext, _ := json.Marshal(map[string]string{"external_workflow_session_id": "run"})
	conversation := orm.Conversation{ID: "unrelated", SourceType: ExternalWorkflowConversationSource, Ext: ext,
		BaseModel: orm.BaseModel{CreateUserID: "owner"}}
	if ExternalWorkflowSessionID(conversation) != "" {
		t.Fatal("unrelated conversation was reclassified")
	}
}
