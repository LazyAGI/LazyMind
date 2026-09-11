package subagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/localworkspace"
	"lazymind/core/state"
)

// runPath is the algorithm-layer SubAgent execution endpoint.
const runPath = "/api/subagent/run"

// subagentRunTimeout bounds a single SubAgent execution. Long tasks rely on ctx, not this ceiling.
const subagentRunTimeout = 2 * time.Hour

var activeRunCancels sync.Map

// CancelRuns interrupts the HTTP streams driving the requested SubAgent tasks.
func CancelRuns(taskIDs []string) {
	for _, taskID := range taskIDs {
		if value, ok := activeRunCancels.Load(taskID); ok {
			(*value.(*context.CancelFunc))()
		}
	}
}

// RunRequest is the body posted to the algorithm layer /api/subagent/run.
// task_id doubles as the request sid (independent FileSystemQueue bucket).
//
// objective, input_slots, and output_slots are intentionally
// omitted: the Python runner reads those from the sub_agent_tasks DB record.
// tools is still forwarded for non-workflow_step agent types; workflow_step tasks
// resolve their tools from the immutable public Host Attempt context.
type RunRequest struct {
	TaskID             string            `json:"task_id"`
	AgentType          string            `json:"agent_type"`
	Params             map[string]any    `json:"params,omitempty"`
	WorkspacePath      string            `json:"workspace_path"`
	Tools              []string          `json:"tools,omitempty"`
	DBDSN              string            `json:"db_dsn"`
	Resume             bool              `json:"resume"`
	LLMConfig          map[string]any    `json:"llm_config,omitempty"`
	ToolConfig         map[string]any    `json:"tool_config,omitempty"`
	WorkspaceExecution map[string]string `json:"workspace_execution,omitempty"`
}

// TaskEvent is one event emitted by the SubAgent SSE stream.
type TaskEvent struct {
	Type         string          `json:"type"`
	TaskID       string          `json:"task_id,omitempty"`
	Progress     int             `json:"progress,omitempty"`
	CurrentPhase string          `json:"current_phase,omitempty"`
	EstimatedSec int             `json:"estimated_sec,omitempty"`
	ArtifactKey  string          `json:"slot,omitempty"`
	ContentType  string          `json:"content_type,omitempty"`
	Seq          int             `json:"seq,omitempty"`
	Value        json.RawMessage `json:"value,omitempty"`
	Sources      json.RawMessage `json:"sources,omitempty"`
	Status       string          `json:"status,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	Message      string          `json:"message,omitempty"`
	// Tool step events forwarded from SubAgent runner for frontend display.
	ToolCalls   json.RawMessage `json:"tool_calls,omitempty"`
	ToolResults json.RawMessage `json:"tool_results,omitempty"`
	// Writer document-level subtasks are carried on progress events for the
	// developer Task Center. They are not workflow steps.
	WritingSubtasks json.RawMessage `json:"writing_subtasks,omitempty"`
	// Text / think streaming content.
	Text  string `json:"text,omitempty"`
	Think string `json:"think,omitempty"`
	// Attempt-scoped Markdown Draft preview fields. These events remain
	// ephemeral and are never persisted as artifacts or steps.
	StreamID   string `json:"stream_id,omitempty"`
	ChunkIndex int64  `json:"chunk_index,omitempty"`
	Delta      string `json:"delta,omitempty"`
}

// algoServiceURL resolves the algorithm chat-service base URL (same host as /api/chat/stream).
func algoServiceURL() string {
	return common.ChatServiceEndpoint()
}

// Run posts to /api/subagent/run, consumes the SSE stream, and routes each event to DB + Redis.
// It blocks until the stream ends (terminal event or connection close).
func Run(ctx context.Context, db *gorm.DB, stateStore state.Store, req RunRequest) error {
	return RunObserved(ctx, db, stateStore, req, nil)
}

// RunObserved is Run with a read-only copy of every accepted wire event.  The
// observer is deliberately called before legacy projections are updated so a
// workflow Executor can drive its own fenced Attempt lifecycle without parsing
// the SSE stream a second time.
func RunObserved(ctx context.Context, db *gorm.DB, stateStore state.Store, req RunRequest, observe func(TaskEvent) error) error {
	runCtx, cancel := context.WithTimeout(ctx, subagentRunTimeout)
	defer cancel()
	// The generation is captured by this launch and passed separately from task
	// Params. An old Python runner can never adopt a resumed task's generation.
	generation := uuid.NewString()
	if stateStore == nil {
		return fmt.Errorf("store not initialized")
	}
	atomicStore, ok := stateStore.(state.CompareAndDeleteStore)
	if !ok {
		return fmt.Errorf("store not initialized")
	}
	activeRun := &cancel
	if err := withWorkspaceRunUpdate(runCtx, db, req.TaskID, func(tx *gorm.DB) error {
		task, err := GetTask(runCtx, tx, req.TaskID)
		if err != nil {
			return err
		}
		if task.Status != StatusPending && task.Status != StatusRunning {
			return ErrTaskTerminal
		}
		if err := stateStore.Set(runCtx, workspaceRunKey(req.TaskID), []byte(generation), subagentRunTimeout); err != nil {
			return err
		}
		if previous, loaded := activeRunCancels.Swap(req.TaskID, activeRun); loaded {
			(*previous.(*context.CancelFunc))()
		}
		return nil
	}); err != nil {
		return err
	}
	req.WorkspaceExecution = map[string]string{"task_id": req.TaskID, "generation": generation}
	defer activeRunCancels.CompareAndDelete(req.TaskID, activeRun)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = withWorkspaceRunUpdate(cleanupCtx, db, req.TaskID, func(_ *gorm.DB) error {
			_, err := atomicStore.CompareAndDelete(cleanupCtx, workspaceRunKey(req.TaskID), []byte(generation))
			return err
		})
	}()
	runError := func(message string) {
		_ = routeRunEvent(runCtx, db, stateStore, generation, TaskEvent{
			Type: "error", TaskID: req.TaskID, Status: StatusFailed, Message: message,
		})
	}

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
		runError(fmt.Sprintf("subagent run request failed: %v", err))
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		runError(fmt.Sprintf("subagent run returned HTTP %d", resp.StatusCode))
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
		if observe != nil {
			if err := observe(ev); err != nil {
				return fmt.Errorf("%w", err)
			}
		}
		if err := routeRunEvent(runCtx, db, stateStore, generation, ev); err != nil {
			message := fmt.Sprintf("persist subagent %s event failed: %v", ev.Type, err)
			runError(message)
			return fmt.Errorf("%s", message)
		}
	}
	if err := scanner.Err(); err != nil && runCtx.Err() == nil {
		runError(fmt.Sprintf("subagent stream read error: %v", err))
		return err
	}
	return nil
}

// routeEvent persists a SubAgent event to DB (authoritative), then appends to Redis (live tail).
func routeEvent(ctx context.Context, db *gorm.DB, stateStore state.Store, ev TaskEvent) error {
	return routeEventWithWorkflowHooks(ctx, db, stateStore, ev, true, true)
}

func routeEventWithWorkflowHooks(ctx context.Context, db *gorm.DB, stateStore state.Store, ev TaskEvent, artifactHook, terminalHook bool) error {
	accepted, err := persistTaskEvent(ctx, db, ev)
	if err == nil && accepted {
		publishTaskEvent(ctx, db, stateStore, ev, artifactHook, terminalHook)
	}
	return err
}

// routeRunEvent keeps generation validation and the DB projection under the
// same task-row lock used by launch/resume. Callbacks run only after commit.
func routeRunEvent(ctx context.Context, db *gorm.DB, stateStore state.Store, generation string, ev TaskEvent) error {
	accepted := false
	err := withWorkspaceRunUpdate(ctx, db, ev.TaskID, func(tx *gorm.DB) error {
		current, err := stateStore.Get(ctx, workspaceRunKey(ev.TaskID))
		if err != nil {
			return err
		}
		if string(current) != generation {
			return fmt.Errorf("conflict")
		}
		accepted, err = persistTaskEvent(ctx, tx, ev)
		return err
	})
	if err == nil && accepted {
		publishTaskEvent(ctx, db, stateStore, ev, true, true)
	}
	return err
}

func persistTaskEvent(ctx context.Context, db *gorm.DB, ev TaskEvent) (bool, error) {
	var err error
	switch ev.Type {
	case "task_start":
		return AcceptTaskStart(ctx, db, ev.TaskID)
	case "progress":
		err = UpdateProgress(ctx, db, ev.TaskID, ev.Progress, ev.CurrentPhase, ev.EstimatedSec)
		if err == nil && len(ev.WritingSubtasks) > 0 {
			err = UpdateWritingSubtasks(ctx, db, ev.TaskID, ev.WritingSubtasks)
		}
	case "artifact":
		err = SaveArtifact(ctx, db, ev.TaskID, ev.ArtifactKey, ev.ContentType, ev.Value, max(1, ev.Seq))
	case "sources":
		err = UpdateSources(ctx, db, ev.TaskID, ev.Sources)
	case "done":
		status := ev.Status
		if status == "" {
			status = StatusSucceeded
		}
		return AcceptFinalStatus(ctx, db, ev.TaskID, status, ev.Summary)
	case "error":
		status := ev.Status
		if status == "" {
			status = StatusFailed
		}
		return AcceptFinalStatus(ctx, db, ev.TaskID, status, ev.Message)
	}
	return err == nil, err
}

func publishTaskEvent(ctx context.Context, db *gorm.DB, stateStore state.Store, ev TaskEvent, artifactHook, terminalHook bool) {
	switch ev.Type {
	case "task_start":
		_ = WriteStatus(ctx, stateStore, ev.TaskID, map[string]any{"status": StatusRunning, "progress": 0})
		if terminalHook {
			routeWorkflowStepStatus(ctx, db, stateStore, ev.TaskID, StatusRunning, "")
		}
	case "progress":
		_ = WriteStatus(ctx, stateStore, ev.TaskID, map[string]any{"status": StatusRunning, "progress": ev.Progress, "current_phase": ev.CurrentPhase})
	case "artifact":
		if artifactHook {
			routeWorkflowArtifact(ctx, db, stateStore, ev.TaskID, ev.ArtifactKey)
		}
	case "done", "error":
		status, summary := ev.Status, ev.Summary
		if ev.Type == "error" {
			summary = ev.Message
			if status == "" {
				status = StatusFailed
			}
		} else if status == "" {
			status = StatusSucceeded
		}
		fields := map[string]any{"status": status, "summary": summary}
		if ev.Type == "done" {
			fields["progress"] = 100
		}
		_ = WriteStatus(ctx, stateStore, ev.TaskID, fields)
		if terminalHook {
			routeWorkflowStepStatus(ctx, db, stateStore, ev.TaskID, status, summary)
		}
	}
	if isArtifactStreamEvent(ev.Type) || ev.Type == "progress" || ev.Type == "done" || ev.Type == "error" {
		taskLiveEvents.publish(ev.TaskID, ev)
	}
	_ = AppendStreamEvent(ctx, stateStore, ev.TaskID, ev)
	PublishConversationTaskEvent(ctx, db, stateStore, ev)
}

// routeError synthesizes a terminal error event when the run cannot be driven by
// the stream. AcceptFinalStatus ignores it if an explicit stop won the race.
func routeError(ctx context.Context, db *gorm.DB, stateStore state.Store, taskID, message string) {
	ev := TaskEvent{Type: "error", TaskID: taskID, Status: StatusFailed, Message: message}
	accepted, _ := AcceptFinalStatus(ctx, db, taskID, StatusFailed, message)
	if !accepted {
		return
	}
	_ = WriteStatus(ctx, stateStore, taskID, map[string]any{"status": StatusFailed, "summary": message})
	_ = AppendStreamEvent(ctx, stateStore, taskID, ev)
	PublishConversationTaskEvent(ctx, db, stateStore, ev)
	routeWorkflowStepStatus(ctx, db, stateStore, taskID, StatusFailed, message)
}

// PublishConversationTaskEvent keeps the conversation stream limited to bounded
// lifecycle changes. Granular text/think/tool events stay on the per-task SSE
// stream; copying token deltas here can exhaust the conversation event transport
// and prevent later task_created events from reaching the frontend.
func PublishConversationTaskEvent(
	ctx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	ev TaskEvent,
) {
	if db == nil || EventHooks == nil || ev.TaskID == "" {
		return
	}
	task, err := GetTask(ctx, db, ev.TaskID)
	if err != nil || task.ConversationID == "" {
		return
	}
	if task.AgentType == "workflow_step" {
		switch ev.Type {
		case "artifact_stream_start", "artifact_stream", "artifact_stream_end", "artifact_stream_abort":
			// WorkflowPanel consumes Writer previews from the one conversation
			// stream even though workflow tasks stay hidden from TaskCenter.
			EventHooks.CallConversationEvent(ctx, stateStore, task.ConversationID, "", "task_updated",
				map[string]any{"task_id": ev.TaskID, "event": ev})
		case "task_start", "progress", "artifact", "done", "error":
			EventHooks.CallConversationEvent(ctx, stateStore, task.ConversationID, "",
				"workflow_runtime_updated", map[string]any{"task_id": ev.TaskID, "change": ev.Type})
		default:
			// Tool and reasoning events for workflow steps are not shown in the
			// standalone task panel and do not affect WorkflowPanel projections.
		}
	}
	switch ev.Type {
	case "task_start", "progress", "sources", "done", "error":
	default:
		return
	}
	EventHooks.CallConversationEvent(ctx, stateStore, task.ConversationID, "", "task_updated",
		map[string]any{"task_id": ev.TaskID, "event": ev})
}

// EventHooks allows external packages (e.g. plugin) to register callbacks for SubAgent events.
// Hooks must be registered at startup before any SubAgent run begins.
var EventHooks = &eventHooks{}

type eventHooks struct {
	onArtifact       func(ctx context.Context, db *gorm.DB, stateStore state.Store, taskID, artifactKey string)
	onTerminalStatus func(ctx context.Context, db *gorm.DB, stateStore state.Store, taskID, status, message string)
	// onConversationEvent is called when a plugin lifecycle event should be pushed to the
	// main conversation SSE stream. convID and historyID identify the target stream;
	// eventType is a bounded workflow lifecycle notification such as
	// "workflow_step_feedback", "step_waiting", "workflow_completed", or "workflow_error".
	onConversationEvent func(ctx context.Context, stateStore state.Store, convID, historyID, eventType string, payload map[string]any) error
}

// RegisterArtifactHook registers a hook called on every artifact event for any SubAgent task.
func (h *eventHooks) RegisterArtifactHook(fn func(ctx context.Context, db *gorm.DB, stateStore state.Store, taskID, artifactKey string)) {
	h.onArtifact = fn
}

// RegisterTerminalStatusHook registers a hook called when a task reaches terminal status.
func (h *eventHooks) RegisterTerminalStatusHook(fn func(ctx context.Context, db *gorm.DB, stateStore state.Store, taskID, status, message string)) {
	h.onTerminalStatus = fn
}

// RegisterConversationEventHook registers a hook that pushes a plugin lifecycle event
// to the main conversation SSE stream. Should be registered by the chat package at startup.
func (h *eventHooks) RegisterConversationEventHook(fn func(ctx context.Context, stateStore state.Store, convID, historyID, eventType string, payload map[string]any) error) {
	h.onConversationEvent = fn
}

// CallConversationEvent invokes the registered conversation event hook if one is set.
func (h *eventHooks) CallConversationEvent(ctx context.Context, stateStore state.Store, convID, historyID, eventType string, payload map[string]any) {
	if h.onConversationEvent != nil {
		_ = h.onConversationEvent(ctx, stateStore, convID, historyID, eventType, payload)
	}
}

// CallConversationEventChecked reports delivery failures to callers that can
// safely retry an idempotent operation. Existing lifecycle notifications keep
// their best-effort behavior through CallConversationEvent.
func (h *eventHooks) CallConversationEventChecked(ctx context.Context, stateStore state.Store, convID, historyID, eventType string, payload map[string]any) error {
	if h.onConversationEvent == nil {
		return nil
	}
	return h.onConversationEvent(ctx, stateStore, convID, historyID, eventType, payload)
}

func routeWorkflowStepStatus(ctx context.Context, db *gorm.DB, stateStore state.Store, taskID, status, message string) {
	if EventHooks.onTerminalStatus != nil {
		EventHooks.onTerminalStatus(ctx, db, stateStore, taskID, status, message)
	}
}

func routeWorkflowArtifact(ctx context.Context, db *gorm.DB, stateStore state.Store, taskID, slot string) {
	if EventHooks.onArtifact != nil {
		EventHooks.onArtifact(ctx, db, stateStore, taskID, slot)
	}
}

func workspaceRunKey(taskID string) string { return "rag/subagent/execution:" + taskID }

// ValidateWorkspaceRun is independent of the parent Chat run: detached tasks
// survive normal parent completion, but not interruption or a later launch.
func ValidateWorkspaceRun(ctx context.Context, db *gorm.DB, stateStore state.Store, req localworkspace.OperationRequest) (*localworkspace.ContextSnapshot, error) {
	invalid := localworkspace.Error("binding_conflict", 409, "conflict")
	if stateStore == nil || req.TaskID == "" || req.Generation == "" || req.RunID != "" || req.HistoryID != "" {
		return nil, invalid
	}
	task, err := GetTask(ctx, db, req.TaskID)
	if err != nil {
		return nil, err
	}
	if task.CreateUserID != req.UserID || task.ConversationID != req.ConversationID ||
		(task.Status != StatusPending && task.Status != StatusRunning) {
		return nil, invalid
	}
	if req.AttemptID == "" {
		if task.AgentType == "workflow_step" {
			return nil, invalid
		}
		generation, err := stateStore.Get(ctx, workspaceRunKey(req.TaskID))
		if err != nil {
			return nil, err
		}
		if string(generation) != req.Generation {
			return nil, invalid
		}
	} else if task.AgentType != "workflow_step" {
		return nil, invalid
	}
	params := map[string]any{}
	if len(task.Params) > 0 && json.Unmarshal(task.Params, &params) != nil {
		return nil, invalid
	}
	snapshot := localworkspace.SnapshotFromParams(params)
	if snapshot == nil {
		if req.WorkspaceID == "" {
			return nil, nil
		}
		return nil, invalid
	}
	if snapshot.WorkspaceID != req.WorkspaceID {
		return nil, invalid
	}
	return snapshot, nil
}

func withWorkspaceRunUpdate(ctx context.Context, db *gorm.DB, taskID string, update func(*gorm.DB) error) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&orm.SubAgentTask{}).Where("id = ?", taskID).
			UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
			return err
		}
		return update(tx)
	})
}
