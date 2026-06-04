package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/lazymind/file_watcher/internal/config"
)

func TestStageFileRejectsTraversalIDs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srcPath := filepath.Join(root, "source.txt")
	if err := os.WriteFile(srcPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	svc := NewStagingService(config.StagingConfig{
		Enabled:       true,
		HostRoot:      filepath.Join(root, "host"),
		ContainerRoot: "/data/staging",
	}, zap.NewNop())

	if _, err := svc.StageFile(context.Background(), "../source", "doc", "v1", srcPath); err == nil {
		t.Fatal("expected traversal-like source_id to be rejected")
	}
}

func TestStageFileRejectsTransientEditorFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srcPath := filepath.Join(root, ".source.txt.swp")
	if err := os.WriteFile(srcPath, []byte("tmp"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	svc := NewStagingService(config.StagingConfig{
		Enabled:       true,
		HostRoot:      filepath.Join(root, "host"),
		ContainerRoot: "/data/staging",
	}, zap.NewNop())

	if _, err := svc.StageFile(context.Background(), "src-1", "doc", "v1", srcPath); err == nil {
		t.Fatal("expected transient editor file to be rejected")
	}
}

func TestStageFilePreservesModTimeAndSkipsUpToDateCopy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	srcPath := filepath.Join(root, "source.txt")
	if err := os.WriteFile(srcPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	modTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(srcPath, modTime, modTime); err != nil {
		t.Fatalf("set source mtime: %v", err)
	}

	svc := NewStagingService(config.StagingConfig{
		Enabled:       true,
		HostRoot:      hostRoot,
		ContainerRoot: "/data/staging",
	}, zap.NewNop())

	first, err := svc.StageFile(context.Background(), "src-1", "doc-1", "v1", srcPath)
	if err != nil {
		t.Fatalf("first stage file: %v", err)
	}
	info, err := os.Stat(first.HostPath)
	if err != nil {
		t.Fatalf("stat staged file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("expected staged file mode 0644, got %o", got)
	}
	if !info.ModTime().Equal(modTime) {
		t.Fatalf("expected staged mtime %v, got %v", modTime, info.ModTime())
	}
	if err := os.Chmod(first.HostPath, 0o600); err != nil {
		t.Fatalf("simulate legacy private staging mode: %v", err)
	}

	second, err := svc.StageFile(context.Background(), "src-1", "doc-1", "v1", srcPath)
	if err != nil {
		t.Fatalf("second stage file: %v", err)
	}
	if second.HostPath != first.HostPath {
		t.Fatalf("expected same staged path, got %q vs %q", second.HostPath, first.HostPath)
	}
	info, err = os.Stat(second.HostPath)
	if err != nil {
		t.Fatalf("stat restaged file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("expected skipped staging to repair mode to 0644, got %o", got)
	}
	wantDir := filepath.Join(hostRoot, "sources", "src-1", "files")
	if filepath.Dir(first.HostPath) != wantDir {
		t.Fatalf("expected source staging dir %q, got %q", wantDir, first.HostPath)
	}
	if strings.Contains(strings.TrimPrefix(first.HostPath, hostRoot), "doc-1") || strings.Contains(strings.TrimPrefix(first.HostPath, hostRoot), "v1") {
		t.Fatalf("expected no document/version nesting in staged path, got %q", first.HostPath)
	}
}

func TestStageFileUsesHashedNameForCollisionAndStableOverwrite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	svc := NewStagingService(config.StagingConfig{
		Enabled:       true,
		HostRoot:      hostRoot,
		ContainerRoot: "/data/staging",
	}, zap.NewNop())

	srcDirA := filepath.Join(root, "a")
	srcDirB := filepath.Join(root, "b")
	if err := os.MkdirAll(srcDirA, 0o755); err != nil {
		t.Fatalf("mkdir a: %v", err)
	}
	if err := os.MkdirAll(srcDirB, 0o755); err != nil {
		t.Fatalf("mkdir b: %v", err)
	}

	srcA := filepath.Join(srcDirA, "same.txt")
	srcB := filepath.Join(srcDirB, "same.txt")
	if err := os.WriteFile(srcA, []byte("aaa"), 0o644); err != nil {
		t.Fatalf("write srcA: %v", err)
	}
	if err := os.WriteFile(srcB, []byte("bbb"), 0o644); err != nil {
		t.Fatalf("write srcB: %v", err)
	}

	resA1, err := svc.StageFile(context.Background(), "src-1", "doc-a", "v1", srcA)
	if err != nil {
		t.Fatalf("stage A first: %v", err)
	}
	resB1, err := svc.StageFile(context.Background(), "src-1", "doc-b", "v1", srcB)
	if err != nil {
		t.Fatalf("stage B first: %v", err)
	}
	if resA1.HostPath == resB1.HostPath {
		t.Fatalf("expected distinct names for collision, got same path %q", resA1.HostPath)
	}
	expectedA := hashedFileRelativePath("src-1", srcA, 0)
	if strings.TrimPrefix(resA1.HostPath, hostRoot+string(filepath.Separator)) != expectedA {
		t.Fatalf("expected first uses hashed relative path %q, got %q", expectedA, strings.TrimPrefix(resA1.HostPath, hostRoot+string(filepath.Separator)))
	}
	expectedB := hashedFileRelativePath("src-1", srcB, 0)
	if strings.TrimPrefix(resB1.HostPath, hostRoot+string(filepath.Separator)) != expectedB {
		t.Fatalf("expected second uses hashed relative path %q, got %q", expectedB, strings.TrimPrefix(resB1.HostPath, hostRoot+string(filepath.Separator)))
	}

	// Updating the same source file should overwrite the original staging path instead of creating another name.
	if err := os.WriteFile(srcA, []byte("aaa-updated"), 0o644); err != nil {
		t.Fatalf("update srcA: %v", err)
	}
	resA2, err := svc.StageFile(context.Background(), "src-1", "doc-a", "v2", srcA)
	if err != nil {
		t.Fatalf("stage A second: %v", err)
	}
	if resA2.HostPath != resA1.HostPath {
		t.Fatalf("expected stable overwrite path %q, got %q", resA1.HostPath, resA2.HostPath)
	}
	data, err := os.ReadFile(resA2.HostPath)
	if err != nil {
		t.Fatalf("read staged A: %v", err)
	}
	if string(data) != "aaa-updated" {
		t.Fatalf("expected overwritten content, got %q", string(data))
	}
}

func TestStageFileMigratesLegacySuffixIndexToHashedName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("mkdir host root: %v", err)
	}

	srcDir := filepath.Join(root, "a")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src dir: %v", err)
	}
	src := filepath.Join(srcDir, "same.txt")
	if err := os.WriteFile(src, []byte("legacy"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	sourceID := "src-1"
	key := stagingIndexKey(sourceID, src)
	legacyName := "same-1.txt"
	raw, err := json.Marshal(stagingIndex{
		Entries: map[string]string{key: legacyName},
	})
	if err != nil {
		t.Fatalf("marshal legacy index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, ".staging-index.json"), raw, 0o644); err != nil {
		t.Fatalf("write legacy index: %v", err)
	}

	svc := NewStagingService(config.StagingConfig{
		Enabled:       true,
		HostRoot:      hostRoot,
		ContainerRoot: "/data/staging",
	}, zap.NewNop())

	res, err := svc.StageFile(context.Background(), sourceID, "doc-a", "v1", src)
	if err != nil {
		t.Fatalf("stage with legacy index: %v", err)
	}
	if filepath.Base(res.HostPath) == legacyName {
		t.Fatalf("expected legacy name %q to be migrated", legacyName)
	}

	expected := hashedFileRelativePath(sourceID, src, 0)
	if strings.TrimPrefix(res.HostPath, hostRoot+string(filepath.Separator)) != expected {
		t.Fatalf("expected migrated hashed path %q, got %q", expected, strings.TrimPrefix(res.HostPath, hostRoot+string(filepath.Separator)))
	}

	persistedRaw, err := os.ReadFile(filepath.Join(hostRoot, ".staging-index.json"))
	if err != nil {
		t.Fatalf("read persisted index: %v", err)
	}
	var persisted stagingIndex
	if err := json.Unmarshal(persistedRaw, &persisted); err != nil {
		t.Fatalf("decode persisted index: %v", err)
	}
	if got := persisted.Entries[key]; got != expected {
		t.Fatalf("expected persisted mapping %q, got %q", expected, got)
	}
}
