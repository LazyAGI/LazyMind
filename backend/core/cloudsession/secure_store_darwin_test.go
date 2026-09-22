//go:build darwin && cgo

package cloudsession

import (
	"path/filepath"
	"testing"
)

func TestDarwinLoginKeychainPathUsesHostHomeOutsideIsolatedServiceHome(t *testing.T) {
	hostHome := filepath.Join(string(filepath.Separator), "Users", "fixture-user")
	t.Setenv("LAZYMIND_HOST_HOME", hostHome)
	t.Setenv("HOME", filepath.Join(string(filepath.Separator), "tmp", "isolated-service-home"))
	want := filepath.Join(hostHome, "Library", "Keychains", "login.keychain-db")
	if got := darwinLoginKeychainPath(); got != want {
		t.Fatalf("keychain path = %q, want %q", got, want)
	}
}
