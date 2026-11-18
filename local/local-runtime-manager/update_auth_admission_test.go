package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// These URLs use the same file under SQLAlchemy's pysqlite dialect. The
// database part is literal (not URL-unescaped); connection options are separate.
func TestRuntimeAuthSQLiteURLsRegisterAndBlockPendingUpdates(t *testing.T) {
	for _, form := range []string{"plain", "driver", "timeout", "driver-options", "literal-percent", "path-alias"} {
		for _, direction := range []string{"registered-scope", "pending-transaction"} {
			t.Run(form+"/"+direction, func(t *testing.T) {
				first, second := newUpdateStateFixture(t), newUpdateStateFixture(t)
				first.cfg.OwnerToken, second.cfg.OwnerToken = "first-owner", "second-owner"
				name := "auth.db"
				if form == "literal-percent" {
					name = "auth%20literal.db"
				}
				shared := filepath.Join(t.TempDir(), name)
				if err := os.WriteFile(shared, nil, 0600); err != nil {
					t.Fatal(err)
				}
				plain := "sqlite:///" + filepath.ToSlash(shared)
				address := plain
				switch form {
				case "driver":
					address = "sqlite+pysqlite:///" + filepath.ToSlash(shared)
				case "timeout":
					address += "?timeout=5"
				case "driver-options":
					address = "sqlite+pysqlite:///" + filepath.ToSlash(shared) + "?check_same_thread=false&timeout=5&cached_statements=128"
				case "path-alias":
					alias := filepath.Join(t.TempDir(), "shared")
					if err := os.Symlink(filepath.Dir(shared), alias); err != nil {
						if runtime.GOOS == "windows" {
							t.Skip("symbolic links unavailable for this Windows user")
						}
						t.Fatal(err)
					}
					address = "sqlite+pysqlite:///" + filepath.ToSlash(filepath.Join(alias, name)) + "?timeout=5"
				}
				second.cfg.AuthService.DatabaseURL = plain
				// The transaction side uses the independently known real file,
				// never the parser under test (notably for literal %20 names).
				second.cfg.UpdateStorage.Databases["auth"] = shared
				t.Setenv(authServiceDatabaseURLEnvVar, address)
				root := t.TempDir()
				if direction == "registered-scope" {
					out, err := admissionCommand(t, first, root, "coordinator").Output()
					if err != nil {
						t.Fatalf("supported source URL could not start: %s %v", out, err)
					}
					requireRegisteredDataRoot(t, root, shared)
				} else {
					writePendingAdmissionJournal(t, root, admissionPlan(t, second, root))
					out, err := admissionCommand(t, first, root, "coordinator").Output()
					var result struct {
						Error string `json:"error"`
					}
					_ = json.Unmarshal(out, &result)
					if err == nil || result.Error != "update_in_progress" {
						t.Fatalf("shared auth writer bypassed update: %s %v", out, err)
					}
				}
			})
		}
	}
}

func TestRuntimeUnknownAuthStorageFailsClosed(t *testing.T) {
	for _, address := range []string{
		"sqlite:///relative.db", "sqlite:///:memory:", "sqlite://",
		"sqlite+unknown:////tmp/auth.db", "unknown://fixture.invalid/db",
		"sqlite:///file:/tmp/auth.db?uri=true", "sqlite:////tmp/auth.db?uri=true",
		"sqlite:////tmp/auth.db?%75ri=true", "sqlite:////tmp/auth.db?timeout=5&uri=true",
		"sqlite:////tmp/auth.db?timeout=1&timeout=2", "sqlite:////tmp/auth.db?timeout=%ZZ",
	} {
		t.Run(address, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			t.Setenv(authServiceDatabaseURLEnvVar, address)
			out, err := admissionCommand(t, f, t.TempDir(), "coordinator").Output()
			var result struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(out, &result)
			if err == nil || result.Error != "scope_invalid" {
				t.Fatalf("unresolved local/unknown storage was allowed: %s %v", out, err)
			}
		})
	}
}

func TestRuntimeAuthScopeIncludesWAL(t *testing.T) {
	f := newUpdateStateFixture(t)
	for _, address := range []string{sqliteURL(f.paths.AuthServiceDBPath), "sqlite+pysqlite:///" + filepath.ToSlash(f.paths.AuthServiceDBPath) + "?timeout=5"} {
		f.cfg.AuthService.DatabaseURL = address
		f.cfg.UpdateStorage = captureUpdateStorage(f.paths, address)
		scope, err := runtimeUpdateScope(f.cfg, f.paths)
		if err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"", "-wal", "-shm"} {
			want := filepath.Clean(f.paths.AuthServiceDBPath) + suffix
			found := false
			for _, path := range scope.DataRoots {
				if path == want {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing auth file %s", suffix)
			}
		}
	}
}

func TestRuntimePostgreSQLAuthDriverCanStart(t *testing.T) {
	for _, address := range []string{"postgresql://fixture.invalid/db", "postgresql+psycopg://fixture.invalid/db", "postgresql+psycopg:///?host=/tmp/fixture-socket"} {
		t.Run(address, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			t.Setenv(authServiceDatabaseURLEnvVar, address)
			out, err := admissionCommand(t, f, t.TempDir(), "coordinator").Output()
			var result struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(out, &result)
			if err != nil || result.Status != "allowed" {
				t.Fatalf("PostgreSQL normal startup failed: %s %v", out, err)
			}
		})
	}
}

func TestDesktopUpdateStateAcceptsEquivalentAuthURLs(t *testing.T) {
	f := newUpdateStateFixture(t)
	before := f.measure(t).DataProfile
	for _, address := range []string{sqliteURL(f.paths.AuthServiceDBPath) + "?timeout=5", "sqlite+pysqlite:///" + filepath.ToSlash(f.paths.AuthServiceDBPath)} {
		f.cfg.AuthService.DatabaseURL = address
		f.cfg.UpdateStorage = captureUpdateStorage(f.paths, address)
		state := defaultRuntimeState(f.cfg, f.cfg.ProcessComposePort, f.paths.RunDirTokenFile)
		state.OverallStatus = "ready"
		if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
			t.Fatal(err)
		}
		if after := f.measure(t).DataProfile; after != before {
			t.Fatal("equivalent auth URL changed data profile")
		}
	}
}
