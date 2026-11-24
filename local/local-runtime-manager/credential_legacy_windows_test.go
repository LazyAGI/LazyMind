//go:build windows

package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/teststorage"
)

func TestWindowsLegacyDesktopPublishesIdentityWithConfiguredExternalStores(t *testing.T) {
	f := newInstalledStorageFixture(t)
	f.registry.DeleteValue(teststorage.DataKey, "DataRoot")
	f.registry.DeleteValue(teststorage.InstallKey, "InstallLocation")
	if err := os.Remove(filepath.Join(f.app, teststorage.AppMarker)); err != nil {
		t.Fatal(err)
	}
	opts := f.options("desktop")
	opts.RuntimeRoot = t.TempDir()
	storesRoot := t.TempDir()
	authStoreRoot := t.TempDir()
	authDB := filepath.Join(authStoreRoot, "custom-auth.db")
	t.Setenv(localSQLiteDirEnvVar, storesRoot)
	t.Setenv(authServiceDatabaseURLEnvVar, sqliteURL(authDB))
	t.Setenv("LAZYMIND_MCP_SECRET_KEY", "")
	cfg, paths, err := newRuntimeConfigWithOptions(opts, true)
	if err != nil {
		t.Fatal(err)
	}
	if paths.registeredCredentialRoot != "" || paths.RuntimeRoot != opts.RuntimeRoot ||
		cfg.AuthService.DatabaseURL != sqliteURL(authDB) || paths.CoreDBPath != filepath.Join(storesRoot, "core", "core.db") {
		t.Fatal("fixture must use the legacy profile and both configured external databases")
	}
	actualPaths := paths
	actualPaths.AuthServiceDBPath = authDB
	writeStoredCredentialFixture(t, actualPaths, "auth", false)
	writeStoredCredentialFixture(t, actualPaths, "model", false)
	authBytes := credentialRecoveryFileBytes(t, authDB)
	coreBytes := credentialRecoveryFileBytes(t, paths.CoreDBPath)
	authTree := credentialRecoveryTree(t, authStoreRoot)
	coreTree := credentialRecoveryTree(t, storesRoot)
	identityPath := filepath.Join(f.legacyRoot, "Desktop", "credential-device.json")
	assertCredentialRecoveryFileAbsent(t, identityPath)

	if err := ensureWindowsDesktopCredentialIdentity(cfg, paths); err != nil {
		t.Fatal(err)
	}
	if _, err := readCredentialDevice(identityPath); err != nil {
		t.Fatalf("expected a usable identity in the legacy Desktop profile: %v", err)
	}
	assertCredentialRecoveryFileUnchanged(t, authDB, authBytes)
	assertCredentialRecoveryFileUnchanged(t, paths.CoreDBPath, coreBytes)
	if !slices.Equal(credentialRecoveryTree(t, authStoreRoot), authTree) ||
		!slices.Equal(credentialRecoveryTree(t, storesRoot), coreTree) {
		t.Error("publication left new files in an external database directory")
	}
}
