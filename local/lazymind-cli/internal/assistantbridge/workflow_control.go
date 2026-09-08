package assistantbridge

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"time"

	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/workflowcontrol"
	"lazymind/agentconnector/internal/workflowmcp"
)

func (s *Server) handleWorkflowControl(w http.ResponseWriter, r *http.Request) {
	var command workflowcontrol.Command
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&command); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid Workflow control request"})
		return
	}
	server, err := s.store.ServerURL(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	if origin := r.Header.Get("Origin"); origin == "" || !sameOrigin(origin, server) {
		writeJSON(w, 403, map[string]string{"error": "Workflow controls require the configured LazyMind page origin"})
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
	if _, err = workflow.State(r.Context(), runID); err != nil {
		writeError(w, err)
		return
	}
	binding, err := workflowcontrol.Load(s.store.Directory(), server, runID)
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": "No DSH binding for this run; query workflow.state from the original DSH session with control configured"})
		return
	}
	configuredURL := strings.TrimSpace(os.Getenv("LAZYMIND_DSH_URL"))
	if binding.DSHURL != "" && configuredURL != "" && !sameOrigin(configuredURL, binding.DSHURL) {
		writeJSON(w, 409, map[string]string{"error": "The run belongs to a different DSH server"})
		return
	}
	dshURL, err := workflowcontrol.ResolveControlURL(s.store.Directory(), binding, configuredURL)
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	binding.DSHURL = dshURL
	if command.Action == "prompt" {
		binding.AwaitReview = false
	}
	_ = workflowcontrol.Save(s.store.Directory(), binding)
	jar, err := cookiejar.New(nil)
	if err != nil {
		writeError(w, err)
		return
	}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
			return errors.New("DSH authentication redirected outside its origin")
		}
		return nil
	}}
	if err := workflowcontrol.Forward(r.Context(), client, binding, command, os.Getenv("LAZYMIND_DSH_TOKEN")); err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"accepted": true})
}
