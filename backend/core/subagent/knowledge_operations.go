package subagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/common/taskdisplay"
	"time"
)

func persistKnowledgeOperation(ctx context.Context, db *gorm.DB, ev TaskEvent) (bool, error) {
	receipt, err := taskdisplay.DecodeKnowledgeOperation(ev.KnowledgeOperation)
	if err != nil || ev.ExecutionID == "" {
		return false, errors.New("knowledge operation execution missing or invalid")
	}
	accepted := false
	err = taskTransaction(ctx, db, func(tx *gorm.DB) error {
		if err := tx.Model(&orm.SubAgentTask{}).Where("id = ?", ev.TaskID).UpdateColumn("display_revision", gorm.Expr("display_revision")).Error; err != nil {
			return err
		}
		task, err := GetTask(ctx, tx, ev.TaskID)
		if err != nil {
			return err
		}
		if task.ExecutionID != ev.ExecutionID {
			return ErrStaleExecution
		}
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(task.ID+"/"+task.ExecutionID+"/knowledge/"+receipt.CallID)).String()
		var previous orm.SubAgentStep
		if err := tx.Where("id = ?", id).Take(&previous).Error; err == nil {
			encoded, _ := json.Marshal(receipt)
			if !bytes.Equal(encoded, previous.Content) {
				return ErrPublicEventConflict
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if isTerminal(task.Status) {
			return ErrTaskTerminal
		}
		var seq int
		if err := tx.Model(&orm.SubAgentStep{}).Where("task_id = ?", task.ID).Select("COALESCE(MAX(seq), -1)").Scan(&seq).Error; err != nil {
			return err
		}
		content, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		row := orm.SubAgentStep{ID: id, TaskID: task.ID, ExecutionID: task.ExecutionID, Seq: seq + 1, Role: "knowledge_op", Content: content, CreatedAt: time.Now().UTC()}
		if err = tx.Create(&row).Error; err != nil {
			return err
		}
		if err = tx.Model(task).UpdateColumn("display_revision", gorm.Expr("display_revision + 1")).Error; err != nil {
			return err
		}
		accepted = true
		return recordOrdinaryWorkflowChange(ctx, tx, task)
	})
	return accepted, err
}
