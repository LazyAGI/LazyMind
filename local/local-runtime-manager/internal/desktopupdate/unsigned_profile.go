package desktopupdate

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
)

const TestAppID = "ai.lazymind.desktop.test"
const TestAppName = "LazyMind Test"

type TestPaths struct {
	Home, Application, Runtime, Profile, Updater, Cache, Logs, Documents, CLI string
}

// Keep test-owned paths separate within the current OS account.
func TestPathsForAccount(platform, name, home string) (TestPaths, error) {
	if platform != "darwin" || name == "" || name == "root" || !filepath.IsAbs(home) {
		return TestPaths{}, fmt.Errorf("unsigned_test_account_required")
	}
	base := filepath.Join(home, "Library", "Application Support")
	p := TestPaths{Home: home, Application: filepath.Join(home, "Applications", TestAppName+".app"),
		Runtime: filepath.Join(base, TestAppName), Profile: filepath.Join(base, "lazymind-desktop-test"),
		Updater: filepath.Join(base, "LazyMindTestUpdater"), Cache: filepath.Join(home, "Library", "Caches", TestAppName),
		Logs: filepath.Join(home, "Library", "Logs", TestAppName), Documents: filepath.Join(home, "Documents", TestAppName), CLI: filepath.Join(home, ".lazymind-test")}
	return p, nil
}

// Reject aliases before creating anything; a test root cannot be redirected
// into an existing application's data or installation directory.
func NoSymlinkAncestors(name string) error {
	for current := filepath.Clean(name); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsigned_test_path_alias")
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func UnsignedPaths() (TestPaths, error) {
	if !UnsignedTestBuild {
		return TestPaths{}, fmt.Errorf("unsigned_test_disabled")
	}
	account, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		return TestPaths{}, err
	}
	p, err := TestPathsForAccount(runtime.GOOS, account.Username, account.HomeDir)
	if err != nil {
		return p, err
	}
	for _, target := range []string{p.Application, p.Runtime, p.Profile, p.Updater, p.Cache, p.Logs, p.Documents, p.CLI} {
		if err := NoSymlinkAncestors(target); err != nil {
			return p, err
		}
	}
	return p, nil
}

func UpdaterRoot() (string, error) {
	if UnsignedTestBuild {
		p, err := UnsignedPaths()
		return p.Updater, err
	}
	base, err := RecoveryBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "LazyMindUpdater"), err
}

// The kernel releases this lock if a test helper crashes. The stage record,
// rather than a permanently orphaned lock file, governs subsequent recovery.
func LockUnsignedInstaller(root string) (func(), error) {
	name := filepath.Join(root, "install.lock")
	if err := NoSymlinkAncestors(name); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	locked, err := tryLock(f)
	if err != nil || !locked {
		f.Close()
		return nil, fmt.Errorf("unsigned_test_busy")
	}
	return func() { unlock(f); f.Close() }, nil
}
