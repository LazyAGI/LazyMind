package assistantbridge

import (
	"encoding/json"
	"lazymind/agentconnector/internal/workbuddyhost"
	"lazymind/agentconnector/internal/workflowhost"
	"net/http"
)

func (s *Server) workbuddyPeer(w http.ResponseWriter, r *http.Request) (workflowhost.Pairing, bool) {
	pair, _, ok := s.workflowHost(w, r)
	if !ok {
		return pair, false
	}
	if pair.Provider != "workbuddy" {
		writeJSON(w, 403, map[string]string{"error": "WorkBuddy pairing required"})
		return pair, false
	}
	return pair, true
}
func (s *Server) handleWorkBuddyResolve(w http.ResponseWriter, r *http.Request) {
	pair, ok := s.workbuddyPeer(w, r)
	if !ok {
		return
	}
	var input struct {
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&input) != nil {
		writeJSON(w, 422, map[string]string{"error": "invalid identity request"})
		return
	}
	identity, err := s.workbuddyIdentities.Resolve(r.Context(), pair.ConnectorID, input.Arguments)
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, identity)
}
func (s *Server) handleWorkBuddyIdentities(w http.ResponseWriter, r *http.Request) {
	pair, ok := s.workbuddyPeer(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"requests": s.workbuddyIdentities.Pending(pair.ConnectorID)})
}
func (s *Server) handleWorkBuddyIdentityReceipt(w http.ResponseWriter, r *http.Request) {
	pair, ok := s.workbuddyPeer(w, r)
	if !ok {
		return
	}
	var input struct {
		ID string `json:"id"`
		workbuddyhost.Identity
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&input) != nil {
		writeJSON(w, 422, map[string]string{"error": "invalid identity receipt"})
		return
	}
	if err := s.workbuddyIdentities.Accept(pair.ConnectorID, input.ID, input.Identity); err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"accepted": true})
}
