//go:build windows

package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/teststorage"
)

const fixtureDesktopModelKey = "l_JSbmfusw38fVGvRPOcm_rKOkAV4OvMSNjNgcvPK38"

func TestCredentialRecoveryIndependentLocalKeepsIdentityAcrossRegistrationRepair(t *testing.T) {
	f := newInstalledStorageFixture(t)
	t.Setenv(authServiceDatabaseURLEnvVar, "")
	registeredIdentity := filepath.Join(f.data, "Desktop", "credential-device.json")
	writeCredentialIdentityFixture(t, registeredIdentity, fixtureDesktopIdentity)
	registeredBefore := credentialRecoveryFileBytes(t, registeredIdentity)
	defer assertCredentialRecoveryFileUnchanged(t, registeredIdentity, registeredBefore)
	defer assertNoLegacyStorageIdentity(t, f.legacyRoot)
	ownerPath := filepath.Join(f.data, teststorage.OwnerMarker)
	owner := credentialRecoveryFileBytes(t, ownerPath)
	if err := os.Remove(ownerPath); err != nil {
		t.Fatal(err)
	}

	opts := f.options("local")
	opts.RuntimeRoot = t.TempDir()
	cfg, paths, err := newRuntimeConfigWithOptions(opts, true)
	if err != nil {
		t.Fatal(err)
	}
	writeStoredCredentialFixture(t, paths, "auth", false)
	writeStoredCredentialFixture(t, paths, "model", false)
	authBefore := credentialRecoveryFileBytes(t, paths.AuthServiceDBPath)
	coreBefore := credentialRecoveryFileBytes(t, paths.CoreDBPath)
	defer assertCredentialRecoveryFileUnchanged(t, paths.AuthServiceDBPath, authBefore)
	defer assertCredentialRecoveryFileUnchanged(t, paths.CoreDBPath, coreBefore)
	identityPath, err := checkedCredentialIdentityPath(paths)
	if err != nil || !pathIsUnderRoot(identityPath, opts.RuntimeRoot) {
		t.Errorf("preflight did not select a path under the independent Local root: %v", err)
	} else {
		assertCredentialRecoveryFileAbsent(t, identityPath)
	}

	cloud, model := credentialRecoveryServiceKeys(t, cfg, paths)
	if t.Failed() {
		return
	}
	if cloud == fixtureDesktopCloudKey || model == fixtureDesktopModelKey {
		t.Error("independent Local selected the Desktop identity")
	}
	identity, err := readCredentialDevice(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(identity.DeviceSecret, "="))
	if err != nil {
		t.Fatal(err)
	}
	for purpose, key := range map[string]string{"cloud-oauth": cloud, "model-provider": model} {
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write([]byte("lazymind/desktop/" + identity.DeviceID + "/" + purpose + "/v1"))
		if key != base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) {
			t.Errorf("%s key does not derive from the independent identity", purpose)
		}
	}
	independentBefore := credentialRecoveryFileBytes(t, identityPath)
	if err := os.WriteFile(ownerPath, owner, 0o600); err != nil {
		t.Fatal(err)
	}

	repairedCfg, repairedPaths, err := newRuntimeConfigWithOptions(opts, true)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := checkedCredentialIdentityPath(repairedPaths); err != nil || got != identityPath {
		t.Errorf("repair changed the independent Local identity path: %v", err)
	}
	repairedCloud, repairedModel := credentialRecoveryServiceKeys(t, repairedCfg, repairedPaths)
	if repairedCloud != cloud || repairedModel != model {
		t.Error("repair changed the independent Local service keys")
	}
	assertCredentialRecoveryFileUnchanged(t, identityPath, independentBefore)
}

func TestCredentialRecoveryHealthyRegisteredDataSharesDeviceKeys(t *testing.T) {
	f := newInstalledStorageFixture(t)
	t.Setenv(authServiceDatabaseURLEnvVar, "")
	identityPath := filepath.Join(f.data, "Desktop", "credential-device.json")
	writeCredentialIdentityFixture(t, identityPath, fixtureDesktopIdentity)
	before := credentialRecoveryFileBytes(t, identityPath)
	for _, mode := range []struct{ name, profile, root string }{
		{"desktop", "desktop", ""},
		{"default local", "local", ""},
		{"local with explicit registered root", "local", f.data},
	} {
		t.Run(mode.name, func(t *testing.T) {
			opts := f.options(mode.profile)
			opts.RuntimeRoot = mode.root
			cfg, paths, err := newRuntimeConfigWithOptions(opts, true)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := checkedCredentialIdentityPath(paths); err != nil || got != identityPath {
				t.Errorf("expected the registered identity: path %q, error %v", got, err)
			}
			cloud, model := credentialRecoveryServiceKeys(t, cfg, paths)
			if cloud != fixtureDesktopCloudKey || model != fixtureDesktopModelKey {
				t.Error("mode did not reuse both registered device keys")
			}
			t.Setenv(cloudCredentialKeyEnvVar, "fixture-cloud-override")
			t.Setenv(modelCredentialKeyEnvVar, "fixture-model-override")
			cloud, model = credentialRecoveryServiceKeys(t, cfg, paths)
			if cloud != "fixture-cloud-override" || model != "fixture-model-override" {
				t.Error("explicit credential keys lost precedence")
			}
		})
	}
	assertCredentialRecoveryFileUnchanged(t, identityPath, before)
	assertNoLegacyStorageIdentity(t, f.legacyRoot)
}

func TestCredentialRecoveryIndependentLocalProtectsActualDatabase(t *testing.T) {
	for _, tc := range []struct{ name, kind, errorText string }{
		{"Auth ciphertext in configured database", "auth", "encrypted credentials"},
		{"Core ciphertext", "model", "encrypted credentials"},
		{"unreadable configured Auth database", "corrupt", "inspect credentials"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newInstalledStorageFixture(t)
			if err := os.Remove(filepath.Join(f.data, teststorage.OwnerMarker)); err != nil {
				t.Fatal(err)
			}
			opts := f.options("local")
			opts.RuntimeRoot = t.TempDir()
			authDB := filepath.Join(opts.RuntimeRoot, "configured-auth.db")
			t.Setenv(authServiceDatabaseURLEnvVar, sqliteURL(authDB))
			cfg, paths, err := newRuntimeConfigWithOptions(opts, true)
			if err != nil || cfg.AuthService.DatabaseURL != sqliteURL(authDB) {
				t.Fatalf("test must start from the configured database URL: %v", err)
			}
			actualPaths := paths
			actualPaths.AuthServiceDBPath = authDB
			var dbPath string
			if tc.kind == "corrupt" {
				dbPath = authDB
				if err := os.WriteFile(dbPath, []byte("not a SQLite database"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				dbPath = writeStoredCredentialFixture(t, actualPaths, tc.kind, true).path
			}
			before := credentialRecoveryFileBytes(t, dbPath)
			independentFiles := credentialRecoveryTree(t, opts.RuntimeRoot)
			registeredFiles := credentialRecoveryTree(t, f.data)
			for service, prepare := range map[string]func(RuntimeConfig, RuntimePaths) ([]string, error){
				"auth": authServiceEnv, "core": coreServiceEnv,
			} {
				env, err := prepare(cfg, paths)
				if env != nil || err == nil || !strings.Contains(err.Error(), tc.errorText) {
					t.Errorf("%s did not reject for the actual database's credential state: %v", service, err)
				}
			}
			assertCredentialRecoveryFileUnchanged(t, dbPath, before)
			if !slices.Equal(credentialRecoveryTree(t, opts.RuntimeRoot), independentFiles) ||
				!slices.Equal(credentialRecoveryTree(t, f.data), registeredFiles) {
				t.Error("rejected credential recovery changed the files under the independent or registered root")
			}
			assertNoLegacyStorageIdentity(t, f.legacyRoot)
		})
	}
}

func TestCredentialRecoveryRejectsExplicitRegisteredRootWhenOwnershipFails(t *testing.T) {
	f := newInstalledStorageFixture(t)
	identityPath := filepath.Join(f.data, "Desktop", "credential-device.json")
	writeCredentialIdentityFixture(t, identityPath, fixtureDesktopIdentity)
	before := credentialRecoveryFileBytes(t, identityPath)
	if err := os.Remove(filepath.Join(f.data, teststorage.OwnerMarker)); err != nil {
		t.Fatal(err)
	}
	t.Setenv(cloudCredentialKeyEnvVar, "fixture-cloud-override")
	t.Setenv(modelCredentialKeyEnvVar, "fixture-model-override")
	opts := f.options("local")
	opts.RuntimeRoot = f.data
	if _, _, err := newRuntimeConfigWithOptions(opts, true); err == nil {
		t.Error("an explicit root and keys bypassed validation of the registered data location")
	}
	assertCredentialRecoveryFileUnchanged(t, identityPath, before)
	assertNoLegacyStorageIdentity(t, f.legacyRoot)
}

func TestCredentialRecoveryRejectsUnprovenOrOverlappingLocalRoot(t *testing.T) {
	for _, mode := range []string{"wrong type", "relative", "child of registered root", "parent of registered root"} {
		t.Run(mode, func(t *testing.T) {
			f := newInstalledStorageFixture(t)
			t.Setenv(authServiceDatabaseURLEnvVar, "")
			if _, _, err := newRuntimeConfigWithOptions(f.options("local"), true); err != nil {
				t.Fatalf("the private registered layout must be usable before injection: %v", err)
			}
			identityPath := filepath.Join(f.data, "Desktop", "credential-device.json")
			writeCredentialIdentityFixture(t, identityPath, fixtureDesktopIdentity)
			registeredIdentity := credentialRecoveryFileBytes(t, identityPath)
			opts := f.options("local")
			switch mode {
			case "wrong type", "relative":
				// A retained-data install has no App registration to corroborate DataRoot.
				f.registry.DeleteValue(teststorage.InstallKey, "InstallLocation")
				if mode == "wrong type" {
					f.registry.SetDWord(teststorage.DataKey, "DataRoot", 1)
				} else {
					f.registry.SetString(teststorage.DataKey, "DataRoot", `relative\Data`)
				}
				opts.RuntimeRoot = t.TempDir()
			default:
				if err := os.Remove(filepath.Join(f.data, teststorage.OwnerMarker)); err != nil {
					t.Fatal(err)
				}
				opts.RuntimeRoot = filepath.Dir(f.data)
				if mode == "child of registered root" {
					opts.RuntimeRoot = filepath.Join(f.data, "Local")
					if err := os.Mkdir(opts.RuntimeRoot, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
			beforeLocal := credentialRecoveryTree(t, opts.RuntimeRoot)
			beforeRegistered := credentialRecoveryTree(t, f.data)
			assertPathRejected := func(stage string, err error) {
				if err == nil {
					t.Errorf("%s accepted the unproven Local root", stage)
					return
				}
				diagnostic := strings.ToLower(err.Error())
				if !strings.Contains(diagnostic, "dataroot") && !strings.Contains(diagnostic, "registered") &&
					!strings.Contains(diagnostic, "registration") && !strings.Contains(diagnostic, "overlap") &&
					!strings.Contains(diagnostic, "data owner") {
					t.Errorf("%s rejected for a reason unrelated to storage ownership: %v", stage, err)
				}
			}
			cfg, paths, err := newRuntimeConfigWithOptions(opts, true)
			if err != nil {
				assertPathRejected("configuration", err)
			} else {
				_, err := checkedCredentialIdentityPath(paths)
				assertPathRejected("identity preflight", err)
				authEnv, authErr := authServiceEnv(cfg, paths)
				coreEnv, coreErr := coreServiceEnv(cfg, paths)
				assertPathRejected("Auth startup", authErr)
				assertPathRejected("Core startup", coreErr)
				if authEnv != nil || coreEnv != nil {
					t.Error("unproven Local root produced a service environment")
				}
			}
			if !slices.Equal(credentialRecoveryTree(t, opts.RuntimeRoot), beforeLocal) ||
				!slices.Equal(credentialRecoveryTree(t, f.data), beforeRegistered) {
				t.Error("unproven Local root changed the files under the selected or registered root")
			}
			assertCredentialRecoveryFileUnchanged(t, identityPath, registeredIdentity)
			assertNoLegacyStorageIdentity(t, f.legacyRoot)
		})
	}
}

func credentialRecoveryTree(t *testing.T, root string) []string {
	t.Helper()
	var names []string
	if err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		names = append(names, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return names
}

func credentialRecoveryServiceKeys(t *testing.T, cfg RuntimeConfig, paths RuntimePaths) (string, string) {
	t.Helper()
	authEnv, authErr := authServiceEnv(cfg, paths)
	coreEnv, coreErr := coreServiceEnv(cfg, paths)
	if authErr != nil || coreErr != nil {
		t.Errorf("could not prepare service keys: Auth %v, Core %v", authErr, coreErr)
		return "", ""
	}
	readKey := func(env []string, name string) string {
		for _, entry := range env {
			if value, ok := strings.CutPrefix(entry, name+"="); ok && value != "" {
				return value
			}
		}
		t.Errorf("service environment omitted %s", name)
		return ""
	}
	return readKey(authEnv, cloudCredentialKeyEnvVar), readKey(coreEnv, modelCredentialKeyEnvVar)
}
