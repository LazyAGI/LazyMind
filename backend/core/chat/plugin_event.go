package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

// ---- Core data structures ----

// PluginEvent is the canonical event structure used by both ChatAgent and StepAgent SSE streams.
type PluginEvent struct {
	Type               string          `json:"type"`
	PluginSessionID    string          `json:"plugin_session_id,omitempty"`
	PluginID           string          `json:"plugin_id,omitempty"`
	StepID             string          `json:"step_id,omitempty"`
	StepMode           string          `json:"step_mode,omitempty"`
	StepExecID         string          `json:"step_exec_id,omitempty"`
	ArtifactID         string          `json:"artifact_id,omitempty"`
	Value              interface{}     `json:"value,omitempty"`
	ResultSummary      string          `json:"result_summary,omitempty"`
	Progress           float64         `json:"progress,omitempty"`
	Message            string          `json:"message,omitempty"`
	UserInput          string          `json:"user_input,omitempty"`
	Error              string          `json:"error,omitempty"`
	Inputs             []StepInputSpec `json:"inputs,omitempty"`
	NumSteps           int             `json:"num_steps,omitempty"`            // mount: total declared steps for maxAutoTurns
	ReachableStepCount int             `json:"reachable_step_count,omitempty"` // step_trigger: reachable steps from triggered step
}

// StepTriggerInfo is extracted from a step_trigger event emitted by ChatAgent.
type StepTriggerInfo struct {
	PluginSessionID    string
	PluginID           string
	StepID             string
	StepMode           string // 'auto' | 'human'
	UserInput          string
	Inputs             []StepInputSpec
	ReachableStepCount int // number of reachable steps from this step (for retry limit calc)
}

// StepInputSpec describes a dependency artifact for a step.
type StepInputSpec struct {
	ArtifactID string `json:"artifact_id"`
	Required   bool   `json:"required"`
}

// StepCompleteInfo is extracted from a step_complete event emitted by StepAgent.
type StepCompleteInfo struct {
	StepExecID      string
	PluginSessionID string
	StepID          string
	StepMode        string
	ResultSummary   string
}

// PluginContext is constructed internally by Go from DB; not parsed from frontend requests.
// The frontend only supplies the boolean `advance` flag; all other fields come from plugin_sessions.
type PluginContext struct {
	PluginSessionID string
	PluginID        string
	Step            string
	Advance         bool
}

// ---- SSE sender abstraction ----

// SSESender writes SSE frames to the HTTP response stream.
type SSESender interface {
	Send(data []byte) error
	SendEvent(eventType string, payload interface{}) error
}

type httpSSESender struct {
	w http.ResponseWriter
}

func (s *httpSSESender) Send(data []byte) error {
	frame := append([]byte("data: "), data...)
	frame = append(frame, '\n', '\n')
	_, err := s.w.Write(frame)
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
	return err
}

func (s *httpSSESender) SendEvent(eventType string, payload interface{}) error {
	b, err := json.Marshal(map[string]interface{}{
		"type": "plugin_event",
		"data": payload,
	})
	if err != nil {
		return err
	}
	return s.Send(b)
}

// ---- Event handler ----

// handlePluginEvent processes one plugin event from a ChatAgent or StepAgent SSE stream.
// Returns non-nil StepTriggerInfo or StepCompleteInfo when those events are encountered.
// The sseSender is nil-safe: pass nil when the caller handles forwarding itself.
func handlePluginEvent(
	event PluginEvent,
	db *gorm.DB,
	sseSender SSESender,
	session *orm.PluginSession,
	stepExecID string,
) (*StepTriggerInfo, *StepCompleteInfo, error) {
	switch event.Type {

	case "mount":
		if db != nil && session == nil {
			newSession := &orm.PluginSession{
				ID:       event.PluginSessionID,
				PluginID: event.PluginID,
			}
			if err := orm.CreatePluginSession(db, newSession); err != nil {
				return nil, nil, fmt.Errorf("CreatePluginSession: %w", err)
			}
		}
		if sseSender != nil {
			_ = sseSender.SendEvent("plugin_event", event)
		}

	case "step_trigger":
		info := &StepTriggerInfo{
			PluginSessionID: event.PluginSessionID,
			PluginID:        event.PluginID,
			StepID:          event.StepID,
			StepMode:        event.StepMode,
			UserInput:       event.UserInput,
			Inputs:          event.Inputs,
		}
		if db != nil {
			_ = orm.UpdateCurrentStep(db, event.PluginSessionID, event.StepID)
		}
		if sseSender != nil {
			stepChange := map[string]interface{}{
				"type":              "step_change",
				"plugin_session_id": event.PluginSessionID,
				"step_id":           event.StepID,
			}
			_ = sseSender.SendEvent("plugin_event", stepChange)
		}
		return info, nil, nil

	case "artifact":
		if db != nil && session != nil {
			valJSON, _ := json.Marshal(event.Value)
			artifact := &orm.PluginSessionArtifact{
				ID:         newPluginID(),
				SessionID:  session.ID,
				StepExecID: stepExecID,
				ArtifactID: event.ArtifactID,
				Value:      valJSON,
			}
			_ = orm.UpsertPluginArtifact(db, artifact)
		}
		if sseSender != nil {
			_ = sseSender.SendEvent("plugin_event", event)
		}

	case "checkpoint":
		if db != nil {
			cp := &orm.PluginSessionStepCheckpoint{
				ID:         newPluginID(),
				StepExecID: stepExecID,
			}
			if valMap, ok := event.Value.(map[string]interface{}); ok {
				extractCheckpointFields(cp, valMap)
			}
			_ = orm.InsertPluginCheckpoint(db, cp)
		}
		// Checkpoints are not forwarded to the frontend.

	case "step_complete":
		if db != nil {
			_ = orm.UpdateStepStatus(db, stepExecID, "done")
		}
		info := &StepCompleteInfo{
			StepExecID:    stepExecID,
			ResultSummary: event.ResultSummary,
		}
		if sseSender != nil {
			stepDone := map[string]interface{}{
				"type":              "step_done",
				"plugin_session_id": event.PluginSessionID,
				"step_id":           event.StepID,
				"step_exec_id":      stepExecID,
			}
			_ = sseSender.SendEvent("plugin_event", stepDone)
		}
		return nil, info, nil

	case "step_error":
		if db != nil {
			_ = orm.UpdateStepStatus(db, stepExecID, "failed")
		}
		if sseSender != nil {
			_ = sseSender.SendEvent("plugin_event", event)
		}

	case "progress":
		if sseSender != nil {
			_ = sseSender.SendEvent("plugin_event", event)
		}
	}

	return nil, nil, nil
}

// newPluginID generates a UUID-like ID for new plugin records.
func newPluginID() string {
	return "ps-" + newConversationID()
}

// ---- DriverAgent caller ----

// CallPluginDriver posts a request to /api/plugin/driver and returns the judgment text.
// Python resolves plugin_id, step_id, artifacts, and attempt count autonomously from the DB.
// On any error, returns a safe fallback string and does not propagate the error.
func CallPluginDriver(
	ctx context.Context,
	pythonBaseURL string,
	pluginSessionID, stepResult string,
) (string, error) {
	payload := map[string]interface{}{
		"plugin_session_id": pluginSessionID,
		"step_result":       stepResult,
	}
	b, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		pythonBaseURL+"/api/plugin/driver", bytes.NewReader(b))
	if err != nil {
		return "Step completed. Proceed.", nil
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("Driver call failed (%v). Proceeding.", err), nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Judgment string `json:"judgment"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Judgment == "" {
		return "Step completed. Proceed.", nil
	}
	return result.Judgment, nil
}

// ---- Dependency checker ----

// checkStepDependencies is a defensive assertion on Go's side.
// The primary validation is done in Python's trigger_plugin_step().
// This function blocks StepAgent invocation if Python-side validation was bypassed.
func checkStepDependencies(db *gorm.DB, trigger *StepTriggerInfo) error {
	if db == nil || len(trigger.Inputs) == 0 {
		return nil
	}
	for _, inp := range trigger.Inputs {
		// Find the most recent artifact record for this artifact_id.
		var artifactRec orm.PluginSessionArtifact
		err := db.Where("session_id = ? AND artifact_id = ?",
			trigger.PluginSessionID, inp.ArtifactID).
			Order("created_at DESC").
			Limit(1).
			First(&artifactRec).Error

		if err != nil {
			// Artifact has never been produced.
			if !inp.Required {
				continue
			}
			return fmt.Errorf("required artifact %q has never been produced", inp.ArtifactID)
		}

		// Artifact exists; check its producer step's latest execution status.
		var stepRec orm.PluginSessionStep
		err = db.Where("session_id = ? AND id = ?",
			trigger.PluginSessionID, artifactRec.StepExecID).
			First(&stepRec).Error
		if err != nil {
			continue // Cannot find step record; allow (Python already validated).
		}

		switch stepRec.StepStatus {
		case "running", "failed", "interrupted":
			// Regardless of required/optional: a step that is in-flight or failed blocks the trigger.
			return fmt.Errorf("artifact %q: producer step %q is in status %q",
				inp.ArtifactID, stepRec.Step, stepRec.StepStatus)

		case "abandoned":
			// Per spec §1.8: abandoned → fall back to the most recent "done" execution of this step.
			// If no done execution exists, treat as "never produced" (optional: ok; required: error).
			// Use a subquery instead of GORM Joins to guarantee INNER JOIN semantics across versions.
			doneExecIDs := db.Model(&orm.PluginSessionStep{}).
				Select("id").
				Where("session_id = ? AND step = ? AND step_status = 'done'",
					trigger.PluginSessionID, stepRec.Step)
			var doneArtifact orm.PluginSessionArtifact
			doneErr := db.
				Where("session_id = ? AND artifact_id = ? AND step_exec_id IN (?)",
					trigger.PluginSessionID, inp.ArtifactID, doneExecIDs).
				Order("created_at DESC").
				Limit(1).
				First(&doneArtifact).Error
			if doneErr != nil {
				// No done execution has ever produced this artifact.
				if !inp.Required {
					continue // Optional: treat as null, StepAgent will self-fallback.
				}
				return fmt.Errorf("required artifact %q: producer step %q is abandoned and no prior done execution found",
					inp.ArtifactID, stepRec.Step)
			}
			// A past done execution exists; the artifact is available.
		}
	}
	return nil
}

// ---- Utility helpers ----

// InjectDriverJudgment appends the driver judgment as a user message in the history.
func InjectDriverJudgment(history []map[string]string, judgment string) []map[string]string {
	return append(history, map[string]string{
		"role":    "user",
		"content": judgment,
	})
}

// buildStepWaitingEvent creates a step_waiting event payload.
func buildStepWaitingEvent(sessionID, stepID string) map[string]interface{} {
	return map[string]interface{}{
		"type":              "step_waiting",
		"plugin_session_id": sessionID,
		"step_id":           stepID,
	}
}

// buildStepChangeEvent creates a step_change event payload.
func buildStepChangeEvent(sessionID, stepID string) map[string]interface{} {
	return map[string]interface{}{
		"type":              "step_change",
		"plugin_session_id": sessionID,
		"step_id":           stepID,
	}
}

// parsePluginEventFromSSELine attempts to parse a plugin event from an SSE data line.
//
// Two wire formats are handled:
//  1. Python ChatAgent:  {"type": "plugin_event", "data": {<actual event>}}
//  2. StepAgent / Go:    {<actual event>}  (event fields at top level)
//
// Returns nil if the line does not contain a recognisable plugin event.
func parsePluginEventFromSSELine(line string) *PluginEvent {
	line = strings.TrimPrefix(line, "data: ")
	if !strings.Contains(line, `"type"`) {
		return nil
	}
	var outer map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &outer); err != nil {
		return nil
	}

	// Format 1: {"type": "plugin_event", "data": {...}}
	if typeRaw, ok := outer["type"]; ok {
		var typeStr string
		if json.Unmarshal(typeRaw, &typeStr) == nil && typeStr == "plugin_event" {
			if dataRaw, ok2 := outer["data"]; ok2 {
				var ev PluginEvent
				if json.Unmarshal(dataRaw, &ev) == nil && ev.Type != "" {
					return &ev
				}
			}
			return nil
		}
	}

	// Format 2: event fields at top level (StepAgent or Go-generated).
	var ev PluginEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return nil
	}
	if ev.Type == "" {
		return nil
	}
	return &ev
}

// ---- recordingSSESender ----

// recordingSSESender wraps httpSSESender and collects plain-text delta content
// from non-plugin SSE frames. This allows the caller to persist the full response
// to ChatHistory even when using streamChatTurn (which streams directly to w).
type recordingSSESender struct {
	httpSSESender
	buf strings.Builder
}

func newRecordingSSESender(w http.ResponseWriter) *recordingSSESender {
	return &recordingSSESender{httpSSESender: httpSSESender{w: w}}
}

func (r *recordingSSESender) Send(data []byte) error {
	// Try to extract delta text from the SSE payload for recording.
	if len(data) > 0 && data[0] == '{' {
		var obj struct {
			Delta string `json:"delta"`
		}
		if json.Unmarshal(data, &obj) == nil && obj.Delta != "" {
			r.buf.WriteString(obj.Delta)
		}
	}
	return r.httpSSESender.Send(data)
}

// RecordedText returns the accumulated text deltas received so far.
func (r *recordingSSESender) RecordedText() string { return r.buf.String() }
