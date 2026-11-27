package workbuddyhost

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/connectorconfig"
)

func TestVersionMinimum(t *testing.T) {
	for _, tc := range []struct{ version, issue string }{
		{"5.7.7", "unsupported_version"}, {"5.8.2", "unsupported_version"}, {"5.8.3-rc.1", "unsupported_version"},
		{"5.8.3", ""}, {"5.8.3+build.1", ""}, {"5.8.10", ""}, {"5.10.0", ""}, {"6.0.0", ""}, {"5.9.0-rc.1", ""},
		{"", "version_unreadable"}, {"v5.8.3", "version_unreadable"}, {"5.8", "version_unreadable"},
		{"5.08.3", "version_unreadable"}, {"5.8.3-01", "version_unreadable"}, {"5.8.3-", "version_unreadable"},
	} {
		if got := VersionIssue(tc.version); got != tc.issue {
			t.Errorf("%q: %q, want %q", tc.version, got, tc.issue)
		}
	}
}

func TestDesktopManifestOnMacAndWindowsLayouts(t *testing.T) {
	for _, app := range []string{"WorkBuddy.app", "WorkBuddy.exe"} {
		t.Run(app, func(t *testing.T) {
			root := t.TempDir()
			application := filepath.Join(root, app)
			resources := filepath.Join(root, "resources")
			if filepath.Ext(app) == ".app" {
				resources = filepath.Join(application, "Contents", "Resources")
			}
			if err := os.MkdirAll(resources, 0700); err != nil {
				t.Fatal(err)
			}
			// Real ASAR pickle header with aligned JSON followed by the desktop manifest.
			manifest := []byte(`{"version":"6.0.0"}`)
			header, _ := json.Marshal(map[string]any{"files": map[string]any{"package.json": map[string]any{"size": len(manifest), "offset": "0"}}})
			padded := (len(header) + 3) &^ 3
			archive := make([]byte, 16+padded)
			binary.LittleEndian.PutUint32(archive, 4)
			binary.LittleEndian.PutUint32(archive[4:], uint32(8+padded))
			binary.LittleEndian.PutUint32(archive[8:], uint32(4+padded))
			binary.LittleEndian.PutUint32(archive[12:], uint32(len(header)))
			copy(archive[16:], header)
			archive = append(archive, manifest...)
			path := filepath.Join(resources, "app.asar")
			if err := os.WriteFile(path, archive, 0600); err != nil {
				t.Fatal(err)
			}
			version, err := agentexec.ElectronVersion(application)
			if err != nil || version != "6.0.0" {
				t.Fatalf("%s %v", version, err)
			}
			// A corrupt archive cannot allocate arbitrary memory or imply an old version.
			binary.LittleEndian.PutUint32(archive[4:], 0xffffffff)
			if err := os.WriteFile(path, archive, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = agentexec.ElectronVersion(application); err == nil {
				t.Fatal("accepted corrupt header")
			}
		})
	}
}

// The expected path is an assertion only. Discovery still uses the platform's
// normal installation registrations or standard Applications directories.
func TestInstalledWorkBuddyDesktopDiscovery(t *testing.T) {
	expected := os.Getenv("LAZYMIND_TEST_WORKBUDDY_DESKTOP")
	if expected == "" {
		t.Skip("set LAZYMIND_TEST_WORKBUDDY_DESKTOP to the expected installed WorkBuddy desktop path")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Fatal("installed WorkBuddy desktop discovery requires macOS or Windows")
	}
	if !filepath.IsAbs(expected) {
		t.Fatalf("expected desktop installation must be an absolute path: %q", expected)
	}
	want, err := os.Stat(expected)
	if err != nil {
		t.Fatalf("read installed WorkBuddy at %s: %v", expected, err)
	}
	t.Setenv("LAZYMIND_HOME", t.TempDir())
	t.Setenv(connectorconfig.EnvironmentKey, "")
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", "")

	application, err := desktopApplication()
	if err != nil {
		t.Fatalf("discover installed WorkBuddy: %v", err)
	}
	found, err := os.Stat(application)
	if err != nil || !os.SameFile(found, want) {
		t.Fatalf("discovered WorkBuddy at %q, wanted installed copy at %q: %v", application, expected, err)
	}
	version := CurrentVersion()
	if version == "" {
		t.Fatalf("could not read the installed WorkBuddy desktop manifest at %s", application)
	}
	if expectedVersion := os.Getenv("LAZYMIND_TEST_WORKBUDDY_DESKTOP_VERSION"); expectedVersion != "" && version != expectedVersion {
		t.Fatalf("installed WorkBuddy desktop manifest version = %q, want %q", version, expectedVersion)
	}
	t.Logf("automatically discovered installed WorkBuddy at %s; desktop manifest version %s", application, version)
}
