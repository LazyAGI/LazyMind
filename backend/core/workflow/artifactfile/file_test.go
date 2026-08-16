package artifactfile

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestMaterializeMetadataAndInlineRoundTrip(t *testing.T) {
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	content := []byte("real image bytes")
	raw, err := json.Marshal(map[string]any{
		"storage": "inline_base64", "name": "kitten.png", "mime_type": "image/png",
		"size": len(content), "content_base64": base64.StdEncoding.EncodeToString(content),
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, directory, err := Materialize("session-1", "artifact-1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if directory == "" {
		t.Fatal("materialized artifact did not return its managed directory")
	}
	var managed map[string]any
	if err := json.Unmarshal(stored, &managed); err != nil {
		t.Fatal(err)
	}
	path, _ := managed["path"].(string)
	if managed["storage"] != "managed_file" || path == "" {
		t.Fatalf("unexpected managed value: %#v", managed)
	}
	if actual, err := os.ReadFile(path); err != nil || string(actual) != string(content) {
		t.Fatalf("stored content=%q err=%v", actual, err)
	}

	metadata := string(Metadata(stored))
	if strings.Contains(metadata, "content_base64") || strings.Contains(metadata, path) ||
		!strings.Contains(metadata, `"url":"/static-files/workflow-artifacts/`) {
		t.Fatalf("unsafe or incomplete metadata: %s", metadata)
	}
	inline, err := Inline(stored)
	if err != nil {
		t.Fatal(err)
	}
	var restored map[string]any
	if err := json.Unmarshal(inline, &restored); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(restored["content_base64"].(string))
	if err != nil || string(decoded) != string(content) || restored["storage"] != "inline_base64" {
		t.Fatalf("round trip content=%q storage=%v err=%v", decoded, restored["storage"], err)
	}
}

func TestMaterializeRejectsMismatchedSize(t *testing.T) {
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	raw := json.RawMessage(`{"storage":"inline_base64","name":"bad.png","size":99,"content_base64":"eA=="}`)
	if _, _, err := Materialize("session-1", "artifact-1", raw); err == nil {
		t.Fatal("expected mismatched artifact size to fail")
	}
}
