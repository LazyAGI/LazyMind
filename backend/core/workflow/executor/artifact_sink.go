package executor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

// DBArtifactSink is the shared executor output writer. Host implementations
// report values through callbacks and never write Host-private Artifact tables.
type DBArtifactSink struct{ DB *gorm.DB }

func (sink DBArtifactSink) Save(ctx context.Context, attempt AttemptContext, artifact Artifact) error {
	if sink.DB == nil || attempt.AttemptID == "" || artifact.Slot == "" {
		return errors.New("artifact sink requires a database, attempt and slot")
	}
	now := time.Now().UTC()
	return sink.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing orm.WorkflowSlotRevision
		err := tx.Where("producer_attempt_id = ? AND slot = ? AND artifact_seq = ?", attempt.AttemptID, artifact.Slot, artifact.Seq).First(&existing).Error
		if err == nil {
			return nil
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		var session orm.WorkflowSession
		if err := tx.Where("id = ?", attempt.SessionID).First(&session).Error; err != nil {
			return err
		}
		var current orm.WorkflowSlotRevision
		revision := 1
		if err := tx.Where("session_id = ? AND slot_id = ? AND selected = ?", attempt.SessionID, artifact.Slot, true).
			Order("revision DESC").First(&current).Error; err == nil {
			revision = current.Revision + 1
			if err := tx.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", current.ID).Update("selected", false).Error; err != nil {
				return err
			}
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		seq := artifact.Seq
		valueID := uuid.NewString()
		var caption *string
		var metadata map[string]any
		if json.Unmarshal(artifact.Value, &metadata) == nil {
			if text := strings.TrimSpace(stringValue(metadata["caption"])); text != "" {
				caption = &text
			}
		}
		if err := tx.Create(&orm.WorkflowHumanArtifact{ID: valueID, SessionID: attempt.SessionID,
			Slot: artifact.Slot, ContentType: artifact.ContentType, Value: append(json.RawMessage(nil), artifact.Value...),
			Caption: caption, CreatedAt: now}).Error; err != nil {
			return err
		}
		row := orm.WorkflowSlotRevision{ID: uuid.NewString(), SessionID: attempt.SessionID, SlotID: artifact.Slot,
			Revision: revision, Selected: true, ArtifactSeq: &seq, HumanArtifactID: &valueID,
			ChangeSource: "host", Slot: artifact.Slot, StepID: attempt.StepID, Attempt: attempt.AttemptNo,
			Validity: "effective", ProducerAttemptID: attempt.AttemptID, CreatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		stateVersion := session.StateVersion + 1
		if err := tx.Model(&session).Updates(map[string]any{"state_version": stateVersion, "updated_at": now}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"artifact_id": row.ID, "attempt_id": attempt.AttemptID,
			"slot": artifact.Slot, "revision": revision, "state_version": stateVersion})
		return tx.Create(&orm.WorkflowEvent{SessionID: attempt.SessionID, OwnerUserID: session.CreateUserID,
			ContractVersion: "workflow.v1", EventType: "artifact.upsert", EntityID: row.ID,
			StateVersion: stateVersion, PayloadJSON: payload, CreatedAt: now}).Error
	})
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
