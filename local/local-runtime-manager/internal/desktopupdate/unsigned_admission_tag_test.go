//go:build lazymind_unsigned_test

package desktopupdate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUnsignedTaggedRegistrationUsesCachedBuildIdentity(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	resources := filepath.Join(root, "Resources")
	_ = os.MkdirAll(resources, 0700)
	_ = os.WriteFile(filepath.Join(resources, "unsigned-test.json"), []byte(`{"appId":"ai.lazymind.desktop.test","mode":"unsigned-test","releaseId":"n1","version":"1.1.0"}`), 0600)
	scope := UpdateScope{Installation: filepath.Join(resources, "runtime")}
	actor, err := CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_UNSIGNED_EXPECTED_RELEASE", "n")
	t.Setenv("LAZYMIND_UNSIGNED_EXPECTED_VERSION", "1.0.0")
	updater := filepath.Join(root, "updater")
	if lease, err := BeginStartup(context.Background(), updater, scope, "owner", "coordinator", actor); err == nil {
		lease.Close()
		t.Fatal("stale process registered")
	}
	if _, err := os.Stat(filepath.Join(updater, "startup", "v1")); !os.IsNotExist(err) {
		t.Fatal("stale admission wrote a record")
	}
	t.Setenv("LAZYMIND_UNSIGNED_EXPECTED_RELEASE", "n1")
	t.Setenv("LAZYMIND_UNSIGNED_EXPECTED_VERSION", "1.1.0")
	lease, err := BeginStartup(context.Background(), updater, scope, "owner", "coordinator", actor)
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
}
