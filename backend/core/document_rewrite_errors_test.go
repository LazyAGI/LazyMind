package main

import "testing"

func TestDocumentRewriteMapsAlgorithmContractErrors(t *testing.T) {
	for _, test := range []struct {
		name           string
		upstreamStatus int
		upstreamCode   string
		status         int
		code           string
	}{
		{"semantic selection", 422, "WORKFLOW_ACTION_INVALID", 400, "DOCUMENT_ACTION_INVALID"},
		{"invalid upstream result", 502, "WORKFLOW_ACTION_RESULT_INVALID", 502, "DOCUMENT_ACTION_RESULT_INVALID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newRewriteFixture(t, "markdown")
			server := newRewriteServer(t, f)
			server.failureStatus = test.upstreamStatus
			server.failureCode = test.upstreamCode
			before := rewriteSnapshot(t, f)
			rewriteError(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", f.body("preview", "")), test.status, test.code)
			if rewriteSnapshot(t, f) != before {
				t.Error("rejected preview mutated Core state")
			}
		})
	}
}
