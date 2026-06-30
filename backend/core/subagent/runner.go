package subagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/state"
)

// runPath is the algorithm-layer SubAgent execution endpoint.
const runPath = "/api/subagent/run"

// subagentRunTimeout bounds a single SubAgent execution. Long tasks rely on ctx, not this ceiling.
const subagentRunTimeout = 2 * time.Hour

// RunRequest is the body posted to the algorithm layer /api/subagent/run.
// task_id doubles as the request sid (independent FileSystemQueue bucket).
//
// objective, input_artifact_keys, and output_artifact_keys are intentionally
// omitted: the Python runner reads those from the sub_agent_tasks DB record.
// tools is still forwarded for non-plugin_step agent types; plugin_step tasks
// resolve their tools from plugin_loader at execution time.
type RunRequest struct {
	TaskID        string         `json:"task_id"`
	AgentType     string         `json:"agent_type"`
	Params        map[string]any `json:"params,omitempty"`
	WorkspacePath string         `json:"workspace_path"`
	Tools         []string       `json:"tools,omitempty"`
	DBDSN         string         `json:"db_dsn"`
	Resume        bool           `json:"resume"`
	LLMConfig     map[string]any `json:"llm_config,omitempty"`
	ToolConfig    map[string]any `json:"tool_config,omitempty"`
}

// TaskEvent is one event emitted by the SubAgent SSE stream.
type TaskEvent struct {
	Type         string          `json:"type"`
	TaskID       string          `json:"task_id,omitempty"`
	Progress     int             `json:"progress,omitempty"`
	CurrentPhase string          `json:"current_phase,omitempty"`
	EstimatedSec int             `json:"estimated_sec,omitempty"`
	ArtifactKey  string          `json:"artifact_key,omitempty"`
	ContentType  string          `json:"content_type,omitempty"`
	Seq          int             `json:"seq,omitempty"`
	Value        json.RawMessage `json:"value,omitempty"`
	Status       string          `json:"status,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	Message      string          `json:"message,omitempty"`
	// Tool step events forwarded from SubAgent runner for frontend display.
	ToolCalls   json.RawMessage `json:"tool_calls,omitempty"`
	ToolResults json.RawMessage `json:"tool_results,omitempty"`
	// Text / think streaming content.
	Text  string `json:"text,omitempty"`
	Think string `json:"think,omitempty"`
}

// algoServiceURL resolves the algorithm chat-service base URL (same host as /api/chat/stream).
func algoServiceURL() string {
	return common.ChatServiceEndpoint()
}

// Run posts to /api/subagent/run, consumes the SSE stream, and routes each event to DB + Redis.
// It blocks until the stream ends (terminal event or connection close).
func Run(ctx context.Context, db *gorm.DB, stateStore state.Store, req RunRequest) error {
	runCtx, cancel := context.WithTimeout(ctx, subagentRunTimeout)
	defer cancel()

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return err
	}
	url := algoServiceURL() + runPath
	httpReq, err := http.NewRequestWithContext(runCtx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(httpReq)
	if err != nil {
		routeError(runCtx, db, stateStore, req.TaskID, fmt.Sprintf("subagent run request failed: %v", err))
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		routeError(runCtx, db, stateStore, req.TaskID, fmt.Sprintf("subagent run returned HTTP %d", resp.StatusCode))
		return fmt.Errorf("subagent run returned non-200: %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(nil, 1024*1024)
	for scanner.Scan() && runCtx.Err() == nil {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "data:")
		line = strings.TrimSpace(line)
		if line == "" || line == "[DONE]" {
			continue
		}
		var ev TaskEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		ev.TaskID = req.TaskID
		routeEvent(runCtx, db, stateStore, ev)
	}
	if err := scanner.Err(); err != nil && runCtx.Err() == nil {
		routeError(runCtx, db, stateStore, req.TaskID, fmt.Sprintf("subagent stream read error: %v", err))
		return err
	}
	return nil
}

// routeEvent persists a SubAgent event to DB (authoritative), then appends to Redis (live tail).
func routeEvent(ctx context.Context, db *gorm.DB, stateStore state.Store, ev TaskEvent) {
	switch ev.Type {
	case "task_start":
		_ = UpdateStatus(ctx, db, ev.TaskID, StatusRunning)
		_ = WriteStatus(ctx, stateStore, ev.TaskID, map[string]any{"status": StatusRunning, "progress": 0})
		// Mirror running status into plugin_session_steps if this is a plugin_step task.
		routePluginStepStatus(ctx, db, stateStore, ev.TaskID, StatusRunning, "")
	case "progress":
		_ = UpdateProgress(ctx, db, ev.TaskID, ev.Progress, ev.CurrentPhase, ev.EstimatedSec)
		_ = WriteStatus(ctx, stateStore, ev.TaskID, map[string]any{
			"status": StatusRunning, "progress": ev.Progress, "current_phase": ev.CurrentPhase,
		})
	case "artifact":
		seq := ev.Seq
		if seq <= 0 {
			seq = 1
		}
		_ = SaveArtifact(ctx, db, ev.TaskID, ev.ArtifactKey, ev.ContentType, ev.Value, seq)
		// Write slot revision if this is a plugin_step task with a slot binding.
		// list_index for partial retry is embedded inside the artifact JSON value and
		// extracted by the plugin hook via extractListIndex — no need to pass it here.
		routePluginArtifact(ctx, db, ev.TaskID, ev.ArtifactKey)
	case "done":
		status := ev.Status
		if status == "" {
			status = StatusSucceeded
		}
		_ = UpdateFinalStatus(ctx, db, ev.TaskID, status, ev.Summary)
		_ = WriteStatus(ctx, stateStore, ev.TaskID, map[string]any{
			"status": status, "progress": 100, "summary": ev.Summary,
		})
		// Handle plugin step completion (auto-advance or step_waiting).
		routePluginStepStatus(ctx, db, stateStore, ev.TaskID, status, ev.Summary)
	case "error":
		status := ev.Status
		if status == "" {
			status = StatusFailed
		}
		_ = UpdateFinalStatus(ctx, db, ev.TaskID, status, ev.Message)
		_ = WriteStatus(ctx, stateStore, ev.TaskID, map[string]any{"status": status, "summary": ev.Message})
		routePluginStepStatus(ctx, db, stateStore, ev.TaskID, status, ev.Message)
	}
	_ = AppendStreamEvent(ctx, stateStore, ev.TaskID, ev)
}

// routeError synthesizes a terminal error event when the run cannot be driven by the stream.
// The actual DB write is protected at the UpdateFinalStatus layer: "failed" will never overwrite
// an already-terminal "interrupted" or "succeeded" status, so a race between
// StopActivePluginSession (which writes interrupted) and the SSE EOF handler (which calls us)
// cannot silently downgrade interrupted → failed.
func routeError(ctx context.Context, db *gorm.DB, stateStore state.Store, taskID, message string) {
	ev := TaskEvent{Type: "error", TaskID: taskID, Status: StatusFailed, Message: message}
	_ = UpdateFinalStatus(ctx, db, taskID, StatusFailed, message)
	_ = WriteStatus(ctx, stateStore, taskID, map[string]any{"status": StatusFailed, "summary": message})
	_ = AppendStreamEvent(ctx, stateStore, taskID, ev)
	routePluginStepStatus(ctx, db, stateStore, taskID, StatusFailed, message)
}

// EventHooks allows external packages (e.g. plugin) to register callbacks for SubAgent events.
// Hooks must be registered at startup before any SubAgent run begins.
var EventHooks = &eventHooks{}

type eventHooks struct {
	onArtifact       func(ctx context.Context, db *gorm.DB, taskID, artifactKey string)
	onTerminalStatus func(ctx context.Context, db *gorm.DB, stateStore state.Store, taskID, status, message string)
	// onConversationEvent is called when a plugin lifecycle event should be pushed to the
	// main conversation SSE stream. convID and historyID identify the target stream;
	// eventType is one of "step_waiting", "plugin_completed", "plugin_error".
	onConversationEvent func(ctx context.Context, stateStore state.Store, convID, historyID, eventType string, payload map[string]any)
}

// RegisterArtifactHook registers a hook called on every artifact event for any SubAgent task.
func (h *eventHooks) RegisterArtifactHook(fn func(ctx context.Context, db *gorm.DB, taskID, artifactKey string)) {
	h.onArtifact = fn
}

// RegisterTerminalStatusHook registers a hook called when a task reaches terminal status.
func (h *eventHooks) RegisterTerminalStatusHook(fn func(ctx context.Context, db *gorm.DB, stateStore state.Store, taskID, status, message string)) {
	h.onTerminalStatus = fn
}

// RegisterConversationEventHook registers a hook that pushes a plugin lifecycle event
// to the main conversation SSE stream. Should be registered by the chat package at startup.
func (h *eventHooks) RegisterConversationEventHook(fn func(ctx context.Context, stateStore state.Store, convID, historyID, eventType string, payload map[string]any)) {
	h.onConversationEvent = fn
}

// CallConversationEvent invokes the registered conversation event hook if one is set.
func (h *eventHooks) CallConversationEvent(ctx context.Context, stateStore state.Store, convID, historyID, eventType string, payload map[string]any) {
	if h.onConversationEvent != nil {
		h.onConversationEvent(ctx, stateStore, convID, historyID, eventType, payload)
	}
}

func routePluginStepStatus(ctx context.Context, db *gorm.DB, stateStore state.Store, taskID, status, message string) {
	if EventHooks.onTerminalStatus != nil {
		EventHooks.onTerminalStatus(ctx, db, stateStore, taskID, status, message)
	}
}

func routePluginArtifact(ctx context.Context, db *gorm.DB, taskID, artifactKey string) {
	if EventHooks.onArtifact != nil {
		EventHooks.onArtifact(ctx, db, taskID, artifactKey)
	}
}
