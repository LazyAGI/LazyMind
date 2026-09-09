package controlstore

import (
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

type ManifestItem struct {
	RevisionID string          `json:"revision_id"`
	Slot       string          `json:"slot"`
	ListIndex  *int            `json:"list_index,omitempty"`
	Value      json.RawMessage `json:"value"`
	ValueHash  string          `json:"value_hash"`
}

type Manifest struct {
	Items  []ManifestItem   `json:"items"`
	Orders map[string][]int `json:"orders"`
}

func ResolveValue(tx *gorm.DB, revision orm.WorkflowSlotRevision) (json.RawMessage, error) {
	if revision.HumanArtifactID != nil {
		var artifact orm.WorkflowHumanArtifact
		if err := tx.Where("id = ?", *revision.HumanArtifactID).First(&artifact).Error; err != nil {
			return nil, err
		}
		return artifact.Value, nil
	}
	if revision.ArtifactSeq != nil {
		var step orm.WorkflowSessionStep
		if err := tx.Where("session_id = ? AND step_id = ? AND attempt = ?", revision.SessionID, revision.StepID, revision.Attempt).First(&step).Error; err != nil {
			return nil, err
		}
		var artifact orm.SubAgentArtifact
		if err := tx.Where("task_id = ? AND slot = ? AND seq = ? AND hidden = ?", step.TaskID, revision.Slot, *revision.ArtifactSeq, false).First(&artifact).Error; err != nil {
			return nil, err
		}
		return artifact.Value, nil
	}
	if len(revision.ContentSnapshot) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return revision.ContentSnapshot, nil
}

func BuildManifest(tx *gorm.DB, sessionID string, slots []string) (string, string, error) {
	manifest := Manifest{Items: []ManifestItem{}, Orders: map[string][]int{}}
	var revisions []orm.WorkflowSlotRevision
	if len(slots) > 0 {
		if err := tx.Where("session_id = ? AND slot_id IN ? AND selected = ? AND validity = ?", sessionID, slots, true, "effective").
			Order("slot_id ASC, list_index ASC, id ASC").Find(&revisions).Error; err != nil {
			return "", "", err
		}
	}
	for _, revision := range revisions {
		value, err := ResolveValue(tx, revision)
		if err != nil {
			return "", "", err
		}
		manifest.Items = append(manifest.Items, ManifestItem{RevisionID: revision.ID, Slot: revision.SlotID,
			ListIndex: revision.ListIndex, Value: value, ValueHash: Hash(value)})
	}
	var orders []orm.WorkflowSlotOrder
	if len(slots) > 0 {
		if err := tx.Where("session_id = ? AND slot_id IN ?", sessionID, slots).Find(&orders).Error; err != nil {
			return "", "", err
		}
	}
	for _, order := range orders {
		var indices []int
		if err := json.Unmarshal(order.OrderList, &indices); err != nil {
			return "", "", err
		}
		manifest.Orders[order.SlotID] = indices
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return "", "", err
	}
	return string(body), Hash(body), nil
}

func CreateReview(tx *gorm.DB, session orm.WorkflowSession, attempt orm.WorkflowSessionStep, slots []string) error {
	if !Controlled(session) || !attempt.ReviewRequired {
		return nil
	}
	var count int64
	if err := tx.Model(&orm.WorkflowReviewCheckpoint{}).Where("attempt_id = ?", attempt.ID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	slots = append([]string(nil), slots...)
	sort.Strings(slots)
	manifest, hash, err := BuildManifest(tx, session.ID, slots)
	if err != nil {
		return err
	}
	slotsJSON, err := json.Marshal(slots)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return tx.Create(&orm.WorkflowReviewCheckpoint{ID: uuid.NewString(), SessionID: session.ID, AttemptID: attempt.ID,
		StepID: attempt.StepID, Version: 1, Status: "pending", SlotsJSON: string(slotsJSON),
		ManifestJSON: manifest, ManifestHash: hash, CreatedAt: now, UpdatedAt: now}).Error
}

// RefreshReviews runs under the same session lock as the material mutation.
func RefreshReviews(tx *gorm.DB, session *orm.WorkflowSession) error {
	if !Controlled(*session) {
		return nil
	}
	var reviews []orm.WorkflowReviewCheckpoint
	if err := tx.Where("session_id = ? AND status = ?", session.ID, "pending").Find(&reviews).Error; err != nil {
		return err
	}
	for _, review := range reviews {
		var slots []string
		if err := json.Unmarshal([]byte(review.SlotsJSON), &slots); err != nil {
			return err
		}
		manifest, hash, err := BuildManifest(tx, session.ID, slots)
		if err != nil {
			return err
		}
		if hash == review.ManifestHash {
			continue
		}
		if err := tx.Model(&review).Updates(map[string]any{"manifest_json": manifest, "manifest_hash": hash,
			"version": gorm.Expr("version + 1"), "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		if err := BumpEvent(tx, session, "review.changed", review.ID, "", map[string]any{"review_id": review.ID}); err != nil {
			return err
		}
	}
	return nil
}

func GuardMaterialEdit(tx *gorm.DB, session orm.WorkflowSession, slot string) error {
	if !Controlled(session) {
		return nil
	}
	if session.Status == "stopped" || session.Dismissed {
		return Reject("SESSION_STOPPED", "resume the workflow before editing")
	}
	var reviews []orm.WorkflowReviewCheckpoint
	if err := tx.Where("session_id = ? AND status IN ?", session.ID, []string{"pending", "accepted"}).Order("created_at DESC").Find(&reviews).Error; err != nil {
		return err
	}
	for _, review := range reviews {
		var slots []string
		if err := json.Unmarshal([]byte(review.SlotsJSON), &slots); err != nil {
			return err
		}
		for _, s := range slots {
			if s != slot {
				continue
			}
			if review.Status == "pending" {
				return nil
			}
			var fresh int64
			if err := tx.Model(&orm.WorkflowSessionStep{}).Where("session_id = ? AND step_id = ? AND validity = 'effective' AND id <> ? AND status IN ?",
				session.ID, review.StepID, review.AttemptID, []string{"queued", "claimed", "running", "pending"}).Count(&fresh).Error; err != nil {
				return err
			}
			if fresh == 0 {
				return Reject("REVIEW_SEALED", "confirmed artifacts are immutable; retry or rewind this step before editing")
			}
			return nil
		}
	}
	return nil
}

func IsSealedRevision(tx *gorm.DB, sessionID, revisionID string) (bool, error) {
	if !tx.Migrator().HasColumn(&orm.WorkflowSession{}, "control_protocol") {
		return false, nil
	}
	var session orm.WorkflowSession
	if err := tx.Where("id = ?", sessionID).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if !Controlled(session) {
		return false, nil
	}
	// An execution input is also immutable even when its producer ran in auto mode.
	var consumers int64
	if tx.Migrator().HasTable(&orm.WorkflowAttemptInputBinding{}) {
		if err := tx.Model(&orm.WorkflowAttemptInputBinding{}).Where("material_revision_id = ?", revisionID).Count(&consumers).Error; err != nil {
			return false, err
		}
		if consumers > 0 {
			return true, nil
		}
	}
	var reviews []orm.WorkflowReviewCheckpoint
	if err := tx.Where("session_id = ? AND status = ?", sessionID, "accepted").Find(&reviews).Error; err != nil {
		return false, err
	}
	for _, review := range reviews {
		var manifest Manifest
		if err := json.Unmarshal([]byte(review.ManifestJSON), &manifest); err != nil {
			return false, err
		}
		for _, item := range manifest.Items {
			if item.RevisionID == revisionID {
				return true, nil
			}
		}
	}
	return false, nil
}
