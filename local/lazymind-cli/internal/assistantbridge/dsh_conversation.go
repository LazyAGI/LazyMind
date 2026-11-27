package assistantbridge

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"lazymind/agentconnector/internal/agentcatalog"
	"lazymind/agentconnector/internal/chatagent"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/workflowhost"
)

func (s *Server) dshConnection(w http.ResponseWriter, r *http.Request) (workflowhost.Pairing, connectorconfig.Config, *coreapi.Client, bool) {
	pair, api, ok := s.workflowHost(w, r)
	if !ok {
		return pair, connectorconfig.Config{}, nil, false
	}
	if pair.Provider != workflowhost.DSHProvider {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "DSH pairing required"})
		return pair, connectorconfig.Config{}, nil, false
	}
	home := s.store.Directory()
	cfg, err := connectorconfig.Load(connectorconfig.Path(home, pair.Provider, pair.Profile))
	if err != nil || !cfg.Controlled || cfg.Provider != pair.Provider || cfg.Profile != pair.Profile || cfg.Home != home || filepath.Base(cfg.PairingFile) != pair.ConnectorID+".json" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "DSH connection unavailable"})
		return pair, cfg, nil, false
	}
	return pair, cfg, api, true
}

// Core requires this same account, machine and exact thread's prior MCP invocation.
func (s *Server) handleDSHConversation(w http.ResponseWriter, r *http.Request) {
	pair, cfg, api, ok := s.dshConnection(w, r)
	if !ok {
		return
	}
	var input struct {
		chatagent.NativeSession
		CWD string `json:"cwd"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 30<<20)).Decode(&input) != nil || input.ThreadID == "" || len(input.Turns) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid native conversation"})
		return
	}
	input.ProjectKey, input.ProjectName = agentcatalog.ProjectPath(pair.Provider, input.CWD, "")
	if input.ProjectKey == "" {
		input.ProjectKey, input.ProjectName = agentcatalog.Project(pair.Provider, "default", "DeepSeek Harness")
	}
	input.TurnCount = len(input.Turns)
	relayWorkflowHost(w, r, api, http.MethodPost, "/external-chat/providers/"+pair.Provider+"/sessions:sync", map[string]any{"host_id": cfg.HostID, "sessions": []chatagent.NativeSession{input.NativeSession}}, nil)
}
