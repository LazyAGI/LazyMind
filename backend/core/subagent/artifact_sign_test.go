package subagent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/core/doc"

	"github.com/gorilla/mux"
)

func TestSignArtifactImageValuePreservesExternalStaticFileURLs(t *testing.T) {
	t.Setenv("LAZYMIND_PUBLIC_BASE_URL", "https://core.example/team/api/core")
	const source = "https://cdn.example/static-files/figure.png?token=a%2Fb+c&part=1&part=2#preview"
	for _, contentType := range []string{"", "image", "image/png"} {
		for _, key := range []string{"url", "path"} {
			raw, _ := json.Marshal(map[string]any{key: source, "caption": "figure"})
			signed := SignArtifactImageValue(contentType, raw)
			var value map[string]any
			if err := json.Unmarshal(signed, &value); err != nil || value["url"] != source || value["caption"] != "figure" {
				t.Fatalf("external image changed (%q, %q): %s (%v)", contentType, key, signed, err)
			}
		}
	}
}

func TestSignArtifactImageValueRefreshesConfiguredCoreURL(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	t.Setenv("LAZYMIND_PUBLIC_BASE_URL", "https://core.example:8443/team/api/core/")
	if err := os.WriteFile(filepath.Join(root, "figure one.png"), []byte("image content"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, contentType := range []string{"", "image", "image/png"} {
		raw := json.RawMessage(`{"path":"https://core.example:8443/team/api/core/static-files/figure%20one.png?expires=1&sig=old"}`)
		signed := SignArtifactImageValue(contentType, raw)
		var value map[string]any
		if err := json.Unmarshal(signed, &value); err != nil {
			t.Fatal(err)
		}
		imageURL, _ := value["url"].(string)
		u, err := url.Parse(imageURL)
		if err != nil || u.Host != "" || u.Path != "/static-files/figure one.png" || u.Query().Get("sig") == "old" {
			t.Fatalf("Core image was not re-signed (%q): %s (%v)", contentType, signed, err)
		}
		req := httptest.NewRequest(http.MethodGet, imageURL, nil)
		req = mux.SetURLVars(req, map[string]string{"path": "figure%20one.png"})
		rec := httptest.NewRecorder()
		doc.GetSignedStaticFile(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "image content" {
			t.Fatalf("refreshed image could not be read (%q): status=%d body=%s", contentType, rec.Code, rec.Body.String())
		}
	}
}

func TestSignArtifactImageValueAcceptsMIMEContentType(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	path := filepath.Join(root, "workflow-artifacts", "scope", "image.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"path": path, "name": "image.png"})
	signed := SignArtifactImageValue("image/png", raw)
	var got map[string]any
	if err := json.Unmarshal(signed, &got); err != nil {
		t.Fatal(err)
	}
	url, _ := got["url"].(string)
	if _, exposed := got["path"]; exposed || !strings.HasPrefix(url, "/static-files/workflow-artifacts/") {
		t.Fatalf("MIME image was not safely signed: %#v", got)
	}
}

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

func TestSignArtifactValueAllowsSharedWorkflowArtifact(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	path := filepath.Join(root, "workflow-artifacts", "session-1", "attempt-1", "result.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"path": path})
	signed := SignArtifactValue("image", raw, filepath.Join(t.TempDir(), "task-workspace"))
	var got map[string]any
	if err := json.Unmarshal(signed, &got); err != nil {
		t.Fatal(err)
	}
	url, _ := got["url"].(string)
	if !strings.HasPrefix(url, "/static-files/workflow-artifacts/session-1/attempt-1/result.png?") {
		t.Fatalf("expected shared workflow artifact URL, got %q", url)
	}
}

func TestSignArtifactValueAllowsCanonicalDockerUploadPathOnDesktop(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	relativePath := filepath.Join("workflow-artifacts", "session-1", "attempt-1", "result.gif")
	localPath := filepath.Join(root, relativePath)
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, []byte("gif"), 0o600); err != nil {
		t.Fatal(err)
	}
	canonicalPath := "/var/lib/lazymind/uploads/" + filepath.ToSlash(relativePath)
	raw, _ := json.Marshal(map[string]any{"path": canonicalPath, "caption": "好的!"})
	signed := SignArtifactValue("image", raw, filepath.Join(t.TempDir(), "task-workspace"))
	var got map[string]any
	if err := json.Unmarshal(signed, &got); err != nil {
		t.Fatal(err)
	}
	if _, exposed := got["path"]; exposed {
		t.Fatalf("canonical server path must not be exposed: %#v", got)
	}
	url, _ := got["url"].(string)
	if !strings.HasPrefix(url, "/static-files/workflow-artifacts/session-1/attempt-1/result.gif?") {
		t.Fatalf("expected signed canonical upload URL, got %q", url)
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
