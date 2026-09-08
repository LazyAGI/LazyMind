package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkflowControlOpenAPIHasTypedContractsWithoutPrivateStorageFields(t *testing.T) {
	spec := operationRegistryOpenAPISpec()
	paths := spec["paths"].(map[string]any)
	for _, path := range []string{"/workflow-sessions/{session_id}/control", "/workflow-sessions/{session_id}/executions:begin", "/workflow-host-actions/{action_id}:settle", "/workflow-sessions/{session_id}/hosted-attempts/{attempt_id}:submit"} {
		post, ok := paths[path].(map[string]any)["post"].(map[string]any)
		if !ok || post["requestBody"] == nil {
			t.Fatalf("missing typed request for %s", path)
		}
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"review_version", "manifest_hash", "execution_handle", "review_after_submit", "dispatch_token", "native_event_seq"} {
		if !strings.Contains(string(encoded), `"`+required+`"`) {
			t.Fatalf("missing %s", required)
		}
	}
	for _, private := range []string{"credential_hash", "dispatch_token_hash", "control_binding_json", "manifest_json"} {
		if strings.Contains(string(encoded), `"`+private+`"`) {
			t.Fatalf("private storage field %s leaked", private)
		}
	}
}
