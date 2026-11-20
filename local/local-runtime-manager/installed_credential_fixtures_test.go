package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// These synthetic vectors were generated independently with Node AES-256-GCM
// using fixtureDesktopIdentity's established cloud/model key vectors.
// Auth packs nonce || ciphertext || tag (auth-service/core/cloud_crypto.py).
// Core's supported V1 BYO wrapper and key hashing are specified by
// backend/core/common/secretcrypto/secretcrypto.go and
// backend/core/modelprovider/credential_legacy.go.
const storedCloudCredentialVector = "AAECAwQFBgcICQoLJcPsehbyxIsJJrmaXfAuWettCy0Ge4LIjDdqmYNaqPQDvAiWykJPmuDtz9kCbCuIF_RXs8S7F5sEg30W1SveJCXcrlkcEmpzgbn2jdPAeLc="
const storedModelCredentialVector = `{"enc":"aes-gcm","nonce":"DA0ODxAREhMUFRYX","v":"yme5pEoESPxKA4Hp+hJfoyF7EeII2Gdw82LtHPJrjAvq"}`

type storedCredentialFixture struct {
	path, query, ciphertext string
	hasRow                  bool
}

func writeStoredCredentialFixture(t *testing.T, paths RuntimePaths, kind string, withRow bool) storedCredentialFixture {
	t.Helper()
	var ddl, insert string
	f := storedCredentialFixture{hasRow: withRow}
	switch kind {
	case "auth":
		// Complete MCPOAuthGrant fields and owner/server uniqueness from
		// backend/auth-service/models/mcp_oauth.py. services/mcp_oauth.py's
		// token() decrypts this ciphertext to obtain the access token.
		f.path, f.ciphertext = paths.AuthServiceDBPath, storedCloudCredentialVector
		f.query = "SELECT ciphertext FROM mcp_oauth_grants WHERE grant_id = 'fixture-grant'"
		ddl = `CREATE TABLE mcp_oauth_grants (
			grant_id VARCHAR(64) PRIMARY KEY, user_id VARCHAR(64) NOT NULL,
			server_id VARCHAR(128) NOT NULL, server_url TEXT NOT NULL,
			grant_version INTEGER NOT NULL, token_version INTEGER NOT NULL,
			status VARCHAR(32) NOT NULL, ciphertext TEXT NOT NULL,
			expires_at FLOAT NOT NULL, lease_id VARCHAR(64) NOT NULL,
			lease_until FLOAT NOT NULL, UNIQUE (user_id, server_id))`
		insert = `INSERT INTO mcp_oauth_grants VALUES
			('fixture-grant','fixture-user','fixture-server','https://example.invalid/mcp',
			1,1,'authorized',?,4102444800,'',0)`
	case "model":
		// Current UserModelProviderGroup + BaseModel fields from
		// backend/core/common/orm/{model_provider,base}.go. V1 BYO remains
		// supported and uses the device-derived model key, not the OS V2 vault.
		f.path, f.ciphertext = paths.CoreDBPath, storedModelCredentialVector
		f.query = "SELECT api_key_ciphertext FROM user_model_provider_groups WHERE id = 'fixture-group'"
		ddl = `CREATE TABLE user_model_provider_groups (
			id VARCHAR(64) PRIMARY KEY, user_model_provider_id VARCHAR(64) NOT NULL,
			name VARCHAR(255) NOT NULL, base_url VARCHAR(1024) NOT NULL,
			api_key TEXT NOT NULL, api_key_ciphertext TEXT NOT NULL DEFAULT '',
			credential_version INTEGER NOT NULL DEFAULT 0,
			credential_revision INTEGER NOT NULL DEFAULT 0 CHECK (credential_revision >= 0),
			is_verified BOOLEAN NOT NULL DEFAULT false,
			create_user_id VARCHAR(255) NOT NULL, create_user_name VARCHAR(255) NOT NULL,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, deleted_at DATETIME)`
		insert = `INSERT INTO user_model_provider_groups
			(id,user_model_provider_id,name,base_url,api_key,api_key_ciphertext,
			credential_version,credential_revision,is_verified,create_user_id,
			create_user_name,created_at,updated_at)
			VALUES ('fixture-group','fixture-provider','fixture','https://example.invalid/v1',
			'',?,1,1,true,'fixture-user','Fixture',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`
	default:
		t.Fatalf("unknown credential fixture %q", kind)
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(f.path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	if withRow {
		if _, err := db.Exec(insert, f.ciphertext); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopen before returning: guards will see a durable, closed SQLite file.
	f.assertUnchanged(t)
	return f
}

func (f storedCredentialFixture) assertUnchanged(t *testing.T) {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(f.path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got string
	err = db.QueryRow(f.query).Scan(&got)
	if !f.hasRow {
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("empty initialized credential table returned a record: %v", err)
		}
		return
	}
	if err != nil || got != f.ciphertext {
		t.Fatalf("persistent credential was changed or lost: %v", err)
	}
}

func TestInstalledCredentialFixturesHaveRealPersistentCiphertext(t *testing.T) {
	for _, kind := range []string{"auth", "model"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			paths := RuntimePaths{
				AuthServiceDBPath: filepath.Join(root, "auth", "authservice.db"),
				CoreDBPath:        filepath.Join(root, "core", "core.db"),
			}
			f := writeStoredCredentialFixture(t, paths, kind, true)
			var key, nonce, ciphertext []byte
			var want string
			var err error
			if kind == "auth" {
				key, err = base64.RawURLEncoding.DecodeString(fixtureDesktopCloudKey)
				if err != nil {
					t.Fatal(err)
				}
				packed, err := base64.URLEncoding.DecodeString(f.ciphertext)
				if err != nil {
					t.Fatal(err)
				}
				nonce, ciphertext = packed[:12], packed[12:]
				want = `{"access_token":"synthetic","refresh_token":"synthetic-refresh"}`
			} else {
				var wrapper struct{ Enc, Nonce, V string }
				if err := json.Unmarshal([]byte(f.ciphertext), &wrapper); err != nil || wrapper.Enc != "aes-gcm" {
					t.Fatalf("invalid Core V1 wrapper: %v", err)
				}
				nonce, err = base64.StdEncoding.DecodeString(wrapper.Nonce)
				if err != nil {
					t.Fatal(err)
				}
				ciphertext, err = base64.StdEncoding.DecodeString(wrapper.V)
				if err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256([]byte("l_JSbmfusw38fVGvRPOcm_rKOkAV4OvMSNjNgcvPK38"))
				key, want = sum[:], "synthetic-api-key"
			}
			block, err := aes.NewCipher(key)
			if err != nil {
				t.Fatal(err)
			}
			aead, err := cipher.NewGCM(block)
			if err != nil {
				t.Fatal(err)
			}
			got, err := aead.Open(nil, nonce, ciphertext, nil)
			if err != nil || string(got) != want {
				t.Fatalf("stored fixture cannot be decrypted with the original identity: %v", err)
			}
		})
	}
}
