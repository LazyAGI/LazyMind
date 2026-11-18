package workflowmcp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidatedPaperBundlePublishesWithoutModelReencoding(t *testing.T) {
	content := []byte("# Accepted manuscript\n\nExact bytes, with actual references.\n")
	hash := fmtHash(content)
	receipt := map[string]any{"payload": map[string]any{"kind": "publication"}, "signature": "unchanged-signature"}
	bundle := map[string]any{"status": "validated", "publication_contract": "paper-agent-v1", "artifacts": []any{
		map[string]any{"slot": "write_document_document", "seq": 1, "content_type": "file", "value": map[string]any{
			"name": "manuscript.md", "size": len(content), "sha256": "sha256:" + hash,
			"content_base64": base64.StdEncoding.EncodeToString(content)}},
		map[string]any{"slot": "write_document_receipt", "seq": 1, "content_type": "json", "value": receipt},
	}}
	var published []map[string]any
	client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tools:call"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": bundle})
		case strings.HasSuffix(r.URL.Path, "/artifacts"):
			var input struct {
				Artifact map[string]any `json:"artifact"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			published = append(published, input.Artifact)
			_, _ = w.Write([]byte(`{"saved":true}`))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
	})
	session := inputToolSession(t, client)
	result, err := client.CallExecutionTool(t.Context(), ExecutionToolCallInput{SessionID: "run", ExecutionID: "attempt",
		ExecutionHandle: "private", ToolName: "paper_prepare_candidate", CommandID: "candidate", Arguments: map[string]any{}, ResultDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	outputs, ok := result["publication_outputs"].([]map[string]any)
	if !ok || len(outputs) != 2 {
		t.Fatalf("missing publication handoff: %#v", result)
	}
	for _, output := range outputs {
		called := inputToolCall(t, session, "workflow.artifact.publish", map[string]any{
			"session_id": "run", "execution_id": "attempt", "execution_handle": "private", "output": output})
		if called.IsError {
			t.Fatalf("publication rejected: %+v", called)
		}
	}
	if len(published) != 2 {
		t.Fatalf("publish count=%d", len(published))
	}
	value := published[0]["value"].(map[string]any)
	if value["content_base64"] != base64.StdEncoding.EncodeToString(content) || value["name"] != "manuscript.md" {
		t.Fatalf("changed accepted file: %#v", value)
	}
	if !reflect.DeepEqual(published[1]["value"], receipt) {
		t.Fatal("signed publication receipt was rewritten")
	}
}

func fmtHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func TestPaperPublicationRejectsCorruptFilesAndLeavesNoPartialHandoff(t *testing.T) {
	for _, name := range []string{"../outside.md", "document.md"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			_, err := saveExecutionResult(root, map[string]any{"publication_contract": "paper-agent-v1", "status": "validated",
				"artifacts": []any{map[string]any{"slot": "document", "seq": 1, "content_type": "file", "value": map[string]any{
					"name": name, "size": 4, "content_base64": base64.StdEncoding.EncodeToString([]byte("text")), "sha256": "wrong"}}}})
			if err == nil {
				t.Fatal("invalid file produced a publication handoff")
			}
			files, err := os.ReadDir(root)
			if err != nil || len(files) != 0 {
				t.Fatalf("partial files escaped cleanup: %v %v", files, err)
			}
			if _, err := os.Stat(filepath.Join(root, "outside.md")); !os.IsNotExist(err) {
				t.Fatal("unsafe artifact escaped its directory")
			}
		})
	}
}
