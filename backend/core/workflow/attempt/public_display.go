package attempt

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/common/taskdisplay"
)

var ErrInvalidPublicDisplay = errors.New("invalid public display event")

// PublicDisplay is an optional, explicit public progress contract. It never
// derives display text from executor logs, prompts or model reasoning.
type PublicDisplay struct {
	SchemaVersion int                             `json:"schema_version"`
	EventKey      string                          `json:"event_key"`
	ProcessSteps  []taskdisplay.PublicProcessStep `json:"process_steps,omitempty"`
	Sources       json.RawMessage                 `json:"sources,omitempty"`
}

func decodePublicDisplay(progress json.RawMessage) (*PublicDisplay, error) {
	if len(progress) > 1<<20 || !json.Valid(progress) {
		return nil, ErrInvalidPublicDisplay
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(progress, &envelope) != nil || envelope == nil {
		return nil, ErrInvalidPublicDisplay
	}
	raw, exists := envelope["public_display"]
	if !exists {
		return nil, nil
	}
	var display PublicDisplay
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&display) != nil || display.SchemaVersion != taskdisplay.SchemaVersion || strings.TrimSpace(display.EventKey) == "" || len(display.EventKey) > 128 || len(display.ProcessSteps) > taskdisplay.PageLimit {
		return nil, ErrInvalidPublicDisplay
	}
	seen := map[string]bool{}
	for _, step := range display.ProcessSteps {
		if taskdisplay.ValidateProcessStep(step) != nil || seen[step.StepID] {
			return nil, ErrInvalidPublicDisplay
		}
		seen[step.StepID] = true
	}
	if len(display.Sources) > 0 {
		var items []map[string]any
		if json.Unmarshal(display.Sources, &items) != nil || items == nil || len(items) > taskdisplay.MaxProcessSteps {
			return nil, ErrInvalidPublicDisplay
		}
		for _, item := range items {
			for _, key := range []string{"url", "title", "snippet"} {
				if raw, exists := item[key]; exists {
					value, ok := raw.(string)
					limit := 2048
					if key == "title" {
						limit = 100
					}
					if key == "snippet" {
						limit = 300
					}
					if !ok || len([]rune(value)) > limit {
						return nil, ErrInvalidPublicDisplay
					}
				}
			}
		}
		// Store only the fields that ordinary readers may see.
		display.Sources, _ = json.Marshal(taskdisplay.NormalizeSources(display.Sources))
	}
	return &display, nil
}

// The caller holds the attempt write lock. Event-key deduplication and process
// state validation therefore share the same transaction as lease validation.
func persistPublicDisplay(tx *gorm.DB, row orm.WorkflowSessionStep, display *PublicDisplay, now time.Time) (bool, error) {
	if display == nil {
		return false, nil
	}
	var events []orm.WorkflowEvent
	if err := tx.Where("session_id = ? AND entity_id = ? AND event_type = ?", row.SessionID, row.ID, "attempt.public_display").Order("id ASC").Find(&events).Error; err != nil {
		return false, err
	}
	encoded, _ := json.Marshal(display)
	previous := map[string]taskdisplay.PublicProcessStep{}
	for _, event := range events {
		var prior PublicDisplay
		if json.Unmarshal(event.PayloadJSON, &prior) != nil {
			continue
		}
		if prior.EventKey == display.EventKey {
			if len(prior.Sources) > 0 {
				prior.Sources, _ = json.Marshal(taskdisplay.NormalizeSources(prior.Sources))
			}
			canonical, _ := json.Marshal(prior)
			if !bytes.Equal(canonical, encoded) {
				return false, ErrInvalidPublicDisplay
			}
			return true, nil
		}
		for _, step := range prior.ProcessSteps {
			previous[step.StepID] = step
		}
	}
	for _, step := range display.ProcessSteps {
		if prior, ok := previous[step.StepID]; ok && (step.Revision <= prior.Revision || taskdisplay.ValidateTransition(prior, step) != nil) {
			return false, ErrInvalidPublicDisplay
		}
		previous[step.StepID] = step
	}
	if len(previous) > taskdisplay.MaxProcessSteps {
		return false, ErrInvalidPublicDisplay
	}
	return false, appendEvent(tx, row, "", "attempt.public_display", encoded, now)
}
