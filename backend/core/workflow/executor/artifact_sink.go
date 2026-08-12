package executor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
)

// DBArtifactSink is the shared executor output writer. Host implementations
// report values through callbacks and never write Host-private Artifact tables.
type DBArtifactSink struct {
	DB           *gorm.DB
	ArtifactRoot string
}

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
		storedValue, err := materializeEmbeddedArtifactValue(
			artifact.Value, artifact.ContentType, sink.ArtifactRoot, attempt.SessionID,
			attempt.AttemptID, artifact.Slot, artifact.Seq,
		)
		if err != nil {
			return err
		}
		artifact.Value = storedValue
		var session orm.WorkflowSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", attempt.SessionID).First(&session).Error; err != nil {
			return err
		}
		cardinality := attempt.OutputCardinalities[artifact.Slot]
		if cardinality != "list" {
			cardinality = "single"
		}
		var listIndex *int
		if cardinality == "list" {
			listIndex = artifactListIndex(artifact.Value)
			if listIndex == nil {
				var maxIndex int
				if err := tx.Model(&orm.WorkflowSlotRevision{}).Select("COALESCE(MAX(list_index), -1)").
					Where("session_id = ? AND slot_id = ?", attempt.SessionID, artifact.Slot).
					Scan(&maxIndex).Error; err != nil {
					return err
				}
				next := maxIndex + 1
				listIndex = &next
			}
		}
		var maxRevision int
		revisionQuery := tx.Model(&orm.WorkflowSlotRevision{}).Select("COALESCE(MAX(revision), 0)").
			Where("session_id = ? AND slot_id = ?", attempt.SessionID, artifact.Slot)
		if cardinality == "list" {
			revisionQuery = revisionQuery.Where("list_index = ?", *listIndex)
		} else {
			revisionQuery = revisionQuery.Where("list_index IS NULL")
		}
		if err := revisionQuery.Scan(&maxRevision).Error; err != nil {
			return err
		}
		revision := maxRevision + 1
		deselect := tx.Model(&orm.WorkflowSlotRevision{}).
			Where("session_id = ? AND slot_id = ? AND selected = ?", attempt.SessionID, artifact.Slot, true)
		if cardinality == "list" {
			deselect = deselect.Where("list_index = ?", *listIndex)
		}
		if err := deselect.Update("selected", false).Error; err != nil {
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
			Revision: revision, ListIndex: listIndex, Selected: true, ArtifactSeq: &seq, HumanArtifactID: &valueID,
			ChangeSource: "host", Slot: artifact.Slot, StepID: attempt.StepID, Attempt: attempt.AttemptNo,
			Validity: "effective", ProducerAttemptID: attempt.AttemptID, CreatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if cardinality == "list" && listIndex != nil {
			if err := appendArtifactOrder(tx, attempt.SessionID, artifact.Slot, *listIndex, now); err != nil {
				return err
			}
		}
		stateVersion := session.StateVersion + 1
		if err := tx.Model(&session).Updates(map[string]any{"state_version": stateVersion, "updated_at": now}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"artifact_id": row.ID, "attempt_id": attempt.AttemptID,
			"slot": artifact.Slot, "revision": revision, "list_index": listIndex, "state_version": stateVersion})
		return tx.Create(&orm.WorkflowEvent{SessionID: attempt.SessionID, OwnerUserID: session.CreateUserID,
			ContractVersion: "workflow.v1", EventType: "artifact.upsert", EntityID: row.ID,
			StateVersion: stateVersion, PayloadJSON: payload, CreatedAt: now}).Error
	})
}

func artifactListIndex(raw json.RawMessage) *int {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	if number, ok := value["list_index"].(float64); ok && number >= 0 && number == float64(int(number)) {
		index := int(number)
		return &index
	}
	return nil
}

func appendArtifactOrder(tx *gorm.DB, sessionID, slotID string, index int, now time.Time) error {
	var order orm.WorkflowSlotOrder
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("session_id = ? AND slot_id = ?", sessionID, slotID).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		encoded, _ := json.Marshal([]int{index})
		return tx.Create(&orm.WorkflowSlotOrder{SessionID: sessionID, SlotID: slotID,
			OrderList: encoded, OrderVersion: 0, UpdatedAt: now}).Error
	}
	if err != nil {
		return err
	}
	var indices []int
	_ = json.Unmarshal(order.OrderList, &indices)
	for _, existing := range indices {
		if existing == index {
			return nil
		}
	}
	indices = append(indices, index)
	encoded, _ := json.Marshal(indices)
	return tx.Model(&orm.WorkflowSlotOrder{}).
		Where("session_id = ? AND slot_id = ?", sessionID, slotID).
		Updates(map[string]any{"order_list": encoded, "order_version": order.OrderVersion + 1, "updated_at": now}).Error
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
