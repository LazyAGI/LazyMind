package subagent

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// ResolveDurableWorkflowArtifact replaces a Workflow task's transient executor
// value with the durable Core-owned value committed for the same output. Normal
// SubAgent artifacts have no Workflow attempt row and retain their original value.
func ResolveDurableWorkflowArtifact(
	ctx context.Context,
	db *gorm.DB,
	taskID, slot string,
	seq int,
	contentType string,
	value json.RawMessage,
	caption *string,
) (string, json.RawMessage, *string, bool) {
	if db == nil || taskID == "" || slot == "" || seq < 1 {
		return contentType, value, caption, false
	}
	var step orm.WorkflowSessionStep
	if db.WithContext(ctx).Select("id").Where("task_id = ?", taskID).First(&step).Error != nil {
		return contentType, value, caption, false
	}
	var revision orm.WorkflowSlotRevision
	if db.WithContext(ctx).
		Where("producer_attempt_id = ? AND slot = ? AND artifact_seq = ? AND selected = ?",
			step.ID, slot, seq, true).
		Order("revision DESC").First(&revision).Error != nil || revision.HumanArtifactID == nil {
		return contentType, value, caption, false
	}
	var durable orm.WorkflowHumanArtifact
	if db.WithContext(ctx).Where("id = ?", *revision.HumanArtifactID).First(&durable).Error != nil {
		return contentType, value, caption, false
	}
	return durable.ContentType, append(json.RawMessage(nil), durable.Value...), durable.Caption, true
}
