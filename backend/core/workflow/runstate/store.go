package runstate

import (
	"errors"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"time"
)

var ErrStateChanged = errors.New("workflow run changed before its state could be written")

// Write persists an already-decided run state inside the caller's session-locked
// transaction. Extra fields share that write, including a reserved state version
// or a controller binding. The caller commits its event and receipt in the same
// transaction; dispatch and control retain their existing version allocation.
func Write(tx *gorm.DB, session *orm.WorkflowSession, status string, fields map[string]any) error {
	updates := make(map[string]any, len(fields)+2)
	for key, value := range fields {
		updates[key] = value
	}
	now := time.Now().UTC()
	updates["status"], updates["updated_at"] = status, now
	result := tx.Model(&orm.WorkflowSession{}).Where("id = ?", session.ID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrStateChanged
	}
	session.Status, session.UpdatedAt = status, now
	return nil
}
