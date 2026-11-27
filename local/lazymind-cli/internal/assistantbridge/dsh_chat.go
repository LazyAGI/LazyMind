package assistantbridge

import (
	"encoding/json"
	"net/http"
	"net/url"

	"lazymind/agentconnector/internal/chatagent"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/workflowhost"
)

const dshExecutionDisabled = "Disabled in LazyMind settings"

// Each paired DSH profile is a separate execution host. The host identity
// never comes from the HTTP body, and Core fences every turn with a lease.
func (s *Server) handleDSHChatClaim(w http.ResponseWriter, r *http.Request) {
	pair, _, api, ok := s.dshConnection(w, r)
	if !ok {
		return
	}
	var runtime struct {
		Ready  bool   `json:"ready"`
		Reason string `json:"unavailable_reason"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&runtime) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid DSH chat readiness"})
		return
	}
	enabled, err := s.policy.Enabled(pair.Provider)
	if err != nil {
		writeError(w, err)
		return
	}
	reason := ""
	if !runtime.Ready {
		switch runtime.Reason {
		case "DSH default model unavailable", "DSH sign-in required":
			reason = runtime.Reason
		default:
			reason = "DSH execution unavailable"
		}
	} else if !enabled {
		reason = dshExecutionDisabled
	}
	var result struct {
		Run *chatagent.Run `json:"run"`
	}
	err = api.DoJSON(r.Context(), http.MethodPost, "/external-chat/hosts/"+workflowhost.DSHProvider+"/claim", map[string]any{
		"host_id": pair.ConnectorID, "installed": true, "ready": enabled && runtime.Ready, "unavailable_reason": reason,
	}, &result)
	if err != nil {
		workflowHostError(w, err)
		return
	}
	if run := result.Run; run != nil {
		if run.Provider != pair.Provider || run.HostID != pair.ConnectorID || run.RunID == "" || run.LeaseToken == "" {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid DSH chat lease"})
			return
		}
		// The long poll may finish after disconnection, sign-out, or revocation.
		if current, _, _, valid := s.dshConnection(w, r); !valid {
			return
		} else if current.RuntimeRevision != pair.RuntimeRevision {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "DSH pairing changed"})
			return
		}
		stillEnabled, err := s.policy.Enabled(pair.Provider)
		if err != nil {
			writeError(w, err)
			return
		}
		if !enabled || !stillEnabled {
			if err := revokeDSHRun(r, api, run.RunID, pair.ConnectorID, run.LeaseToken); err != nil {
				workflowHostError(w, err)
				return
			}
			result.Run = nil
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// Revocation also settles the Core run, so disabling execution cannot leave
// a queued recovery that silently replays the prompt on the next activation.
func revokeDSHRun(r *http.Request, api *coreapi.Client, id, host, lease string) error {
	return api.DoJSON(r.Context(), http.MethodPost, "/external-chat/runs/"+url.PathEscape(id)+":event", map[string]string{
		"host_id": host, "lease_token": lease, "event_id": id + ":execution-revoked", "type": "failed", "error": dshExecutionDisabled,
	}, nil)
}

func (s *Server) handleDSHChatHeartbeat(w http.ResponseWriter, r *http.Request) {
	pair, _, api, ok := s.dshConnection(w, r)
	if !ok {
		return
	}
	var input struct {
		Lease string `json:"lease_token"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&input) != nil || input.Lease == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "DSH chat lease required"})
		return
	}
	enabled, err := s.policy.Enabled(pair.Provider)
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("run")
	if !enabled {
		if err := revokeDSHRun(r, api, id, pair.ConnectorID, input.Lease); err != nil {
			workflowHostError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"stop_requested": true})
		return
	}
	relayWorkflowHost(w, r, api, http.MethodPost, "/external-chat/runs/"+url.PathEscape(id)+":heartbeat", map[string]string{
		"host_id": pair.ConnectorID, "lease_token": input.Lease,
	}, nil)
}

func (s *Server) handleDSHChatEvent(w http.ResponseWriter, r *http.Request) {
	pair, _, api, ok := s.dshConnection(w, r)
	if !ok {
		return
	}
	var input struct {
		chatagent.Event
		HostID string `json:"host_id"`
		Lease  string `json:"lease_token"`
		ID     string `json:"event_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&input) != nil || input.Lease == "" || input.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid DSH chat event"})
		return
	}
	enabled, err := s.policy.Enabled(pair.Provider)
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("run")
	if !enabled {
		if err := revokeDSHRun(r, api, id, pair.ConnectorID, input.Lease); err != nil {
			workflowHostError(w, err)
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"error": dshExecutionDisabled})
		return
	}
	input.HostID = pair.ConnectorID
	relayWorkflowHost(w, r, api, http.MethodPost, "/external-chat/runs/"+url.PathEscape(id)+":event", input, nil)
}
