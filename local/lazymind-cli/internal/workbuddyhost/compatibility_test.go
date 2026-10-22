package workbuddyhost

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
			version, err := desktopVersion(application)
			if err != nil || version != "6.0.0" {
				t.Fatalf("%s %v", version, err)
			}
			// A corrupt archive cannot allocate arbitrary memory or imply an old version.
			binary.LittleEndian.PutUint32(archive[4:], 0xffffffff)
			if err := os.WriteFile(path, archive, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = desktopVersion(application); err == nil {
				t.Fatal("accepted corrupt header")
			}
		})
	}
}
