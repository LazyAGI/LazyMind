//go:build lazymind_unsigned_test && !windows

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/unsignedupdate"
)

func TestUnsignedGuardTerminationReleasesWriterBeforeHandoff(t *testing.T) {
	const fixtureKey = "LAZYMIND_GUARD_STOP_FIXTURE"
	if home := os.Getenv(fixtureKey); home != "" {
		p, err := desktopupdate.TestPathsForAccount("darwin", "lazymind-test", home)
		if err != nil {
			t.Fatal(err)
		}
		resolveUnsignedProfile = func() (desktopupdate.TestPaths, error) { return p, nil }
		resolveRuntimeRecoveryRoot = func() (string, error) { return p.Updater, nil }
		resources := filepath.Join(p.Application, "Contents", "Resources", "runtime")
		runner := &fakeRunner{t: t}
		cli := NewCLI(io.Discard, io.Discard, runner, "fixture-runtime")
		err = cli.Run(context.Background(), []string{"guard", "--owner-pid", strconv.Itoa(os.Getppid()), "--owner-token", "fixture-owner", "--profile", "desktop", "--repo-root", filepath.Join(resources, "app"), "--resources-root", resources, "--runtime-root", p.Runtime})
		if err != nil {
			t.Fatal("guard cancellation failed", err)
		}
		if len(runner.calls) != 0 {
			t.Fatal("guard ran shutdown while owner remained alive", runner.calls)
		}
		return
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := desktopupdate.TestPathsForAccount("darwin", "lazymind-test", home)
	if err != nil {
		t.Fatal(err)
	}
	resources := filepath.Join(p.Application, "Contents", "Resources", "runtime")
	repo := filepath.Join(resources, "app")
	if err = os.MkdirAll(filepath.Join(repo, "local", "local-runtime-manager"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{filepath.Join(repo, "Makefile"), filepath.Join(repo, "local", "local-runtime-manager", "go.mod")} {
		if err = os.WriteFile(file, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	marker := func(version, release string) []byte {
		b, _ := json.Marshal(map[string]string{"appId": desktopupdate.TestAppID, "mode": "unsigned-test", "version": version, "releaseId": release, "platform": "darwin", "arch": "arm64", "signingIdentity": "adhoc", "trustDomain": "fixture", "audience": "fixture", "channel": "personal-test"})
		return b
	}
	if err = os.WriteFile(filepath.Join(p.Application, "Contents", "Resources", "unsigned-test.json"), marker("1.0.0", "source"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_UNSIGNED_EXPECTED_RELEASE", "source")
	t.Setenv("LAZYMIND_UNSIGNED_EXPECTED_VERSION", "1.0.0")
	t.Setenv(authServiceDatabaseURLEnvVar, "")
	config := desktopupdate.Config{Mode: "unsigned-test", TrustDomain: "fixture", Audience: "fixture", Channel: "personal-test", ReleaseID: "source", Version: "1.0.0", Platform: "darwin", Arch: "arm64", SigningIdentity: "adhoc", UpdaterProtocol: 1, PolicyURL: "http://127.0.0.1:18080/policy.json", AllowedOrigins: []string{"http://127.0.0.1:18080"}, PublicKeys: map[string]string{"test": base64.StdEncoding.EncodeToString(make([]byte, 32))}}
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	header := &zip.FileHeader{Name: "LazyMind Test.app/Contents/Resources/unsigned-test.json"}
	header.SetMode(0600)
	w, err := z.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write(marker("1.1.0", "target")); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(p.Updater, "downloads")
	if err = os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(cache, "fixture.zip")
	if err = os.WriteFile(pack, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive.Bytes())
	parent, err := desktopupdate.CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	alive := true
	installed := false
	installer := unsignedupdate.Installer{Config: config, Paths: p, Inspect: func(context.Context, string) error { return nil }, Verify: func(context.Context, string) error { return nil }, SameProcess: func(desktopupdate.ProcessIdentity) (bool, error) { return alive, nil }, Launch: func(context.Context, string) error { installed = true; return nil }}
	id, err := installer.Prepare(context.Background(), unsignedupdate.Request{PackagePath: pack, Release: desktopupdate.Release{ID: "target", Version: "1.1.0", Platform: "darwin", Arch: "arm64", SigningIdentity: "adhoc", Package: desktopupdate.Artifact{Size: int64(archive.Len()), SHA256: hex.EncodeToString(digest[:])}}}, parent)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestUnsignedGuardTerminationReleasesWriterBeforeHandoff$")
	child.Env = append(os.Environ(), fixtureKey+"="+home)
	var output bytes.Buffer
	child.Stdout = &output
	child.Stderr = &output
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	registry := filepath.Join(p.Updater, "startup", "v1")
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, _ := os.ReadDir(registry)
		registered := false
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			b, _ := os.ReadFile(filepath.Join(registry, entry.Name()))
			var record struct{ Actor struct{ PID int } }
			if json.Unmarshal(b, &record) == nil && record.Actor.PID == child.Process.Pid {
				registered = true
			}
		}
		if registered {
			break
		}
		if time.Now().After(deadline) {
			_ = child.Process.Kill()
			_ = child.Wait()
			t.Fatal("guard did not register", output.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err = installer.Install(context.Background(), id, parent, func() error { t.Error("accepted live guard"); return nil }); desktopupdate.ErrorCode(err) != "runtime_busy" {
		t.Fatal("guard was not the handoff blocker", err)
	}
	if err = child.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err = child.Wait(); err != nil {
		t.Fatal("guard did not terminate gracefully", err, output.String())
	}
	entries, err := os.ReadDir(registry)
	if err != nil || len(entries) != 0 {
		t.Fatal("guard writer lease was not removed", err, len(entries))
	}
	if err = installer.Install(context.Background(), id, parent, func() error { alive = false; return nil }); err != nil {
		t.Fatal("guard-free handoff failed", err)
	}
	if !installed {
		t.Fatal("test replacement did not reach the launch boundary")
	}
}
