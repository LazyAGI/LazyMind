package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
	"lazymind/core/common/secretcrypto"
)

// Synthetic credentials only; targetKey follows Electron's existing HMAC derivation.
const sourceKey = "acceptance-source-key-fixture"
const targetKey = "l_JSbmfusw38fVGvRPOcm_rKOkAV4OvMSNjNgcvPK38"
const providerSecret = "synthetic-provider-credential-do-not-log"
const identityJSON = `{"version":1,"deviceId":"11111111-2222-4333-8444-555555555555","deviceSecret":"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"}`

type fixture struct {
	options recoveryOptions
	dbPath  string
	before  []modelRow
	config  string
}

type modelRow struct {
	ID, Owner, Name, URL, Plaintext, Ciphertext, Deleted string
	Version, Verified                                    int
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"profile", "runtime/data/stores/sqlite/core", "runtime/state", "runtime/run", "repo/local"} {
		must(t, os.MkdirAll(filepath.Join(root, dir), 0700))
	}
	write(t, filepath.Join(root, "profile/credential-device.json"), identityJSON)
	write(t, filepath.Join(root, "runtime/state/runtime-state.json"), `{"profile":"desktop","overallStatus":"stopped","services":{}}`)
	f := fixture{
		options: recoveryOptions{AcceptanceRoot: root, LocalConfigPath: filepath.Join(root, "repo/local/config.env"), SourceKey: sourceKey},
		dbPath:  filepath.Join(root, "runtime/data/stores/sqlite/core/core.db"),
		config:  "# preserve existing local settings\nLAZYMIND_RUNTIME_ROOT=" + filepath.Join(root, "runtime") + "\nUNRELATED_SETTING=keep\n",
	}
	write(t, f.options.LocalConfigPath, f.config)
	db := openDB(t, f.dbPath)
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE user_model_provider_groups (
 id TEXT PRIMARY KEY, create_user_id TEXT NOT NULL, name TEXT NOT NULL, base_url TEXT NOT NULL,
 api_key TEXT NOT NULL DEFAULT '', api_key_ciphertext TEXT NOT NULL DEFAULT '',
 credential_version INTEGER NOT NULL DEFAULT 0, is_verified INTEGER NOT NULL DEFAULT 0, deleted_at TEXT
);
CREATE TABLE preservation_marker (value TEXT NOT NULL);
INSERT INTO preservation_marker VALUES ('task, selection and notification data must stay unchanged');`)
	must(t, err)
	for _, row := range []struct{ id, key, deleted string }{
		{"a", sourceKey, ""}, {"b", sourceKey, "2026-09-01"}, {"current", targetKey, ""}, {"keyless", "", ""},
	} {
		ciphertext := ""
		if row.key != "" {
			ciphertext = encrypt(t, providerSecret, row.key)
		}
		_, err = db.Exec(`INSERT INTO user_model_provider_groups
 (id,create_user_id,name,base_url,api_key_ciphertext,credential_version,is_verified,deleted_at)
 VALUES (?,?,?,?,?,1,1,NULLIF(?,''))`, row.id, "owner-"+row.id, "connection-"+row.id, "https://example.test", ciphertext, row.deleted)
		must(t, err)
	}
	f.before = readRows(t, f.dbPath)
	return f
}

func TestRecoveryReproducesLocalDesktopMismatch(t *testing.T) {
	raw := encrypt(t, providerSecret, sourceKey)
	plain, ok, err := secretcrypto.DecodeAESGCM(json.RawMessage(raw), sourceKey)
	if err != nil || !ok || string(plain) != providerSecret {
		t.Fatal("source credential fixture is invalid")
	}
	if _, _, err := secretcrypto.DecodeAESGCM(json.RawMessage(raw), targetKey); err == nil {
		t.Fatal("different desktop identity unexpectedly decrypted the local credential")
	}
}

func TestRecoveryDryRunDoesNotWrite(t *testing.T) {
	f := newFixture(t)
	report, err := invoke(t, f.options)
	must(t, err)
	if report.Reencrypted != 2 || report.AlreadyCurrent != 1 || report.BackupDir != "" {
		t.Fatal("dry run must report source and current counts without creating a backup")
	}
	assertUnchanged(t, f)
}

func TestRecoveryApplyPreservesDataAndBacksUp(t *testing.T) {
	f := newFixture(t)
	f.options.Apply = true
	report, err := invoke(t, f.options)
	must(t, err)
	if report.Reencrypted != 2 || report.AlreadyCurrent != 1 || report.BackupDir == "" {
		t.Fatal("recovery must rotate only source ciphertext and record its backup")
	}
	after := readRows(t, f.dbPath)
	for i, row := range after {
		before := f.before[i]
		if row.Ciphertext != "" {
			plain, ok, err := secretcrypto.DecodeAESGCM(json.RawMessage(row.Ciphertext), targetKey)
			if err != nil || !ok || string(plain) != providerSecret || strings.Contains(row.Ciphertext, providerSecret) {
				t.Fatal("destination must contain usable encrypted credentials only")
			}
		}
		if row.ID == "current" && row.Ciphertext != before.Ciphertext {
			t.Fatal("already-current ciphertext must remain byte-for-byte unchanged")
		}
		row.Ciphertext = before.Ciphertext
		if row != before {
			t.Fatal("recovery changed connection metadata or plaintext")
		}
	}
	config := read(t, f.options.LocalConfigPath)
	if !strings.Contains(config, f.config) || strings.Count(config, "LAZYMIND_MODEL_PROVIDER_SECRET_KEY=") != 1 || !strings.Contains(config, "LAZYMIND_MODEL_PROVIDER_SECRET_KEY="+targetKey+"\n") {
		t.Fatal("Local must use the desktop key without losing unrelated settings")
	}
	if !reflect.DeepEqual(readRows(t, filepath.Join(report.BackupDir, "core.db")), f.before) || read(t, filepath.Join(report.BackupDir, "config.env")) != f.config {
		t.Fatal("backup must preserve original credentials and configuration")
	}
	if read(t, filepath.Join(report.BackupDir, "credential-device.json")) != identityJSON {
		t.Fatal("backup must retain the existing desktop identity")
	}
	for _, path := range []string{f.options.LocalConfigPath, report.BackupDir, filepath.Join(report.BackupDir, "core.db"), filepath.Join(report.BackupDir, "config.env"), filepath.Join(report.BackupDir, "credential-device.json")} {
		info, err := os.Stat(path)
		must(t, err)
		if info.Mode().Perm()&0077 != 0 {
			t.Fatal("credential configuration and backups must be owner-only")
		}
	}
	assertMarker(t, f.dbPath)
	if read(t, filepath.Join(f.options.AcceptanceRoot, "profile/credential-device.json")) != identityJSON {
		t.Fatal("recovery must not replace the device identity")
	}
}

func TestRecoveryRepeatIsIdempotent(t *testing.T) {
	f := newFixture(t)
	f.options.Apply = true
	_, err := invoke(t, f.options)
	must(t, err)
	f.before = readRows(t, f.dbPath)
	f.config = read(t, f.options.LocalConfigPath)
	report, err := invoke(t, f.options)
	must(t, err)
	if report.Reencrypted != 0 || report.AlreadyCurrent != 3 {
		t.Fatal("a repeated recovery must not rotate credentials again")
	}
	assertUnchanged(t, f)
}

func TestRecoveryResumesWhenConfigAlreadyUsesDesktopKey(t *testing.T) {
	f := newFixture(t)
	// A crash after safely replacing config but before DB commit must be recoverable.
	write(t, f.options.LocalConfigPath, f.config+"LAZYMIND_MODEL_PROVIDER_SECRET_KEY="+targetKey+"\n")
	f.options.Apply = true
	report, err := invoke(t, f.options)
	must(t, err)
	if report.Reencrypted != 2 || report.AlreadyCurrent != 1 {
		t.Fatal("interrupted recovery did not resume")
	}
	for _, row := range readRows(t, f.dbPath) {
		if row.Ciphertext == "" {
			continue
		}
		plain, ok, err := secretcrypto.DecodeAESGCM(json.RawMessage(row.Ciphertext), targetKey)
		if err != nil || !ok || string(plain) != providerSecret {
			t.Fatal("resumed recovery left unreadable credentials")
		}
	}
}

func TestRecoveryRepairsConfigAfterCredentialsAlreadyConverted(t *testing.T) {
	f := newFixture(t)
	for _, id := range []string{"a", "b"} {
		db := openDB(t, f.dbPath)
		_, err := db.Exec("UPDATE user_model_provider_groups SET api_key_ciphertext=? WHERE id=?", encrypt(t, providerSecret, targetKey), id)
		must(t, err)
		must(t, db.Close())
	}
	f.before = readRows(t, f.dbPath)
	f.options.Apply = true
	report, err := invoke(t, f.options)
	must(t, err)
	if report.Reencrypted != 0 || report.AlreadyCurrent != 3 || !reflect.DeepEqual(readRows(t, f.dbPath), f.before) {
		t.Fatal("config repair must not re-encrypt already converted credentials")
	}
	if !strings.Contains(read(t, f.options.LocalConfigPath), "LAZYMIND_MODEL_PROVIDER_SECRET_KEY="+targetKey+"\n") {
		t.Fatal("config was not repaired")
	}
}

func TestRecoveryReplacesExistingSourceConfigKey(t *testing.T) {
	f := newFixture(t)
	write(t, f.options.LocalConfigPath, f.config+"LAZYMIND_MODEL_PROVIDER_SECRET_KEY="+sourceKey+"\n")
	f.options.Apply = true
	_, err := invoke(t, f.options)
	must(t, err)
	config := read(t, f.options.LocalConfigPath)
	if !strings.Contains(config, f.config) || strings.Count(config, "LAZYMIND_MODEL_PROVIDER_SECRET_KEY=") != 1 || strings.Contains(config, sourceKey) || !strings.Contains(config, targetKey) {
		t.Fatal("existing source key was not replaced safely")
	}
}

func TestRecoveryFilesystemFailureDoesNotChangeCredentials(t *testing.T) {
	for _, which := range []string{"backup", "config"} {
		t.Run(which, func(t *testing.T) {
			f := newFixture(t)
			path := filepath.Join(f.options.AcceptanceRoot, "credential-recovery-backups")
			if which == "config" {
				path = filepath.Dir(f.options.LocalConfigPath)
			}
			must(t, os.MkdirAll(path, 0700))
			must(t, os.Chmod(path, 0500))
			t.Cleanup(func() { _ = os.Chmod(path, 0700) })
			f.options.Apply = true
			report, err := invoke(t, f.options)
			if err == nil || report.Reencrypted != 0 {
				t.Fatal("unwritable backup/config must not allow credential mutation")
			}
			assertSafeError(t, err)
			assertUnchanged(t, f)
		})
	}
}

func TestRecoveryCLIDefaultIsReadOnlyAndOutputIsSafe(t *testing.T) {
	f := newFixture(t)
	t.Setenv("LAZYMIND_RECOVERY_SOURCE_KEY", sourceKey)
	var out, errOut bytes.Buffer
	code := run([]string{"--acceptance-root", f.options.AcceptanceRoot, "--local-config", f.options.LocalConfigPath}, &out, &errOut)
	if code != 0 || !json.Valid(out.Bytes()) {
		t.Fatal("CLI must default to a successful read-only JSON report")
	}
	assertSafeError(t, errors.New(out.String()+errOut.String()))
	assertUnchanged(t, f)
}

func TestRecoveryCLIApplyAndErrorOutput(t *testing.T) {
	for _, wrongKey := range []bool{false, true} {
		t.Run(map[bool]string{false: "apply", true: "reject"}[wrongKey], func(t *testing.T) {
			f := newFixture(t)
			key := sourceKey
			if wrongKey {
				key = "wrong-key-fixture"
			}
			t.Setenv("LAZYMIND_RECOVERY_SOURCE_KEY", key)
			var out, errOut bytes.Buffer
			code := run([]string{"--acceptance-root", f.options.AcceptanceRoot, "--local-config", f.options.LocalConfigPath, "--apply"}, &out, &errOut)
			assertSafeError(t, errors.New(out.String()+errOut.String()))
			if wrongKey {
				if code == 0 || errOut.Len() == 0 {
					t.Fatal("CLI must report a safe failure")
				}
				assertUnchanged(t, f)
			} else {
				if code != 0 || !json.Valid(out.Bytes()) || !strings.Contains(read(t, f.options.LocalConfigPath), targetKey) {
					t.Fatal("explicit apply must complete recovery and emit a JSON report")
				}
			}
		})
	}
}

func TestRecoveryRejectsUnsafeInputWithoutWrites(t *testing.T) {
	cases := map[string]func(*testing.T, *fixture){
		"wrong source key": func(t *testing.T, f *fixture) { f.options.SourceKey = "wrong-key-fixture" },
		"empty source key": func(t *testing.T, f *fixture) { f.options.SourceKey = " " },
		"missing identity": func(t *testing.T, f *fixture) {
			must(t, os.Remove(filepath.Join(f.options.AcceptanceRoot, "profile/credential-device.json")))
		},
		"invalid identity": func(t *testing.T, f *fixture) {
			write(t, filepath.Join(f.options.AcceptanceRoot, "profile/credential-device.json"), `{"version":1,"deviceSecret":"bad"}`)
		},
		"running runtime": func(t *testing.T, f *fixture) {
			write(t, filepath.Join(f.options.AcceptanceRoot, "runtime/state/runtime-state.json"), `{"overallStatus":"ready","services":{"core":{"status":"running"}}}`)
		},
		"unknown runtime state": func(t *testing.T, f *fixture) {
			write(t, filepath.Join(f.options.AcceptanceRoot, "runtime/state/runtime-state.json"), `{}`)
		},
		"live process": func(t *testing.T, f *fixture) {
			raw, err := json.Marshal(map[string]any{"version": 1, "processes": []map[string]any{{"pid": os.Getpid(), "service": "core"}}})
			must(t, err)
			write(t, filepath.Join(f.options.AcceptanceRoot, "runtime/run/processes.json"), string(raw))
		},
		"unknown ciphertext": func(t *testing.T, f *fixture) {
			changeCiphertext(t, f.dbPath, encrypt(t, providerSecret, "another-key"))
		},
		"damaged ciphertext": func(t *testing.T, f *fixture) {
			changeCiphertext(t, f.dbPath, `{"enc":"aes-gcm","nonce":"bad","v":"bad"}`)
		},
		"unexpected local key": func(t *testing.T, f *fixture) {
			write(t, f.options.LocalConfigPath, f.config+"LAZYMIND_MODEL_PROVIDER_SECRET_KEY=unrelated-key\n")
		},
		"config symlink": func(t *testing.T, f *fixture) {
			other := filepath.Join(t.TempDir(), "other.env")
			write(t, other, f.config)
			must(t, os.Remove(f.options.LocalConfigPath))
			must(t, os.Symlink(other, f.options.LocalConfigPath))
		},
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			prepare(t, &f)
			f.before = readRows(t, f.dbPath)
			f.config = read(t, f.options.LocalConfigPath)
			f.options.Apply = true
			report, err := invoke(t, f.options)
			if err == nil || report.Reencrypted != 0 {
				t.Fatal("unsafe recovery must fail before changing any credential")
			}
			assertSafeError(t, err)
			assertUnchanged(t, f)
		})
	}
}

func TestRecoveryWriteFailureRollsBackAllRowsAndConfig(t *testing.T) {
	f := newFixture(t)
	db := openDB(t, f.dbPath)
	_, err := db.Exec(`CREATE TRIGGER reject_second_credential BEFORE UPDATE ON user_model_provider_groups
 WHEN NEW.id='b' BEGIN SELECT RAISE(ABORT,'synthetic-provider-credential-do-not-log'); END`)
	must(t, err)
	must(t, db.Close())
	f.options.Apply = true
	report, err := invoke(t, f.options)
	if err == nil || report.Reencrypted != 0 {
		t.Fatal("partial write must not be reported as success")
	}
	assertSafeError(t, err)
	assertUnchanged(t, f)
}

func TestRecoveryBackupIncludesCommittedWALData(t *testing.T) {
	f := newFixture(t)
	db := openDB(t, f.dbPath)
	defer db.Close()
	_, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; INSERT INTO preservation_marker VALUES ('committed in WAL')")
	must(t, err)
	f.options.Apply = true
	report, err := invoke(t, f.options)
	must(t, err)
	backup := openDB(t, filepath.Join(report.BackupDir, "core.db"))
	defer backup.Close()
	var count int
	must(t, backup.QueryRow("SELECT count(*) FROM preservation_marker WHERE value='committed in WAL'").Scan(&count))
	if count != 1 {
		t.Fatal("backup lost committed WAL data")
	}
}

func invoke(t *testing.T, options recoveryOptions) (recoveryReport, error) {
	t.Helper()
	report, err := recoverCredentials(options)
	if errors.Is(err, errors.ErrUnsupported) {
		t.Fatal("expected stage-one failure: recovery behavior is not implemented")
	}
	return report, err
}

func assertUnchanged(t *testing.T, f fixture) {
	t.Helper()
	if !reflect.DeepEqual(readRows(t, f.dbPath), f.before) || read(t, f.options.LocalConfigPath) != f.config {
		t.Fatal("recovery changed data or configuration on a read-only/failed operation")
	}
	assertMarker(t, f.dbPath)
}

func assertSafeError(t *testing.T, err error) {
	t.Helper()
	for _, secret := range []string{sourceKey, targetKey, providerSecret, "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("error exposed a credential")
		}
	}
}

func assertMarker(t *testing.T, path string) {
	t.Helper()
	db := openDB(t, path)
	defer db.Close()
	var marker string
	must(t, db.QueryRow("SELECT value FROM preservation_marker").Scan(&marker))
	if marker != "task, selection and notification data must stay unchanged" {
		t.Fatal("unrelated data changed")
	}
}

func changeCiphertext(t *testing.T, path, value string) {
	t.Helper()
	db := openDB(t, path)
	defer db.Close()
	_, err := db.Exec("UPDATE user_model_provider_groups SET api_key_ciphertext=? WHERE id='b'", value)
	must(t, err)
}

func readRows(t *testing.T, path string) []modelRow {
	t.Helper()
	db := openDB(t, path)
	defer db.Close()
	rows, err := db.Query(`SELECT id,create_user_id,name,base_url,api_key,api_key_ciphertext,COALESCE(deleted_at,''),credential_version,is_verified FROM user_model_provider_groups ORDER BY id`)
	must(t, err)
	defer rows.Close()
	var result []modelRow
	for rows.Next() {
		var row modelRow
		must(t, rows.Scan(&row.ID, &row.Owner, &row.Name, &row.URL, &row.Plaintext, &row.Ciphertext, &row.Deleted, &row.Version, &row.Verified))
		result = append(result, row)
	}
	must(t, rows.Err())
	return result
}

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	must(t, err)
	return db
}

func encrypt(t *testing.T, value, key string) string {
	t.Helper()
	raw, err := secretcrypto.EncodeAESGCM([]byte(value), key)
	must(t, err)
	return string(raw)
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	must(t, err)
	return string(raw)
}

func write(t *testing.T, path, value string) {
	t.Helper()
	must(t, os.WriteFile(path, []byte(value), 0600))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal("fixture operation failed")
	}
}
