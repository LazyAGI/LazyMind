package mcpbridge

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalOriginalTransferUsesAuthorizedHostRootAndKeepsSource(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "paper.txt")
	os.WriteFile(path, []byte("source bytes"), 0600)
	args := map[string]any{"knowledge_base_id": "dataset", "files": []any{map[string]any{"source": map[string]any{"kind": "local_file", "path": path}, "name": "display.txt"}}}
	calls := 0
	result, err := transferLocalOriginals(context.Background(), args, []string{root}, func(_ context.Context, file *os.File, name string) (string, error) {
		calls++
		b, _ := io.ReadAll(file)
		if string(b) != "source bytes" || name != "paper.txt" {
			t.Fatal("wrong transfer")
		}
		return "controlled-ref", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	source := result["files"].([]any)[0].(map[string]any)["source"].(map[string]any)
	if source["kind"] != "uploaded_file" || source["file_ref"] != "controlled-ref" || source["path"] != nil || calls != 1 {
		t.Fatalf("raw path forwarded: %+v", source)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("source was moved")
	}
}
func TestLocalOriginalTransferRejectsPathAndSymlinkEscapeBeforeUpload(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(outside, []byte("private"), 0600)
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{outside, link, "relative.txt"} {
		args := map[string]any{"files": []any{map[string]any{"source": map[string]any{"kind": "local_file", "path": path}}}}
		calls := 0
		_, err := transferLocalOriginals(context.Background(), args, []string{root}, func(context.Context, *os.File, string) (string, error) { calls++; return "bad", nil })
		if err == nil || calls != 0 {
			t.Fatalf("unsafe local path accepted: %s %v", path, err)
		}
	}
}
