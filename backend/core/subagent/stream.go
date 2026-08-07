package subagent

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
)

const taskSSEHeartbeatInterval = 30 * time.Second
const taskWriterSSEHeartbeatInterval = 2 * time.Second

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

func isWriterDraftStreamTask(task *orm.SubAgentTask) bool {
	if task == nil || task.AgentType != "plugin_step" {
		return false
	}
	var params struct {
		PluginID string `json:"plugin_id"`
		StepID   string `json:"step_id"`
	}
	return json.Unmarshal(task.Params, &params) == nil &&
		params.PluginID == "writer-plugin" && params.StepID == "write_document"
}

func writerDraftHeartbeatsEnabled(ctx context.Context, db *gorm.DB, taskID string) bool {
	if db == nil {
		return false
	}
	task, err := GetTask(ctx, db, taskID)
	return err == nil && isWriterDraftStreamTask(task)
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
	taskID := common.PathVar(r, "task_id")
	if taskID == "" {
		common.ReplyErr(w, "task_id required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		common.ReplyErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	stateStore := store.State()

	t, err := GetTask(ctx, db, taskID)
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "task not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query task failed", http.StatusInternalServerError)
		return
	}
	if t.CreateUserID != requestUserID(r) {
		common.ReplyErr(w, "task not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// 1. DB snapshot: task_start + history progress + history artifacts + history steps.
	writeTaskSSE(w, flusher, TaskEvent{Type: "task_start", TaskID: taskID})
	writeTaskSSE(w, flusher, TaskEvent{
		Type: "progress", TaskID: taskID,
		Progress: t.ProgressPct, CurrentPhase: t.CurrentPhase, EstimatedSec: t.EstimatedSec,
	})
	steps, _ := LoadSteps(ctx, db, taskID)
	lastStepSeq := -1
	for i := range steps {
		if steps[i].Seq > lastStepSeq {
			lastStepSeq = steps[i].Seq
		}
		ev := stepToTaskEvent(taskID, &steps[i])
		if ev != nil {
			writeTaskSSE(w, flusher, *ev)
		}
	}
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
			_, _, _ = replayArtifactStreamEvents(ctx, stateStore, w, flusher, taskID, nil)
		}
		emitTerminal(w, flusher, taskID, t.Status, t.Summary)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
		return
	}

	// 3. Still running: start at offset zero even when the LIST does not exist yet.
	// The first Draft stream event may be created after the browser connects.
	if stateStore == nil {
		pollDBUntilTerminal(ctx, db, w, flusher, taskID, t.WorkspacePath, lastStepSeq)
		return
	}
	tailRedisStream(ctx, db, stateStore, w, flusher, taskID, t.WorkspacePath, lastStepSeq)
}

func replayArtifactStreamEvents(
	ctx context.Context,
	stateStore state.Store,
	w http.ResponseWriter,
	flusher http.Flusher,
	taskID string,
	sent map[string]struct{},
) (int64, *TaskEvent, error) {
	existing, err := StreamEventsFrom(ctx, stateStore, taskID, 0)
	if err != nil {
		return 0, nil, err
	}
	var terminal *TaskEvent
	for _, raw := range existing {
		var ev TaskEvent
		if json.Unmarshal([]byte(raw), &ev) != nil {
			continue
		}
		if isArtifactStreamEvent(ev.Type) {
			writeArtifactStreamEventOnce(w, flusher, ev, sent)
		}
		if ev.Type == "done" || ev.Type == "error" {
			copy := ev
			terminal = &copy
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

// stepToTaskEvent converts a persisted step back to a TaskEvent for the DB snapshot replay.
// Returns nil for step roles that have no frontend representation.
func stepToTaskEvent(taskID string, s *orm.SubAgentStep) *TaskEvent {
	switch s.Role {
	case "text":
		var c struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(s.Content, &c)
		if c.Content == "" {
			return nil
		}
		return &TaskEvent{Type: "text", TaskID: taskID, Text: c.Content}
	case "think":
		var c struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(s.Content, &c)
		if c.Content == "" {
			return nil
		}
		return &TaskEvent{Type: "think", TaskID: taskID, Think: c.Content}
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
			return &TaskEvent{Type: "tool_calls", TaskID: taskID, ToolCalls: c.ToolCalls}
		}
		var c struct {
			ToolResults json.RawMessage `json:"tool_results"`
		}
		_ = json.Unmarshal(s.Content, &c)
		if len(c.ToolResults) == 0 {
			return nil
		}
		return &TaskEvent{Type: "tool_results", TaskID: taskID, ToolResults: c.ToolResults}
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
) {
	liveEvents, unsubscribe := taskLiveEvents.subscribe(taskID)
	defer unsubscribe()
	sentArtifactStreamEvents := make(map[string]struct{})

	// DB-backed events were already sent by the snapshot. Draft preview events are
	// Redis-only, so replay all of those still available within the LIST TTL.
	from, terminal, err := replayArtifactStreamEvents(
		ctx, stateStore, w, flusher, taskID, sentArtifactStreamEvents,
	)
	if err != nil {
		pollDBUntilTerminal(ctx, db, w, flusher, taskID, workspacePath, lastStepSeq)
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
				steps, _ := LoadSteps(ctx, db, taskID)
				for i := range steps {
					if steps[i].Seq > lastStepSeq {
						lastStepSeq = steps[i].Seq
					}
				}
				pollDBUntilTerminal(ctx, db, w, flusher, taskID, workspacePath, lastStepSeq)
				return
			}
			wroteEvent := false
			for _, raw := range events {
				var ev TaskEvent
				if json.Unmarshal([]byte(raw), &ev) != nil {
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
				emitTerminal(w, flusher, taskID, t.Status, t.Summary)
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
) {
	lastProgress := -1
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
			heartbeatsEnabled = isWriterDraftStreamTask(t)
			heartbeatsConfigured = true
		}
		if t.ProgressPct != lastProgress {
			writeTaskSSE(w, flusher, TaskEvent{
				Type: "progress", TaskID: taskID,
				Progress: t.ProgressPct, CurrentPhase: t.CurrentPhase, EstimatedSec: t.EstimatedSec,
			})
			lastProgress = t.ProgressPct
		}
		steps, _ := LoadSteps(ctx, db, taskID)
		for i := range steps {
			if steps[i].Seq <= lastStepSeq {
				continue
			}
			if ev := stepToTaskEvent(taskID, &steps[i]); ev != nil {
				writeTaskSSE(w, flusher, *ev)
			}
			lastStepSeq = steps[i].Seq
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
			emitTerminal(w, flusher, taskID, t.Status, t.Summary)
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
