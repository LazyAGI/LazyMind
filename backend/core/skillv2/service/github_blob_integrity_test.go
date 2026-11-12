package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// These object IDs were independently obtained with git hash-object --stdin.
const rightGitBlobSHA = "19981f7e045c3a2c35d15e977b6f56f1923bb400"

func TestGitHubPackageRejectsEqualLengthBlobMismatchAndCleansArtifacts(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	fixture := newPackageHTTPFixture(t, nil, map[string]string{"/raw/acme/repo/" + packageCommit + "/SKILL.md": "wrong"})
	got, err := fixture.downloader().writeGitHubPackage(context.Background(), fixture.server.Client(), "acme", "repo", packageCommit, "", "repo", []githubTreeEntry{{Path: "SKILL.md", Mode: "100644", Type: "blob", SHA: rightGitBlobSHA, Size: 5}})
	if got.Cleanup != nil {
		got.Cleanup()
	}
	var retrieval *githubRetrievalError
	if !errors.As(err, &retrieval) || !strings.Contains(err.Error(), "blob SHA") {
		t.Fatalf("same-length corrupted raw content was accepted: %v", err)
	}
	if got.Path != "" {
		t.Fatalf("corrupted package was published at %s", got.Path)
	}
	assertNoGitHubDownloadArtifacts(t)
}

func TestGitHubPackageAcceptsVerifiedGitBlobs(t *testing.T) {
	for _, test := range []struct{ name, content, sha string }{
		{"text", "right", rightGitBlobSHA},
		{"uppercase object ID", "right", "19981F7E045C3A2C35D15E977B6F56F1923BB400"},
		{"empty", "", "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"},
		{"binary", "\x00\xff\x01", "046f393ee9d8a6a0754b8ddadce727b4ce35f272"},
		{"opaque fixture ID", "right", "opaque-fixture-id"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPackageHTTPFixture(t, nil, map[string]string{"/raw/acme/repo/" + packageCommit + "/SKILL.md": test.content})
			got, err := fixture.downloader().writeGitHubPackage(context.Background(), fixture.server.Client(), "acme", "repo", packageCommit, "", "repo", []githubTreeEntry{{Path: "SKILL.md", Mode: "100644", Type: "blob", SHA: test.sha, Size: int64(len(test.content))}})
			if err != nil {
				t.Fatal(err)
			}
			if contents := packageZipContents(t, got); contents["SKILL.md"] != test.content {
				t.Fatalf("verified contents=%v", contents)
			}
		})
	}
}

func TestGitHubBlobMismatchUsesPinnedArchiveAndCleansArtifacts(t *testing.T) {
	for _, fallbackFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovered", true: "fallback fails"}[fallbackFails], func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			entry, err := writer.Create("repo-root/SKILL.md")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = io.WriteString(entry, "right"); err != nil {
				t.Fatal(err)
			}
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			routes := map[string]any{
				"/api/repos/acme/repo/git/trees/" + packageCommit + "?recursive=1": packageTree(packageEntry("SKILL.md", "100644", "blob", rightGitBlobSHA, 5)),
				"/archive.zip": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if fallbackFails {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					_, _ = w.Write(archive.Bytes())
				}),
			}
			fixture := newPackageHTTPFixture(t, routes, map[string]string{"/raw/acme/repo/" + packageCommit + "/SKILL.md": "wrong"})
			downloader := fixture.downloader()
			downloader.client = &http.Client{Transport: packageRewriteTransport{base: fixture.server.Client().Transport, baseURL: fixture.server.URL}}
			got, err := downloader.DownloadSource(context.Background(), packageSource(""))
			if got.Cleanup != nil {
				defer got.Cleanup()
			}
			if fallbackFails {
				if err == nil {
					t.Fatal("corrupted raw bytes were returned when recovery failed")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if got.PathPrefix != nil {
					t.Fatal("corrupted raw package was returned instead of the pinned archive")
				}
				if files := packageZipContents(t, got); len(files) != 1 || files["repo-root/SKILL.md"] != "right" {
					t.Fatalf("recovered archive=%v", files)
				}
			}
			fixture.mu.Lock()
			requests := append([]string(nil), fixture.requests...)
			fixture.mu.Unlock()
			if len(requests) != 3 || requests[2] != "/archive.zip" {
				t.Fatalf("did not recover with the pinned archive: %v", requests)
			}
			if got.Cleanup != nil {
				got.Cleanup()
			}
			assertNoGitHubDownloadArtifacts(t)
		})
	}
}

func assertNoGitHubDownloadArtifacts(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary download artifacts=%v error=%v", entries, err)
	}
}
