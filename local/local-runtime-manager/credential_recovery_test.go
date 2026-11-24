package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialRecoveryPreflightDistinguishesCloudReferencesFromCiphertext(t *testing.T) {
	const managed = "managed-reference-v1"
	const cli = "cli-profile-reference-v1"
	for _, tc := range []struct {
		name, method, location string
		credential, state      string
		mcpCiphertext          string
		withSecondEncryptedRow bool
		wantProtected          bool
	}{
		{name: "managed reference", method: "managed_oauth", location: "cloud", credential: managed, state: managed},
		{name: "CLI reference", method: "cli_personal_app", location: "local", credential: cli, state: cli},
		{name: "reference and encrypted state", method: "managed_oauth", location: "cloud", credential: managed, state: storedCloudCredentialVector, wantProtected: true},
		{name: "encrypted credential and reference", method: "cli_personal_app", location: "local", credential: storedCloudCredentialVector, state: cli, wantProtected: true},
		{name: "reference row followed by encrypted row", method: "managed_oauth", location: "cloud", credential: managed, state: managed, withSecondEncryptedRow: true, wantProtected: true},
		{name: "unknown reference", method: "managed_oauth", location: "cloud", credential: "managed-reference-v2", state: managed, wantProtected: true},
		{name: "reference outside cloud columns", method: "managed_oauth", location: "cloud", credential: managed, state: managed, mcpCiphertext: managed, wantProtected: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identityPath := fixtureCredentialDevicePath(t)
			root := t.TempDir()
			t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg-data"))
			t.Setenv(localSQLiteDirEnvVar, "")
			t.Setenv(authServiceDatabaseURLEnvVar, "")
			path := filepath.Join(root, "data", "stores", "sqlite", "auth", "authservice.db")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", sqliteDSN(path))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`CREATE TABLE cloud_auth_connections (
				connection_method TEXT NOT NULL, credential_location TEXT NOT NULL,
				credential_ciphertext TEXT NOT NULL, auth_state_ciphertext TEXT NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO cloud_auth_connections VALUES (?, ?, ?, ?)`, tc.method, tc.location, tc.credential, tc.state); err != nil {
				t.Fatal(err)
			}
			if tc.withSecondEncryptedRow {
				if _, err := db.Exec(`INSERT INTO cloud_auth_connections VALUES ('legacy_byo', 'local', ?, ?)`, storedCloudCredentialVector, storedCloudCredentialVector); err != nil {
					t.Fatal(err)
				}
			}
			if tc.mcpCiphertext != "" {
				if _, err := db.Exec(`CREATE TABLE mcp_oauth_grants (ciphertext TEXT NOT NULL)`); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`INSERT INTO mcp_oauth_grants VALUES (?)`, tc.mcpCiphertext); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before := credentialRecoveryFileBytes(t, path)
			got, err := checkedCredentialIdentityPath(RuntimePaths{RuntimeRoot: root, AuthServiceDBPath: path})
			if tc.wantProtected {
				if err == nil || !strings.Contains(err.Error(), "encrypted credentials") {
					t.Errorf("preflight did not protect a device credential: %v", err)
				}
			} else if err != nil || got != identityPath {
				t.Errorf("preflight blocked reference-only storage: path %q, error %v", got, err)
			}
			assertCredentialRecoveryFileUnchanged(t, path, before)
			assertCredentialRecoveryFileAbsent(t, identityPath)
		})
	}
}

func credentialRecoveryFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertCredentialRecoveryFileUnchanged(t *testing.T, path string, before []byte) {
	t.Helper()
	if after := credentialRecoveryFileBytes(t, path); !bytes.Equal(before, after) {
		t.Errorf("file changed during credential recovery: %s", path)
	}
}

func assertCredentialRecoveryFileAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("expected %q to remain absent: %v", path, err)
	}
}
