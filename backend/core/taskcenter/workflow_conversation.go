package taskcenter

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

const ExternalWorkflowConversationSource = "external_workflow"

// ExternalWorkflowSessionID identifies a result-only conversation, not a host
// binding. The runtime's conversation scope and external identity stay intact.
func ExternalWorkflowSessionID(conversation orm.Conversation) string {
	if conversation.SourceType != ExternalWorkflowConversationSource {
		return ""
	}
	var metadata struct {
		SessionID string `json:"external_workflow_session_id"`
	}
	if json.Unmarshal(conversation.Ext, &metadata) != nil || metadata.SessionID == "" ||
		conversation.ID != externalWorkflowConversationID(conversation.CreateUserID, metadata.SessionID) {
		return ""
	}
	return metadata.SessionID
}

func externalWorkflowConversationID(owner, sessionID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("lazymind:workflow-results:"+owner+":"+sessionID)).String()
}

// EnsureExternalWorkflowConversation runs in the artifact writer's transaction,
// under its session lock. It only creates a presentation record; it never creates
// Chat histories, invokes a model, or assigns a made-up external host identity.
func EnsureExternalWorkflowConversation(ctx context.Context, tx *gorm.DB, session orm.WorkflowSession, artifactTime time.Time) error {
	if session.ConversationID != "" || session.OriginHost != "external-agent" ||
		session.ControllerHost != "external-agent" || session.Dismissed || session.CreateUserID == "" {
		return nil
	}
	// Chat is optional in isolated runtime deployments and during schema rollout.
	if !tx.Migrator().HasTable(&orm.Conversation{}) || !tx.Migrator().HasTable(&orm.TaskCenterTask{}) {
		return nil
	}
	if err := EnsureWorkflowTask(ctx, tx, session); err != nil {
		return err
	}
	var task orm.TaskCenterTask
	if err := tx.WithContext(ctx).Where("user_id = ? AND plugin_session_id = ?", session.CreateUserID, session.ID).
		Order("created_at ASC, id ASC").Take(&task).Error; err != nil {
		return err
	}
	conversationID := externalWorkflowConversationID(session.CreateUserID, session.ID)
	if task.ConversationID != "" {
		// Keep the durable task link even after a conversation is permanently
		// deleted, so a later publication or recovery cannot resurrect it.
		if task.ConversationID != conversationID {
			return nil
		}
		return tx.WithContext(ctx).Model(&orm.Conversation{}).
			Where("id = ? AND create_user_id = ? AND source_type = ? AND deleted_at IS NULL AND archived_at IS NULL AND updated_at < ?",
				conversationID, session.CreateUserID, ExternalWorkflowConversationSource, artifactTime).
			UpdateColumn("updated_at", artifactTime).Error
	}
	if task.ArchivedAt != nil {
		return nil
	}
	title := session.WorkflowRef
	if title == "" {
		title = session.WorkflowID
	}
	if task.Title != nil && strings.TrimSpace(*task.Title) != "" {
		title = *task.Title
	}
	ext, _ := json.Marshal(map[string]string{"external_workflow_session_id": session.ID})
	conversation := orm.Conversation{ID: conversationID, DisplayName: title, TitleSource: "user",
		SourceType: ExternalWorkflowConversationSource, Ext: ext, Models: json.RawMessage(`[]`),
		SearchConfig: json.RawMessage(`{}`),
		BaseModel:    orm.BaseModel{CreateUserID: session.CreateUserID, CreatedAt: session.CreatedAt, UpdatedAt: artifactTime}}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&conversation).Error; err != nil {
		return err
	}
	var stored orm.Conversation
	if err := tx.WithContext(ctx).Where("id = ? AND create_user_id = ?", conversationID, session.CreateUserID).
		Take(&stored).Error; err != nil {
		return err
	}
	if ExternalWorkflowSessionID(stored) != session.ID {
		return gorm.ErrInvalidData
	}
	return tx.WithContext(ctx).Model(&orm.TaskCenterTask{}).
		Where("id = ? AND user_id = ? AND conversation_id = ?", task.ID, session.CreateUserID, "").
		UpdateColumn("conversation_id", conversationID).Error
}

// RecoverExternalWorkflowConversations exposes historical runs when their owner
// next opens Chat. Only stored outputs qualify; no execution is started here.
func RecoverExternalWorkflowConversations(ctx context.Context, db *gorm.DB, owner string) error {
	if owner == "" || !db.Migrator().HasTable(&orm.WorkflowSession{}) ||
		!db.Migrator().HasTable(&orm.WorkflowSlotRevision{}) || !db.Migrator().HasTable(&orm.TaskCenterTask{}) {
		return nil
	}
	var sessions []orm.WorkflowSession
	if err := db.WithContext(ctx).Where("create_user_id = ? AND origin_host = ? AND controller_host = ? AND conversation_id = ? AND dismissed = ?",
		owner, "external-agent", "external-agent", "", false).
		Where("EXISTS (SELECT 1 FROM plugin_slot_revisions outputs WHERE outputs.session_id = plugin_sessions.id AND outputs.validity <> ?)", "deleted").
		Where("NOT EXISTS (SELECT 1 FROM task_center_tasks linked WHERE linked.plugin_session_id = plugin_sessions.id AND (linked.conversation_id <> '' OR linked.archived_at IS NOT NULL))").
		Order("created_at DESC, id ASC").Limit(100).Find(&sessions).Error; err != nil {
		return err
	}
	for _, session := range sessions {
		if err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
			var current orm.WorkflowSession
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND create_user_id = ?", session.ID, owner).Take(&current).Error; err != nil {
				return err
			}
			var latest orm.WorkflowSlotRevision
			if err := tx.Where("session_id = ? AND validity <> ?", session.ID, "deleted").
				Order("created_at DESC, id ASC").Take(&latest).Error; err != nil {
				return err
			}
			return EnsureExternalWorkflowConversation(ctx, tx, current, latest.CreatedAt)
		}); err != nil {
			return err
		}
	}
	return nil
}
