package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubArchiveFallbackSelectsUniqueSkillBeforePackageLimits(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "repository.zip")
	files := map[string][]byte{
		"repo-main/README.md":                  []byte("not part of the skill"),
		"repo-main/skills/demo/SKILL.md":       externalSkillMD("demo", "A skill."),
		"repo-main/skills/demo/scripts/run.sh": []byte("echo demo"),
	}
	for i := 0; i < 520; i++ {
		files[fmt.Sprintf("repo-main/irrelevant/%d.txt", i)] = []byte("unrelated")
	}
	writeSkillZip(t, archive, files)
	archiveURL := "https://github.com/example/repo/archive/main.zip"
	svc := &SkillService{downloader: NewFakeZipDownloader(map[string]string{archiveURL: archive})}
	pkg, sourceType, sourceRef, err := svc.filesFromSource(context.Background(), "user", SourceInput{Type: "url", URL: archiveURL, SourceURL: "https://github.com/example/repo?tab=readme-ov-file"})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.PackageRoot != "demo" || len(pkg.Files) != 2 || sourceType != "url" || sourceRef != "https://github.com/example/repo?tab=readme-ov-file" {
		t.Fatalf("unexpected imported package/source: %#v, %q, %q", pkg, sourceType, sourceRef)
	}
}

func TestGitHubArchiveFallbackRejectsMultipleSkills(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "repository.zip")
	writeSkillZip(t, archive, map[string][]byte{
		"repo-main/SKILL.md":        externalSkillMD("root", "Root."),
		"repo-main/nested/SKILL.md": externalSkillMD("nested", "Nested."),
	})
	archiveURL := "https://github.com/example/repo/archive/main.zip"
	svc := &SkillService{downloader: NewFakeZipDownloader(map[string]string{archiveURL: archive})}
	if _, _, _, err := svc.filesFromSource(context.Background(), "user", SourceInput{Type: "url", URL: archiveURL}); err == nil || !strings.Contains(err.Error(), "multiple skills") {
		t.Fatalf("error = %v, want multiple-skill error", err)
	}
}

type sourceAwareTestDownloader struct {
	archive string
	cleaned bool
}

func (d *sourceAwareTestDownloader) Download(context.Context, string) (DownloadedZip, error) {
	return DownloadedZip{}, fmt.Errorf("whole repository download should not be called")
}

func (d *sourceAwareTestDownloader) DownloadSource(context.Context, SourceInput) (DownloadedZip, error) {
	prefix := ""
	return DownloadedZip{Path: d.archive, PathPrefix: &prefix, PackageRoot: "selected-skill", Cleanup: func() { d.cleaned = true }}, nil
}

func TestSourceAwareDownloadOverridesRepositoryPrefixAndRetainsDirectoryName(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "random-temporary-name.zip")
	writeSkillZip(t, archive, map[string][]byte{"SKILL.md": externalSkillMD("demo", "Demo."), "refs/SKILL.md": []byte("reference")})
	d := &sourceAwareTestDownloader{archive: archive}
	svc := &SkillService{downloader: d}
	pkg, _, _, err := svc.filesFromSource(context.Background(), "user", SourceInput{Type: "url", URL: "https://github.com/example/repo/archive/main.zip", PathPrefix: "skills/selected-skill"})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.PackageRoot != "selected-skill" || len(pkg.Files) != 2 || !d.cleaned {
		t.Fatalf("unexpected package or cleanup: %#v, cleaned=%v", pkg, d.cleaned)
	}
}

func TestGitHubArchiveFallbackHonorsExplicitRootSkillDocument(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "repository.zip")
	writeSkillZip(t, archive, map[string][]byte{
		"repo-main/SKILL.md":          externalSkillMD("root", "Root."),
		"repo-main/examples/SKILL.md": []byte("example document"),
	})
	archiveURL := "https://github.com/example/repo/archive/main.zip"
	svc := &SkillService{downloader: NewFakeZipDownloader(map[string]string{archiveURL: archive})}
	pkg, _, _, err := svc.filesFromSource(context.Background(), "user", SourceInput{Type: "url", URL: archiveURL, SourceURL: "https://github.com/example/repo/blob/main/SKILL.md", PathPrefixResolved: true})
	if err != nil || string(pkg.Files["examples/SKILL.md"]) != "example document" {
		t.Fatalf("explicit root skill rejected: %#v, %v", pkg, err)
	}
}
