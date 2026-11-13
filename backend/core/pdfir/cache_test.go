package pdfir

import (
	"bufio"
	"context"
	"crypto/x509"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSharedArchiveCacheValidatesSourceAndModel(t *testing.T) {
	source := filepath.Join(t.TempDir(), "paper.pdf")
	hash := strings.Repeat("a", 64)
	raw := archive(t, `[{"type":"text","text":"hello","page_idx":0,"bbox":[0,0,1,1]}]`)
	SaveCachedArchive(source, hash, "vlm", raw)
	if len(ReadCachedArchive(source, hash, "vlm")) == 0 {
		t.Fatal("cache miss")
	}
	if ReadCachedArchive(source, strings.Repeat("b", 64), "vlm") != nil {
		t.Fatal("source mismatch")
	}
	if ReadCachedArchive(source, hash, "pipeline") != nil {
		t.Fatal("model mismatch")
	}
	SaveCachedArchive(source, hash, "vlm", []byte("broken"))
	if ReadCachedArchive(source, hash, "vlm") != nil {
		t.Fatal("invalid archive reused")
	}
}
func TestMineruTransportDiagnosticDoesNotLeakURL(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://storage/path?secret=token", Err: x509.HostnameError{Certificate: &x509.Certificate{}, Host: "mineru.net"}}
	got := mineruTransportError(err).Error()
	if !strings.Contains(got, "hostname mismatch") || strings.Contains(got, "secret") {
		t.Fatal(got)
	}
	if strings.Contains(mineruTransportError(errors.New("secret=token")).Error(), "secret") {
		t.Fatal("leaked transport detail")
	}
}

func TestSharedArchiveLockCancellationAndRelease(t *testing.T) {
	source := filepath.Join(t.TempDir(), "paper.pdf")
	hash := strings.Repeat("a", 64)
	unlock, err := LockCachedArchive(context.Background(), source, hash, "vlm")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = LockCachedArchive(ctx, source, hash, "vlm")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wanted canceled lock wait, got %v", err)
	}
	unlock()
	unlock, err = LockCachedArchive(context.Background(), source, hash, "vlm")
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

func TestSharedArchiveLockInteroperatesWithPythonFlock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix flock interoperability")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	source := filepath.Join(t.TempDir(), "paper.pdf")
	hash := strings.Repeat("a", 64)
	path := CachePath(source, hash, "vlm")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-c", "import fcntl,sys; f=open(sys.argv[1], 'w'); fcntl.flock(f, fcntl.LOCK_EX); print('locked', flush=True); sys.stdin.read(1)", path+".lock")
	out, _ := cmd.StdoutPipe()
	in, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { in.Close(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("Python lock: %q %v", line, err)
	}
	waitCtx, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	_, err = LockCachedArchive(waitCtx, source, hash, "vlm")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Python flock did not exclude Go: %v", err)
	}
	in.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	unlock, err := LockCachedArchive(ctx, source, hash, "vlm")
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
