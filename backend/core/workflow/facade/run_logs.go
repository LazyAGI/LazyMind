package facade

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
)

type runLog struct {
	Time         time.Time `json:"time"`
	Event        string    `json:"event"`
	StepID       string    `json:"step_id,omitempty"`
	AttemptID    string    `json:"attempt_id,omitempty"`
	Attempt      int       `json:"attempt,omitempty"`
	Tool         string    `json:"tool,omitempty"`
	Status       string    `json:"status,omitempty"`
	DurationMS   *int64    `json:"duration_ms,omitempty"`
	ErrorCode    string    `json:"error_code,omitempty"`
	ErrorSummary string    `json:"error_summary,omitempty"`
}

type runLogs struct {
	RunID        string    `json:"run_id"`
	Cutoff       time.Time `json:"cutoff"`
	Scheduling   []runLog  `json:"scheduling"`
	Agent        []runLog  `json:"agent"`
	HostRequired bool      `json:"host_required"`
	HostGrant    string    `json:"host_grant,omitempty"`
	HostProvider string    `json:"host_provider,omitempty"`
}

var logIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.:-]{0,127}$`)
var logErrorCode = regexp.MustCompile(`^[A-Z][A-Z_0-9]{0,63}$`)

func logToken(value string) string {
	if logIdentifier.MatchString(value) {
		return value
	}
	return ""
}

func logStatus(value string) string {
	switch value {
	case "active", "waiting", "waiting_input", "pending", "queued", "claimed", "running",
		"paused", "stopped", "completed", "succeeded", "failed", "interrupted", "cancelled",
		"canceled", "skipped", "accepted", "dispatching", "unknown":
		return value
	}
	return ""
}

func (h Handler) RunLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	now := time.Now().UTC()
	cutoff := now
	if raw := r.URL.Query().Get("cutoff_ms"); raw != "" {
		ms, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || ms <= 0 {
			fail(w, http.StatusBadRequest, "INVALID_LOG_CUTOFF", "Invalid workflow log cutoff", false)
			return
		}
		if clicked := time.UnixMilli(ms).UTC(); clicked.Before(cutoff) {
			cutoff = clicked
		}
	}
	runID, owner := mux.Vars(r)["session_id"], r.Header.Get("X-User-Id")
	session, err := h.logSession(r.Context(), runID, owner)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "WORKFLOW_LOGS_UNAVAILABLE", "Workflow logs are unavailable", true)
		return
	}
	logs, err := h.readRunLogs(r.Context(), session, owner, cutoff)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "WORKFLOW_LOGS_UNAVAILABLE", "Workflow logs are unavailable", true)
		return
	}
	if logs.HostRequired {
		binding, err := controlstore.DecodeBinding(session)
		logs.HostProvider = logToken(binding.Provider)
		if err == nil && binding.ConnectorID != "" && binding.DriverSession != "" && binding.CredentialHash != "" {
			logs.HostGrant = issueHostLogGrant(runID, owner, binding, cutoff, now)
		}
	}
	writeJSON(w, http.StatusOK, envelope{Data: logs})
}

func (h Handler) logSession(ctx context.Context, runID, owner string) (orm.WorkflowSession, error) {
	var session orm.WorkflowSession
	err := h.Store.Database().WithContext(ctx).
		Select("id", "origin_host", "controller_host", "status", "control_binding_json", "created_at", "updated_at").
		Where("id = ? AND create_user_id = ?", runID, owner).Take(&session).Error
	return session, err
}

func (h Handler) readRunLogs(ctx context.Context, session orm.WorkflowSession, owner string, cutoff time.Time) (runLogs, error) {
	logs := runLogs{RunID: session.ID, Cutoff: cutoff, Scheduling: []runLog{}, Agent: []runLog{},
		HostRequired: session.OriginHost == "external-agent" || session.ControllerHost == "external-agent"}
	if !session.CreatedAt.IsZero() && !session.CreatedAt.After(cutoff) {
		logs.Scheduling = append(logs.Scheduling, runLog{Time: session.CreatedAt, Event: "workflow.started"})
	}
	last, err := h.Store.LatestEventID(ctx, session.ID, owner)
	if err != nil {
		return logs, err
	}
	for after := int64(0); after < last; {
		events, err := h.Store.Replay(ctx, session.ID, owner, after, 100)
		if err != nil {
			return logs, err
		}
		for _, event := range events {
			after = event.ID
			if event.ID > last || event.CreatedAt.After(cutoff) {
				continue
			}
			var fields struct {
				Status string `json:"status"`
				Kind   string `json:"kind"`
			}
			_ = json.Unmarshal(event.PayloadJSON, &fields)
			label := logToken(event.EventType)
			if label == "control.changed" && logToken(fields.Kind) != "" {
				label += "." + fields.Kind
			}
			logs.Scheduling = append(logs.Scheduling, runLog{Time: event.CreatedAt, Event: label, Status: logStatus(fields.Status)})
		}
		if len(events) < 100 {
			break
		}
	}

	db := h.Store.Database().WithContext(ctx)
	var attempts []orm.WorkflowSessionStep
	if err := db.Select("id", "step_id", "attempt", "task_id", "status", "terminal_code", "created_at", "updated_at").
		Where("session_id = ? AND created_at <= ?", session.ID, cutoff).Find(&attempts).Error; err != nil {
		return logs, err
	}
	for _, attempt := range attempts {
		entry := runLog{Time: attempt.CreatedAt, Event: "step.attempt_created", StepID: logToken(attempt.StepID), AttemptID: attempt.ID, Attempt: attempt.Attempt}
		logs.Scheduling = append(logs.Scheduling, entry)
		if !attempt.UpdatedAt.After(cutoff) {
			state := entry
			state.Time, state.Event, state.Status = attempt.UpdatedAt, "step.status", logStatus(attempt.Status)
			if logErrorCode.MatchString(attempt.TerminalCode) {
				state.ErrorCode = attempt.TerminalCode
				if state.Status == "failed" {
					state.ErrorSummary = "Workflow step failed"
				}
			}
			logs.Scheduling = append(logs.Scheduling, state)
		}
		if !logs.HostRequired && attempt.TaskID != "" {
			var task orm.SubAgentTask
			if err := db.Select("id", "status", "created_at", "started_at", "finished_at").
				Where("id = ? AND create_user_id = ? AND created_at <= ?", attempt.TaskID, owner, cutoff).Find(&task).Error; err != nil {
				return logs, err
			}
			if task.ID == "" {
				continue
			}
			var steps []orm.SubAgentStep
			if err := db.Select("role", "content", "created_at").
				Where("task_id = ? AND created_at <= ? AND role IN ?", task.ID, cutoff, []string{"assistant", "tool"}).
				Order("seq ASC").Find(&steps).Error; err != nil {
				return logs, err
			}
			entry.Event, entry.Time = "agent.task", task.CreatedAt
			if task.StartedAt != nil && !task.StartedAt.After(cutoff) {
				entry.Time, entry.Status = *task.StartedAt, "running"
			}
			logs.Agent = append(logs.Agent, entry)
			logs.Agent = append(logs.Agent, nativeToolLogs(entry, steps)...)
			if task.FinishedAt != nil && !task.FinishedAt.After(cutoff) {
				finished := entry
				finished.Time, finished.Status = *task.FinishedAt, logStatus(task.Status)
				duration := max(int64(0), finished.Time.Sub(entry.Time).Milliseconds())
				finished.DurationMS = &duration
				logs.Agent = append(logs.Agent, finished)
			}
		}
	}
	if !session.UpdatedAt.After(cutoff) && logStatus(session.Status) != "" {
		logs.Scheduling = append(logs.Scheduling, runLog{Time: session.UpdatedAt, Event: "workflow.status", Status: session.Status})
	}
	return logs, nil
}

func nativeToolLogs(attempt runLog, steps []orm.SubAgentStep) []runLog {
	type toolRecord struct {
		ID       string          `json:"id"`
		CallID   string          `json:"tool_call_id"`
		Name     string          `json:"name"`
		Result   json.RawMessage `json:"result"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	calls := make(map[string]runLog)
	var logs []runLog
	for _, step := range steps {
		var content struct {
			Calls   []toolRecord `json:"tool_calls"`
			Results []toolRecord `json:"tool_results"`
		}
		if json.Unmarshal(step.Content, &content) != nil {
			continue
		}
		for _, call := range content.Calls {
			entry := attempt
			entry.Time, entry.Event, entry.Tool, entry.Status = step.CreatedAt, "tool.call", logToken(call.Name), "running"
			if entry.Tool == "" {
				entry.Tool = logToken(call.Function.Name)
			}
			if callID := cmp.Or(call.ID, call.CallID); callID != "" {
				calls[callID] = entry
			}
			logs = append(logs, entry)
		}
		for _, result := range content.Results {
			entry := attempt
			entry.Time, entry.Event, entry.Tool = step.CreatedAt, "tool.result", logToken(result.Name)
			if call, ok := calls[cmp.Or(result.CallID, result.ID)]; ok {
				duration := max(int64(0), step.CreatedAt.Sub(call.Time).Milliseconds())
				entry.Tool, entry.DurationMS = call.Tool, &duration
			}
			entry.Status = "completed"
			var outcome struct {
				IsError  bool            `json:"is_error"`
				MCPError bool            `json:"isError"`
				Error    json.RawMessage `json:"error"`
			}
			if json.Unmarshal(result.Result, &outcome) == nil && (outcome.IsError || outcome.MCPError || (len(outcome.Error) > 0 && string(outcome.Error) != "null" && string(outcome.Error) != "false" && string(outcome.Error) != `""`)) {
				entry.Status, entry.ErrorCode, entry.ErrorSummary = "failed", "TOOL_ERROR", "Tool reported an error"
			}
			logs = append(logs, entry)
		}
	}
	return logs
}
