package download

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitCommand runs git inside dir with a hermetic identity so tests never
// depend on the developer's global git config.
func gitCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	gitCommand(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-q", "-m", "init")
}

func TestLocalCommit(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	want := gitCommand(t, dir, "rev-parse", "HEAD")
	got, err := LocalCommit(context.Background(), dir)
	if err != nil {
		t.Fatalf("LocalCommit: %v", err)
	}
	if got != want {
		t.Fatalf("commit=%q, want %q", got, want)
	}
}

func TestRemoteRevision(t *testing.T) {
	work := t.TempDir()
	initGitRepo(t, work)
	branch := gitCommand(t, work, "symbolic-ref", "--short", "HEAD")
	bare := filepath.Join(t.TempDir(), "repo.git")
	gitCommand(t, "", "clone", "-q", "--bare", work, bare)
	want := gitCommand(t, work, "rev-parse", "HEAD")

	got, err := RemoteRevision(context.Background(), "file://"+bare, branch)
	if err != nil {
		t.Fatalf("RemoteRevision: %v", err)
	}
	if got != want {
		t.Fatalf("revision=%q, want %q", got, want)
	}
}

func TestRemoteRevisionHEADFallback(t *testing.T) {
	work := t.TempDir()
	initGitRepo(t, work)
	bare := filepath.Join(t.TempDir(), "repo.git")
	gitCommand(t, "", "clone", "-q", "--bare", work, bare)
	want := gitCommand(t, work, "rev-parse", "HEAD")

	got, err := RemoteRevision(context.Background(), "file://"+bare, "")
	if err != nil {
		t.Fatalf("RemoteRevision(HEAD): %v", err)
	}
	if got != want {
		t.Fatalf("revision=%q, want %q", got, want)
	}
}

func TestRemoteRevisionRejectsNonGitURL(t *testing.T) {
	if _, err := RemoteRevision(context.Background(), "https://example.com/archive.zip", "master"); err == nil {
		t.Fatal("expected error for non-git url")
	}
}

func TestFetchHonorsContextDeadline(t *testing.T) {
	// A stalled server that only unblocks when the request context is done:
	// Fetch must surface the deadline instead of hanging forever.
	serveBytes(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Fetch(ctx, "https://example.com/a.txt", "", t.TempDir(), nil); err == nil {
		t.Fatal("expected fetch to fail on context deadline")
	}
}
