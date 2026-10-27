package subagent

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/taskdisplay"
	"lazymind/core/state"
	"lazymind/core/store"
)

const taskSSEHeartbeatInterval = 30 * time.Second
const taskWriterSSEHeartbeatInterval = 2 * time.Second
const taskLiveSubscriberBuffer = 256

// taskLiveEventBroker forwards ephemeral Task events to SSE clients connected to
// this Core process. Redis remains the replay source and cross-process fallback.
type taskLiveEventBroker struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan TaskEvent]struct{}
}

func newTaskLiveEventBroker() *taskLiveEventBroker {
	return &taskLiveEventBroker{subscribers: make(map[string]map[chan TaskEvent]struct{})}
}

func (b *taskLiveEventBroker) subscribe(taskID string) (<-chan TaskEvent, func()) {
	ch := make(chan TaskEvent, taskLiveSubscriberBuffer)
	b.mu.Lock()
	if b.subscribers[taskID] == nil {
		b.subscribers[taskID] = make(map[chan TaskEvent]struct{})
	}
	b.subscribers[taskID][ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subscribers[taskID], ch)
			if len(b.subscribers[taskID]) == 0 {
				delete(b.subscribers, taskID)
			}
			b.mu.Unlock()
		})
	}
}

func (b *taskLiveEventBroker) publish(taskID string, event TaskEvent) {
	b.mu.RLock()
	channels := make([]chan TaskEvent, 0, len(b.subscribers[taskID]))
	for ch := range b.subscribers[taskID] {
		channels = append(channels, ch)
	}
	b.mu.RUnlock()

	for _, ch := range channels {
		select {
		case ch <- event:
		default:
			// Redis already has the event; a slow subscriber recovers on polling.
		}
	}
}

var taskLiveEvents = newTaskLiveEventBroker()

func isTerminal(status string) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusInterrupted, StatusCanceled:
		return true
	}
	return false
}

func writeTaskSSE(w http.ResponseWriter, flusher http.Flusher, ev any) {
	b, _ := json.Marshal(ev)
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n\n"))
	if flusher != nil {
		flusher.Flush()
	}
}

func writeTaskHeartbeat(w http.ResponseWriter, flusher http.Flusher) {
	_, _ = w.Write([]byte(": heartbeat\n\n"))
	if flusher != nil {
		flusher.Flush()
	}
}

func resetTaskHeartbeatTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(taskWriterSSEHeartbeatInterval)
}

func taskStreamHeartbeatsEnabled(task *orm.SubAgentTask) bool {
	if task == nil || task.AgentType != "workflow_step" {
		return false
	}
	var params struct {
		StreamHeartbeat bool `json:"stream_heartbeat"`
	}
	return json.Unmarshal(task.Params, &params) == nil && params.StreamHeartbeat
}

func writerDraftHeartbeatsEnabled(ctx context.Context, db *gorm.DB, taskID string) bool {
	if db == nil {
		return false
	}
	task, err := GetTask(ctx, db, taskID)
	return err == nil && taskStreamHeartbeatsEnabled(task)
}

func isArtifactStreamEvent(eventType string) bool {
	switch eventType {
	case "artifact_stream_start", "artifact_stream", "artifact_stream_end", "artifact_stream_abort":
		return true
	}
	return false
}

func artifactStreamEventKey(ev TaskEvent) string {
	if !isArtifactStreamEvent(ev.Type) || ev.StreamID == "" || ev.ChunkIndex <= 0 {
		return ""
	}
	return ev.Type + ":" + ev.StreamID + ":" + strconv.FormatInt(ev.ChunkIndex, 10)
}

func writeArtifactStreamEventOnce(
	w http.ResponseWriter,
	flusher http.Flusher,
	ev TaskEvent,
	sent map[string]struct{},
) bool {
	key := artifactStreamEventKey(ev)
	if key != "" && sent != nil {
		if _, exists := sent[key]; exists {
			return false
		}
		sent[key] = struct{}{}
	}
	writeTaskSSE(w, flusher, ev)
	return true
}

func prepareTaskEventForSSE(ev TaskEvent, workspacePath string) TaskEvent {
	if ev.Type == "artifact" {
		ev.Value = SignArtifactValue(
			ev.ContentType, normalizeJSON(ev.Value, "{}"), workspacePath,
		)
	}
	return ev
}

// StreamTask handles GET /tasks/{task_id}:stream.
// Reconnect protocol: DB snapshot (task_start + history progress + history artifacts) first,
// then if terminal send done/error; if still running, replay/tail the state stream.
func StreamTask(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("view") == "ordinary" {
		taskdisplay.PrepareRequest(w, r)
	}
	taskID := common.PathVar(r, "task_id")
	if taskID == "" {
		replyTaskError(w, r, "task_id required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		replyTaskError(w, r, "store not initialized", http.StatusInternalServerError)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		replyTaskError(w, r, "streaming not supported", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	stateStore := store.State()

	t, err := GetTask(ctx, db, taskID)
	if err != nil {
		if IsNotFound(err) {
			replyTaskError(w, r, "task not found", http.StatusNotFound)
			return
		}
		replyTaskError(w, r, "query task failed", http.StatusInternalServerError)
		return
	}
	if t.CreateUserID != requestUserID(r) {
		replyTaskError(w, r, "task not found", http.StatusNotFound)
		return
	}

	if r.URL.Query().Get("view") == "ordinary" {
		streamOrdinaryTask(w, r, db, taskID, flusher)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// 1. DB snapshot: task_start + history progress + history artifacts + history steps.
	writeTaskSSE(w, flusher, TaskEvent{Type: "task_start", TaskID: taskID, ExecutionID: t.ExecutionID, Snapshot: true})
	writeTaskSSE(w, flusher, TaskEvent{
		Type: "progress", TaskID: taskID,
		Progress: t.ProgressPct, CurrentPhase: t.CurrentPhase, EstimatedSec: t.EstimatedSec,
	})
	writeTaskSSE(w, flusher, TaskEvent{
		Type: "sources", TaskID: taskID,
		Sources: normalizeJSON(json.RawMessage(t.Sources), "[]"),
	})
	steps, _ := LoadSteps(ctx, db, taskID)
	lastStepSeq, cursor := writeTaskStepsSnapshot(w, flusher, t, steps)
	arts, _ := LoadArtifacts(ctx, db, taskID)
	sentArtifacts := make(map[string]bool)
	writeTaskArtifactsSnapshot(w, flusher, t, arts, sentArtifacts)

	// Re-read current execution/status after Redis: a terminal snapshot can resume
	// while its replay read is in flight. Start at zero even for a missing LIST.
	// The first Draft stream event may be created after the browser connects.
	if stateStore == nil {
		pollDBUntilTerminal(ctx, db, w, flusher, taskID, t.WorkspacePath, lastStepSeq, cursor, sentArtifacts)
		return
	}
	tailRedisStream(ctx, db, stateStore, w, flusher, taskID, t.WorkspacePath, lastStepSeq, cursor, sentArtifacts)
}

func replayArtifactStreamEvents(
	ctx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	w http.ResponseWriter,
	flusher http.Flusher,
	taskID string,
	sent map[string]struct{},
	lastStepSeq *int,
	cursor *common.ModelOutput,
	sentArtifacts map[string]bool,
) (int64, *orm.SubAgentTask, error) {
	existing, err := StreamEventsFrom(ctx, stateStore, taskID, 0)
	if err != nil {
		return 0, nil, err
	}
	// The run may have changed while Redis was being read, including before the
	// first real task_start. Rebuild from DB before filtering this offset-zero replay.
	task, err := refreshTaskExecutionSnapshot(ctx, db, w, flusher, taskID, lastStepSeq, cursor, sentArtifacts)
	if err != nil {
		return 0, nil, err
	}
	if cursor != nil {
		artifacts, err := LoadArtifacts(ctx, db, taskID)
		if err != nil {
			return 0, nil, err
		}
		writeTaskArtifactsSnapshot(w, flusher, task, artifacts, sentArtifacts)
	}
	var pending []TaskEvent
	nextIndex := 0
	if cursor != nil {
		nextIndex = cursor.CallIndex + 1
	}
	for _, raw := range existing {
		var ev TaskEvent
		if json.Unmarshal([]byte(raw), &ev) != nil {
			continue
		}
		if cursor != nil && ev.ExecutionID != "" && ev.ExecutionID != cursor.RunID {
			continue
		}
		if isArtifactStreamEvent(ev.Type) {
			writeArtifactStreamEventOnce(w, flusher, ev, sent)
		}
		if cursor != nil && (ev.Type == "tool_calls" || ev.Type == "tool_results") && ev.Seq > *lastStepSeq {
			writeTaskSSE(w, flusher, ev)
			*lastStepSeq = ev.Seq
		}
		if m := ev.ModelOutput; m != nil && cursor != nil {
			if m.RunID != cursor.RunID || m.CallIndex < nextIndex {
				continue
			}
			switch m.Phase {
			case "begin":
				if m.CallIndex == nextIndex && len(pending) == 0 {
					pending = []TaskEvent{ev}
				}
			case "":
				if len(pending) > 0 && m.CallIndex == nextIndex && (ev.Type == "text" || ev.Type == "think") {
					pending = append(pending, ev)
				}
			case "commit", "rollback":
				if len(pending) == 0 || m.CallIndex != nextIndex {
					continue
				}
				if m.Phase == "commit" {
					for _, frame := range pending {
						writeTaskSSE(w, flusher, frame)
					}
				} else {
					writeTaskSSE(w, flusher, pending[0])
				}
				writeTaskSSE(w, flusher, ev)
				*cursor = *m
				pending = nil
				nextIndex++
			}
		}
	}
	if len(pending) > 0 {
		if isTerminal(task.Status) {
			writeTaskSSE(w, flusher, pending[0])
			boundary := *pending[0].ModelOutput
			boundary.Phase = "rollback"
			*cursor = boundary
			writeTaskSSE(w, flusher, TaskEvent{Type: "model_output", TaskID: taskID, ExecutionID: boundary.RunID, ModelOutput: &boundary})
		} else {
			*cursor = *pending[0].ModelOutput
			for _, ev := range pending {
				writeTaskSSE(w, flusher, ev)
			}
		}
	}

	return int64(len(existing)), task, nil
}

func emitTerminal(w http.ResponseWriter, flusher http.Flusher, taskID, status, summary string) {
	if status == StatusSucceeded {
		writeTaskSSE(w, flusher, TaskEvent{Type: "done", TaskID: taskID, Status: status, Summary: summary})
		return
	}
	writeTaskSSE(w, flusher, TaskEvent{Type: "error", TaskID: taskID, Status: status, Message: summary})
}

func emitTaskTerminal(w http.ResponseWriter, flusher http.Flusher, task *orm.SubAgentTask) {
	ev := TaskEvent{Type: "error", TaskID: task.ID, ExecutionID: task.ExecutionID, Status: task.Status, Message: task.Summary, CurrentPhase: task.CurrentPhase}
	if task.Status == StatusSucceeded {
		ev.Type = "done"
		ev.Message = ""
		ev.Summary = task.Summary
	}
	writeTaskSSE(w, flusher, ev)
}

func writeTaskStepsSnapshot(w http.ResponseWriter, flusher http.Flusher, task *orm.SubAgentTask, steps []orm.SubAgentStep) (int, *common.ModelOutput) {
	seq := -1
	var cursor *common.ModelOutput
	if task.AgentType != "workflow_step" && task.ExecutionID != "" {
		cursor = &common.ModelOutput{RunID: task.ExecutionID, Phase: "commit"}
	}
	for i := range steps {
		row := &steps[i]
		seq = max(seq, row.Seq)
		if row.ExecutionID != "" && row.ExecutionID != task.ExecutionID {
			continue
		}
		if row.Role == "model_runtime" && cursor != nil {
			var record taskModelRecord
			if json.Unmarshal(row.Content, &record) == nil && record.ModelOutput.RunID == task.ExecutionID && (record.ModelOutput.Phase == "commit" || record.ModelOutput.Phase == "rollback") {
				value := record.ModelOutput
				cursor = &value
			}
			continue
		}
		if ev := stepToTaskEvent(task.ID, row); ev != nil {
			writeTaskSSE(w, flusher, *ev)
		}
	}
	if cursor != nil {
		writeTaskSSE(w, flusher, TaskEvent{Type: "model_output", TaskID: task.ID, ExecutionID: task.ExecutionID, ModelOutput: cursor})
	}
	return seq, cursor
}

func writeTaskArtifactsSnapshot(w http.ResponseWriter, flusher http.Flusher, task *orm.SubAgentTask, artifacts []orm.SubAgentArtifact, sent map[string]bool) {
	for i := range artifacts {
		artifact := &artifacts[i]
		key := artifactDedupKey(artifact)
		if artifact.Hidden || sent[key] {
			continue
		}
		sent[key] = true
		writeTaskSSE(w, flusher, TaskEvent{Type: "artifact", TaskID: task.ID, ArtifactKey: artifact.Slot, ContentType: artifact.ContentType, Seq: artifact.Seq,
			Value: SignArtifactValue(artifact.ContentType, normalizeJSON(artifact.Value, "{}"), task.WorkspacePath)})
	}
}

func refreshTaskStepsSnapshot(ctx context.Context, db *gorm.DB, w http.ResponseWriter, flusher http.Flusher, task *orm.SubAgentTask, seq int, cursor *common.ModelOutput, sentArtifacts map[string]bool) (int, *common.ModelOutput, error) {
	steps, err := LoadSteps(ctx, db, task.ID)
	if err != nil {
		return seq, cursor, err
	}
	artifacts, err := LoadArtifacts(ctx, db, task.ID)
	if err != nil {
		return seq, cursor, err
	}
	writeTaskSSE(w, flusher, TaskEvent{Type: "task_start", TaskID: task.ID, ExecutionID: task.ExecutionID, Snapshot: true})
	seq, cursor = writeTaskStepsSnapshot(w, flusher, task, steps)
	writeTaskArtifactsSnapshot(w, flusher, task, artifacts, sentArtifacts)
	return seq, cursor, nil
}

func refreshTaskExecutionSnapshot(ctx context.Context, db *gorm.DB, w http.ResponseWriter, flusher http.Flusher, taskID string, seq *int, cursor *common.ModelOutput, sentArtifacts map[string]bool) (*orm.SubAgentTask, error) {
	task, err := GetTask(ctx, db, taskID)
	if err != nil || cursor == nil || task.ExecutionID == cursor.RunID && !isTerminal(task.Status) {
		return task, err
	}
	// A terminal row can be newer than the LIST slice even within the same run.
	// Rebuild its committed cursor before deciding which previews to retract.
	nextSeq, nextCursor, err := refreshTaskStepsSnapshot(ctx, db, w, flusher, task, *seq, cursor, sentArtifacts)
	if err != nil {
		return nil, err
	}
	*seq, *cursor = nextSeq, *nextCursor
	return task, nil
}

func closeTaskPreview(w http.ResponseWriter, flusher http.Flusher, taskID string, cursor *common.ModelOutput) {
	if cursor != nil && cursor.Phase == "begin" {
		cursor.Phase = "rollback"
		writeTaskSSE(w, flusher, TaskEvent{Type: "model_output", TaskID: taskID, ExecutionID: cursor.RunID, ModelOutput: cursor})
	}
}

// stepToTaskEvent converts a persisted step back to a TaskEvent for the DB snapshot replay.
// Returns nil for step roles that have no frontend representation.
func stepToTaskEvent(taskID string, s *orm.SubAgentStep) *TaskEvent {
	switch s.Role {
	case "plan":
		var content struct {
			Steps        []string `json:"steps"`
			ScopeVersion int      `json:"scope_version"`
		}
		if json.Unmarshal(s.Content, &content) != nil || len(content.Steps) == 0 {
			return nil
		}
		return &TaskEvent{Type: "plan", TaskID: taskID, Steps: content.Steps, ScopeVersion: content.ScopeVersion}

	case "text":
		var c struct {
			Content     string              `json:"content"`
			ModelOutput *common.ModelOutput `json:"model_output,omitempty"`
		}
		_ = json.Unmarshal(s.Content, &c)
		if c.Content == "" {
			return nil
		}
		return &TaskEvent{Type: "text", TaskID: taskID, Text: c.Content, ExecutionID: s.ExecutionID, ModelOutput: c.ModelOutput}
	case "think":
		var c struct {
			Content     string              `json:"content"`
			ModelOutput *common.ModelOutput `json:"model_output,omitempty"`
		}
		_ = json.Unmarshal(s.Content, &c)
		if c.Content == "" {
			return nil
		}
		return &TaskEvent{Type: "think", TaskID: taskID, Think: c.Content, ExecutionID: s.ExecutionID, ModelOutput: c.ModelOutput}
	case "assistant", "tool":
		// assistant step content: {"tool_calls": [...], "text": ""}
		// tool step content: {"tool_results": [...]}
		// Extract the inner array and forward it.
		if s.Role == "assistant" {
			var c struct {
				ToolCalls json.RawMessage `json:"tool_calls"`
			}
			_ = json.Unmarshal(s.Content, &c)
			if len(c.ToolCalls) == 0 {
				return nil
			}
			return &TaskEvent{Type: "tool_calls", TaskID: taskID, ToolCalls: c.ToolCalls, ExecutionID: s.ExecutionID, Seq: s.Seq}
		}
		var c struct {
			ToolResults json.RawMessage `json:"tool_results"`
		}
		_ = json.Unmarshal(s.Content, &c)
		if len(c.ToolResults) == 0 {
			return nil
		}
		return &TaskEvent{Type: "tool_results", TaskID: taskID, ToolResults: c.ToolResults, ExecutionID: s.ExecutionID, Seq: s.Seq}
	}
	return nil
}

// tailRedisStream replays ephemeral Draft events, then tails new Task events until terminal.
func tailRedisStream(
	ctx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	w http.ResponseWriter,
	flusher http.Flusher,
	taskID string,
	workspacePath string,
	lastStepSeq int,
	cursor *common.ModelOutput,
	sentArtifacts map[string]bool,
) {
	liveEvents, unsubscribe := taskLiveEvents.subscribe(taskID)
	defer unsubscribe()
	sentArtifactStreamEvents := make(map[string]struct{})

	// Merge durable effects beyond the DB snapshot watermark and replay cached
	// model/Draft previews still available within the LIST TTL.
	from, task, err := replayArtifactStreamEvents(
		ctx, db, stateStore, w, flusher, taskID, sentArtifactStreamEvents, &lastStepSeq, cursor, sentArtifacts,
	)
	if err != nil {
		if cursor != nil {
			if current, err := GetTask(ctx, db, taskID); err == nil {
				lastStepSeq, cursor, _ = refreshTaskStepsSnapshot(ctx, db, w, flusher, current, lastStepSeq, cursor, sentArtifacts)
			}
		}
		pollDBUntilTerminal(ctx, db, w, flusher, taskID, workspacePath, lastStepSeq, cursor, sentArtifacts)
		return
	}
	if isTerminal(task.Status) {
		emitTaskTerminal(w, flusher, task)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
		return
	}
	artifactStreamStarted := len(sentArtifactStreamEvents) > 0
	pollTicker := time.NewTicker(300 * time.Millisecond)
	defer pollTicker.Stop()
	var heartbeatTimer *time.Timer
	var heartbeat <-chan time.Time
	if writerDraftHeartbeatsEnabled(ctx, db, taskID) {
		heartbeatTimer = time.NewTimer(taskWriterSSEHeartbeatInterval)
		heartbeat = heartbeatTimer.C
		defer heartbeatTimer.Stop()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-liveEvents:
			if cursor != nil && ev.ExecutionID != "" && ev.ExecutionID != cursor.RunID {
				continue
			}
			if ev.Type == "progress" {
				writeTaskSSE(w, flusher, ev)
				continue
			}
			if ev.Type == "done" || ev.Type == "error" {
				if cursor != nil {
					continue
				}
				writeTaskSSE(w, flusher, ev)
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				flusher.Flush()
				return
			}
			if !isArtifactStreamEvent(ev.Type) {
				continue
			}
			ev = prepareTaskEventForSSE(ev, workspacePath)
			if writeArtifactStreamEventOnce(
				w, flusher, ev, sentArtifactStreamEvents,
			) {
				artifactStreamStarted = true
				if heartbeatTimer != nil {
					resetTaskHeartbeatTimer(heartbeatTimer)
				}
			}
		case <-pollTicker.C:
			events, err := StreamEventsFrom(ctx, stateStore, taskID, from)
			if err != nil {
				if cursor != nil {
					if current, err := GetTask(ctx, db, taskID); err == nil {
						lastStepSeq, cursor, _ = refreshTaskStepsSnapshot(ctx, db, w, flusher, current, lastStepSeq, cursor, sentArtifacts)
					}
					pollDBUntilTerminal(ctx, db, w, flusher, taskID, workspacePath, lastStepSeq, cursor, sentArtifacts)
					return
				}
				steps, _ := LoadSteps(ctx, db, taskID)
				for i := range steps {
					if steps[i].Seq > lastStepSeq {
						lastStepSeq = steps[i].Seq
					}
				}
				pollDBUntilTerminal(ctx, db, w, flusher, taskID, workspacePath, lastStepSeq, cursor, sentArtifacts)
				return
			}
			runID := ""
			if cursor != nil {
				runID = cursor.RunID
			}
			task, err = refreshTaskExecutionSnapshot(ctx, db, w, flusher, taskID, &lastStepSeq, cursor, sentArtifacts)
			if err != nil {
				pollDBUntilTerminal(ctx, db, w, flusher, taskID, workspacePath, lastStepSeq, cursor, sentArtifacts)
				return
			}
			if cursor != nil && isTerminal(task.Status) {
				emitTaskTerminal(w, flusher, task)
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				flusher.Flush()
				return
			}
			if cursor != nil && cursor.RunID != runID {
				// A new execution gets one full replay per polling turn. Reusing
				// its DB cursor excludes old runs and already closed model calls.
				from, task, err = replayArtifactStreamEvents(ctx, db, stateStore, w, flusher, taskID, sentArtifactStreamEvents, &lastStepSeq, cursor, sentArtifacts)
				if err != nil {
					pollDBUntilTerminal(ctx, db, w, flusher, taskID, workspacePath, lastStepSeq, cursor, sentArtifacts)
					return
				}
				if isTerminal(task.Status) {
					emitTaskTerminal(w, flusher, task)
					_, _ = w.Write([]byte("data: [DONE]\n\n"))
					flusher.Flush()
					return
				}
				continue
			}
			wroteEvent := false
			for _, raw := range events {
				var ev TaskEvent
				if json.Unmarshal([]byte(raw), &ev) != nil {
					from++
					continue
				}
				if cursor != nil && (ev.ExecutionID != "" && ev.ExecutionID != cursor.RunID || ev.ModelOutput != nil && ev.ModelOutput.RunID != cursor.RunID) {
					from++
					continue
				}
				if cursor != nil && (ev.Type == "tool_calls" || ev.Type == "tool_results") {
					if ev.Seq <= lastStepSeq {
						from++
						continue
					}
					lastStepSeq = max(lastStepSeq, ev.Seq)
				}
				if ev.Type == "artifact" {
					artifacts, err := LoadArtifacts(ctx, db, taskID)
					if err != nil {
						pollDBUntilTerminal(ctx, db, w, flusher, taskID, workspacePath, lastStepSeq, cursor, sentArtifacts)
						return
					}
					writeTaskArtifactsSnapshot(w, flusher, task, artifacts, sentArtifacts)
					from++
					continue
				}
				if m := ev.ModelOutput; m != nil && cursor != nil && m.Phase != "" && m.CallIndex >= cursor.CallIndex {
					*cursor = *m
				}
				if ev.Type == "done" || ev.Type == "error" {
					from++
					continue
				}
				ev = prepareTaskEventForSSE(ev, workspacePath)
				if isArtifactStreamEvent(ev.Type) {
					wroteStreamEvent := writeArtifactStreamEventOnce(
						w, flusher, ev, sentArtifactStreamEvents,
					)
					wroteEvent = wroteStreamEvent || wroteEvent
					artifactStreamStarted = artifactStreamStarted || wroteStreamEvent
				} else {
					writeTaskSSE(w, flusher, ev)
					wroteEvent = true
				}
				from++
			}
			if heartbeatTimer != nil && artifactStreamStarted && wroteEvent {
				resetTaskHeartbeatTimer(heartbeatTimer)
			}
			// Check DB terminal state in case: (a) Redis stream expired mid-flight, or
			// (b) the task finished between the initial GetTask snapshot and the moment we
			// started tailing (race: done event already in LIST but skipped by from=len(existing)).
			// In both cases, emit terminal and stop regardless of whether the Redis key still exists.
			if isTerminal(task.Status) {
				closeTaskPreview(w, flusher, taskID, cursor)
				emitTaskTerminal(w, flusher, task)
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				flusher.Flush()
				return
			}
		case <-heartbeat:
			writeTaskHeartbeat(w, flusher)
			heartbeatTimer.Reset(taskWriterSSEHeartbeatInterval)
		}
	}
}

// pollDBUntilTerminal polls the DB row, emitting progress/artifact diffs until terminal.
func pollDBUntilTerminal(
	ctx context.Context,
	db *gorm.DB,
	w http.ResponseWriter,
	flusher http.Flusher,
	taskID string,
	workspacePath string,
	lastStepSeq int,
	cursor *common.ModelOutput,
	sentArtifacts map[string]bool,
) {
	lastProgress := -1
	lastPhase := ""
	lastSources := ""
	lastHeartbeat := time.Now()
	heartbeatsEnabled := false
	heartbeatsConfigured := false
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		t, err := GetTask(ctx, db, taskID)
		if err != nil {
			return
		}
		if cursor != nil && t.ExecutionID != cursor.RunID {
			lastStepSeq, cursor, err = refreshTaskStepsSnapshot(ctx, db, w, flusher, t, lastStepSeq, cursor, sentArtifacts)
			if err != nil {
				return
			}
		}
		if !heartbeatsConfigured {
			heartbeatsEnabled = taskStreamHeartbeatsEnabled(t)
			heartbeatsConfigured = true
		}
		if t.ProgressPct != lastProgress || t.CurrentPhase != lastPhase {
			writeTaskSSE(w, flusher, TaskEvent{
				Type: "progress", TaskID: taskID,
				Progress: t.ProgressPct, CurrentPhase: t.CurrentPhase, EstimatedSec: t.EstimatedSec,
			})
			lastProgress = t.ProgressPct
			lastPhase = t.CurrentPhase
		}
		sources := string(normalizeJSON(json.RawMessage(t.Sources), "[]"))
		if sources != lastSources {
			writeTaskSSE(w, flusher, TaskEvent{
				Type: "sources", TaskID: taskID, Sources: json.RawMessage(sources),
			})
			lastSources = sources
		}
		steps, _ := LoadSteps(ctx, db, taskID)
		for i := range steps {
			row := &steps[i]
			if row.Seq <= lastStepSeq {
				continue
			}
			lastStepSeq = row.Seq
			if row.ExecutionID != "" && row.ExecutionID != t.ExecutionID {
				continue
			}
			ev := stepToTaskEvent(taskID, row)
			if row.Role == "model_runtime" {
				var record taskModelRecord
				if json.Unmarshal(row.Content, &record) == nil && record.ModelOutput.RunID == t.ExecutionID {
					ev = &TaskEvent{Type: "model_output", TaskID: taskID, ExecutionID: t.ExecutionID, ModelOutput: &record.ModelOutput}
				}
			}
			if ev == nil {
				continue
			}
			if m := ev.ModelOutput; m != nil && cursor != nil {
				if m.CallIndex < cursor.CallIndex || m.CallIndex == cursor.CallIndex && cursor.Phase != "begin" {
					continue
				}
				if m.CallIndex > cursor.CallIndex && cursor.Phase != "begin" {
					begin := *m
					begin.Phase = "begin"
					writeTaskSSE(w, flusher, TaskEvent{Type: "model_output", TaskID: taskID, ExecutionID: t.ExecutionID, ModelOutput: &begin})
					cursor = &begin
				}
				if m.Phase != "" {
					value := *m
					cursor = &value
				}
			}
			writeTaskSSE(w, flusher, *ev)
		}
		arts, _ := LoadArtifacts(ctx, db, taskID)
		writeTaskArtifactsSnapshot(w, flusher, t, arts, sentArtifacts)
		if isTerminal(t.Status) {
			emitTaskTerminal(w, flusher, t)
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			flusher.Flush()
			return
		}
		if heartbeatsEnabled && time.Since(lastHeartbeat) >= taskWriterSSEHeartbeatInterval {
			writeTaskHeartbeat(w, flusher)
			lastHeartbeat = time.Now()
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func artifactDedupKey(a *orm.SubAgentArtifact) string {
	return a.Slot + "#" + strconv.Itoa(a.Seq)
}
