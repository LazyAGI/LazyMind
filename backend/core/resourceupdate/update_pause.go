package resourceupdate

import (
	"context"
	"gorm.io/gorm"
	"lazymind/core/common/maintenancegate"
	"lazymind/core/common/orm"
)

var updateWork = maintenancegate.New()

func PauseForUpdate(epoch uint64) (*maintenancegate.Ticket, error) { return updateWork.Pause(epoch) }
func ResumeFromUpdate(epoch uint64) error                          { return updateWork.Resume(epoch) }

// A reservation is not proof of algorithm completion. Only the owning request's
// durable terminal stats establish that its final business writes have ended.
func ActiveForUpdate(ctx context.Context, db *gorm.DB) (int64, error) {
	var tasks []orm.ResourceUpdateTask
	if err := db.WithContext(ctx).Where("status = ?", "running").Find(&tasks).Error; err != nil {
		return 0, err
	}
	var active int64
	for _, task := range tasks {
		if task.TaskType != orm.ResourceUpdateTaskTypeOrganizeSkill {
			active++
			continue
		}
		requestID := skillTaskRequestID(task)
		if requestID == "" {
			active++
			continue
		}
		var terminal int64
		if err := db.WithContext(ctx).Model(&orm.SkillReviewStats{}).Where("userid = ? AND requestid = ? AND status IN ?", task.UserID, requestID, []string{"completed", "failed"}).Count(&terminal).Error; err != nil {
			return 0, err
		}
		if terminal == 0 {
			active++
		}
	}
	return active, nil
}

func SealForBootstrap(key string) (*maintenancegate.Ticket, error) { return updateWork.Seal(key) }
func UnsealBootstrap(key string) error                             { return updateWork.Unseal(key) }
