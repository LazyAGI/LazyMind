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
	if _, exposed := got["path"]; exposed {
		t.Fatalf("server path must not be exposed, got %#v", got["path"])
	}
	url, _ := got["url"].(string)
	if !strings.HasPrefix(url, "/static-files/subagent/user-1/task-1/writing_task.json?") {
		t.Fatalf("expected signed subagent url, got %q", url)
	}
}

func TestSignArtifactValueResolvesLegacyRelativePath(t *testing.T) {
	subRoot := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", subRoot)
	workspace := filepath.Join(subRoot, "user-1", "task-1")

	out := SignArtifactValue("file", json.RawMessage(`{
		"type":"text","path":"large/output.txt","size":12
	}`), workspace)
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal signed artifact: %v", err)
	}
	if _, exposed := got["path"]; exposed {
		t.Fatalf("resolved server path must not be exposed, got %#v", got["path"])
	}
	url, _ := got["url"].(string)
	if !strings.HasPrefix(url, "/static-files/subagent/user-1/task-1/large/output.txt?") {
		t.Fatalf("expected signed relative artifact url, got %q", url)
	}
}

func TestSignArtifactValueRejectsLegacyPathOutsideWorkspace(t *testing.T) {
	raw := json.RawMessage(`{"filename":"secret.txt","path":"../../secret.txt"}`)
	signed := SignArtifactValue("file", raw, "/tmp/subagent/task-1")
	var got map[string]any
	if err := json.Unmarshal(signed, &got); err != nil {
		t.Fatalf("unmarshal signed artifact: %v", err)
	}
	if got["url"] != nil {
		t.Fatalf("path outside workspace must not receive a signed URL: %v", got["url"])
	}
	if _, exposed := got["path"]; exposed {
		t.Fatalf("path outside workspace should be removed, got %v", got["path"])
	}
}

func TestSignArtifactValueRejectsAbsolutePathOutsideWorkspace(t *testing.T) {
	raw := json.RawMessage(`{"filename":"secret.txt","path":"/tmp/other/secret.txt"}`)
	signed := SignArtifactValue("file", raw, "/tmp/subagent/task-1")
	var got map[string]any
	if err := json.Unmarshal(signed, &got); err != nil {
		t.Fatalf("unmarshal signed artifact: %v", err)
	}
	_, pathExposed := got["path"]
	if got["url"] != nil || pathExposed {
		t.Fatalf("absolute path outside workspace must be cleared: %#v", got)
	}
}

func TestSignArtifactFileListOmitsUnsignableServerPaths(t *testing.T) {
	raw := json.RawMessage(`{"paths":["/private/server/secret.txt","https://example.com/public.txt"]}`)
	signed := SignArtifactValue("file_list", raw, "")
	var got map[string]any
	if err := json.Unmarshal(signed, &got); err != nil {
		t.Fatalf("unmarshal signed artifact: %v", err)
	}
	paths, _ := got["paths"].([]any)
	if len(paths) != 1 || paths[0] != "https://example.com/public.txt" {
		t.Fatalf("only public paths should be returned: %#v", paths)
	}
}
