package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	workflowfacade "lazymind/core/workflow/facade"
	workflowstore "lazymind/core/workflow/store"
)

// External leases permit reading these routes but do not permit PATCH Artifact
// or the Panel save routes. An owner identity alone must not advertise save.
func TestDocumentDescriptorExternalReadDoesNotAdvertiseSave(t *testing.T) {
	for _, list := range []bool{true, false} {
		t.Run(map[bool]string{true: "list", false: "read"}[list], func(t *testing.T) {
			f := newDescriptorFixture(t)
			f.seed(t, "descriptor-artifact", "unknown-slot", "text/markdown", `{"text":"lease read"}`)
			descriptorAlgorithm(t, 200, descriptorMarkdown)
			h := workflowfacade.Handler{Store: workflowstore.New(f.db.DB)}
			req := httptest.NewRequest(http.MethodGet, "/workflow-artifacts/descriptor-artifact", nil)
			req.Header.Set("X-User-Id", "descriptor-owner")
			req.Header.Set("X-LazyMind-External-Ref", "external-read-run")
			req = mux.SetURLVars(req, map[string]string{"artifact_id": "descriptor-artifact", "session_id": "descriptor-session"})
			w := httptest.NewRecorder()
			// The lease middleware is outside this handler-level capability check.
			if list {
				h.ListArtifacts(w, req)
			} else {
				h.ReadArtifact(w, req)
			}
			requireDescriptor(t, descriptorRecords(t, w)[0], "markdown", false)
		})
	}
}
