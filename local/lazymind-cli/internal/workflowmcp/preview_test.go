package workflowmcp

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestPaperPreviewUsesWorkerFileWithoutPublishing(t *testing.T) {
	calls := 0
	client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/api/core/workflow-sessions/run/hosted-attempts/attempt/preview" {
			t.Errorf("unexpected preview route %s", r.URL.Path)
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input["text"] != "# Outline\n\nFirst section." || input["text_file"] != nil || input["execution_handle"] != "private" || input["seq"] != float64(1) {
			t.Errorf("input = %#v", input)
		}
		_, _ = w.Write([]byte(`{"accepted":true,"seq":1}`))
	})
	file := filepath.Join(t.TempDir(), "preview.md")
	if err := os.WriteFile(file, []byte("# Outline\n\nFirst section."), 0600); err != nil {
		t.Fatal(err)
	}
	input := PreviewInput{SessionID: "run", ExecutionID: "attempt", ExecutionHandle: "private", Slot: "write_outline_document", Seq: 1, TextFile: file, State: "streaming"}
	result := inputToolCall(t, inputToolSession(t, client), "workflow.artifact.preview", input)
	if result.IsError || calls != 1 {
		t.Fatalf("preview = %+v, calls=%d", result, calls)
	}
	input.Text = new(string)
	if _, err := client.Preview(t.Context(), input); err == nil || calls != 1 {
		t.Fatal("ambiguous text performed an HTTP call")
	}
	input.Text, input.TextFile = nil, "relative.md"
	if _, err := client.Preview(t.Context(), input); err == nil || calls != 1 {
		t.Fatal("relative file performed an HTTP call")
	}
}
