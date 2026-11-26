package assistantbridge

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/workflowhost"
)

type workflowLogRead struct {
	ID              string `json:"id"`
	RunID           string `json:"run_id"`
	DriverID        string `json:"driver_session_id"`
	CutoffMS        int64  `json:"cutoff_ms"`
	ResultsCutoffMS int64  `json:"results_cutoff_ms"`
	StartedMS       int64  `json:"started_ms"`
}

type workflowHostLog struct {
	Time         time.Time `json:"time"`
	Event        string    `json:"event"`
	Tool         string    `json:"tool,omitempty"`
	Status       string    `json:"status,omitempty"`
	DurationMS   *int64    `json:"duration_ms,omitempty"`
	ErrorCode    string    `json:"error_code,omitempty"`
	ErrorSummary string    `json:"error_summary,omitempty"`
}

type pendingWorkflowLog struct {
	read    workflowLogRead
	pair    workflowhost.Pairing
	sent    bool
	entries chan []workflowHostLog
}

type workflowLogBinding struct {
	Connector     string `json:"connector_id"`
	Provider      string `json:"provider"`
	Driver        string `json:"driver_session_id"`
	Cutoff        int64  `json:"cutoff_ms"`
	ResultsCutoff int64  `json:"results_cutoff_ms"`
	Started       int64  `json:"started_ms"`
}

func (s *Server) handleWorkflowLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		Grant string `json:"grant"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input) != nil || input.Grant == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "INVALID_LOG_GRANT"})
		return
	}
	api, err := coreapi.New(s.store)
	if err != nil {
		workflowHostError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	scope, err := s.store.AccountScope()
	if err != nil {
		workflowHostError(w, err)
		return
	}
	runID := r.PathValue("session")
	path := "/workflow-sessions/" + url.PathEscape(runID) + "/log-export"
	var binding workflowLogBinding
	if err := api.DoJSON(ctx, http.MethodPost, path, input, &binding); err != nil {
		workflowHostError(w, err)
		return
	}
	pair, err := workflowhost.Load(s.store.Directory(), binding.Connector, scope)
	if err != nil || pair.Provider != binding.Provider {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "LOG_HOST_NOT_PAIRED"})
		return
	}
	headers := http.Header{"X-Workflow-Host-Credential": {pair.Token}}
	if err := api.DoJSONHeaders(ctx, http.MethodPost, path, input, nil, headers); err != nil {
		workflowHostError(w, err)
		return
	}

	read := workflowLogRead{ID: rand.Text(), RunID: runID, DriverID: binding.Driver, CutoffMS: binding.Cutoff,
		ResultsCutoffMS: max(binding.ResultsCutoff, binding.Cutoff), StartedMS: binding.Started}
	finish := func(entries []workflowHostLog) {
		if entries == nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "LOG_HOST_UNAVAILABLE"})
			return
		}
		if _, err := workflowhost.Verify(s.store.Directory(), pair.ConnectorID, pair.Token, scope); err != nil {
			workflowHostError(w, err)
			return
		}
		if err := api.DoJSONHeaders(ctx, http.MethodPost, path, input, nil, headers); err != nil {
			workflowHostError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"run_id": runID, "entries": filterWorkflowHostLogs(read, entries)})
	}
	if pair.Provider == "workbuddy" {
		finish(readWorkBuddyWorkflowLogs(ctx, read))
		return
	}
	job := &pendingWorkflowLog{read: read, pair: pair, entries: make(chan []workflowHostLog, 1)}
	s.mu.Lock()
	if s.workflowLogs[pair.ConnectorID] != nil {
		s.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"code": "LOG_HOST_BUSY"})
		return
	}
	if s.workflowLogs == nil {
		s.workflowLogs = make(map[string]*pendingWorkflowLog)
	}
	s.workflowLogs[pair.ConnectorID] = job
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.workflowLogs, pair.ConnectorID)
		s.mu.Unlock()
	}()

	select {
	case entries := <-job.entries:
		finish(entries)
	case <-ctx.Done():
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"code": "LOG_HOST_TIMEOUT"})
	}
}

func (s *Server) takeWorkflowLogRead(pair workflowhost.Pairing) *workflowLogRead {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.workflowLogs[pair.ConnectorID]
	if job == nil || job.sent || job.pair != pair {
		return nil
	}
	job.sent = true
	return &job.read
}

var workflowLogTool = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.:-]{0,127}$`)

func (s *Server) handleWorkflowLogResult(w http.ResponseWriter, r *http.Request) {
	pair, _, ok := s.workflowHost(w, r)
	if !ok {
		return
	}
	var entries []workflowHostLog
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&entries) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "LOG_HOST_INVALID_RESULT"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.workflowLogs[pair.ConnectorID]
	if job == nil || !job.sent || job.pair != pair || job.read.ID != r.PathValue("request") {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "LOG_HOST_REQUEST_EXPIRED"})
		return
	}
	select {
	case job.entries <- entries:
	default:
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func filterWorkflowHostLogs(read workflowLogRead, entries []workflowHostLog) []workflowHostLog {
	valid := make([]workflowHostLog, 0, len(entries))
	for _, entry := range entries {
		at := entry.Time.UnixMilli()
		if entry.Time.IsZero() || at > read.ResultsCutoffMS {
			continue
		}
		if at > read.CutoffMS && (entry.Event != "tool.result" || entry.DurationMS == nil || *entry.DurationMS < at-read.CutoffMS) {
			continue
		}
		switch entry.Event {
		case "agent.turn", "tool.call", "tool.result":
		default:
			continue
		}
		switch entry.Status {
		case "", "running", "completed", "failed", "interrupted":
		default:
			entry.Status = ""
		}
		if !workflowLogTool.MatchString(entry.Tool) {
			entry.Tool = ""
		}
		if entry.DurationMS != nil && *entry.DurationMS < 0 {
			entry.DurationMS = nil
		}
		entry.ErrorCode, entry.ErrorSummary = "", ""
		if entry.Status == "failed" {
			entry.ErrorCode, entry.ErrorSummary = "HOST_EXECUTION_FAILED", "Host reported an execution error"
		}
		valid = append(valid, entry)
	}
	return valid
}
