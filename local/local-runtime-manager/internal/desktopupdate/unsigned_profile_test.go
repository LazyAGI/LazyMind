package desktopupdate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUnsignedProfileUsesCurrentMacAccountWithSeparatePaths(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	for _, name := range []string{"", "root"} {
		if _, err := TestPathsForAccount("darwin", name, home); err == nil {
			t.Fatal("accepted account", name)
		}
	}
	if _, err := TestPathsForAccount("windows", "lazymind-test", home); err == nil {
		t.Fatal("enabled Windows installer")
	}
	p, err := TestPathsForAccount("darwin", "developer", home)
	if err != nil {
		t.Fatal(err)
	}
	if p.Application != filepath.Join(home, "Applications", "LazyMind Test.app") || p.Updater != filepath.Join(home, "Library", "Application Support", "LazyMindTestUpdater") {
		t.Fatal(p)
	}
	out := t.TempDir()
	alias := filepath.Join(home, "alias")
	if err = os.Symlink(out, alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlink privilege unavailable")
		}
		t.Fatal(err)
	}
	if NoSymlinkAncestors(filepath.Join(alias, "new")) == nil {
		t.Fatal("accepted redirected test storage")
	}
	if !UnsignedTestBuild {
		t.Setenv("LAZYMIND_DESKTOP_ALLOW_UNSIGNED_UPDATE_TEST", "true")
		if _, err = UnsignedPaths(); err == nil {
			t.Fatal("environment enabled unsigned profile in normal build")
		}
	}
}

func TestUnsignedInstallationLockCanBeReusedAfterRelease(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	release, err := LockUnsignedInstaller(root)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := LockUnsignedInstaller(root); err == nil {
		second()
		t.Fatal("concurrent installer accepted")
	}
	release()
	release, err = LockUnsignedInstaller(root)
	if err != nil {
		t.Fatal("left a permanent lock", err)
	}
	release()
}
