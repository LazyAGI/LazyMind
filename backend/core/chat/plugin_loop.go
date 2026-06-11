package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

const maxAutoTurnsPerStep = 5

// ---- Plugin Event Loop ----

// streamPluginLoop drives the complete plugin auto/human execution loop.
// It is called from streamSingleAnswer when plugin_context is present.
// sseSender writes SSE frames directly to the frontend.
func streamPluginLoop(
	ctx context.Context,
	db *gorm.DB,
	pythonBaseURL string,
	pctx PluginContext,
	reqBody map[string]any,
	sseSender SSESender,
	convID string,
) {
	userID, _ := reqBody["user_id"].(string)
	// numSteps is updated from the mount event; default to 1 to avoid div-by-zero.
	numSteps := max(1, countSteps(db, pctx.PluginSessionID))
	maxAutoTurns := maxAutoTurnsPerStep * numSteps
	stepAttemptCount := make(map[string]int)
	currentReqBody := cloneReqBody(reqBody)

	// Handle advance=true: check the last step record to decide how to proceed.
	if pctx.Advance && pctx.Step != "" {
		lastRec, err := orm.QueryLatestStepRecord(db, pctx.PluginSessionID, pctx.Step)
		if err == nil && lastRec != nil {
			switch lastRec.StepStatus {
			case "running":
				// Still running; tell the frontend to wait.
				_ = sseSender.SendEvent("plugin_event", map[string]interface{}{
					"type":    "info",
					"message": "step is still running, please wait",
				})
				_ = sseSender.Send([]byte("[DONE]"))
				return

			case "interrupted":
				// Restore from checkpoint; skip ChatAgent for this step.
				runInterruptedStep(ctx, db, pythonBaseURL, pctx, lastRec, sseSender)
				_ = sseSender.Send([]byte("[DONE]"))
				return

			default:
				// Step is done; synthesize message for ChatAgent to decide next step.
				syntheticMsg := fmt.Sprintf(
					"Step %q completed. User confirmed to proceed. Please trigger the next appropriate step.",
					pctx.Step,
				)
				currentReqBody = overrideUserMessage(currentReqBody, syntheticMsg)
				// Inject step summaries so ChatAgent has semantic context for the decision.
				currentReqBody = injectStepsContext(currentReqBody, db, pctx.PluginSessionID)
			}
		}
	}

	for turn := 0; turn < maxAutoTurns; turn++ {
		// 1. Call ChatAgent for this turn.
		stepTrigger, mountNumSteps, updatedSessionID, err := streamChatTurn(ctx, pythonBaseURL, currentReqBody, sseSender, db, pctx.PluginSessionID, convID, userID)
		if updatedSessionID != pctx.PluginSessionID {
			// First mount assigned a real session ID; update pctx for all subsequent calls.
			pctx.PluginSessionID = updatedSessionID
			// Also update plugin_context in reqBody so subsequent ChatAgent turns carry the real ID.
			if pc, ok := currentReqBody["plugin_context"].(map[string]any); ok {
				pc["plugin_session_id"] = updatedSessionID
			}
		}
		if mountNumSteps > 0 {
			// Update maxAutoTurns when mount event provides authoritative num_steps.
			numSteps = mountNumSteps
			maxAutoTurns = maxAutoTurnsPerStep * numSteps
		}
		if err != nil || stepTrigger == nil {
			break // Natural end of conversation or error.
		}

		// 2. Per-step retry limit check (auto mode only).
		if stepTrigger.StepMode == "auto" {
			stepAttemptCount[stepTrigger.StepID]++
			reachable := max(1, stepTrigger.ReachableStepCount)
			maxRetries := max(1, int(math.Floor(float64(maxAutoTurns-turn)/float64(reachable)*1.5)))
			if stepAttemptCount[stepTrigger.StepID] > maxRetries {
				_ = sseSender.SendEvent("plugin_event", map[string]interface{}{
					"type":              "step_error",
					"plugin_session_id": pctx.PluginSessionID,
					"step_id":           stepTrigger.StepID,
					"error":             fmt.Sprintf("step %q exceeded max retries (%d)", stepTrigger.StepID, maxRetries),
				})
				break
			}
		}

		// 3. Create step execution record.
		stepExecID := newConversationID()
		workspacePath := buildWorkspacePath(pythonBaseURL, pctx.PluginSessionID, stepExecID)
		_ = os.MkdirAll(workspacePath, 0755)

		stepRec := &orm.PluginSessionStep{
			ID:            stepExecID,
			SessionID:     pctx.PluginSessionID,
			Step:          stepTrigger.StepID,
			StepMode:      stepTrigger.StepMode,
			StepStatus:    "running",
			LastHeartbeat: time.Now(),
			WorkspacePath: workspacePath,
		}
		_ = orm.InsertPluginSessionStep(db, stepRec)

		// Emit step_change to frontend.
		_ = sseSender.SendEvent("plugin_event", buildStepChangeEvent(pctx.PluginSessionID, stepTrigger.StepID))

		// 4. Load checkpoint and artifacts.
		checkpoint, _ := orm.LoadLatestCheckpoint(db, pctx.PluginSessionID, stepTrigger.StepID)
		artifacts, _ := orm.LoadPluginSessionArtifacts(db, pctx.PluginSessionID)

		// 5. Run StepAgent.
		session, _ := orm.GetPluginSession(db, pctx.PluginSessionID)
		stepComplete := streamStepTurn(ctx, pythonBaseURL, stepTrigger, stepExecID,
			workspacePath, artifacts, checkpoint, db, session, sseSender)

		if stepComplete == nil {
			break
		}

		if stepTrigger.StepMode == "human" {
			// 6b. Human mode: emit step_waiting and end this SSE stream.
			_ = sseSender.SendEvent("plugin_event",
				buildStepWaitingEvent(pctx.PluginSessionID, stepTrigger.StepID))
			_ = sseSender.Send([]byte("[DONE]"))
			return
		}

		// 6a. Auto mode: call DriverAgent, inject judgment, continue.
		freshArtifacts, _ := orm.LoadPluginSessionArtifacts(db, pctx.PluginSessionID)
		judgment, _ := CallPluginDriver(ctx, pythonBaseURL,
			stepTrigger.PluginID, stepTrigger.StepID,
			stepComplete.ResultSummary, freshArtifacts, turn+1)
		currentReqBody = injectDriverJudgmentIntoReqBody(currentReqBody, judgment)
		// Advance plugin_context.step so Python knows which step just completed,
		// allowing get_reachable_steps to return the *next* step rather than the same one.
		currentReqBody = advancePluginContextStep(currentReqBody, stepTrigger.StepID)
		// Inject fresh step summaries so ChatAgent can make an informed decision
		// without reading the full conversation history.
		currentReqBody = injectStepsContext(currentReqBody, db, pctx.PluginSessionID)
	}

	_ = sseSender.Send([]byte("[DONE]"))
}

// runInterruptedStep resumes an interrupted step directly (skip ChatAgent).
func runInterruptedStep(
	ctx context.Context,
	db *gorm.DB,
	pythonBaseURL string,
	pctx PluginContext,
	lastRec *orm.PluginSessionStep,
	sseSender SSESender,
) {
	checkpoint, _ := orm.LoadLatestCheckpoint(db, pctx.PluginSessionID, pctx.Step)
	artifacts, _ := orm.LoadPluginSessionArtifacts(db, pctx.PluginSessionID)
	stepExecID := newConversationID()
	workspacePath := buildWorkspacePath(pythonBaseURL, pctx.PluginSessionID, stepExecID)
	_ = os.MkdirAll(workspacePath, 0755)

	stepRec := &orm.PluginSessionStep{
		ID:            stepExecID,
		SessionID:     pctx.PluginSessionID,
		Step:          pctx.Step,
		StepMode:      lastRec.StepMode,
		StepStatus:    "running",
		LastHeartbeat: time.Now(),
		WorkspacePath: workspacePath,
	}
	_ = orm.InsertPluginSessionStep(db, stepRec)

	_ = sseSender.SendEvent("plugin_event", buildStepChangeEvent(pctx.PluginSessionID, pctx.Step))

	trigger := &StepTriggerInfo{
		PluginSessionID: pctx.PluginSessionID,
		PluginID:        pctx.PluginID,
		StepID:          pctx.Step,
		StepMode:        lastRec.StepMode,
	}
	session, _ := orm.GetPluginSession(db, pctx.PluginSessionID)
	streamStepTurn(ctx, pythonBaseURL, trigger, stepExecID, workspacePath,
		artifacts, checkpoint, db, session, sseSender)
}

// streamChatTurn calls the Python /api/chat/stream endpoint for one turn.
// It returns the first step_trigger event found (or nil), the num_steps from the mount
// event (0 if no mount occurred), the (possibly updated) pluginSessionID, and any transport error.
// Text deltas are forwarded to sseSender until a step_trigger is received.
func streamChatTurn(
	ctx context.Context,
	pythonBaseURL string,
	reqBody map[string]any,
	sseSender SSESender,
	db *gorm.DB,
	pluginSessionID string,
	convID string,
	userID string,
) (*StepTriggerInfo, int, string, error) {
	b, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		pythonBaseURL+"/api/chat/stream", bytes.NewReader(b))
	if err != nil {
		return nil, 0, pluginSessionID, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, pluginSessionID, err
	}
	defer resp.Body.Close()

	var stepTrigger *StepTriggerInfo
	triggered := false
	mountNumSteps := 0

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1 MB per line
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		// Try to parse as plugin event first.
		ev := parsePluginEventFromSSELine(line)
		if ev != nil {
			if ev.Type == "mount" && db != nil && pluginSessionID == "" {
				// Assign a real session ID (discard the placeholder from Python).
				realSessionID := newPluginID()
				newSession := &orm.PluginSession{
					ID:             realSessionID,
					PluginID:       ev.PluginID,
					ConversationID: convID,
					CreateUserID:   userID,
				}
				_ = orm.CreatePluginSession(db, newSession)
				// Replace placeholder with real ID before forwarding to frontend.
				ev.PluginSessionID = realSessionID
				// Also propagate real ID for the rest of this SSE scan.
				pluginSessionID = realSessionID
				_ = sseSender.SendEvent("plugin_event", ev)
				if ev.NumSteps > 0 {
					mountNumSteps = ev.NumSteps
				}
			} else if ev.Type == "mount" && ev.NumSteps > 0 {
				mountNumSteps = ev.NumSteps
				_ = sseSender.SendEvent("plugin_event", ev)
			} else if ev.Type == "step_trigger" && !triggered {
				triggered = true
				stepTrigger = &StepTriggerInfo{
					PluginSessionID:    ev.PluginSessionID,
					PluginID:           ev.PluginID,
					StepID:             ev.StepID,
					StepMode:           ev.StepMode,
					UserInput:          ev.UserInput,
					Inputs:             ev.Inputs,
					ReachableStepCount: ev.ReachableStepCount,
				}
				if stepTrigger.PluginSessionID == "" {
					stepTrigger.PluginSessionID = pluginSessionID
				}
				// Do not forward step_trigger to frontend.
			}
			continue
		}

		// Forward text delta chunks when no step_trigger yet received.
		if !triggered {
			_ = sseSender.Send([]byte(data))
		}
	}

	return stepTrigger, mountNumSteps, pluginSessionID, nil
}

// streamStepTurn calls /api/plugin/step and processes the resulting SSE stream.
func streamStepTurn(
	ctx context.Context,
	pythonBaseURL string,
	trigger *StepTriggerInfo,
	stepExecID, workspacePath string,
	artifacts, checkpoint map[string]interface{},
	db *gorm.DB,
	session *orm.PluginSession,
	sseSender SSESender,
) *StepCompleteInfo {
	payload := map[string]interface{}{
		"plugin_id":         trigger.PluginID,
		"step_id":           trigger.StepID,
		"step_exec_id":      stepExecID,
		"plugin_session_id": trigger.PluginSessionID,
		"step_workspace":    workspacePath,
		"user_input":        trigger.UserInput,
		"artifacts":         artifacts,
		"checkpoint":        checkpoint,
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		pythonBaseURL+"/api/plugin/step", bytes.NewReader(b))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 600 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var stepComplete *StepCompleteInfo
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1 MB per line
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var ev PluginEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}

		// Update heartbeat on each event.
		if db != nil {
			_ = orm.UpdateStepHeartbeat(db, stepExecID)
		}

		_, complete, _ := handlePluginEvent(ev, db, sseSender, session, stepExecID)
		if complete != nil {
			// handlePluginEvent sets StepExecID; fill in the trigger context fields.
			complete.PluginSessionID = trigger.PluginSessionID
			complete.StepID = trigger.StepID
			complete.StepMode = trigger.StepMode
			stepComplete = complete
		}
		if ev.Type == "step_error" {
			// step_error was already forwarded to frontend by handlePluginEvent.
			return nil
		}
	}

	return stepComplete
}

// ---- Helpers ----

// advancePluginContextStep updates the plugin_context.step field in a cloned reqBody
// to reflect the step that just completed. Python uses this to compute reachable steps
// for the next ChatAgent turn, preventing re-triggering of the same step.
// It also injects a fresh steps_context summary fetched from the database.
func advancePluginContextStep(reqBody map[string]any, completedStepID string) map[string]any {
	clone := cloneReqBody(reqBody)
	if pc, ok := clone["plugin_context"].(map[string]any); ok {
		pc["step"] = completedStepID
		pc["advance"] = true
		clone["plugin_context"] = pc
	}
	return clone
}

// injectStepsContext fetches step summaries from the DB and writes them into
// plugin_context.steps_context so ChatAgent can make an informed next-step decision
// without reading the full conversation history.
func injectStepsContext(reqBody map[string]any, db *gorm.DB, sessionID string) map[string]any {
	if db == nil || sessionID == "" {
		return reqBody
	}
	entries, err := orm.LoadStepsContext(db, sessionID)
	if err != nil || len(entries) == 0 {
		return reqBody
	}
	clone := cloneReqBody(reqBody)
	if pc, ok := clone["plugin_context"].(map[string]any); ok {
		pc["steps_context"] = entries
		clone["plugin_context"] = pc
	}
	return clone
}

func overrideUserMessage(reqBody map[string]any, msg string) map[string]any {
	clone := cloneReqBody(reqBody)
	clone["query"] = msg
	if hist, ok := clone["history"].([]any); ok {
		// Replace the last user-role message with the synthetic message,
		// or append a new one if no user message is found at the tail.
		replaced := false
		for i := len(hist) - 1; i >= 0; i-- {
			if entry, ok := hist[i].(map[string]any); ok {
				if entry["role"] == "user" {
					newEntry := make(map[string]any, len(entry))
					for k, v := range entry {
						newEntry[k] = v
					}
					newEntry["content"] = msg
					updated := make([]any, len(hist))
					copy(updated, hist)
					updated[i] = newEntry
					clone["history"] = updated
					replaced = true
					break
				}
			}
		}
		if !replaced {
			clone["history"] = append(hist, map[string]any{"role": "user", "content": msg})
		}
	}
	return clone
}

func injectDriverJudgmentIntoReqBody(reqBody map[string]any, judgment string) map[string]any {
	clone := cloneReqBody(reqBody)
	hist, _ := clone["history"].([]any)
	hist = append(hist, map[string]any{"role": "user", "content": judgment})
	clone["history"] = hist
	// Do NOT also set query: Python's handle_chat uses query as the current-turn input
	// and appends it to history internally. Setting both would produce two identical
	// user messages in the context window.
	// The judgment already appears as the last history entry; the agent will pick it up.
	clone["query"] = ""
	return clone
}

func cloneReqBody(src map[string]any) map[string]any {
	b, _ := json.Marshal(src)
	var clone map[string]any
	_ = json.Unmarshal(b, &clone)
	return clone
}

func buildWorkspacePath(pythonBaseURL, sessionID, stepExecID string) string {
	base := os.Getenv("PLUGIN_WORKSPACE_BASE")
	if base == "" {
		base = "/data/plugin_workspace"
	}
	return base + "/" + sessionID + "/" + stepExecID
}

// extractCheckpointFields safely extracts typed fields from an unmarshaled JSON value.
// JSON numbers unmarshal as float64; strings and nested objects are handled gracefully.
func extractCheckpointFields(cp *orm.PluginSessionStepCheckpoint, valMap map[string]interface{}) {
	if v, ok := valMap["completed_count"].(float64); ok {
		cp.CompletedCount = int(v)
	}
	if v, ok := valMap["total_count"].(float64); ok {
		cp.TotalCount = int(v)
	}
	if v, ok := valMap["phase_note"].(string); ok {
		cp.PhaseNote = v
	}
	if pr, ok := valMap["partial_results"]; ok {
		prJSON, _ := json.Marshal(pr)
		cp.PartialResults = prJSON
	}
}

func countSteps(db *gorm.DB, sessionID string) int {
	if db == nil || sessionID == "" {
		return 1
	}
	var count int64
	db.Model(&orm.PluginSessionStep{}).
		Where("session_id = ?", sessionID).
		Distinct("step").
		Count(&count)
	// If no steps have been executed yet (fresh session), return a conservative
	// default so maxAutoTurns is not artificially small before the mount event
	// provides the authoritative num_steps value.
	if count == 0 {
		return 5
	}
	return int(count)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---- SSE response builder ----

// PluginSSESender wraps an http.ResponseWriter to implement SSESender.
type PluginSSESender struct {
	W       http.ResponseWriter
	Flusher http.Flusher
}

func (s *PluginSSESender) Send(data []byte) error {
	frame := append([]byte("data: "), data...)
	frame = append(frame, '\n', '\n')
	_, err := s.W.Write(frame)
	if s.Flusher != nil {
		s.Flusher.Flush()
	}
	return err
}

func (s *PluginSSESender) SendEvent(eventType string, payload interface{}) error {
	b, err := json.Marshal(map[string]interface{}{
		"type": "plugin_event",
		"data": payload,
	})
	if err != nil {
		return err
	}
	return s.Send(b)
}

// ---- Stream reader for StepAgent responses ----

// readSSEEvents reads SSE data from an io.Reader and sends parsed events to a channel.
func readSSEEvents(r io.Reader) <-chan string {
	ch := make(chan string, 64)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				ch <- strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return ch
}
