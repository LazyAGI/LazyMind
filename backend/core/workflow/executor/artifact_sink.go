package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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
		seq := artifact.Seq
		valueID := uuid.NewString()
		var caption *string
		var metadata map[string]any
		if json.Unmarshal(artifact.Value, &metadata) == nil {
			if text := strings.TrimSpace(stringValue(metadata["caption"])); text != "" {
				caption = &text
			}
		}
		cardinality, err := loadSlotCardinality(tx, session.WorkflowRevisionID, artifact.Slot)
		if err != nil {
			return err
		}
		revision, listIndex, err := prepareSlotRevision(tx, attempt.SessionID, artifact.Slot, cardinality, metadata)
		if err != nil {
			return err
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
		if cardinality == "list" && listIndex != nil && metadataListIndex(metadata) == nil {
			if err := appendSlotOrder(tx, attempt.SessionID, artifact.Slot, *listIndex, now); err != nil {
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

type workflowSlotManifest struct {
	Slots []struct {
		ID          string `yaml:"id"`
		Cardinality string `yaml:"cardinality"`
	} `yaml:"slots"`
}

func loadSlotCardinality(tx *gorm.DB, revisionID, slot string) (string, error) {
	if strings.TrimSpace(revisionID) == "" {
		return "single", nil
	}
	var blob orm.WorkflowBlob
	err := tx.Table("plugin_blobs b").Select("b.*").
		Joins("JOIN plugin_revision_entries e ON e.blob_hash = b.hash").
		Where("e.revision_id = ? AND e.path = ?", revisionID, "workflow.yaml").First(&blob).Error
	if err != nil {
		return "", fmt.Errorf("load workflow slot manifest: %w", err)
	}
	var manifest workflowSlotManifest
	if err := yaml.Unmarshal(blob.Content, &manifest); err != nil {
		return "", fmt.Errorf("parse workflow slot manifest: %w", err)
	}
	for _, item := range manifest.Slots {
		if item.ID == slot {
			if strings.EqualFold(strings.TrimSpace(item.Cardinality), "list") {
				return "list", nil
			}
			return "single", nil
		}
	}
	return "", fmt.Errorf("artifact slot %q is not declared in workflow revision %s", slot, revisionID)
}

func metadataListIndex(metadata map[string]any) *int {
	if metadata == nil {
		return nil
	}
	raw, ok := metadata["list_index"]
	if !ok {
		return nil
	}
	var value int
	switch typed := raw.(type) {
	case float64:
		value = int(typed)
		if float64(value) != typed {
			return nil
		}
	case int:
		value = typed
	default:
		return nil
	}
	if value < 0 {
		return nil
	}
	return &value
}

func prepareSlotRevision(tx *gorm.DB, sessionID, slot, cardinality string, metadata map[string]any) (int, *int, error) {
	if cardinality != "list" {
		var current orm.WorkflowSlotRevision
		revision := 1
		err := tx.Where("session_id = ? AND slot_id = ? AND selected = ?", sessionID, slot, true).
			Order("revision DESC").First(&current).Error
		if err == nil {
			revision = current.Revision + 1
			if err := tx.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", current.ID).
				Update("selected", false).Error; err != nil {
				return 0, nil, err
			}
		} else if err != gorm.ErrRecordNotFound {
			return 0, nil, err
		}
		return revision, nil, nil
	}

	listIndex := metadataListIndex(metadata)
	if listIndex == nil {
		var maxIndex int
		if err := tx.Model(&orm.WorkflowSlotRevision{}).Select("COALESCE(MAX(list_index), -1)").
			Where("session_id = ? AND slot_id = ?", sessionID, slot).Scan(&maxIndex).Error; err != nil {
			return 0, nil, err
		}
		next := maxIndex + 1
		return 1, &next, nil
	}

	var maxRevision int
	if err := tx.Model(&orm.WorkflowSlotRevision{}).Select("COALESCE(MAX(revision), 0)").
		Where("session_id = ? AND slot_id = ? AND list_index = ?", sessionID, slot, *listIndex).
		Scan(&maxRevision).Error; err != nil {
		return 0, nil, err
	}
	if err := tx.Model(&orm.WorkflowSlotRevision{}).
		Where("session_id = ? AND slot_id = ? AND list_index = ? AND selected = ?", sessionID, slot, *listIndex, true).
		Update("selected", false).Error; err != nil {
		return 0, nil, err
	}
	return maxRevision + 1, listIndex, nil
}

func appendSlotOrder(tx *gorm.DB, sessionID, slot string, listIndex int, now time.Time) error {
	var order orm.WorkflowSlotOrder
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("session_id = ? AND slot_id = ?", sessionID, slot).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		value, _ := json.Marshal([]int{listIndex})
		return tx.Create(&orm.WorkflowSlotOrder{SessionID: sessionID, SlotID: slot,
			OrderList: value, UpdatedAt: now}).Error
	}
	if err != nil {
		return err
	}
	var values []int
	_ = json.Unmarshal(order.OrderList, &values)
	for _, value := range values {
		if value == listIndex {
			return nil
		}
	}
	values = append(values, listIndex)
	encoded, _ := json.Marshal(values)
	return tx.Model(&orm.WorkflowSlotOrder{}).
		Where("session_id = ? AND slot_id = ?", sessionID, slot).
		Updates(map[string]any{"order_list": encoded, "order_version": order.OrderVersion + 1,
			"updated_at": now}).Error
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
