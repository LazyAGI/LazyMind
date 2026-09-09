package assistantbridge

import "net/http"

// Old pages must not fall back to natural-language control or DSH private login formats.
func (s *Server) handleWorkflowControl(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusConflict, map[string]string{"error": "Refresh the LazyMind workflow page to use typed workflow controls. Legacy runs remain available for reading."})
}
