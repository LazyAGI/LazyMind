package subagent

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

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

// StreamTask handles GET /tasks/{task_id}:stream.
// Reconnect protocol: DB snapshot (task_start + history progress + history artifacts) first,
// then if terminal send done/error; if still running, tail Redis (fallback to DB polling).
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
	rdb := store.Redis()

	t, err := GetTask(ctx, db, taskID)
	if err != nil {
		if IsNotFound(err) {
			common.ReplyErr(w, "task not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query task failed", http.StatusInternalServerError)
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
	for i := range steps {
		ev := stepToTaskEvent(taskID, &steps[i])
		if ev != nil {
			writeTaskSSE(w, flusher, *ev)
		}
	}
	arts, _ := LoadArtifacts(ctx, db, taskID)
	for i := range arts {
		writeTaskSSE(w, flusher, TaskEvent{
			Type: "artifact", TaskID: taskID,
			ArtifactKey: arts[i].ArtifactKey, ContentType: arts[i].ContentType,
			Seq: arts[i].Seq, Value: normalizeJSON(arts[i].Value, "{}"),
		})
	}

	// 2. Already terminal: emit done/error and stop (no Redis subscription).
	if isTerminal(t.Status) {
		emitTerminal(w, flusher, taskID, t.Status, t.Summary)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
		return
	}

	// 3. Still running: tail Redis from current end; fall back to DB polling if key missing.
	exists, _ := StreamExists(ctx, rdb, taskID)
	if rdb == nil || !exists {
		pollDBUntilTerminal(ctx, db, w, flusher, taskID)
		return
	}
	tailRedisStream(ctx, db, rdb, w, flusher, taskID)
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

// tailRedisStream tails the Redis event LIST from current end until a terminal event arrives.
func tailRedisStream(ctx context.Context, db *gorm.DB, rdb *redis.Client, w http.ResponseWriter, flusher http.Flusher, taskID string) {
	// Start tailing from the current tail so we only forward new events (snapshot already sent).
	existing, _ := StreamEventsFrom(ctx, rdb, taskID, 0)
	from := int64(len(existing))
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		events, err := StreamEventsFrom(ctx, rdb, taskID, from)
		if err != nil {
			pollDBUntilTerminal(ctx, db, w, flusher, taskID)
			return
		}
		for _, raw := range events {
			var ev TaskEvent
			if json.Unmarshal([]byte(raw), &ev) != nil {
				from++
				continue
			}
			writeTaskSSE(w, flusher, ev)
			from++
			if ev.Type == "done" || ev.Type == "error" {
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				flusher.Flush()
				return
			}
		}
		// Check DB terminal state in case Redis stream expired mid-flight.
		if t, err := GetTask(ctx, db, taskID); err == nil && isTerminal(t.Status) {
			if exists, _ := StreamExists(ctx, rdb, taskID); !exists {
				emitTerminal(w, flusher, taskID, t.Status, t.Summary)
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				flusher.Flush()
				return
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// pollDBUntilTerminal polls the DB row, emitting progress/artifact diffs until terminal.
func pollDBUntilTerminal(ctx context.Context, db *gorm.DB, w http.ResponseWriter, flusher http.Flusher, taskID string) {
	lastProgress := -1
	sentArtifacts := map[string]bool{}
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
		if t.ProgressPct != lastProgress {
			writeTaskSSE(w, flusher, TaskEvent{
				Type: "progress", TaskID: taskID,
				Progress: t.ProgressPct, CurrentPhase: t.CurrentPhase, EstimatedSec: t.EstimatedSec,
			})
			lastProgress = t.ProgressPct
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
				ArtifactKey: arts[i].ArtifactKey, ContentType: arts[i].ContentType,
				Seq: arts[i].Seq, Value: normalizeJSON(arts[i].Value, "{}"),
			})
		}
		if isTerminal(t.Status) {
			emitTerminal(w, flusher, taskID, t.Status, t.Summary)
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			flusher.Flush()
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func artifactDedupKey(a *orm.SubAgentArtifact) string {
	return a.ArtifactKey + "#" + strconv.Itoa(a.Seq)
}
