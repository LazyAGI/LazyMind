package store

import (
	"context"
	"encoding/json"
	"errors"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// ArtifactPreview is public, provisional text. It never creates a slot revision.
type ArtifactPreview struct {
	AttemptID string `json:"attempt_id"`
	StepID    string `json:"step_id"`
	Attempt   int    `json:"attempt"`
	Slot      string `json:"slot"`
	StreamID  string `json:"stream_id"`
	Seq       int    `json:"seq"`
	Content   string `json:"content"`
	State     string `json:"state"`
}

func LatestArtifactPreview(ctx context.Context, db *gorm.DB, sessionID, attemptID string) (*ArtifactPreview, error) {
	var event orm.WorkflowEvent
	err := db.WithContext(ctx).Where("session_id = ? AND entity_id = ? AND event_type = ?",
		sessionID, attemptID, "artifact.preview").Order("id DESC").First(&event).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var preview ArtifactPreview
	if err := json.Unmarshal(event.PayloadJSON, &preview); err != nil {
		return nil, err
	}
	return &preview, nil
}
