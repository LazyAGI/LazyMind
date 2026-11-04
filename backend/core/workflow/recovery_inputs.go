package workflow

import (
	"context"
	"crypto/sha256"
	"fmt"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
	"lazymind/core/workflow/publication"
	workflowstore "lazymind/core/workflow/store"
)

// snapshotRecoveryInputs runs under the transition's session lock/transaction.
// Feedback is an immutable external input, not a reverse DAG dependency: rewinding
// its ancestor must not destroy the review/research result needed by the rerun.
func snapshotRecoveryInputs(ctx context.Context, tx *gorm.DB, session *orm.WorkflowSession,
	graph *graphengine.CompiledStateGraph, commandID, step string) error {
	bindings := graph.Runtime.RecoveryInputs[step]
	if len(bindings) == 0 {
		return nil
	}
	snapshot, err := loadRuntimeSnapshot(ctx, tx, session.ID, graph)
	if err != nil {
		return err
	}
	valid := map[string]bool{}
	for _, value := range snapshot.Materials {
		if value.Valid {
			valid[value.RevisionID] = true
		}
	}
	repo := workflowstore.New(tx)
	for target, source := range bindings {
		var revisions []orm.WorkflowSlotRevision
		if err := tx.Where("session_id = ? AND slot_id = ? AND selected = ? AND validity = ?",
			session.ID, source, true, "effective").Order("revision DESC").Find(&revisions).Error; err != nil {
			return err
		}
		for _, revision := range revisions {
			if !valid[revision.ID] {
				continue
			}
			var count int64
			if err := tx.Model(&orm.WorkflowInputBinding{}).Where(
				"workflow_session_id = ? AND material_id = ? AND created_by_command_id = ?",
				session.ID, target, commandID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				break
			}
			// Resolve managed file carriers as well as inline/edited artifacts.
			content, err := publication.Bytes(tx.WithContext(ctx), revision)
			if err != nil {
				return err
			}
			extension, mime := ".json", "application/json"
			if graph.MaterialTypes[target] == "text" {
				extension, mime = ".txt", "text/plain"
			} else if graph.MaterialTypes[target] == "file" {
				extension, mime = ".bin", "application/octet-stream"
			}
			resource, _, err := repo.ImportInputResource(ctx, session.CreateUserID,
				"feedback-"+revision.ID+extension, mime,
				fmt.Sprintf("%x", sha256.Sum256(content)), content)
			if err != nil {
				return err
			}
			// Keep old bindings for immutable attempt history, select only the new one.
			if err := tx.Model(&orm.WorkflowInputBinding{}).Where(
				"workflow_session_id = ? AND material_id = ? AND validity = ?",
				session.ID, target, "effective").Update("validity", "stale").Error; err != nil {
				return err
			}
			if err := repo.BindInput(ctx, session.CreateUserID, workflowstore.InputBinding{
				WorkflowSessionID: session.ID, MaterialID: target, ResourceType: "input_resource",
				ResourceID: resource.ID, ResourceRevision: resource.Revision, ContentHash: resource.ContentHash,
				CreatedByCommandID: commandID,
			}); err != nil {
				return err
			}
			break
		}
	}
	return nil
}
