package subagent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignArtifactFileValueAddsSignedURL(t *testing.T) {
	subRoot := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", subRoot)

	fullPath := filepath.Join(subRoot, "user-1", "task-1", "writing_task.json")
	raw, err := json.Marshal(map[string]any{
		"path":     fullPath,
		"filename": "writing_task.json",
	})
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}

	out := SignArtifactImageValue("file", raw)
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal signed artifact: %v", err)
	}
	if got["path"] != fullPath {
		t.Fatalf("expected original path preserved, got %#v", got["path"])
	}
	url, _ := got["url"].(string)
	if !strings.HasPrefix(url, "/static-files/subagent/user-1/task-1/writing_task.json?") {
		t.Fatalf("expected signed subagent url, got %q", url)
	}
}
