package main

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDesktopResetUsesManifestPythonForBothScopes(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("Python is required for the isolated desktop reset regression")
	}
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	for _, scope := range []ResetScope{ResetScopeKB, ResetScopeAll} {
		t.Run(string(scope), func(t *testing.T) {
			repo, resources, data := t.TempDir(), t.TempDir(), t.TempDir()
			writeComposeFixture(t, repo)
			manifest := []byte(`{"version":1,"profile":"desktop","paths":{"pythonRuntime":"python/runtime","algorithmVenv":"python/algorithm"}}`)
			manifestPath := filepath.Join(resources, runtimeManifestFileName)
			if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, paths, err := NewRuntimeConfigWithOptions(RuntimeConfigOptions{
				Profile: "desktop", OwnerToken: "desktop-reset-test", RepoRoot: repo,
				ResourcesRoot: resources, RuntimeRoot: data,
			})
			if err != nil {
				t.Fatal(err)
			}
			if paths.AlgorithmVenv != filepath.Join(resources, "python", "algorithm") {
				t.Fatalf("manifest interpreter was not selected: %s", paths.AlgorithmVenv)
			}
			// A synthetic packaged venv uses the host Python; no dependency install.
			if out, err := exec.Command(python, "-m", "venv", "--without-pip", paths.AlgorithmVenv).CombinedOutput(); err != nil {
				t.Fatalf("create test packaged Python: %v (%s)", err, out)
			}
			if err := paths.EnsureAllDirs(); err != nil {
				t.Fatal(err)
			}
			execSQL := func(path, statement string) {
				t.Helper()
				db, err := sql.Open("sqlite", sqliteDSN(path))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err := db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			execSQL(paths.CoreDBPath, `CREATE TABLE documents(id INTEGER); INSERT INTO documents VALUES(1);
CREATE TABLE conversations(id INTEGER, search_config TEXT, body TEXT);
INSERT INTO conversations VALUES(1, '{"dataset_ids":["kb"]}', 'keep conversation');
CREATE TABLE models(id INTEGER); INSERT INTO models VALUES(1);`)
			execSQL(paths.LazyLLMDBPath, `CREATE TABLE lazyllm_documents(id INTEGER); INSERT INTO lazyllm_documents VALUES(1);
CREATE TABLE unrelated(id INTEGER); INSERT INTO unrelated VALUES(1);`)
			marker := filepath.Join(paths.UploadRoot, "uploaded-document")
			if err := os.WriteFile(marker, []byte("test upload"), 0o644); err != nil {
				t.Fatal(err)
			}
			runner := &fakeRunner{t: t}
			if runtime.GOOS != "windows" {
				runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
					if cmd.Name != paths.LocalProxyStopScript {
						t.Fatalf("unexpected shutdown command: %s", cmd.Name)
					}
					return CommandResult{}, nil
				})
			}
			runner.handlers = append(runner.handlers,
				func(cmd Command) (CommandResult, error) {
					if cmd.Name != paths.AlgorithmPython {
						t.Fatalf("desktop reset must use manifest Python without uv: %s", cmd.Name)
					}
					return (&ExecRunner{}).Run(context.Background(), cmd)
				},
			)
			manager := NewRuntimeManager(runner, filepath.Join(resources, "bin", "manager"))
			// No real service is started or stopped by this isolated fixture.
			manager.probeAPI = func(int, time.Duration) bool { return false }
			manager.processScanner = func(RuntimePaths) ([]LocalProcessRecord, error) { return nil, nil }
			if err := manager.Reset(context.Background(), cfg, paths, scope); err != nil {
				t.Fatalf("desktop %s reset failed: %v", scope, err)
			}
			wantCalls := 1
			if runtime.GOOS != "windows" {
				wantCalls++
			}
			runner.assertCommandCount(wantCalls)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("KB upload was not removed: %v", err)
			}
			if raw, err := os.ReadFile(manifestPath); err != nil || string(raw) != string(manifest) {
				t.Fatalf("packaged manifest was changed: %v", err)
			}
			if _, err := os.Stat(paths.AlgorithmPython); err != nil {
				t.Fatalf("packaged Python was removed: %v", err)
			}
			if scope == ResetScopeAll {
				for _, path := range localAllResetPaths(paths) {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("all reset kept runtime path %s: %v", path, err)
					}
				}
				return
			}
			query := func(path, statement string, dest ...any) {
				t.Helper()
				db, err := sql.Open("sqlite", sqliteDSN(path))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if err := db.QueryRow(statement).Scan(dest...); err != nil {
					t.Fatal(err)
				}
			}
			for _, tc := range []struct {
				path, statement string
				want            int
			}{
				{paths.CoreDBPath, "SELECT COUNT(*) FROM documents", 0},
				{paths.CoreDBPath, "SELECT COUNT(*) FROM models", 1},
				{paths.LazyLLMDBPath, "SELECT COUNT(*) FROM sqlite_master WHERE name='lazyllm_documents'", 0},
				{paths.LazyLLMDBPath, "SELECT COUNT(*) FROM unrelated", 1},
			} {
				var got int
				query(tc.path, tc.statement, &got)
				if got != tc.want {
					t.Fatalf("%s: got %d, want %d", tc.statement, got, tc.want)
				}
			}
			var search, body string
			query(paths.CoreDBPath, "SELECT search_config, body FROM conversations WHERE id=1", &search, &body)
			if search != "{}" || body != "keep conversation" {
				t.Fatalf("KB reset changed conversation content: %q, %q", search, body)
			}
		})
	}
}

func TestResetPathsDoNotIncludeLegacyRepoDataDirs(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}

	legacyDataRoot := filepath.Join(paths.RepoRoot, "data") + string(filepath.Separator)
	for _, path := range append(localKBResetPaths(paths), localAllResetPaths(paths)...) {
		if path == filepath.Join(paths.RepoRoot, "data") || strings.HasPrefix(path, legacyDataRoot) {
			t.Fatalf("reset path %q must not target legacy repo data dir", path)
		}
	}
}

func TestLocalResetRejectsForeignPythonRuntimeBeforeUV(t *testing.T) {
	root, foreign := t.TempDir(), t.TempDir()
	paths := RuntimePaths{
		BuildRoot:        root,
		PythonRuntimeDir: filepath.Join(root, "runtimes", "python", runtime.GOOS+"-"+runtime.GOARCH),
	}
	if err := os.MkdirAll(filepath.Dir(paths.PythonRuntimeDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createDirectoryLink(foreign, paths.PythonRuntimeDir); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{t: t}
	err := NewRuntimeManager(runner, "").resetLocalSQLiteKBState(context.Background(), RuntimeConfig{Profile: "local"}, paths)
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("local reset bypassed Python directory protection: %v", err)
	}
	runner.assertCommandCount(0)
	if entries, err := os.ReadDir(foreign); err != nil || len(entries) != 0 {
		t.Fatalf("foreign runtime was modified: %v, %v", entries, err)
	}
}

func TestResetPathsIncludeRuntimeUploadData(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}

	assertStringSliceContains(t, localKBResetPaths(paths), paths.UploadRoot)
	assertStringSliceContains(t, localAllResetPaths(paths), filepath.Join(paths.RuntimeRoot, "data"))
}

func assertStringSliceContains(t *testing.T, items []string, want string) {
	t.Helper()
	for _, item := range items {
		if item == want {
			return
		}
	}
	t.Fatalf("missing %q in %#v", want, items)
}
