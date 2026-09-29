package taskguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

type OrganizeMutation string

const (
	OrganizeRename      OrganizeMutation = "rename"
	OrganizeTrash       OrganizeMutation = "trash"
	OrganizeMetadata    OrganizeMutation = "metadata"
	organizeRollbackKey                  = "organize_rollback"
)

type organizeMutationSnapshot struct {
	Kind     OrganizeMutation         `json:"kind"`
	Before   orm.SkillV2Skill         `json:"before"`
	Index    *orm.SkillSearchIndex    `json:"index,omitempty"`
	Installs []orm.SkillMarketInstall `json:"installs,omitempty"`
}

// RecordOrganizeMutation runs in the mutation's transaction, before changing
// published package state. Draft content needs no snapshot: cancellation drops
// only this task's overlays, leaving published revisions intact.
func RecordOrganizeMutation(ctx context.Context, tx *gorm.DB, userID, taskID, skillID string, kind OrganizeMutation) error {
	if taskKind(taskID) != "organize" {
		return nil
	}
	if err := LockOrganizeTask(ctx, tx, userID, taskID); err != nil {
		return err
	}
	var task orm.ResourceUpdateTask
	if err := tx.WithContext(ctx).Where("user_id = ? AND task_type = ? AND status = ? AND (trigger_id = ? OR result_id = ?)",
		userID, orm.ResourceUpdateTaskTypeOrganizeSkill, orm.ResourceUpdateTaskStatusRunning,
		"skill_organize:"+userID+":"+taskID, taskID).Take(&task).Error; err != nil {
		return err
	}
	result, journal, err := decodeOrganizeJournal(task.ResultJSON)
	if err != nil {
		return err
	}
	snapshot := organizeMutationSnapshot{Kind: kind}
	if err := tx.Where("id = ? AND owner_user_id = ?", skillID, userID).Take(&snapshot.Before).Error; err != nil {
		return err
	}
	if tx.Migrator().HasTable(&orm.SkillSearchIndex{}) {
		var index orm.SkillSearchIndex
		err := tx.Where("skill_id = ?", skillID).Take(&index).Error
		if err == nil {
			snapshot.Index = &index
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if kind == OrganizeTrash && tx.Migrator().HasTable(&orm.SkillMarketInstall{}) {
		if err := tx.Where("skill_id = ? AND user_id = ?", skillID, userID).Find(&snapshot.Installs).Error; err != nil {
			return err
		}
	}
	encodedJournal, err := json.Marshal(append(journal, snapshot))
	if err != nil {
		return err
	}
	result[organizeRollbackKey] = encodedJournal
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return tx.Model(&orm.ResourceUpdateTask{}).Where("id = ?", task.ID).UpdateColumn("result_json", json.RawMessage(encodedResult)).Error
}

// RollbackOrganizeMutations runs while cancellation holds the reservation lock.
// Reverse mutation order also restores intermediate renames without colliding
// with names vacated by later operations in the same task.
func RollbackOrganizeMutations(ctx context.Context, tx *gorm.DB, task orm.ResourceUpdateTask) error {
	result, journal, err := decodeOrganizeJournal(task.ResultJSON)
	if err != nil {
		return err
	}
	for i := len(journal) - 1; i >= 0; i-- {
		snapshot := journal[i]
		before := snapshot.Before
		if before.OwnerUserID != task.UserID {
			return fmt.Errorf("organize rollback owner mismatch")
		}
		updates := map[string]any{"updated_at": before.UpdatedAt}
		switch snapshot.Kind {
		case OrganizeRename:
			updates["category"], updates["skill_name"], updates["relative_root"] = before.Category, before.SkillName, before.RelativeRoot
		case OrganizeTrash:
			updates["deleted_at"], updates["trash_expires_at"], updates["deleted_by"] = before.DeletedAt, before.TrashExpiresAt, before.DeletedBy
		case OrganizeMetadata:
			updates["field"], updates["tags"], updates["aliases"], updates["keywords"] = before.Field, before.Tags, before.Aliases, before.Keywords
		default:
			return fmt.Errorf("unknown organize rollback mutation %q", snapshot.Kind)
		}
		query := tx.WithContext(ctx).Model(&orm.SkillV2Skill{}).Where("id = ? AND owner_user_id = ?", before.ID, task.UserID)
		if before.HeadRevisionID == nil {
			query = query.Where("head_revision_id IS NULL")
		} else {
			query = query.Where("head_revision_id = ?", *before.HeadRevisionID)
		}
		updated := query.Updates(updates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("skill changed during organize rollback")
		}
		if tx.Migrator().HasTable(&orm.SkillSearchIndex{}) {
			if snapshot.Index == nil {
				if err := tx.Where("skill_id = ?", before.ID).Delete(&orm.SkillSearchIndex{}).Error; err != nil {
					return err
				}
			} else if err := tx.Save(snapshot.Index).Error; err != nil {
				return err
			}
		}
		if len(snapshot.Installs) > 0 {
			if err := tx.Create(&snapshot.Installs).Error; err != nil {
				return err
			}
		}
	}
	if len(journal) == 0 {
		return nil
	}
	delete(result, organizeRollbackKey)
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return tx.Model(&orm.ResourceUpdateTask{}).Where("id = ?", task.ID).UpdateColumn("result_json", json.RawMessage(encoded)).Error
}

// PrepareOrganizeCancellation never drops content when the rollback history is
// incomplete or another operation has committed a conflicting change. A SQL
// error must be rolled back to the savepoint before PostgreSQL can continue.
func PrepareOrganizeCancellation(ctx context.Context, tx *gorm.DB, task orm.ResourceUpdateTask) (map[string]any, error) {
	result, journal, decodeErr := decodeOrganizeJournal(task.ResultJSON)
	// Review commit claims drafts before updating published skills. Match that
	// lock order before rollback, including trashed sources without overlays.
	var ids []string
	taskIDs := []string{strings.TrimPrefix(task.TriggerID, "skill_organize:"+task.UserID+":")}
	if task.ResultID != "" {
		taskIDs = append(taskIDs, task.ResultID)
	}
	if err := tx.Table("skill_drafts AS d").Select("d.skill_id").Joins("JOIN skills AS s ON s.id = d.skill_id").Where("s.owner_user_id = ? AND d.task_id IN ?", task.UserID, taskIDs).Pluck("d.skill_id", &ids).Error; err != nil {
		return nil, err
	}
	for _, snapshot := range journal {
		if snapshot.Before.OwnerUserID == task.UserID {
			ids = append(ids, snapshot.Before.ID)
		}
	}
	sort.Strings(ids)
	for i, id := range ids {
		if i > 0 && id == ids[i-1] {
			continue
		}
		if err := tx.Table("skill_drafts").Where("skill_id = ?", id).UpdateColumn("version", gorm.Expr("version")).Error; err != nil {
			return nil, err
		}
	}
	reason := "rollback history unavailable for this task"
	if decodeErr == nil && string(result["organize_rollback_version"]) == "1" {
		if err := tx.SavePoint("organize_cancel_rollback").Error; err != nil {
			return nil, err
		}
		err := RollbackOrganizeMutations(ctx, tx, task)
		if err == nil {
			return nil, nil
		}
		if restoreErr := tx.RollbackTo("organize_cancel_rollback").Error; restoreErr != nil {
			return nil, restoreErr
		}
		reason = err.Error()
	}
	return map[string]any{"pending_review": true, "rollback_status": "incomplete", "rollback_error": reason}, nil
}

func decodeOrganizeJournal(raw json.RawMessage) (map[string]json.RawMessage, []organizeMutationSnapshot, error) {
	result := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, nil, err
		}
	}
	if result == nil {
		result = map[string]json.RawMessage{}
	}
	var journal []organizeMutationSnapshot
	if encoded := result[organizeRollbackKey]; len(encoded) > 0 {
		if err := json.Unmarshal(encoded, &journal); err != nil {
			return nil, nil, err
		}
	}
	return result, journal, nil
}
