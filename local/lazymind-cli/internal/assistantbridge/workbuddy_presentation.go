package assistantbridge

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/workbuddyhost"
)

var presentationID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,256}$`)

// Read existing bindings; displaying a run never creates or migrates a binding.
// created_at, not updated_at, selects the newest successfully bound run.
func (s *Server) handleWorkBuddyPresentation(w http.ResponseWriter, r *http.Request) {
	pair, api, ok := s.workflowHost(w, r)
	if !ok {
		return
	}
	if pair.Provider != "workbuddy" {
		writeJSON(w, 403, map[string]string{"error": "WorkBuddy pairing required"})
		return
	}
	driver := r.URL.Query().Get("session_id")
	if !presentationID.MatchString(driver) {
		writeJSON(w, 422, map[string]string{"error": "invalid session_id"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	var newest time.Time
	run, token := "", ""
	seen := map[string]bool{}
	for {
		var page struct {
			Sessions []struct {
				ID      string    `json:"session_id"`
				Created time.Time `json:"created_at"`
			} `json:"sessions"`
			Next string `json:"next_page_token"`
		}
		if err := api.DoJSON(ctx, "GET", "/workflow-sessions?page_size=100&page_token="+url.QueryEscape(token), nil, &page); err != nil {
			workflowHostError(w, err)
			return
		}
		for _, item := range page.Sessions {
			if !presentationID.MatchString(item.ID) || item.Created.IsZero() || item.Created.Before(newest) {
				continue
			}
			var state struct {
				Control struct {
					Binding struct {
						Bound     bool   `json:"bound"`
						Provider  string `json:"provider"`
						Connector string `json:"connector_id"`
						Driver    string `json:"driver_session_id"`
					} `json:"binding"`
				} `json:"control"`
			}
			if err := api.DoJSON(ctx, "GET", "/workflow-sessions/"+item.ID+"/control?view=control", nil, &state); err != nil {
				var apiErr *coreapi.Error
				// The account-wide inventory also contains legacy runs, which
				// cannot have a binding in the current control protocol.
				if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusNotFound ||
					apiErr.StatusCode == http.StatusConflict && apiErr.Code == "CONTROL_PROTOCOL_REQUIRED") {
					continue
				}
				workflowHostError(w, err)
				return
			}
			b := state.Control.Binding
			if b.Bound && b.Provider == pair.Provider && b.Connector == pair.ConnectorID && b.Driver == driver &&
				(item.Created.After(newest) || item.Created.Equal(newest) && item.ID > run) {
				run, newest = item.ID, item.Created
			}
		}
		if page.Next == "" {
			break
		}
		if seen[page.Next] {
			writeJSON(w, 502, map[string]string{"error": "invalid session pagination"})
			return
		}
		seen[page.Next] = true
		token = page.Next
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]string{"session_id": driver, "run_id": run})
}

// The wrapper contains no credentials or run data. Core authenticates the
// original Workflow page independently, using its existing browser session.
func (s *Server) handleWorkBuddyFrame(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	api, err := coreapi.New(s.store)
	if err != nil {
		workflowHostError(w, err)
		return
	}
	frontendURL, err := api.WebURL(ctx)
	if err != nil {
		workflowHostError(w, err)
		return
	}
	connection, err := connectorconfig.ForProvider(s.store.Directory(), "workbuddy", "")
	if err == nil && connection != nil {
		frontendURL, err = connection.URL("LAZYMIND_WEB_URL", frontendURL)
	}
	if err != nil {
		workflowHostError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(workbuddyhost.PresentationHTML(frontendURL))
}
