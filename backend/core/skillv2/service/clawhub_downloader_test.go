package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"lazymind/core/skillv2/httperr"
	"lazymind/core/skillv2/skillpackage"
)

func TestClawHubDownloadPreservesHostedBinaryZIP(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "hosted.zip")
	files := map[string][]byte{"SKILL.md": externalSkillMD("hosted", "A hosted skill."), "image.bin": {0, 255, 128, 3}}
	writeSkillZip(t, archive, files)
	zipBytes, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/download": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipBytes) }),
	}, nil)
	got, err := fixture.downloader().downloadClawHubSource(context.Background(), SourceInput{URL: fixture.server.URL + "/download"})
	if err != nil {
		t.Fatal(err)
	}
	defer got.Cleanup()
	actual, err := os.ReadFile(got.Path)
	if err != nil || !bytes.Equal(actual, zipBytes) {
		t.Fatalf("hosted archive changed: err=%v", err)
	}
}

func TestClawHubDownloadResolvesPinnedGitHubDirectory(t *testing.T) {
	fixture := clawHubHandoffFixture(t, clawHubTestHandoff(), false)
	got, err := fixture.downloader().downloadClawHubSource(context.Background(), clawHubTestSource(fixture))
	if err != nil {
		t.Fatal(err)
	}
	defer got.Cleanup()
	pkg, err := skillpackage.ReadZip(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got.PathPrefix == nil || *got.PathPrefix != "" || got.PackageRoot != "target" || !reflect.DeepEqual(pkg.Files, clawHubTestFiles()) {
		t.Fatalf("unexpected selected package: prefix=%v root=%q files=%v", got.PathPrefix, got.PackageRoot, pkg.Files)
	}
	for _, request := range fixture.requests {
		if strings.Contains(request, "sibling") {
			t.Fatalf("downloaded a sibling skill: %s", request)
		}
	}
}

func TestClawHubDownloadExtractsGitHubArchiveFallback(t *testing.T) {
	fixture := clawHubHandoffFixture(t, clawHubTestHandoff(), true)
	downloader := fixture.downloader()
	downloader.client = &http.Client{Transport: packageRewriteTransport{base: fixture.server.Client().Transport, baseURL: fixture.server.URL}}
	got, err := downloader.downloadClawHubSource(context.Background(), clawHubTestSource(fixture))
	if err != nil {
		t.Fatal(err)
	}
	defer got.Cleanup()
	pkg, err := skillpackage.ReadZip(got.Path)
	if err != nil || got.PathPrefix == nil || *got.PathPrefix != "" || got.PackageRoot != "target" || !reflect.DeepEqual(pkg.Files, clawHubTestFiles()) {
		t.Fatalf("archive fallback did not select only target: %#v %v, error=%v", got, pkg.Files, err)
	}
}

func TestClawHubDownloadRejectsUntrustedOrIncompleteHandoffs(t *testing.T) {
	for _, tt := range []struct{ key, value string }{
		{"sourceRef", "external"}, {"repo", "https://evil.test/repo"}, {"repo", "evil.test/owner/repo"},
		{"repo", "../repo"}, {"repo", "owner/repo?token=secret"}, {"repo", "owner/repo#fragment"},
		{"commit", "main"}, {"commit", strings.Repeat("z", 40)}, {"commit", ""},
		{"path", "../target"}, {"path", "/target"}, {"path", "skills//target"}, {"path", "skills/./target"},
		{"path", "skills\\target"}, {"path", ""}, {"contentHash", ""}, {"contentHash", "sha256:bad"},
	} {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			handoff := clawHubTestHandoff()
			handoff[tt.key] = tt.value
			fixture := newPackageHTTPFixture(t, map[string]any{"/download": handoff}, nil)
			_, err := fixture.downloader().downloadClawHubSource(context.Background(), clawHubTestSource(fixture))
			if err == nil || !strings.Contains(err.Error(), "ClawHub") {
				t.Fatalf("invalid handoff accepted: error=%v", err)
			}
			if len(fixture.requests) != 1 {
				t.Fatalf("invalid handoff issued upstream requests: %v", fixture.requests)
			}
		})
	}
}

func TestClawHubDownloadNeverFollowsDescriptorArchiveURL(t *testing.T) {
	handoff := clawHubTestHandoff()
	handoff["archiveUrl"] = "http://127.0.0.1:1/private-secrets"
	fixture := clawHubHandoffFixture(t, handoff, false)
	got, err := fixture.downloader().downloadClawHubSource(context.Background(), clawHubTestSource(fixture))
	if err != nil {
		t.Fatal(err)
	}
	got.Cleanup()
}

func TestClawHubDownloadRejectsFolderHashMismatch(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "archive fallback"}[fallback], func(t *testing.T) {
			handoff := clawHubTestHandoff()
			handoff["contentHash"] = strings.Repeat("a", 64)
			fixture := clawHubHandoffFixture(t, handoff, fallback)
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			downloader := fixture.downloader()
			downloader.client = &http.Client{Transport: packageRewriteTransport{base: fixture.server.Client().Transport, baseURL: fixture.server.URL}}
			got, err := downloader.downloadClawHubSource(context.Background(), clawHubTestSource(fixture))
			if got.Cleanup != nil {
				got.Cleanup()
			}
			if err == nil || !strings.Contains(err.Error(), "hash") {
				t.Fatalf("unapproved folder content accepted: err=%v", err)
			}
			entries, err := os.ReadDir(tmp)
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary initial/upstream download leaked: %v, %v", entries, err)
			}
		})
	}
}

func TestClawHubDownloadSharesDescriptorAndGitHubTransferBudget(t *testing.T) {
	handoff := clawHubTestHandoff()
	handoff["padding"] = strings.Repeat("x", 2048)
	fixture := newPackageHTTPFixture(t, map[string]any{
		"/download": handoff,
		"/api/repos/acme/repo/git/trees/" + packageCommit: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// This response fits a fresh transfer budget but exceeds the remaining
			// budget after reading the handoff. No large body needs to be sent.
			w.Header().Set("Content-Length", "104857600")
			w.WriteHeader(http.StatusOK)
		}),
		"/archive.zip": http.StatusNotFound,
	}, nil)
	downloader := fixture.downloader()
	downloader.client = &http.Client{Transport: packageRewriteTransport{base: fixture.server.Client().Transport, baseURL: fixture.server.URL}}
	_, err := downloader.downloadClawHubSource(context.Background(), clawHubTestSource(fixture))
	if err == nil || httperr.ForError(err).Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("combined descriptor/GitHub transfer budget was reset: %v", err)
	}
}

func TestClawHubFolderHashMatchesOfficialCLIOrdering(t *testing.T) {
	// Produced by the official CLI's buildGitHubFolderContentHash using Node's
	// localeCompare: _meta, .secret, a.txt, A.txt, é.txt, scripts/run.sh, SKILL.md.
	files := map[string][]byte{"SKILL.md": []byte("skill"), "A.txt": []byte("A"), "a.txt": []byte("a"), "_meta": []byte("m"), ".secret": []byte("s"), "é.txt": []byte("e"), "scripts/run.sh": []byte("run")}
	if got := clawHubFolderContentHash(files); got != "cd4de090001256b9759152add4d2683b370dca561ed8cbb4165f59670edc0fdb" {
		t.Fatalf("folder fingerprint differs from official CLI: %s", got)
	}
}

func TestClawHubDownloadVerifiesOriginalNamesAndMetadataBeforeNormalization(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "archive fallback"}[fallback], func(t *testing.T) {
			handoff := clawHubTestHandoff()
			handoff["contentHash"] = "7ad9d4041ebe8a7f1dc1b0bd1b3370d9ef05f92a1d1c9470627797e4b67f1026"
			fixture := clawHubHandoffFixture(t, handoff, fallback)
			fixture.routes["/api/repos/acme/repo/git/trees/target-tree?recursive=1"] = packageTree(packageEntry("skill.md", "100644", "blob", "skill", 5), packageEntry("asset.bin", "100644", "blob", "asset", 4), packageEntry(".DS_Store", "100644", "blob", "metadata", 8))
			fixture.raw["/raw/acme/repo/"+packageCommit+"/skills/target/skill.md"] = "skill"
			fixture.raw["/raw/acme/repo/"+packageCommit+"/skills/target/.DS_Store"] = "metadata"
			if fallback {
				archive := filepath.Join(t.TempDir(), "raw-upstream.zip")
				writeSkillZip(t, archive, map[string][]byte{"repo-sha/skills/target/skill.md": []byte("skill"), "repo-sha/skills/target/asset.bin": {0, 255, 128, 2}, "repo-sha/skills/target/.DS_Store": []byte("metadata")})
				body, err := os.ReadFile(archive)
				if err != nil {
					t.Fatal(err)
				}
				fixture.routes["/archive.zip"] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) })
			}
			downloader := fixture.downloader()
			downloader.client = &http.Client{Transport: packageRewriteTransport{base: fixture.server.Client().Transport, baseURL: fixture.server.URL}}
			got, err := downloader.downloadClawHubSource(context.Background(), clawHubTestSource(fixture))
			if err != nil {
				t.Fatalf("approved original folder rejected after normalization: %v", err)
			}
			got.Cleanup()
		})
	}
}

func TestClawHubDownloadBoundsAndRejectsNonArchiveResponses(t *testing.T) {
	for _, body := range []string{"<html>blocked</html>", `{"sourceRef":`, "null", `{}`, `{"sourceRef":"public-github"} {}`, strings.Repeat(" ", 1<<20+1)} {
		t.Run(body[:min(len(body), 45)], func(t *testing.T) {
			fixture := newPackageHTTPFixture(t, map[string]any{
				"/download": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }),
			}, nil)
			if _, err := fixture.downloader().downloadClawHubSource(context.Background(), clawHubTestSource(fixture)); err == nil {
				t.Fatal("non-archive response accepted")
			}
		})
	}
}

func TestClawHubDownloadHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := HTTPZipDownloader{}.downloadClawHubSource(ctx, SourceInput{URL: "https://clawhub.ai/api/v1/download?slug=target"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestClawHubDownloadCancelsGitHubFollowupAndCleansFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	fixture := clawHubHandoffFixture(t, clawHubTestHandoff(), false)
	started := make(chan struct{})
	fixture.routes["/raw/acme/repo/"+packageCommit+"/skills/target/SKILL.md"] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		got, err := fixture.downloader().downloadClawHubSource(ctx, clawHubTestSource(fixture))
		if got.Cleanup != nil {
			got.Cleanup()
		}
		finished <- err
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(3 * time.Second):
		t.Fatal("GitHub file fetch did not start")
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("followup did not preserve cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("GitHub handoff ignored cancellation")
	}
	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled handoff leaked temporary files: %v, %v", entries, err)
	}
}

func TestClawHubDownloadRemovesTemporaryFilesAfterHandoffFailure(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	fixture := newPackageHTTPFixture(t, map[string]any{"/download": map[string]any{"sourceRef": "public-github"}}, nil)
	_, err := fixture.downloader().downloadClawHubSource(context.Background(), clawHubTestSource(fixture))
	if err == nil {
		t.Fatal("incomplete handoff accepted")
	}
	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary download leaked: %v, %v", entries, err)
	}
}

func clawHubTestSource(fixture *packageHTTPFixture) SourceInput {
	return SourceInput{Type: "url", URL: fixture.server.URL + "/download", SourceURL: "https://clawhub.ai/acme/skills/target", CanonicalName: "target", PathPrefixResolved: true}
}

func clawHubTestHandoff() map[string]any {
	return map[string]any{
		"sourceRef": "public-github", "repo": "acme/repo", "commit": packageCommit, "path": "skills/target",
		"contentHash": "d147af63d655fa35cbb446e35d035c3cb5910effeaae0d0cb5173c952d630b6d", "archiveUrl": "https://api.github.com/repos/acme/repo/zipball/" + packageCommit,
	}
}

func clawHubTestFiles() map[string][]byte {
	return map[string][]byte{"SKILL.md": []byte("skill"), "asset.bin": {0, 255, 128, 2}}
}

func clawHubHandoffFixture(t *testing.T, handoff map[string]any, fallback bool) *packageHTTPFixture {
	t.Helper()
	routes := map[string]any{
		"/download": handoff,
		"/api/repos/acme/repo/git/trees/" + packageCommit:        packageTree(packageEntry("skills", "040000", "tree", "skills-tree", 0)),
		"/api/repos/acme/repo/git/trees/skills-tree":             packageTree(packageEntry("target", "040000", "tree", "target-tree", 0), packageEntry("sibling", "040000", "tree", "sibling-tree", 0)),
		"/api/repos/acme/repo/git/trees/target-tree?recursive=1": packageTree(packageEntry("SKILL.md", "100644", "blob", "skill", 5), packageEntry("asset.bin", "100644", "blob", "asset", 4)),
	}
	if fallback {
		routes["/api/repos/acme/repo/git/trees/"+packageCommit] = http.StatusNotFound
		archive := filepath.Join(t.TempDir(), "upstream.zip")
		files := map[string][]byte{"repo-sha/README.md": []byte("not a skill"), "repo-sha/skills/sibling/SKILL.md": []byte("different skill")}
		for name, value := range clawHubTestFiles() {
			files["repo-sha/skills/target/"+name] = value
		}
		writeSkillZip(t, archive, files)
		zipBytes, err := os.ReadFile(archive)
		if err != nil {
			t.Fatal(err)
		}
		routes["/archive.zip"] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipBytes) })
	}
	fixture := newPackageHTTPFixture(t, routes, map[string]string{
		"/raw/acme/repo/" + packageCommit + "/skills/target/SKILL.md":  "skill",
		"/raw/acme/repo/" + packageCommit + "/skills/target/asset.bin": string([]byte{0, 255, 128, 2}),
	})
	return fixture
}
