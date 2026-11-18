package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// The subprocess replaces the signature provider, not the runtime's identity
// validation. Real concurrent codesign on the packaged App took over 13 seconds.
func TestNativeIdentityAcceptsSlowValidProviderAndHonorsCancellation(t *testing.T) {
	paths := RuntimePaths{ResourcesRoot: t.TempDir()}
	helper := filepath.Join(paths.ResourcesRoot, "bin", executableName("lazymind-update-helper"))
	if err := os.MkdirAll(filepath.Dir(helper), 0700); err != nil {
		t.Fatal(err)
	}
	// A packaged Mac provider response is valid on every host running this
	// consumer test. No native policy or host-specific installation is needed.
	valid, err := json.Marshal(map[string]any{"status": "enabled", "config": desktopupdate.Config{
		TrustDomain: "fixture", Audience: "official", Channel: "stable", Mode: "official",
		ReleaseID: "target", Version: "1.1.0", Platform: "darwin", Arch: "arm64",
		SigningIdentity: "FIXTURE_TEAM", UpdaterProtocol: 2,
		PolicyURL: "https://updates.example.invalid/policy.json", AllowedOrigins: []string{"https://updates.example.invalid"},
		PublicKeys: map[string]string{"fixture": base64.StdEncoding.EncodeToString(make([]byte, 32))},
	}})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "identity.go")
	code := "package main\nimport (\"os\";\"time\")\nfunc main(){time.Sleep(14*time.Second);os.Stdout.Write([]byte(" + strconv.Quote(string(valid)) + "))}\n"
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", helper, source).CombinedOutput(); err != nil {
		t.Fatalf("build identity provider: %v %s", err, output)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	identity, err := readNativeConfiguration(ctx, paths)
	if err != nil || identity.ReleaseID != "target" {
		t.Fatalf("valid slow identity was rejected: release=%q err=%v", identity.ReleaseID, err)
	}
	short, stop := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer stop()
	started := time.Now()
	if _, err := readNativeConfiguration(short, paths); err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("identity provider ignored caller cancellation: %v", err)
	}
}
