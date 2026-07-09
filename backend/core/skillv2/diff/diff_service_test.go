package diff

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestDiffTree_RevisionVsRevision_DetectsAddedDeletedModified(t *testing.T) {
	service := NewService(ServiceDeps{})
	oldFS := fakeReadOnlySkillFS{
		entries: []EntryInfo{
			fileEntry("SKILL.md", "h_skill_v1", false, "markdown"),
			fileEntry("references/a.md", "h_a", false, "markdown"),
			fileEntry("assets/logo.png", "h_logo", true, "image"),
		},
	}
	newFS := fakeReadOnlySkillFS{
		entries: []EntryInfo{
			fileEntry("SKILL.md", "h_skill_v2", false, "markdown"),
			fileEntry("references/b.md", "h_b", false, "markdown"),
			fileEntry("assets/logo.png", "h_logo", true, "image"),
		},
	}

	result, err := service.Compare(context.Background(), oldFS, newFS, DiffOptions{})
	if err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	assertDiffStatus(t, result, "SKILL.md", "modified")
	assertDiffStatus(t, result, "references/a.md", "deleted")
	assertDiffStatus(t, result, "references/b.md", "added")
	assertDiffStatusOptional(t, result, "assets/logo.png", "unchanged")
}

func TestDiffTree_RevisionVsDraft_MergesDraftOverlay(t *testing.T) {
	service := NewService(ServiceDeps{})
	oldFS := fakeReadOnlySkillFS{entries: []EntryInfo{
		fileEntry("SKILL.md", "h_skill", false, "markdown"),
		fileEntry("references/a.md", "h_a", false, "markdown"),
	}}
	draftFS := fakeReadOnlySkillFS{entries: []EntryInfo{
		fileEntry("SKILL.md", "h_skill", false, "markdown"),
		fileEntry("references/b.md", "h_b", false, "markdown"),
	}}

	result, err := service.Compare(context.Background(), oldFS, draftFS, DiffOptions{})
	if err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	assertDiffStatus(t, result, "references/a.md", "deleted")
	assertDiffStatus(t, result, "references/b.md", "added")
	assertDiffStatusOptional(t, result, "SKILL.md", "unchanged")
}

func TestDiffTree_EmptyDirAddedDeleted(t *testing.T) {
	service := NewService(ServiceDeps{})
	withoutDir := fakeReadOnlySkillFS{entries: []EntryInfo{fileEntry("SKILL.md", "h_skill", false, "markdown")}}
	withDir := fakeReadOnlySkillFS{entries: []EntryInfo{
		fileEntry("SKILL.md", "h_skill", false, "markdown"),
		dirEntry("notes"),
	}}

	added, err := service.Compare(context.Background(), withoutDir, withDir, DiffOptions{})
	if err != nil {
		t.Fatalf("Compare added dir returned error: %v", err)
	}
	assertDiffStatus(t, added, "notes", "added")
	if got := diffFileByPath(t, added, "notes"); got.Type != "dir" {
		t.Fatalf("notes type = %q, want dir", got.Type)
	}

	deleted, err := service.Compare(context.Background(), withDir, withoutDir, DiffOptions{})
	if err != nil {
		t.Fatalf("Compare deleted dir returned error: %v", err)
	}
	assertDiffStatus(t, deleted, "notes", "deleted")
}

func TestDiffTree_FileRename_ShowsDeletedAndAdded(t *testing.T) {
	service := NewService(ServiceDeps{})
	oldFS := fakeReadOnlySkillFS{entries: []EntryInfo{fileEntry("references/old.md", "h_same", false, "markdown")}}
	newFS := fakeReadOnlySkillFS{entries: []EntryInfo{fileEntry("references/new.md", "h_same", false, "markdown")}}

	result, err := service.Compare(context.Background(), oldFS, newFS, DiffOptions{})
	if err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	assertDiffStatus(t, result, "references/old.md", "deleted")
	assertDiffStatus(t, result, "references/new.md", "added")
}

func TestDiffFile_Text_ReturnsGitHubLikeLines(t *testing.T) {
	service := NewService(ServiceDeps{})
	oldFS := fakeReadOnlySkillFS{entries: []EntryInfo{fileEntry("SKILL.md", "h_old", false, "markdown")}, files: map[string][]byte{"SKILL.md": []byte("# Title\nold\n")}}
	newFS := fakeReadOnlySkillFS{entries: []EntryInfo{fileEntry("SKILL.md", "h_new", false, "markdown")}, files: map[string][]byte{"SKILL.md": []byte("# Title\nnew\n")}}

	result, err := service.CompareFile(context.Background(), oldFS, newFS, DiffOptions{Path: "SKILL.md", ContextLines: 3})
	if err != nil {
		t.Fatalf("CompareFile returned error: %v", err)
	}
	assertLineTypes(t, result.DiffEntryLines, "HUNK", "CONTEXT", "DELETION", "ADDITION")
}

func TestDiffFile_Binary_DegradesToFileLevel(t *testing.T) {
	service := NewService(ServiceDeps{})
	oldFS := fakeReadOnlySkillFS{entries: []EntryInfo{fileEntry("assets/logo.png", "h_old", true, "image")}}
	newFS := fakeReadOnlySkillFS{entries: []EntryInfo{fileEntry("assets/logo.png", "h_new", true, "image")}}

	result, err := service.CompareFile(context.Background(), oldFS, newFS, DiffOptions{Path: "assets/logo.png"})
	if err != nil {
		t.Fatalf("CompareFile returned error: %v", err)
	}
	if !result.Binary || len(result.DiffEntryLines) != 0 || result.Status != "modified" {
		t.Fatalf("binary file diff should degrade to file level, got %#v", result)
	}
}

func TestDiffFile_LargeText_DegradesWithoutCache(t *testing.T) {
	service := NewService(ServiceDeps{MaxTextBytes: 512 * 1024})
	large := bytes.Repeat([]byte("a"), 512*1024+1)
	oldFS := fakeReadOnlySkillFS{entries: []EntryInfo{fileEntry("SKILL.md", "h_old", false, "markdown")}, files: map[string][]byte{"SKILL.md": []byte("# old\n")}}
	newFS := fakeReadOnlySkillFS{entries: []EntryInfo{fileEntry("SKILL.md", "h_large", false, "markdown")}, files: map[string][]byte{"SKILL.md": large}}

	result, err := service.CompareFile(context.Background(), oldFS, newFS, DiffOptions{Path: "SKILL.md"})
	if err != nil {
		t.Fatalf("CompareFile returned error: %v", err)
	}
	if !result.TooLarge || len(result.DiffEntryLines) != 0 || result.Status != "modified" {
		t.Fatalf("large text should degrade without full lines, got %#v", result)
	}
	if result.CacheWritten {
		t.Fatal("large text diff wrote cache")
	}
}

func TestDiffFile_NonUTF8_DegradesToBinary(t *testing.T) {
	service := NewService(ServiceDeps{})
	oldFS := fakeReadOnlySkillFS{entries: []EntryInfo{fileEntry("bad.txt", "h_old", false, "text")}, files: map[string][]byte{"bad.txt": []byte("ok\n")}}
	newFS := fakeReadOnlySkillFS{entries: []EntryInfo{fileEntry("bad.txt", "h_bad", false, "text")}, files: map[string][]byte{"bad.txt": {0xff, 0xfe, 0xfd}}}

	result, err := service.CompareFile(context.Background(), oldFS, newFS, DiffOptions{Path: "bad.txt"})
	if err != nil {
		t.Fatalf("CompareFile returned error: %v", err)
	}
	if !result.Binary || len(result.DiffEntryLines) != 0 || result.Status != "modified" {
		t.Fatalf("non-UTF8 file should degrade to binary diff, got %#v", result)
	}
}

type fakeReadOnlySkillFS struct {
	entries []EntryInfo
	files   map[string][]byte
}

func (f fakeReadOnlySkillFS) ListAll(ctx context.Context) ([]EntryInfo, error) {
	return f.entries, nil
}

func (f fakeReadOnlySkillFS) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return f.files[path], nil
}

func fileEntry(path, hash string, binary bool, fileType string) EntryInfo {
	return EntryInfo{Path: path, Type: "file", BlobHash: hash, Binary: binary, FileType: fileType}
}

func dirEntry(path string) EntryInfo {
	return EntryInfo{Path: path, Type: "dir", FileType: "directory"}
}

func assertDiffStatus(t *testing.T, diff SkillDiff, path, status string) {
	t.Helper()
	got := diffFileByPath(t, diff, path)
	if got.Status != status {
		t.Fatalf("%s status = %q, want %q", path, got.Status, status)
	}
}

func assertDiffStatusOptional(t *testing.T, diff SkillDiff, path, status string) {
	t.Helper()
	for _, file := range diff.Files {
		if file.Path == path && file.Status != status {
			t.Fatalf("%s status = %q, want %q", path, file.Status, status)
		}
	}
}

func diffFileByPath(t *testing.T, diff SkillDiff, path string) DiffFile {
	t.Helper()
	for _, file := range diff.Files {
		if file.Path == path {
			return file
		}
	}
	t.Fatalf("diff missing path %q in %#v", path, diff.Files)
	return DiffFile{}
}

func assertLineTypes(t *testing.T, lines []DiffEntryLine, want ...string) {
	t.Helper()
	seen := map[string]bool{}
	for _, line := range lines {
		seen[strings.ToUpper(line.Type)] = true
	}
	for _, typ := range want {
		if !seen[typ] {
			t.Fatalf("missing diff line type %s in %#v", typ, lines)
		}
	}
}
