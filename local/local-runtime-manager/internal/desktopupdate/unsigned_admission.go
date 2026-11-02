package desktopupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Use the same admission lock as both public startup checks and writer
// registration. A new process cannot finish admission during replacement.
func WithUnsignedReplacement(ctx context.Context, root string, scope UpdateScope, parent ProcessIdentity, replace func() error) error {
	resolved, err := normalizeScope(scope)
	if err != nil {
		return err
	}
	lock, err := acquireAdmission(ctx, root)
	if err != nil {
		return err
	}
	defer releaseLock(lock)
	entries, err := os.ReadDir(filepath.Join(root, "startup", "v1"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		var record startupRecord
		if !strings.HasSuffix(entry.Name(), ".json") || readMetadata(filepath.Join(root, "startup", "v1", entry.Name()), &record) != nil || record.Version != 1 || (record.Role != "writer" && record.Role != "coordinator") {
			return failure("registration_corrupt", nil)
		}
		live, err := SameProcess(record.Actor)
		if err != nil {
			return err
		}
		if live && scopesOverlap(record.Scope, resolved) && record.Actor != parent {
			return failure("runtime_busy", nil)
		}
	}
	return replace()
}

// Electron supplies the identity cached from its own packaged code, not a
// later read of the installation path. A launch admitted after replacement
// must not start old code against the newly replaced resources.
func CheckUnsignedCaller(installation, release, version string) error {
	if release == "" || version == "" {
		return fmt.Errorf("unsigned_test_build_identity")
	}
	marker := filepath.Join(filepath.Dir(installation), "unsigned-test.json")
	if err := NoSymlinkAncestors(marker); err != nil {
		return err
	}
	b, err := os.ReadFile(marker)
	var identity struct {
		AppID                    string `json:"appId"`
		Mode, ReleaseID, Version string
	}
	if err != nil || len(b) > 8192 || json.Unmarshal(b, &identity) != nil || identity.AppID != TestAppID || identity.Mode != "unsigned-test" || identity.ReleaseID != release || identity.Version != version {
		return fmt.Errorf("unsigned_test_build_identity")
	}
	return nil
}
