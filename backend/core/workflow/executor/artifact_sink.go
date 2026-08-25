package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/artifactfile"
)

// DBArtifactSink is the shared executor output writer. Host implementations
// report values through callbacks and never write Host-private Artifact tables.
type DBArtifactSink struct{ DB *gorm.DB }

func (sink DBArtifactSink) Save(ctx context.Context, attempt AttemptContext, artifact Artifact) error {
	if sink.DB == nil || attempt.AttemptID == "" || artifact.Slot == "" {
		return errors.New("artifact sink requires a database, attempt and slot")
	}
	now := time.Now().UTC()
	valueID := uuid.NewString()
	storedValue, cleanupDirectory, err := artifactfile.Materialize(attempt.SessionID, valueID, artifact.Value)
	if err != nil {
		return err
	}
	var caption *string
	var metadata map[string]any
	if json.Unmarshal(storedValue, &metadata) == nil {
		if text := strings.TrimSpace(stringValue(metadata["caption"])); text != "" {
			caption = &text
		}
	}
	persisted := false
	err = sink.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		cardinality := strings.TrimSpace(attempt.OutputCardinality[artifact.Slot])
		if cardinality == "" {
			var loadErr error
			cardinality, loadErr = loadSlotCardinality(tx, session.WorkflowRevisionID, artifact.Slot)
			if loadErr != nil {
				return loadErr
			}
		}
		if cardinality != "list" {
			cardinality = "single"
		}
		listIndex, appendList, err := artifactListIndex(tx, attempt, artifact, cardinality)
		if err != nil {
			return err
		}
		revision, err := nextArtifactRevision(tx, attempt.SessionID, artifact.Slot, cardinality, listIndex)
		if err != nil {
			return err
		}
		selected := tx.Model(&orm.WorkflowSlotRevision{}).Where(
			"session_id = ? AND slot_id = ? AND selected = ?", attempt.SessionID, artifact.Slot, true,
		)
		if cardinality == "list" {
			selected = selected.Where("list_index = ?", *listIndex)
		}
		if err := selected.Update("selected", false).Error; err != nil {
			return err
		}
		seq := artifact.Seq
		if err := tx.Create(&orm.WorkflowHumanArtifact{ID: valueID, SessionID: attempt.SessionID,
			Slot: artifact.Slot, ContentType: artifact.ContentType, Value: storedValue,
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
		if appendList {
			if err := appendArtifactListOrder(tx, attempt.SessionID, artifact.Slot, *listIndex, now); err != nil {
				return err
			}
		}
		stateVersion := session.StateVersion + 1
		if err := tx.Model(&session).Updates(map[string]any{"state_version": stateVersion, "updated_at": now}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"artifact_id": row.ID, "attempt_id": attempt.AttemptID,
			"slot": artifact.Slot, "revision": revision, "list_index": listIndex, "state_version": stateVersion})
		if err := tx.Create(&orm.WorkflowEvent{SessionID: attempt.SessionID, OwnerUserID: session.CreateUserID,
			ContractVersion: "workflow.v1", EventType: "artifact.upsert", EntityID: row.ID,
			StateVersion: stateVersion, PayloadJSON: payload, CreatedAt: now}).Error; err != nil {
			return err
		}
		persisted = true
		return nil
	})
	if (err != nil || !persisted) && cleanupDirectory != "" {
		_ = os.RemoveAll(cleanupDirectory)
	}
	return err
}

func artifactListIndex(tx *gorm.DB, attempt AttemptContext, artifact Artifact, cardinality string) (*int, bool, error) {
	if cardinality != "list" {
		return nil, false, nil
	}
	if selected := attempt.PartialSelector[artifact.Slot]; len(selected) > 0 {
		position := artifact.Seq - 1
		if position < 0 || position >= len(selected) {
			return nil, false, gorm.ErrInvalidData
		}
		index := selected[position]
		return &index, false, nil
	}
	var metadata map[string]any
	if json.Unmarshal(artifact.Value, &metadata) == nil {
		if index := metadataListIndex(metadata); index != nil {
			return index, false, nil
		}
	}
	var maxIndex int
	if err := tx.Model(&orm.WorkflowSlotRevision{}).Select("COALESCE(MAX(list_index), -1)").
		Where("session_id = ? AND slot_id = ?", attempt.SessionID, artifact.Slot).Scan(&maxIndex).Error; err != nil {
		return nil, false, err
	}
	index := maxIndex + 1
	return &index, true, nil
}

func nextArtifactRevision(tx *gorm.DB, sessionID, slot, cardinality string, listIndex *int) (int, error) {
	query := tx.Model(&orm.WorkflowSlotRevision{}).Select("COALESCE(MAX(revision), 0)").
		Where("session_id = ? AND slot_id = ?", sessionID, slot)
	if cardinality == "list" {
		query = query.Where("list_index = ?", *listIndex)
	} else {
		query = query.Where("list_index IS NULL")
	}
	var revision int
	if err := query.Scan(&revision).Error; err != nil {
		return 0, err
	}
	return revision + 1, nil
}

func appendArtifactListOrder(tx *gorm.DB, sessionID, slot string, listIndex int, now time.Time) error {
	var order orm.WorkflowSlotOrder
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("session_id = ? AND slot_id = ?", sessionID, slot).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		encoded, _ := json.Marshal([]int{listIndex})
		return tx.Create(&orm.WorkflowSlotOrder{SessionID: sessionID, SlotID: slot,
			OrderList: encoded, UpdatedAt: now}).Error
	}
	if err != nil {
		return err
	}
	var current []int
	if err := json.Unmarshal(order.OrderList, &current); err != nil {
		return err
	}
	for _, index := range current {
		if index == listIndex {
			return nil
		}
	}
	encoded, _ := json.Marshal(append(current, listIndex))
	return tx.Model(&orm.WorkflowSlotOrder{}).Where("session_id = ? AND slot_id = ?", sessionID, slot).
		Updates(map[string]any{"order_list": encoded, "order_version": order.OrderVersion + 1, "updated_at": now}).Error
}

type workflowSlotManifest struct {
	Slots []struct {
		ID          string `yaml:"id"`
		Cardinality string `yaml:"cardinality"`
	} `yaml:"slots"`
}

// loadSlotCardinality keeps compatibility with attempt payloads produced before
// OutputCardinality was embedded in the neutral executor contract.
func loadSlotCardinality(tx *gorm.DB, revisionID, slot string) (string, error) {
	if strings.TrimSpace(revisionID) == "" {
		return "single", nil
	}
	var blob orm.WorkflowBlob
	err := tx.Table("plugin_blobs b").
		Select("b.*").
		Joins("JOIN plugin_revision_entries e ON e.blob_hash = b.hash").
		Where("e.revision_id = ? AND e.path = ?", revisionID, "workflow.yaml").
		First(&blob).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Older hosted attempts can reference revisions whose compiled graph was
		// persisted without the source manifest. They predate OutputCardinality,
		// so retain the historical single-value default when no manifest exists.
		return "single", nil
	}
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

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

// ValidateRequiredOutputs keeps completion policy shared by managed, remote,
// and externally hosted execution paths.
func ValidateRequiredOutputs(ctx context.Context, db *gorm.DB, attempt AttemptContext) error {
	if len(attempt.RequiredOutputs) == 0 {
		return nil
	}
	var rows []orm.WorkflowSlotRevision
	if err := db.WithContext(ctx).Where(
		"producer_attempt_id = ? AND validity = 'effective'", attempt.AttemptID,
	).Find(&rows).Error; err != nil {
		return err
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		seen[row.SlotID] = true
	}
	for _, slot := range attempt.RequiredOutputs {
		if !seen[slot] {
			return errors.New("required output missing: " + slot)
		}
	}
	return nil
}
