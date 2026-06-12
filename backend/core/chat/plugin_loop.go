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
				llmCfg, _ := currentReqBody["llm_config"].(map[string]interface{})
				runInterruptedStep(ctx, db, pythonBaseURL, pctx, lastRec, llmCfg, sseSender)
				_ = sseSender.Send([]byte("[DONE]"))
				return

			default:
				// Step is done; synthesize message for ChatAgent to decide next step.
				syntheticMsg := fmt.Sprintf(
					"Step %q completed. User confirmed to proceed. Please trigger the next appropriate step.",
					pctx.Step,
				)
				currentReqBody = overrideUserMessage(currentReqBody, syntheticMsg)
			}
		}
	}

	for turn := 0; turn < maxAutoTurns; turn++ {
		// 1. Call ChatAgent for this turn.
		injectPluginContext(currentReqBody, pctx, pctx.Step)
		// Use a fresh session_id each turn so lazyllm globals don't carry over stale state.
		convIDRaw0, _ := currentReqBody["conversation_id"].(string)
		if convIDRaw0 == "" {
			convIDRaw0 = convID
		}
		currentReqBody["session_id"] = upstreamSessionID(convIDRaw0)
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
			fmt.Printf("[Plugin] streamChatTurn returned nil stepTrigger err=%v session=%s\n",
				err, pctx.PluginSessionID)
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

		// 3. Defensive dependency check (Go-side guard; Python already validated).
		if depErr := checkStepDependencies(db, stepTrigger); depErr != nil {
			_ = sseSender.SendEvent("plugin_event", map[string]interface{}{
				"type":              "step_error",
				"plugin_session_id": pctx.PluginSessionID,
				"step_id":           stepTrigger.StepID,
				"error":             depErr.Error(),
			})
			break
		}

		// 4. Create step execution record.
		stepExecID := newConversationID()
		workspacePath := buildWorkspacePath(pctx.PluginSessionID, stepExecID)
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
		if err := orm.InsertPluginSessionStep(db, stepRec); err != nil {
			fmt.Printf("[Plugin] InsertPluginSessionStep failed session=%s step=%s err=%v\n",
				pctx.PluginSessionID, stepTrigger.StepID, err)
		}
		_ = sseSender.SendEvent("plugin_event", buildStepChangeEvent(pctx.PluginSessionID, stepTrigger.StepID))

		// 5. Run StepAgent — Python queries artifacts/checkpoint/previous_summary autonomously.
		llmConfig, _ := currentReqBody["llm_config"].(map[string]interface{})
		session, _ := orm.GetPluginSession(db, pctx.PluginSessionID)
		stepComplete := streamStepTurn(ctx, pythonBaseURL, stepTrigger, stepExecID,
			llmConfig, db, session, sseSender)

		if stepComplete == nil {
			break
		}

		if stepTrigger.StepMode == "human" {
			// 5b. Human mode: emit step_waiting and end this SSE stream.
			_ = sseSender.SendEvent("plugin_event",
				buildStepWaitingEvent(pctx.PluginSessionID, stepTrigger.StepID))
			_ = sseSender.Send([]byte("[DONE]"))
			return
		}

		// 5a. Auto mode: call DriverAgent, inject judgment, continue.
		// Python queries artifacts autonomously inside /api/plugin/driver.
		// Update pctx.Step so next turn's plugin_context.current_step_id is fresh.
		pctx.Step = stepTrigger.StepID
		llmCfgForDriver, _ := currentReqBody["llm_config"].(map[string]interface{})
		judgment, _ := CallPluginDriver(ctx, pythonBaseURL,
			stepTrigger.PluginSessionID, stepComplete.ResultSummary, llmCfgForDriver)

		// If DriverAgent signals "DONE", the plugin workflow is complete — no more turns needed.
		if strings.HasPrefix(strings.TrimSpace(judgment), "DONE") {
			fmt.Printf("[Plugin] streamPluginLoopFromTrigger: DriverAgent signaled DONE after step=%s session=%s\n",
				stepTrigger.StepID, pctx.PluginSessionID)
			break
		}

		currentReqBody = injectDriverJudgmentIntoReqBody(currentReqBody, judgment)
	}

	// Mark session as finished so the next user message is treated as a normal chat.
	if pctx.PluginSessionID != "" && db != nil {
		_ = orm.DeactivatePluginSession(db, pctx.PluginSessionID)
	}

	_ = sseSender.Send([]byte("[DONE]"))
}

// runInterruptedStep resumes an interrupted step directly (skip ChatAgent).
// Python queries checkpoint, artifacts, and workspace path autonomously.
// streamPluginLoopFromTrigger is the cold-start fast path: the first step_trigger
// was captured during the initial ChatAgent turn so there is no need for a second
// ChatAgent turn (advance_step). The first iteration executes firstTrigger directly;
// subsequent iterations (auto-mode multi-step) behave like streamPluginLoop.
func streamPluginLoopFromTrigger(
	ctx context.Context,
	db *gorm.DB,
	pythonBaseURL string,
	pctx PluginContext,
	firstTrigger *StepTriggerInfo,
	reqBody map[string]any,
	sseSender SSESender,
	convID string,
) {
	userID, _ := reqBody["user_id"].(string)
	numSteps := max(1, countSteps(db, pctx.PluginSessionID))
	maxAutoTurns := maxAutoTurnsPerStep * numSteps
	stepAttemptCount := make(map[string]int)
	currentReqBody := cloneReqBody(reqBody)

	for turn := 0; turn < maxAutoTurns; turn++ {
		var stepTrigger *StepTriggerInfo

		if turn == 0 {
			// First iteration: use the pre-captured trigger — skip streamChatTurn entirely.
			stepTrigger = firstTrigger
		} else {
			// Subsequent iterations: inject current plugin context so ChatAgent gets
			// advance_step tool and knows which plugin/step is active.
			injectPluginContext(currentReqBody, pctx, pctx.Step)
			// Use a fresh session_id for each ChatAgent turn so lazyllm globals
			// do not carry over stale state from a previous turn with the same id.
			convIDRaw, _ := currentReqBody["conversation_id"].(string)
			if convIDRaw == "" {
				convIDRaw = convID
			}
			currentReqBody["session_id"] = upstreamSessionID(convIDRaw)
			// Subsequent iterations: normal ChatAgent turn.
			var mountNumSteps int
			var updatedSessionID string
			var err error
			stepTrigger, mountNumSteps, updatedSessionID, err = streamChatTurn(
				ctx, pythonBaseURL, currentReqBody, sseSender, db, pctx.PluginSessionID, convID, userID,
			)
			if updatedSessionID != pctx.PluginSessionID {
				pctx.PluginSessionID = updatedSessionID
			}
			if mountNumSteps > 0 {
				numSteps = mountNumSteps
				maxAutoTurns = maxAutoTurnsPerStep * numSteps
			}
			if err != nil || stepTrigger == nil {
				fmt.Printf("[Plugin] streamPluginLoopFromTrigger: streamChatTurn returned nil "+
					"stepTrigger err=%v session=%s\n", err, pctx.PluginSessionID)
				break
			}
		}

		// Per-step retry limit check (auto mode only).
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

		// Defensive dependency check.
		if depErr := checkStepDependencies(db, stepTrigger); depErr != nil {
			_ = sseSender.SendEvent("plugin_event", map[string]interface{}{
				"type":              "step_error",
				"plugin_session_id": pctx.PluginSessionID,
				"step_id":           stepTrigger.StepID,
				"error":             depErr.Error(),
			})
			break
		}

		// Create step execution record.
		stepExecID := newConversationID()
		workspacePath := buildWorkspacePath(pctx.PluginSessionID, stepExecID)
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
		if err := orm.InsertPluginSessionStep(db, stepRec); err != nil {
			fmt.Printf("[Plugin] streamPluginLoopFromTrigger: InsertPluginSessionStep failed "+
				"session=%s step=%s err=%v\n", pctx.PluginSessionID, stepTrigger.StepID, err)
		}
		_ = sseSender.SendEvent("plugin_event", buildStepChangeEvent(pctx.PluginSessionID, stepTrigger.StepID))

		// Run StepAgent.
		llmConfig, _ := currentReqBody["llm_config"].(map[string]interface{})
		session, _ := orm.GetPluginSession(db, pctx.PluginSessionID)
		fmt.Printf("[Plugin] streamPluginLoopFromTrigger: calling streamStepTurn turn=%d step=%s session=%s\n",
			turn, stepTrigger.StepID, pctx.PluginSessionID)
		stepComplete := streamStepTurn(ctx, pythonBaseURL, stepTrigger, stepExecID,
			llmConfig, db, session, sseSender)

		if stepComplete == nil {
			fmt.Printf("[Plugin] streamPluginLoopFromTrigger: streamStepTurn nil turn=%d step=%s session=%s\n",
				turn, stepTrigger.StepID, pctx.PluginSessionID)
			break
		}

		if stepTrigger.StepMode == "human" {
			_ = sseSender.SendEvent("plugin_event",
				buildStepWaitingEvent(pctx.PluginSessionID, stepTrigger.StepID))
			_ = sseSender.Send([]byte("[DONE]"))
			return
		}

		// Auto mode: call DriverAgent, inject judgment, continue.
		// Update pctx.Step so next turn's plugin_context.current_step_id is fresh.
		pctx.Step = stepTrigger.StepID
		llmCfgForDriver, _ := currentReqBody["llm_config"].(map[string]interface{})
		judgment, _ := CallPluginDriver(ctx, pythonBaseURL,
			stepTrigger.PluginSessionID, stepComplete.ResultSummary, llmCfgForDriver)

		// If DriverAgent signals "DONE", the plugin workflow is complete.
		if strings.HasPrefix(strings.TrimSpace(judgment), "DONE") {
			fmt.Printf("[Plugin] streamPluginLoopFromTrigger: DriverAgent signaled DONE after step=%s session=%s\n",
				stepTrigger.StepID, pctx.PluginSessionID)
			break
		}

		currentReqBody = injectDriverJudgmentIntoReqBody(currentReqBody, judgment)
	}

	if pctx.PluginSessionID != "" && db != nil {
		_ = orm.DeactivatePluginSession(db, pctx.PluginSessionID)
	}

	_ = sseSender.Send([]byte("[DONE]"))
}
func runInterruptedStep(
	ctx context.Context,
	db *gorm.DB,
	pythonBaseURL string,
	pctx PluginContext,
	lastRec *orm.PluginSessionStep,
	llmConfig map[string]interface{},
	sseSender SSESender,
) {
	stepExecID := newConversationID()
	workspacePath := buildWorkspacePath(pctx.PluginSessionID, stepExecID)
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
	if err := orm.InsertPluginSessionStep(db, stepRec); err != nil {
		fmt.Printf("[Plugin] runInterruptedStep InsertPluginSessionStep failed session=%s step=%s err=%v\n",
			pctx.PluginSessionID, pctx.Step, err)
	}

	_ = sseSender.SendEvent("plugin_event", buildStepChangeEvent(pctx.PluginSessionID, pctx.Step))

	trigger := &StepTriggerInfo{
		PluginID: pctx.PluginID,
		StepID:   pctx.Step,
		StepMode: lastRec.StepMode,
	}
	session, _ := orm.GetPluginSession(db, pctx.PluginSessionID)
	streamStepTurn(ctx, pythonBaseURL, trigger, stepExecID, llmConfig, db, session, sseSender)
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
		// Accept both SSE "data: {...}" format and bare JSON lines (Python's sse_line format).
		if !strings.HasPrefix(line, "data: ") && !strings.HasPrefix(line, "{") {
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
				// Update current_step_id in DB so Python middleware knows which step is active.
				if db != nil && stepTrigger.PluginSessionID != "" && stepTrigger.StepID != "" {
					_ = orm.UpdateCurrentStep(db, stepTrigger.PluginSessionID, stepTrigger.StepID)
				}
				// Also emit a step_change event so the frontend stays in sync.
				if sseSender != nil {
					_ = sseSender.SendEvent("plugin_event", map[string]interface{}{
						"type":              "step_change",
						"plugin_session_id": stepTrigger.PluginSessionID,
						"step_id":           stepTrigger.StepID,
					})
				}
				// First step_trigger is enough — break immediately so Go can start
				// executing the step without waiting for Python's full agent loop
				// (which may keep calling advance_step for subsequent steps).
				break
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
// Python resolves plugin_id, step_id, workspace path, artifacts, checkpoint, and
// previous_summary autonomously from the DB using plugin_session_id.
func streamStepTurn(
	ctx context.Context,
	pythonBaseURL string,
	trigger *StepTriggerInfo,
	stepExecID string,
	llmConfig map[string]interface{},
	db *gorm.DB,
	session *orm.PluginSession,
	sseSender SSESender,
) *StepCompleteInfo {
	payload := map[string]interface{}{
		"plugin_session_id": trigger.PluginSessionID,
		"step_exec_id":      stepExecID,
		"user_input":        trigger.UserInput,
	}
	if llmConfig != nil {
		payload["llm_config"] = llmConfig
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
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("[Plugin] /api/plugin/step returned %d: %s\n", resp.StatusCode, string(body))
		return nil
	}

	var stepComplete *StepCompleteInfo
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1 MB per line
	for scanner.Scan() {
		line := scanner.Text()
		// Accept both SSE "data: {...}" format and bare JSON lines (Python's sse_line format).
		if !strings.HasPrefix(line, "data: ") && !strings.HasPrefix(line, "{") {
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

		// StepAgent must not emit step_trigger events; doing so would clobber
		// current_step_id in the DB and confuse the outer plugin loop.
		// Drop such events with a warning rather than delegating to handlePluginEvent.
		if ev.Type == "step_trigger" {
			fmt.Printf("[Core] [WARN] streamStepTurn: StepAgent emitted unexpected step_trigger "+
				"(step_id=%s session=%s) — ignored\n", ev.StepID, ev.PluginSessionID)
			continue
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

	// Build a minimal history so the second-turn ChatAgent has context:
	// the original user query as the first message, so the LLM knows it is
	// inside an image-generation (or other plugin) flow and must call advance_step.
	originalQuery, _ := clone["query"].(string)
	if originalQuery != "" {
		existingHist, _ := clone["history"].([]any)
		newEntry := map[string]any{"role": "user", "content": originalQuery}
		clone["history"] = append(existingHist, newEntry)
	}

	// Use judgment as the current-turn query so ReactAgent receives a non-empty input
	// and calls advance_step.
	clone["query"] = judgment
	return clone
}

func cloneReqBody(src map[string]any) map[string]any {
	b, _ := json.Marshal(src)
	var clone map[string]any
	_ = json.Unmarshal(b, &clone)
	return clone
}

// injectPluginContext writes the current plugin session state into reqBody["plugin_context"]
// so that each ChatAgent turn knows which plugin/step is active and gets the advance_step tool.
func injectPluginContext(reqBody map[string]any, pctx PluginContext, currentStepID string) {
	pc := map[string]any{
		"plugin_id":         pctx.PluginID,
		"plugin_session_id": pctx.PluginSessionID,
		"current_step_id":   currentStepID,
	}
	reqBody["plugin_context"] = pc
}

func buildWorkspacePath(sessionID, stepExecID string) string {
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
