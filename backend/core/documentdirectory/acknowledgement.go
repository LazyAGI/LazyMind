package documentdirectory

import (
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func acknowledgeBatch(tx *gorm.DB, uid, id string) error {
	var b orm.DocumentDirectoryBatch
	if err := tx.Where("id=? AND user_id=?", id, uid).Take(&b).Error; err != nil {
		return err
	}
	status := b.Status
	if b.JobID != "" {
		var j orm.AsyncJob
		if err := tx.Where("id=? AND create_user_id=?", b.JobID, uid).Take(&j).Error; err != nil {
			return err
		}
		status = j.Status
	}
	if status != "succeeded" && status != "failed" && status != "canceled" {
		return ErrBusy
	}
	var unread int64
	if err := tx.Model(&orm.DocumentDirectoryBatch{}).Where("parent_id=? AND user_id=? AND acknowledged=?", id, uid, false).Count(&unread).Error; err != nil {
		return err
	}
	if unread > 0 {
		return ErrBusy
	}
	if err := tx.Model(&b).Update("acknowledged", true).Error; err != nil {
		return err
	}
	return acknowledgeParents(tx, uid, b.ParentID)
}

// Aggregating a completed result must not fail because the parent's runner
// has not yet finalized its lease. The batch status is committed with routing.
func acknowledgeParents(tx *gorm.DB, uid, id string) error {
	if id == "" {
		return nil
	}
	var b orm.DocumentDirectoryBatch
	if err := tx.Where("id=? AND user_id=?", id, uid).Take(&b).Error; err != nil {
		return err
	}
	if b.Status != "succeeded" && b.Status != "failed" && b.Status != "canceled" {
		return nil
	}
	var unread int64
	if err := tx.Model(&orm.DocumentDirectoryBatch{}).Where("parent_id=? AND user_id=? AND acknowledged=?", id, uid, false).Count(&unread).Error; err != nil {
		return err
	}
	if unread != 0 {
		return nil
	}
	if err := tx.Model(&b).Update("acknowledged", true).Error; err != nil {
		return err
	}
	return acknowledgeParents(tx, uid, b.ParentID)
}
