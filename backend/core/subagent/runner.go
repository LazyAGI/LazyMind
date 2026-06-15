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

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"lazymind/core/common"
)

// runPath is the algorithm-layer SubAgent execution endpoint.
const runPath = "/api/subagent/run"

// subagentRunTimeout bounds a single SubAgent execution. Long tasks rely on ctx, not this ceiling.
const subagentRunTimeout = 2 * time.Hour

// RunRequest is the body posted to the algorithm layer /api/subagent/run.
// task_id doubles as the request sid (independent FileSystemQueue bucket).
type RunRequest struct {
	TaskID             string         `json:"task_id"`
	AgentType          string         `json:"agent_type"`
	Objective          string         `json:"objective"`
	Params             map[string]any `json:"params,omitempty"`
	InputArtifactKeys  []string       `json:"input_artifact_keys"`
	OutputArtifactKeys []string       `json:"output_artifact_keys"`
	WorkspacePath      string         `json:"workspace_path"`
	Tools              []string       `json:"tools,omitempty"`
	DBDSN              string         `json:"db_dsn"`
	Resume             bool           `json:"resume"`
	LLMConfig          map[string]any `json:"llm_config,omitempty"`
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
func Run(ctx context.Context, db *gorm.DB, rdb *redis.Client, req RunRequest) error {
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
		routeError(runCtx, db, rdb, req.TaskID, fmt.Sprintf("subagent run request failed: %v", err))
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		routeError(runCtx, db, rdb, req.TaskID, fmt.Sprintf("subagent run returned HTTP %d", resp.StatusCode))
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
		routeEvent(runCtx, db, rdb, ev)
	}
	if err := scanner.Err(); err != nil && runCtx.Err() == nil {
		routeError(runCtx, db, rdb, req.TaskID, fmt.Sprintf("subagent stream read error: %v", err))
		return err
	}
	return nil
}

// routeEvent persists a SubAgent event to DB (authoritative), then appends to Redis (live tail).
func routeEvent(ctx context.Context, db *gorm.DB, rdb *redis.Client, ev TaskEvent) {
	switch ev.Type {
	case "task_start":
		_ = UpdateStatus(ctx, db, ev.TaskID, StatusRunning)
		_ = WriteStatus(ctx, rdb, ev.TaskID, map[string]any{"status": StatusRunning, "progress": 0})
	case "progress":
		_ = UpdateProgress(ctx, db, ev.TaskID, ev.Progress, ev.CurrentPhase, ev.EstimatedSec)
		_ = WriteStatus(ctx, rdb, ev.TaskID, map[string]any{
			"status": StatusRunning, "progress": ev.Progress, "current_phase": ev.CurrentPhase,
		})
	case "artifact":
		seq := ev.Seq
		if seq <= 0 {
			seq = 1
		}
		_ = SaveArtifact(ctx, db, ev.TaskID, ev.ArtifactKey, ev.ContentType, ev.Value, seq)
	case "done":
		status := ev.Status
		if status == "" {
			status = StatusSucceeded
		}
		_ = UpdateFinalStatus(ctx, db, ev.TaskID, status, ev.Summary)
		_ = WriteStatus(ctx, rdb, ev.TaskID, map[string]any{
			"status": status, "progress": 100, "summary": ev.Summary,
		})
	case "error":
		status := ev.Status
		if status == "" {
			status = StatusFailed
		}
		_ = UpdateFinalStatus(ctx, db, ev.TaskID, status, ev.Message)
		_ = WriteStatus(ctx, rdb, ev.TaskID, map[string]any{"status": status, "summary": ev.Message})
	}
	_ = AppendStreamEvent(ctx, rdb, ev.TaskID, ev)
}

// routeError synthesizes a terminal error event when the run cannot be driven by the stream.
func routeError(ctx context.Context, db *gorm.DB, rdb *redis.Client, taskID, message string) {
	ev := TaskEvent{Type: "error", TaskID: taskID, Status: StatusFailed, Message: message}
	_ = UpdateFinalStatus(ctx, db, taskID, StatusFailed, message)
	_ = WriteStatus(ctx, rdb, taskID, map[string]any{"status": StatusFailed, "summary": message})
	_ = AppendStreamEvent(ctx, rdb, taskID, ev)
}
