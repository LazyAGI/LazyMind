package workflowmcp

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestGenerationClaimPreservesAllContextOnMCPWire(t *testing.T) {
	request := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": "guidance and result IDs"},
		map[string]any{"role": "assistant", "content": "Context block received."},
		map[string]any{"role": "user", "content": "schema and style catalog"},
		map[string]any{"role": "assistant", "content": "Context block received."},
		map[string]any{"role": "user", "content": "outline and previous_error"},
	}, "response_format": map[string]any{"type": "json_object"}}
	client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/model-generations/job:claim") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "running", "generation_request": request})
	})
	session := inputToolSession(t, client)
	for _, directory := range []string{"", t.TempDir()} {
		result := inputToolCall(t, session, "workflow.generation.claim", GenerationClaimInput{
			SessionID: "run", JobID: "job", CommandID: "stable-worker-command", ResultDirectory: directory,
		})
		if result.IsError {
			t.Fatal(result.Content)
		}
		fields := result.StructuredContent.(map[string]any)
		actual := fields["generation_request"]
		if directory != "" {
			file := fields["generation_request_file"].(map[string]any)
			raw, err := os.ReadFile(file["result_path"].(string))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &actual); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(actual, request) {
			t.Fatalf("generation context was changed: %#v", actual)
		}
		if !strings.Contains(fields["generation_request_instructions"].(string), "all 5 messages") {
			t.Fatal("complete-context instructions missing from claim receipt")
		}
	}
}
