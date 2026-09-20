package workflow

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/artifactgraph"
)

func lockArtifactMutationSession(tx *gorm.DB, sessionID string) (*orm.WorkflowSession, error) {
	return artifactgraph.LockSession(tx, sessionID)
}

func appendArtifactUpsertEvent(
	tx *gorm.DB,
	session *orm.WorkflowSession,
	revision *orm.WorkflowSlotRevision,
	draftVersion int64,
	now time.Time,
) error {
	nextStateVersion := session.StateVersion + 1
	updated := tx.Model(&orm.WorkflowSession{}).
		Where("id = ? AND state_version = ?", session.ID, session.StateVersion).
		Updates(map[string]any{
			"state_version": gorm.Expr("state_version + 1"),
			"updated_at":    now,
		})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrConflict
	}
	payload := map[string]any{
		"artifact_id":   revision.ID,
		"slot_id":       revision.SlotID,
		"slot":          revision.Slot,
		"revision":      revision.Revision,
		"change_source": revision.ChangeSource,
		"state_version": nextStateVersion,
	}
	if revision.ListIndex != nil {
		payload["list_index"] = *revision.ListIndex
	}
	if revision.ProducerAttemptID != "" {
		payload["attempt_id"] = revision.ProducerAttemptID
	}
	if draftVersion > 0 {
		payload["draft_version"] = draftVersion
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	event := orm.WorkflowEvent{
		SessionID: session.ID, OwnerUserID: session.CreateUserID,
		ContractVersion: "workflow.v1", EventType: "artifact.upsert", EntityID: revision.ID,
		StateVersion: nextStateVersion, PayloadJSON: encoded, CreatedAt: now,
	}
	return tx.Create(&event).Error
}
