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
	writeTaskSSE(w, flusher, TaskEvent{Type: "task_start", TaskID: taskID, ExecutionID: t.ExecutionID})
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
	for i := range arts {
		if arts[i].Hidden {
			continue
		}
		writeTaskSSE(w, flusher, TaskEvent{
			Type: "artifact", TaskID: taskID,
			ArtifactKey: arts[i].Slot, ContentType: arts[i].ContentType,
			Seq: arts[i].Seq,
			Value: SignArtifactValue(
				arts[i].ContentType, normalizeJSON(arts[i].Value, "{}"), t.WorkspacePath,
			),
		})
	}

	// 2. Already terminal: replay any Redis-only Draft preview still within TTL,
	// then emit the authoritative DB terminal state.
	if isTerminal(t.Status) {
		if stateStore != nil {
			_, _, _ = replayArtifactStreamEvents(ctx, stateStore, w, flusher, taskID, nil, cursor, true)
		}
		emitTaskTerminal(w, flusher, t)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
		return
	}

	// 3. Still running: start at offset zero even when the LIST does not exist yet.
	// The first Draft stream event may be created after the browser connects.
	if stateStore == nil {
		pollDBUntilTerminal(ctx, db, w, flusher, taskID, t.WorkspacePath, lastStepSeq, cursor)
		return
	}
	tailRedisStream(ctx, db, stateStore, w, flusher, taskID, t.WorkspacePath, lastStepSeq, cursor)
}

func replayArtifactStreamEvents(
	ctx context.Context,
	stateStore state.Store,
	w http.ResponseWriter,
	flusher http.Flusher,
	taskID string,
	sent map[string]struct{},
	cursor *common.ModelOutput, terminalStatus bool,
) (int64, *TaskEvent, error) {
	existing, err := StreamEventsFrom(ctx, stateStore, taskID, 0)
	if err != nil {
		return 0, nil, err
	}
	var terminal *TaskEvent
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
		if ev.Type == "done" || ev.Type == "error" {
			if cursor != nil && ev.ExecutionID != cursor.RunID {
				continue
			}
			copy := ev
			terminal = &copy
		}
	}
	if len(pending) > 0 {
		if terminalStatus || terminal != nil {
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

	return int64(len(existing)), terminal, nil
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

func refreshTaskStepsSnapshot(ctx context.Context, db *gorm.DB, w http.ResponseWriter, flusher http.Flusher, taskID string, seq int, cursor *common.ModelOutput) (int, *common.ModelOutput) {
	task, err := GetTask(ctx, db, taskID)
	if err != nil {
		return seq, cursor
	}
	steps, err := LoadSteps(ctx, db, taskID)
	if err != nil {
		return seq, cursor
	}
	writeTaskSSE(w, flusher, TaskEvent{Type: "task_start", TaskID: taskID, ExecutionID: task.ExecutionID})
	return writeTaskStepsSnapshot(w, flusher, task, steps)
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
			return &TaskEvent{Type: "tool_calls", TaskID: taskID, ToolCalls: c.ToolCalls, ExecutionID: s.ExecutionID}
		}
		var c struct {
			ToolResults json.RawMessage `json:"tool_results"`
		}
		_ = json.Unmarshal(s.Content, &c)
		if len(c.ToolResults) == 0 {
			return nil
		}
		return &TaskEvent{Type: "tool_results", TaskID: taskID, ToolResults: c.ToolResults, ExecutionID: s.ExecutionID}
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
) {
	liveEvents, unsubscribe := taskLiveEvents.subscribe(taskID)
	defer unsubscribe()
	sentArtifactStreamEvents := make(map[string]struct{})

	// DB-backed events were already sent by the snapshot. Draft preview events are
	// Redis-only, so replay all of those still available within the LIST TTL.
	from, terminal, err := replayArtifactStreamEvents(
		ctx, stateStore, w, flusher, taskID, sentArtifactStreamEvents, cursor, false,
	)
	if err != nil {
		if cursor != nil {
			lastStepSeq, cursor = refreshTaskStepsSnapshot(ctx, db, w, flusher, taskID, lastStepSeq, cursor)
		}
		pollDBUntilTerminal(ctx, db, w, flusher, taskID, workspacePath, lastStepSeq, cursor)
		return
	}
	if terminal != nil {
		writeTaskSSE(w, flusher, *terminal)
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
					lastStepSeq, cursor = refreshTaskStepsSnapshot(ctx, db, w, flusher, taskID, lastStepSeq, cursor)
					pollDBUntilTerminal(ctx, db, w, flusher, taskID, workspacePath, lastStepSeq, cursor)
					return
				}
				steps, _ := LoadSteps(ctx, db, taskID)
				for i := range steps {
					if steps[i].Seq > lastStepSeq {
						lastStepSeq = steps[i].Seq
					}
				}
				pollDBUntilTerminal(ctx, db, w, flusher, taskID, workspacePath, lastStepSeq, cursor)
				return
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
				if m := ev.ModelOutput; m != nil && cursor != nil && m.Phase != "" && m.CallIndex >= cursor.CallIndex {
					*cursor = *m
				}
				if ev.Type == "done" || ev.Type == "error" {
					closeTaskPreview(w, flusher, taskID, cursor)
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
				if ev.Type == "done" || ev.Type == "error" {
					_, _ = w.Write([]byte("data: [DONE]\n\n"))
					flusher.Flush()
					return
				}
			}
			if heartbeatTimer != nil && artifactStreamStarted && wroteEvent {
				resetTaskHeartbeatTimer(heartbeatTimer)
			}
			// Check DB terminal state in case: (a) Redis stream expired mid-flight, or
			// (b) the task finished between the initial GetTask snapshot and the moment we
			// started tailing (race: done event already in LIST but skipped by from=len(existing)).
			// In both cases, emit terminal and stop regardless of whether the Redis key still exists.
			if t, err := GetTask(ctx, db, taskID); err == nil && isTerminal(t.Status) {
				closeTaskPreview(w, flusher, taskID, cursor)
				emitTaskTerminal(w, flusher, t)
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
) {
	lastProgress := -1
	lastPhase := ""
	lastSources := ""
	sentArtifacts := map[string]bool{}
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
		for i := range arts {
			key := artifactDedupKey(&arts[i])
			if sentArtifacts[key] {
				continue
			}
			sentArtifacts[key] = true
			writeTaskSSE(w, flusher, TaskEvent{
				Type: "artifact", TaskID: taskID,
				ArtifactKey: arts[i].Slot, ContentType: arts[i].ContentType,
				Seq: arts[i].Seq,
				Value: SignArtifactValue(
					arts[i].ContentType, normalizeJSON(arts[i].Value, "{}"), workspacePath,
				),
			})
		}
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
