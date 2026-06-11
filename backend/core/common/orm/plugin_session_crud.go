package orm

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// CreatePluginSession inserts a new plugin session record.
func CreatePluginSession(db *gorm.DB, s *PluginSession) error {
	return db.Create(s).Error
}

// GetPluginSession fetches a plugin session by ID.
func GetPluginSession(db *gorm.DB, sessionID string) (*PluginSession, error) {
	var s PluginSession
	if err := db.Where("id = ?", sessionID).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

// GetActivePluginSession returns the most recent plugin session for a conversation.
// Returns nil, nil when no session exists (not an error condition).
func GetActivePluginSession(db *gorm.DB, conversationID string) (*PluginSession, error) {
	var s PluginSession
	err := db.Where("conversation_id = ?", conversationID).
		Order("created_at DESC").
		Limit(1).
		First(&s).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// UpdateCurrentStep updates the current_step_id field of a plugin session.
func UpdateCurrentStep(db *gorm.DB, sessionID, stepID string) error {
	return db.Model(&PluginSession{}).
		Where("id = ?", sessionID).
		Updates(map[string]interface{}{
			"current_step_id": stepID,
			"updated_at":      time.Now(),
		}).Error
}

// UpdateSessionMeta merges new artifact values into plugin_sessions.meta JSON.
func UpdateSessionMeta(db *gorm.DB, sessionID string, mergeMap map[string]interface{}) error {
	var s PluginSession
	if err := db.Where("id = ?", sessionID).First(&s).Error; err != nil {
		return err
	}

	existing := make(map[string]interface{})
	if len(s.Meta) > 0 {
		if err := json.Unmarshal(s.Meta, &existing); err != nil {
			existing = make(map[string]interface{})
		}
	}
	for k, v := range mergeMap {
		existing[k] = v
	}
	merged, err := json.Marshal(existing)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}

	return db.Model(&PluginSession{}).
		Where("id = ?", sessionID).
		Updates(map[string]interface{}{
			"meta":       merged,
			"updated_at": time.Now(),
		}).Error
}

// InsertPluginSessionStep creates a new step execution record.
func InsertPluginSessionStep(db *gorm.DB, step *PluginSessionStep) error {
	return db.Create(step).Error
}

// UpdateStepStatus updates the step_status field for a step execution.
func UpdateStepStatus(db *gorm.DB, stepExecID, status string) error {
	return db.Model(&PluginSessionStep{}).
		Where("id = ?", stepExecID).
		Updates(map[string]interface{}{
			"step_status": status,
			"updated_at":  time.Now(),
		}).Error
}

// UpdateStepHeartbeat refreshes last_heartbeat for an active step execution.
func UpdateStepHeartbeat(db *gorm.DB, stepExecID string) error {
	return db.Model(&PluginSessionStep{}).
		Where("id = ?", stepExecID).
		Update("last_heartbeat", time.Now()).Error
}

// QueryLatestStepRecord returns the most recent execution record for the given step.
func QueryLatestStepRecord(db *gorm.DB, sessionID, step string) (*PluginSessionStep, error) {
	var rec PluginSessionStep
	err := db.Where("session_id = ? AND step = ?", sessionID, step).
		Order("created_at DESC").
		Limit(1).
		First(&rec).Error
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// MarkStaleRunningStepsInterrupted marks running steps whose heartbeat has
// not been updated for more than staleThreshold as interrupted.
// Call this on service startup to recover from process crashes.
func MarkStaleRunningStepsInterrupted(db *gorm.DB, staleThreshold time.Duration) error {
	cutoff := time.Now().Add(-staleThreshold)
	return db.Model(&PluginSessionStep{}).
		Where("step_status = 'running' AND last_heartbeat < ?", cutoff).
		Updates(map[string]interface{}{
			"step_status": "interrupted",
			"updated_at":  time.Now(),
		}).Error
}

// InsertPluginCheckpoint saves a step checkpoint.
// The sequence is automatically set to max(current) + 1 for the given step_exec_id.
func InsertPluginCheckpoint(db *gorm.DB, cp *PluginSessionStepCheckpoint) error {
	var maxSeq int
	db.Model(&PluginSessionStepCheckpoint{}).
		Where("step_exec_id = ?", cp.StepExecID).
		Select("COALESCE(MAX(sequence), 0)").
		Scan(&maxSeq)
	cp.Sequence = maxSeq + 1
	return db.Create(cp).Error
}

// LoadLatestCheckpoint returns the most recent checkpoint for the latest running
// (or interrupted) execution of the given step in the session.
// Returns an empty map if no checkpoint exists.
func LoadLatestCheckpoint(db *gorm.DB, sessionID, step string) (map[string]interface{}, error) {
	var stepRec PluginSessionStep
	err := db.Where("session_id = ? AND step = ? AND step_status IN ('running','interrupted')", sessionID, step).
		Order("created_at DESC").
		Limit(1).
		First(&stepRec).Error
	if err != nil {
		return map[string]interface{}{}, nil
	}

	var cp PluginSessionStepCheckpoint
	err = db.Where("step_exec_id = ?", stepRec.ID).
		Order("sequence DESC").
		Limit(1).
		First(&cp).Error
	if err != nil {
		return map[string]interface{}{}, nil
	}

	result := map[string]interface{}{
		"completed_count": cp.CompletedCount,
		"total_count":     cp.TotalCount,
		"phase_note":      cp.PhaseNote,
	}
	if len(cp.PartialResults) > 0 {
		var partial []interface{}
		if err2 := json.Unmarshal(cp.PartialResults, &partial); err2 == nil {
			result["partial_results"] = partial
		}
	}
	return result, nil
}

// UpsertPluginArtifact inserts a new artifact record and syncs meta on the session.
func UpsertPluginArtifact(db *gorm.DB, artifact *PluginSessionArtifact) error {
	if err := db.Create(artifact).Error; err != nil {
		return err
	}

	// Sync the artifact value into plugin_sessions.meta.
	var rawValue interface{}
	if len(artifact.Value) > 0 {
		if err := json.Unmarshal(artifact.Value, &rawValue); err != nil {
			rawValue = string(artifact.Value)
		}
	}
	return UpdateSessionMeta(db, artifact.SessionID, map[string]interface{}{
		artifact.ArtifactID: rawValue,
	})
}

// LoadPluginSessionArtifacts returns the latest value for every artifact_id in the session.
func LoadPluginSessionArtifacts(db *gorm.DB, sessionID string) (map[string]interface{}, error) {
	var rows []PluginSessionArtifact
	// Fetch all artifacts for the session, ordered newest first.
	if err := db.Where("session_id = ?", sessionID).
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	result := make(map[string]interface{})
	for _, row := range rows {
		if _, seen := result[row.ArtifactID]; seen {
			continue // keep only the newest
		}
		var val interface{}
		if len(row.Value) > 0 {
			if err := json.Unmarshal(row.Value, &val); err != nil {
				val = string(row.Value)
			}
		}
		result[row.ArtifactID] = val
	}
	return result, nil
}

// StepContextEntry summarises one step execution for the ChatAgent decision context.
// Go builds this from artifacts + checkpoint; the LLM never sees raw artifact values.
type StepContextEntry struct {
	StepID  string `json:"step_id"`
	Status  string `json:"status"`  // done | interrupted | failed
	Summary string `json:"summary"` // step_summary artifact, or checkpoint fallback
}

// LoadStepsContext returns one StepContextEntry per distinct step that has been
// executed in the session (most recent attempt only).
// Summary is filled from the step_summary artifact when available; otherwise
// it falls back to a structured string built from the latest checkpoint fields.
func LoadStepsContext(db *gorm.DB, sessionID string) ([]StepContextEntry, error) {
	// Fetch the latest execution record for each distinct step.
	type stepRow struct {
		ID         string
		Step       string
		StepStatus string
	}
	var rows []stepRow
	if err := db.Raw(`
		SELECT DISTINCT ON (step) id, step, step_status
		FROM plugin_session_steps
		WHERE session_id = ?
		ORDER BY step, created_at DESC
	`, sessionID).Scan(&rows).Error; err != nil {
		return nil, err
	}

	// Load all step_summary artifacts for the session up front (one query).
	var summaryRows []PluginSessionArtifact
	if err := db.Where("session_id = ? AND artifact_id = 'step_summary'", sessionID).
		Order("created_at DESC").
		Find(&summaryRows).Error; err != nil {
		return nil, err
	}
	// Map step_exec_id -> summary string (newest artifact wins per step).
	summaryByExecID := make(map[string]string)
	for _, row := range summaryRows {
		if _, seen := summaryByExecID[row.StepExecID]; seen {
			continue
		}
		var val interface{}
		if err := json.Unmarshal(row.Value, &val); err == nil {
			summaryByExecID[row.StepExecID] = fmt.Sprintf("%v", val)
		}
	}

	entries := make([]StepContextEntry, 0, len(rows))
	for _, r := range rows {
		entry := StepContextEntry{
			StepID: r.Step,
			Status: r.StepStatus,
		}

		if s, ok := summaryByExecID[r.ID]; ok {
			entry.Summary = s
		} else if r.StepStatus == "interrupted" || r.StepStatus == "running" {
			// Fallback: build summary from latest checkpoint fields.
			var cp PluginSessionStepCheckpoint
			err := db.Where("step_exec_id = ?", r.ID).
				Order("sequence DESC").
				Limit(1).
				First(&cp).Error
			if err == nil && (cp.CompletedCount > 0 || cp.TotalCount > 0) {
				entry.Summary = fmt.Sprintf("interrupted, completed %d/%d",
					cp.CompletedCount, cp.TotalCount)
				if cp.PhaseNote != "" {
					entry.Summary += fmt.Sprintf(", phase: %s", cp.PhaseNote)
				}
			} else {
				entry.Summary = "interrupted"
			}
		}

		entries = append(entries, entry)
	}
	return entries, nil
}
