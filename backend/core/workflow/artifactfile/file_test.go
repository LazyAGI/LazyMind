package artifactfile

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestMaterializeKeepsDisplayNameOutOfDiskPath(t *testing.T) {
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	for _, name := range []string{"CON.txt", "A: report?.pptx", strings.Repeat("训", 180) + ".pptx"} {
		t.Run(name[:3], func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{
				"storage": "inline_base64", "name": name, "content_base64": "eA==",
			})
			if err != nil {
				t.Fatal(err)
			}
			stored, _, err := Materialize("run", "revision", raw)
			if err != nil {
				t.Fatal(err)
			}
			display, reference, ok := Reference(stored)
			if !ok || display != name || !strings.HasSuffix(reference, "/file"+filepath.Ext(name)) {
				t.Fatalf("display=%q reference=%q ok=%v", display, reference, ok)
			}
		})
	}
}

func TestMaterializeRejectsMismatchedSize(t *testing.T) {
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	raw := json.RawMessage(`{"storage":"inline_base64","name":"bad.png","size":99,"content_base64":"eA=="}`)
	if _, _, err := Materialize("session-1", "artifact-1", raw); err == nil {
		t.Fatal("expected mismatched artifact size to fail")
	}
}

func TestSnapshotPreservesPublicReferencesWithoutReadingFiles(t *testing.T) {
	for _, path := range []string{
		"https://placehold.co/640x360/png", "http://example.com/image.png",
		"data:image/png;base64,eA==", "/static-files/image.png", "/api/core/static-files/image.png",
	} {
		t.Run(path, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"path": path, "caption": "image"})
			value, directory, err := Snapshot("run", "image", "image", raw)
			if err != nil || directory != "" || string(value) != string(raw) {
				t.Fatalf("reference changed: value=%s directory=%q err=%v", value, directory, err)
			}
		})
	}
}

func TestSnapshotSealsUploadedFileBytesAndRejectsOutsidePaths(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	source := filepath.Join(root, "working.txt")
	if err := os.WriteFile(source, []byte("reviewed bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(map[string]any{"path": source})
	frozen, _, err := Snapshot("run", "revision", "file", value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("later edits"), 0600); err != nil {
		t.Fatal(err)
	}
	inline, err := Inline(frozen)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	json.Unmarshal(inline, &result)
	actual, _ := base64.StdEncoding.DecodeString(result["content_base64"].(string))
	if string(actual) != "reviewed bytes" || result["sha256"] == nil {
		t.Fatalf("reviewed bytes changed: %s", inline)
	}
	outside := filepath.Join(t.TempDir(), "private.txt")
	os.WriteFile(outside, []byte("not a workflow upload"), 0600)
	bad, _ := json.Marshal(map[string]any{"path": outside})
	if _, _, err := Snapshot("run", "outside", "file", bad); err == nil {
		t.Fatal("arbitrary file path accepted")
	}
	link := filepath.Join(root, "symlink.txt")
	if err := os.Symlink(outside, link); err == nil {
		bad, _ = json.Marshal(map[string]any{"path": link})
		if _, _, err := Snapshot("run", "symlink", "file", bad); err == nil {
			t.Fatal("symlink escaped storage")
		}
	}
}

func TestMaterializeFallsBackUnderLongUploadRoot(t *testing.T) {
	// The original scope layout exceeds the budget, but the folded path fits.
	parent := t.TempDir()
	root := filepath.Join(parent, strings.Repeat("u", max(1, 145-len(parent)-1)))
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	raw := json.RawMessage(`{"storage":"inline_base64","name":"report.pptx","content_base64":"eA=="}`)
	stored, directory, err := Materialize("session", strings.Repeat("revision", 35), raw)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(directory) != filepath.Join(root, "workflow-artifacts") {
		t.Fatalf("fallback escaped managed root: %s", directory)
	}
	inlined, err := Inline(stored)
	if err != nil || !strings.Contains(string(inlined), `"content_base64":"eA=="`) {
		t.Fatalf("fallback cannot be read: %s %v", inlined, err)
	}
	_, otherDirectory, err := Materialize("other-session", strings.Repeat("revision", 35), raw)
	if err != nil || otherDirectory == directory {
		t.Fatalf("fallback merged sessions: %s %v", otherDirectory, err)
	}
}

func TestMaterializeUsesLongRootWithoutBudgetError(t *testing.T) {
	root := t.TempDir()
	for len(root) < 280 {
		root = filepath.Join(root, strings.Repeat("u", 40))
	}
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	raw := json.RawMessage(`{"storage":"inline_base64","name":"report.pptx","content_base64":"eA=="}`)
	stored, directory, err := Materialize("session", "revision", raw)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(directory) != filepath.Join(root, "workflow-artifacts") {
		t.Fatalf("long path escaped managed storage: %s", directory)
	}
	inlined, err := Inline(stored)
	if err != nil || !strings.Contains(string(inlined), `"content_base64":"eA=="`) {
		t.Fatalf("long path round trip failed: %s %v", inlined, err)
	}
	name, reference, ok := Reference(stored)
	if !ok || name != "report.pptx" || !strings.HasPrefix(reference, "/static-files/workflow-artifacts/") {
		t.Fatalf("long path cannot be downloaded: %q %q %v", name, reference, ok)
	}
}

func TestStoragePathCountsUTF16AndTemporaryFilename(t *testing.T) {
	root := t.TempDir()
	// Each emoji uses two UTF-16 units even though Go counts one rune.
	long := filepath.Join(root, strings.Repeat("😀", 110), "file.txt")
	if storagePathFits(long) {
		t.Fatal("UTF-16 length was counted as runes")
	}
	parent := root + string(filepath.Separator) + strings.Repeat("a", 225-len(root))
	if storagePathFits(filepath.Join(parent, "a")) {
		t.Fatal("temporary filename reserve was ignored")
	}
}

func TestInlineReadsLegacyArtifactFilename(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	path := filepath.Join(root, "workflow-artifacts", "old-session", "old-id", "original.pptx")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"storage": "managed_file", "path": path, "name": "original.pptx"})
	if _, err := Inline(raw); err != nil {
		t.Fatalf("legacy artifact is unreadable: %v", err)
	}
}
