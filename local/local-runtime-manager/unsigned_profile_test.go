//go:build lazymind_unsigned_test

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestUnsignedRuntimeRejectsUnresolvedProfileBeforeCommands(t *testing.T) {
	previous := resolveUnsignedProfile
	resolveUnsignedProfile = func() (desktopupdate.TestPaths, error) {
		return desktopupdate.TestPaths{}, fmt.Errorf("profile unavailable")
	}
	defer func() { resolveUnsignedProfile = previous }()
	cli := NewCLI(io.Discard, io.Discard, &fakeRunner{t: t}, "fixture")
	for _, command := range []string{"up", "reset", "startup-guard", "update-state", "update-control"} {
		if err := cli.Run(context.Background(), []string{command}); err == nil {
			t.Fatal("unresolved profile accepted", command)
		}
	}
}

func TestUnsignedProfileResolvesCurrentOSAccountWithoutHomeOverride(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getuid() == 0 {
		t.Skip("requires a non-root macOS account")
	}
	account, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LAZYMIND_HOST_HOME", t.TempDir())
	p, err := desktopupdate.UnsignedPaths()
	if err != nil {
		t.Fatal(err)
	}
	if p.Home != account.HomeDir || p.Runtime != filepath.Join(account.HomeDir, "Library", "Application Support", "LazyMind Test") {
		t.Fatal("test paths escaped the OS account", p)
	}
}

func TestUnsignedRuntimeConfigStaysInsideTestRoots(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	p, _ := desktopupdate.TestPathsForAccount("darwin", "lazymind-test", home)
	previous := resolveUnsignedProfile
	resolveUnsignedProfile = func() (desktopupdate.TestPaths, error) { return p, nil }
	defer func() { resolveUnsignedProfile = previous }()
	t.Setenv("LAZYMIND_HOST_HOME", home)
	t.Setenv("LAZYMIND_RUNTIME_ROOT", p.Runtime)
	t.Setenv("LAZYMIND_LOCAL_NETWORK_PROFILE", "localhost")
	resources := filepath.Join(p.Application, "Contents", "Resources", "runtime")
	repo := filepath.Join(resources, "app")
	_ = os.MkdirAll(repo, 0700)
	_ = os.WriteFile(filepath.Join(repo, "Makefile"), nil, 0600)
	_ = os.MkdirAll(filepath.Join(repo, "local", "local-runtime-manager"), 0700)
	_ = os.WriteFile(filepath.Join(repo, "local", "local-runtime-manager", "go.mod"), nil, 0600)
	opts := RuntimeConfigOptions{Profile: "desktop", RepoRoot: repo, ResourcesRoot: resources, RuntimeRoot: p.Runtime}
	_, paths, err := newRuntimeConfigWithOptions(opts, true)
	if err != nil {
		t.Fatal(err)
	}
	if paths.CacheDir != filepath.Join(home, "Library", "Caches", "LazyMind Test") {
		t.Fatal(paths.CacheDir)
	}
	opts.RuntimeRoot = filepath.Join(home, "Library", "Application Support", "LazyMind")
	if _, _, err = newRuntimeConfigWithOptions(opts, true); err == nil {
		t.Fatal("accepted official runtime")
	}
	if _, err = os.Stat(opts.RuntimeRoot); !os.IsNotExist(err) {
		t.Fatal("created official data")
	}
}
