package desktopupdate

import (
	"errors"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
)

const TestAppID = "ai.lazymind.desktop.test"
const TestAppName = "LazyMind Test"

var unsignedPolicyPath = regexp.MustCompile(`^/datasets/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+/resolve/master/updates/unsigned-test/(?:[A-Za-z0-9._-]+/)*[A-Za-z0-9_-]+\.json$`)

func validUnsignedTestConfig(c Config) bool {
	u, err := url.Parse(c.PolicyURL)
	return c.Mode == "unsigned-test" && c.Audience != "official" && c.Channel == "personal-test" &&
		c.SigningIdentity == "adhoc" && c.Platform == "darwin" && c.UpdaterProtocol == 2 &&
		err == nil && u.Scheme == "https" && u.Host == "modelscope.cn" && u.User == nil &&
		u.RawQuery == "" && u.Fragment == "" && u.RawPath == "" && u.Path == path.Clean(u.Path) && unsignedPolicyPath.MatchString(u.Path)
}

type UnsignedTestPaths struct {
	Home, Application, Runtime, Profile, Updater, Cache, Logs, Documents, CLI string
}

func UnsignedPaths() (UnsignedTestPaths, error) {
	if !UnsignedTestBuild || runtime.GOOS != "darwin" || os.Getuid() == 0 {
		return UnsignedTestPaths{}, errors.New("unsigned_test_disabled")
	}
	base, err := RecoveryBase()
	if err != nil {
		return UnsignedTestPaths{}, err
	}
	home := filepath.Dir(filepath.Dir(base))
	p := UnsignedTestPaths{
		Home: home, Application: filepath.Join(home, "Applications", TestAppName+".app"),
		Runtime: filepath.Join(base, TestAppName), Profile: filepath.Join(base, "lazymind-desktop-test"),
		Updater: filepath.Join(base, "LazyMindTestUpdater"), Cache: filepath.Join(home, "Library", "Caches", TestAppName),
		Logs: filepath.Join(home, "Library", "Logs", TestAppName), Documents: filepath.Join(home, "Documents", TestAppName),
		CLI: filepath.Join(home, ".lazymind-test"),
	}
	for _, target := range []string{p.Application, p.Runtime, p.Profile, p.Updater, p.Cache, p.Logs, p.Documents, p.CLI} {
		if err := NoUnsignedPathAliases(target); err != nil {
			return UnsignedTestPaths{}, err
		}
	}
	return p, nil
}

// Installing a test update may not follow aliases into another installation.
func NoUnsignedPathAliases(name string) error {
	for current := filepath.Clean(name); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsigned_test_path_alias")
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}
