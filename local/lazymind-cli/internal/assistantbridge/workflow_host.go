package assistantbridge

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

func workflowHostError(w http.ResponseWriter, err error) {
	if credentials.IsAuthenticationRequired(err) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "HOST_AUTH_REQUIRED", "error": "Sign in to LazyMind to restore the workflow connection"})
		return
	}
	var pairingError *workflowhost.PairingError
	if errors.As(err, &pairingError) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": pairingError.Code, "error": pairingError.Error()})
		return
	}
	var apiError *coreapi.Error
	if errors.As(err, &apiError) {
		writeJSON(w, apiError.StatusCode, map[string]string{"code": apiError.Code, "error": apiError.Message})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"code": "WORKFLOW_HOST_UNAVAILABLE", "error": err.Error()})
}

func relayWorkflowHost(w http.ResponseWriter, r *http.Request, api *coreapi.Client, method, path string, body any, headers http.Header) {
	var result map[string]any
	if err := api.DoJSONHeaders(r.Context(), method, path, body, &result, headers); err != nil {
		workflowHostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) workflowPairing(w http.ResponseWriter, r *http.Request, localSnapshot bool) (workflowhost.Pairing, bool) {
	accountScope := s.store.AccountScope
	if localSnapshot {
		accountScope = s.store.AccountScopeSnapshot
	}
	scope, err := accountScope()
	if err != nil {
		workflowHostError(w, err)
		return workflowhost.Pairing{}, false
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	pair, err := workflowhost.Verify(s.store.Directory(), r.Header.Get("X-LazyMind-Connector-Id"), token, scope)
	if err != nil {
		workflowHostError(w, err)
		return pair, false
	}
	return pair, true
}

func (s *Server) workflowHost(w http.ResponseWriter, r *http.Request) (workflowhost.Pairing, *coreapi.Client, bool) {
	pair, ok := s.workflowPairing(w, r, false)
	if !ok {
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
		RunID  string `json:"run_id"`
		Driver string `json:"driver_session_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&input); err != nil || input.RunID == "" || input.Driver == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "run_id and driver_session_id are required"})
		return
	}
	cancellation := "none"
	if pair.Provider == workflowhost.DSHProvider || pair.Provider == "workbuddy" {
		cancellation = "session"
	}
	relayWorkflowHost(w, r, api, http.MethodPost, "/workflow-sessions/"+url.PathEscape(input.RunID)+"/host-binding", map[string]any{
		"connector_id": pair.ConnectorID, "credential": pair.Token, "provider": pair.Provider,
		"driver_session_id": input.Driver, "cancellation": cancellation,
	}, nil)
}

func (s *Server) handleWorkflowHostState(w http.ResponseWriter, r *http.Request) {
	_, api, ok := s.workflowHost(w, r)
	if !ok {
		return
	}
	relayWorkflowHost(w, r, api, http.MethodGet, "/workflow-sessions/"+url.PathEscape(r.PathValue("session"))+"/control?view=control", nil, nil)
}

func dshRuntimeIdentity(r *http.Request) workflowhost.DSHRuntimeIdentity {
	return workflowhost.DSHRuntimeIdentity{
		Dispatcher: r.Header.Get("X-LazyMind-DSH-Dispatcher"), Process: r.Header.Get("X-LazyMind-DSH-Process"),
		ConfigRevision: r.Header.Get("X-LazyMind-DSH-Config-Revision"), PairingRevision: r.Header.Get("X-LazyMind-DSH-Pairing-Revision"),
	}
}

func dshRuntimeLoad(r *http.Request) (bool, workflowhost.DSHLoad) {
	startedAt, _ := strconv.ParseInt(r.Header.Get("X-LazyMind-DSH-Process-Started-At"), 10, 64)
	return r.Header.Get("X-LazyMind-MCP-Ready") == "1", workflowhost.DSHLoad{
		Build: r.Header.Get("X-LazyMind-DSH-Build"), Instance: r.Header.Get("X-LazyMind-DSH-Process"), StartedAt: startedAt,
		Panel: r.Header.Get("X-LazyMind-DSH-Panel"), ClientBuild: r.Header.Get("X-LazyMind-DSH-Client-Build"),
	}
}

func (s *Server) handleDSHRuntimeHeartbeat(w http.ResponseWriter, r *http.Request) {
	pair, ok := s.workflowPairing(w, r, true)
	if !ok {
		return
	}
	mcpReady, load := dshRuntimeLoad(r)
	if err := workflowhost.RecordRuntimeHeartbeat(r.Context(), s.store.Directory(), pair, dshRuntimeIdentity(r), mcpReady, load); err != nil {
		workflowHostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleWorkflowHostActions(w http.ResponseWriter, r *http.Request) {
	pair, api, ok := s.workflowHost(w, r)
	if !ok {
		return
	}
	home := s.store.Directory()
	identity := dshRuntimeIdentity(r)
	split := pair.Provider == workflowhost.DSHProvider && identity.Dispatcher != ""
	if split && !identity.Current(home, pair) {
		workflowHostError(w, &workflowhost.PairingError{Code: "HOST_PAIRING_INVALID"})
		return
	}
	query := url.Values{"connector_id": {pair.ConnectorID}}
	if after := r.URL.Query().Get("after"); after != "" {
		query.Set("after", after)
	}
	var result map[string]any
	err := api.DoJSONHeaders(r.Context(), http.MethodGet, "/workflow-host-actions?"+query.Encode(), nil, &result,
		http.Header{"X-Workflow-Host-Credential": {pair.Token}})
	issue := ""
	if err != nil {
		issue = "lazymind_unavailable"
		var apiError *coreapi.Error
		if credentials.IsAuthenticationRequired(err) || errors.As(err, &apiError) && apiError.StatusCode == http.StatusUnauthorized {
			issue = "lazymind_auth_required"
		}
	}
	if _, valid := result["actions"].([]any); err == nil && !valid {
		issue = "lazymind_unavailable"
	}
	var recordErr error
	if split {
		// A request timeout is still a Core observation when this
		// dispatcher remains current.
		recordCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Second)
		defer cancel()
		recordErr = workflowhost.RecordRuntimeConnection(recordCtx, home, pair, identity, issue)
	} else {
		mcpReady, loaded := dshRuntimeLoad(r)
		recordErr = workflowhost.RecordRuntimeStatus(home, pair, issue, mcpReady, loaded)
	}
	if recordErr != nil {
		log.Printf("DSH dispatcher status could not be recorded")
	}
	if err != nil {
		workflowHostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleWorkflowHostAction(w http.ResponseWriter, r *http.Request) {
	pair, api, ok := s.workflowHost(w, r)
	if !ok {
		return
	}
	relayWorkflowHost(w, r, api, http.MethodGet, "/workflow-host-actions/"+url.PathEscape(r.PathValue("action"))+"?connector_id="+url.QueryEscape(pair.ConnectorID),
		nil, http.Header{"X-Workflow-Host-Credential": {pair.Token}})
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
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "instance_id is required"})
		return
	}
	relayWorkflowHost(w, r, api, http.MethodPost, "/workflow-host-actions/"+url.PathEscape(r.PathValue("action"))+":claim", map[string]any{
		"connector_id": pair.ConnectorID, "credential": pair.Token, "instance_id": input.InstanceID,
	}, nil)
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
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid host receipt"})
		return
	}
	relayWorkflowHost(w, r, api, http.MethodPost, "/workflow-host-actions/"+url.PathEscape(r.PathValue("action"))+":settle", map[string]any{
		"connector_id": pair.ConnectorID, "credential": pair.Token, "instance_id": input.InstanceID,
		"dispatch_token": input.DispatchToken, "status": input.Status, "native_event_seq": input.NativeEventSeq, "error": input.Error,
	}, nil)
}
