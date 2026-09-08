package assistantbridge

import (
	"encoding/json"
	"net/http"
	"strings"

	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/workflowcontrol"
	"lazymind/agentconnector/internal/workflowmcp"
)

// handleWorkflowBinding records the DSH Session that owns a Workflow run.
// The caller is a local DSH integration plugin; the run is first read through
// the authenticated LazyMind API so an arbitrary local caller cannot bind a
// run it cannot access.
func (s *Server) handleWorkflowBinding(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SessionID string `json:"session_id"`
		DSHURL    string `json:"dsh_url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Workflow binding request"})
		return
	}
	input.SessionID = strings.TrimSpace(input.SessionID)
	if input.SessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "DSH session_id is required"})
		return
	}
	if err := workflowcontrol.ValidateEndpoint(input.DSHURL); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	server, err := s.store.ServerURL(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	api, err := coreapi.New(s.store)
	if err != nil {
		writeError(w, err)
		return
	}
	workflow, err := workflowmcp.NewClient(api, workflowmcp.StartOrigin{})
	if err != nil {
		writeError(w, err)
		return
	}
	runID := r.PathValue("session")
	if _, err := workflow.State(r.Context(), runID); err != nil {
		writeError(w, err)
		return
	}
	if err := workflowcontrol.Save(s.store.Directory(), workflowcontrol.Binding{
		WorkflowSessionID: runID, SessionID: input.SessionID, ServerURL: server, DSHURL: input.DSHURL,
	}); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"bound": true})
}
