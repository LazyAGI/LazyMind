package assistantbridge

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/workflowhost"
)

func workflowHostError(w http.ResponseWriter, err error) {
	var apiError *coreapi.Error
	if errors.As(err, &apiError) {
		writeJSON(w, apiError.StatusCode, map[string]string{"code": apiError.Code, "error": apiError.Message})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"code": "WORKFLOW_HOST_UNAVAILABLE", "error": err.Error()})
}

func (s *Server) workflowHost(w http.ResponseWriter, r *http.Request) (workflowhost.Pairing, *coreapi.Client, bool) {
	scope, err := s.store.AccountScope()
	if err != nil {
		workflowHostError(w, err)
		return workflowhost.Pairing{}, nil, false
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	pair, err := workflowhost.Verify(s.store.Directory(), r.Header.Get("X-LazyMind-Connector-Id"), token, scope)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return pair, nil, false
	}
	api, err := coreapi.New(s.store)
	if err != nil {
		workflowHostError(w, err)
		return pair, nil, false
	}
	return pair, api, true
}

func (s *Server) handleWorkflowHostBind(w http.ResponseWriter, r *http.Request) {
	pair, api, ok := s.workflowHost(w, r)
	if !ok {
		return
	}
	var input struct {
		RunID    string `json:"run_id"`
		Driver   string `json:"driver_session_id"`
		Executor string `json:"executor_session_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&input); err != nil || input.RunID == "" || input.Driver == "" {
		writeJSON(w, 422, map[string]string{"error": "run_id and driver_session_id are required"})
		return
	}
	var result map[string]any
	err := api.DoJSON(r.Context(), http.MethodPost, "/workflow-sessions/"+url.PathEscape(input.RunID)+"/host-binding", map[string]any{
		"connector_id": pair.ConnectorID, "credential": pair.Token, "provider": "deepseek-harness",
		"driver_session_id": input.Driver, "executor_session_id": input.Executor,
	}, &result)
	if err != nil {
		workflowHostError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) handleWorkflowHostState(w http.ResponseWriter, r *http.Request) {
	_, api, ok := s.workflowHost(w, r)
	if !ok {
		return
	}
	var result map[string]any
	err := api.DoJSON(r.Context(), http.MethodGet, "/workflow-sessions/"+url.PathEscape(r.PathValue("session"))+"/control?view=control", nil, &result)
	if err != nil {
		workflowHostError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) handleWorkflowHostActions(w http.ResponseWriter, r *http.Request) {
	pair, api, ok := s.workflowHost(w, r)
	if !ok {
		return
	}
	query := url.Values{"connector_id": {pair.ConnectorID}}
	if after := r.URL.Query().Get("after"); after != "" {
		query.Set("after", after)
	}
	var result map[string]any
	err := api.DoJSONHeaders(r.Context(), http.MethodGet, "/workflow-host-actions?"+query.Encode(), nil, &result,
		http.Header{"X-Workflow-Host-Credential": {pair.Token}})
	if err != nil {
		workflowHostError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) handleWorkflowHostAction(w http.ResponseWriter, r *http.Request) {
	pair, api, ok := s.workflowHost(w, r)
	if !ok {
		return
	}
	var result map[string]any
	err := api.DoJSONHeaders(r.Context(), http.MethodGet, "/workflow-host-actions/"+url.PathEscape(r.PathValue("action"))+"?connector_id="+url.QueryEscape(pair.ConnectorID),
		nil, &result, http.Header{"X-Workflow-Host-Credential": {pair.Token}})
	if err != nil {
		workflowHostError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) handleWorkflowHostClaim(w http.ResponseWriter, r *http.Request) {
	pair, api, ok := s.workflowHost(w, r)
	if !ok {
		return
	}
	var input struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&input); err != nil || input.InstanceID == "" {
		writeJSON(w, 422, map[string]string{"error": "instance_id is required"})
		return
	}
	var result map[string]any
	err := api.DoJSON(r.Context(), http.MethodPost, "/workflow-host-actions/"+url.PathEscape(r.PathValue("action"))+":claim", map[string]any{
		"connector_id": pair.ConnectorID, "credential": pair.Token, "instance_id": input.InstanceID,
	}, &result)
	if err != nil {
		workflowHostError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) handleWorkflowHostReceipt(w http.ResponseWriter, r *http.Request) {
	pair, api, ok := s.workflowHost(w, r)
	if !ok {
		return
	}
	var input struct {
		InstanceID     string `json:"instance_id"`
		DispatchToken  string `json:"dispatch_token"`
		Status         string `json:"status"`
		NativeEventSeq int64  `json:"native_event_seq,omitempty"`
		Error          string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&input); err != nil {
		writeJSON(w, 422, map[string]string{"error": "invalid host receipt"})
		return
	}
	var result map[string]any
	err := api.DoJSON(r.Context(), http.MethodPost, "/workflow-host-actions/"+url.PathEscape(r.PathValue("action"))+":settle", map[string]any{
		"connector_id": pair.ConnectorID, "credential": pair.Token, "instance_id": input.InstanceID,
		"dispatch_token": input.DispatchToken, "status": input.Status, "native_event_seq": input.NativeEventSeq, "error": input.Error,
	}, &result)
	if err != nil {
		workflowHostError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
