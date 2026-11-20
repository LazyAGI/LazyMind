//go:build windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/teststorage"
	"golang.org/x/sys/windows/registry"
)

type installedStorageFixture struct {
	registry                   *teststorage.Registry
	app, data, resources, repo string
	legacyRoot                 string
}

func newInstalledStorageFixture(t *testing.T) installedStorageFixture {
	t.Helper()
	r := teststorage.New(t)
	home := t.TempDir()
	localAppData := filepath.Join(home, "AppData", "Local")
	for key, value := range map[string]string{
		localHostHomeEnvVar: home, "HOME": home, "USERPROFILE": home,
		"LOCALAPPDATA": localAppData, runtimeRootEnvVar: "", localBuildRootEnvVar: "",
		localSQLiteDirEnvVar: "", "LAZYMIND_FILE_WATCHER_BASE_ROOT": "",
		"LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR": "", cloudCredentialKeyEnvVar: "",
		modelCredentialKeyEnvVar: "", "LAZYMIND_DESKTOP_DATA_ROOT": "",
	} {
		t.Setenv(key, value)
	}
	storage := filepath.Join(t.TempDir(), "QA Tools", "中文")
	f := installedStorageFixture{
		registry: r, app: filepath.Join(storage, "App"), data: filepath.Join(storage, "Data"),
		legacyRoot: filepath.Join(localAppData, "LazyMind"),
	}
	f.resources = filepath.Join(f.app, "resources", "runtime")
	f.repo = filepath.Join(f.resources, "app")
	if err := os.MkdirAll(f.repo, 0o700); err != nil {
		t.Fatal(err)
	}
	writeComposeFixture(t, f.repo)
	if err := os.MkdirAll(f.data, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(f.app, teststorage.AppMarker):    "",
		filepath.Join(f.data, teststorage.OwnerMarker): teststorage.SID(t),
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.SetString(teststorage.DataKey, "DataRoot", f.data)
	r.SetString(teststorage.InstallKey, "InstallLocation", f.app)
	return f
}

func (f installedStorageFixture) options(profile string) RuntimeConfigOptions {
	return RuntimeConfigOptions{Profile: profile, RepoRoot: f.repo, ResourcesRoot: f.resources}
}

func TestInstalledStorageRuntimeConsumersFollowRegisteredDataRoot(t *testing.T) {
	f := newInstalledStorageFixture(t)
	// Both paths are temporary directories on the test volume. Real C/D volume
	// behavior belongs to the Windows installer lifecycle tests, not this unit.
	for _, profile := range []string{"desktop", "local"} {
		t.Run(profile, func(t *testing.T) {
			cfg, paths, err := newRuntimeConfigWithOptions(f.options(profile), true)
			if err != nil {
				t.Fatal(err)
			}
			for name, pair := range map[string][2]string{
				"runtime": {paths.RuntimeRoot, f.data},
				"data":    {paths.DataDir, filepath.Join(f.data, "data")},
				"cache":   {paths.CacheDir, filepath.Join(f.data, "Cache")},
				"logs":    {paths.LogsDir, filepath.Join(f.data, "Logs")},
				"state":   {paths.StateFile, filepath.Join(f.data, "state", "runtime-state.json")},
				"inbox":   {cfg.FileWatcher.WatchHostDir, filepath.Join(f.data, "Inbox")},
				"uploads": {paths.UploadRoot, filepath.Join(f.data, "data", "core", "uploads")},
			} {
				if filepath.Clean(pair[0]) != filepath.Clean(pair[1]) {
					t.Errorf("%s = %q, want %q", name, pair[0], pair[1])
				}
			}
			for name, value := range map[string]string{
				"uv cache": paths.UVCacheDir, "pip cache": paths.PipCacheDir,
				"xdg cache": paths.XDGCacheDir, "core database": paths.CoreDBPath,
				"auth database": paths.AuthServiceDBPath, "scan database": paths.ScanDBPath,
				"watcher staging": paths.FileWatcherBaseRoot,
			} {
				if !pathIsUnderRoot(value, f.data) {
					t.Errorf("%s = %q is outside registered data root %q", name, value, f.data)
				}
			}
			if profile == "desktop" && !pathIsUnderRoot(paths.AlgorithmPython, f.app) {
				t.Errorf("bundled Python = %q, want application resources under %q", paths.AlgorithmPython, f.app)
			}
		})
	}
}

func TestInstalledStoragePythonCacheFallbackUsesResolvedFields(t *testing.T) {
	root := t.TempDir()
	paths := RuntimePaths{
		RuntimeRoot: filepath.Join(root, "Data"), BuildRoot: filepath.Join(root, "App"),
		UVCacheDir:  filepath.Join(root, "Data", "Cache", "uv"),
		PipCacheDir: filepath.Join(root, "Data", "Cache", "pip"),
		XDGCacheDir: filepath.Join(root, "Data", "Cache", "xdg"),
	}
	// Keep the existing rejection of inherited runtime/build cache paths. Its
	// fallback must now be the resolved application cache, not host LocalAppData.
	t.Setenv("UV_CACHE_DIR", filepath.Join(paths.RuntimeRoot, "untrusted-uv"))
	t.Setenv("PIP_CACHE_DIR", filepath.Join(paths.BuildRoot, "untrusted-pip"))
	got := map[string]string{}
	for _, entry := range pythonDependencyCacheEnv(paths) {
		if name, value, ok := strings.Cut(entry, "="); ok {
			got[name] = value
		}
	}
	for name, want := range map[string]string{"UV_CACHE_DIR": paths.UVCacheDir, "PIP_CACHE_DIR": paths.PipCacheDir} {
		if got[name] != want {
			t.Errorf("%s = %q, want resolved fallback %q", name, got[name], want)
		}
	}
	if !pathIsUnderRoot(got["XDG_CACHE_HOME"], paths.RuntimeRoot) {
		t.Errorf("Python preparation XDG cache = %q is outside selected data root", got["XDG_CACHE_HOME"])
	}
}

func TestInstalledStorageUsesExistingIdentityAcrossModes(t *testing.T) {
	f := newInstalledStorageFixture(t)
	identityPath := filepath.Join(f.data, "Desktop", "credential-device.json")
	writeCredentialIdentityFixture(t, identityPath, fixtureDesktopIdentity)
	for _, profile := range []string{"local", "desktop"} {
		cfg, paths, err := newRuntimeConfigWithOptions(f.options(profile), true)
		if err != nil {
			t.Fatal(err)
		}
		env, err := authServiceEnv(cfg, paths)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range env {
			if strings.HasPrefix(entry, cloudCredentialKeyEnvVar+"=") {
				found = true
				if strings.TrimPrefix(entry, cloudCredentialKeyEnvVar+"=") != fixtureDesktopCloudKey {
					t.Errorf("%s did not reuse the registered Desktop cloud identity", profile)
				}
			}
		}
		if !found {
			t.Errorf("%s cloud key is missing", profile)
		}
	}
	// Independent fixed vector for the same published fixture identity.
	if got, err := modelCredentialKey(); err != nil || got != "l_JSbmfusw38fVGvRPOcm_rKOkAV4OvMSNjNgcvPK38" {
		t.Errorf("model identity = %q, %v; want existing registered identity", got, err)
	}
	if raw, err := os.ReadFile(identityPath); err != nil || string(raw) != fixtureDesktopIdentity {
		t.Errorf("registered identity was changed: %v", err)
	}
	assertNoLegacyStorageIdentity(t, f.legacyRoot)
}

func TestInstalledStorageInvalidLocationFailsWithoutCreatingLegacyIdentity(t *testing.T) {
	for _, scenario := range []string{"empty", "wrong type", "expand string", "relative", "missing directory", "missing registration", "mismatched App", "wrong owner", "missing owner"} {
		t.Run(scenario, func(t *testing.T) {
			f := newInstalledStorageFixture(t)
			switch scenario {
			case "empty":
				f.registry.SetString(teststorage.DataKey, "DataRoot", "")
			case "wrong type":
				f.registry.SetDWord(teststorage.DataKey, "DataRoot", 1)
			case "expand string":
				f.registry.SetExpandString(teststorage.DataKey, "DataRoot", f.data)
			case "relative":
				f.registry.SetString(teststorage.DataKey, "DataRoot", `relative\Data`)
			case "missing directory":
				f.registry.SetString(teststorage.DataKey, "DataRoot", filepath.Join(t.TempDir(), "missing", "Data"))
			case "missing registration":
				f.registry.DeleteValue(teststorage.DataKey, "DataRoot")
			case "mismatched App":
				f.registry.SetString(teststorage.InstallKey, "InstallLocation", t.TempDir())
			case "wrong owner":
				if err := os.WriteFile(filepath.Join(f.data, teststorage.OwnerMarker), []byte("S-1-0-0"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing owner":
				if err := os.Remove(filepath.Join(f.data, teststorage.OwnerMarker)); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := newRuntimeConfigWithOptions(f.options("desktop"), true); err == nil {
				t.Error("invalid installed storage was accepted by runtime configuration")
			}
			if _, err := cloudCredentialKey(); err == nil {
				t.Error("invalid installed storage generated or used a cloud identity")
			}
			if _, err := modelCredentialKey(); err == nil {
				t.Error("invalid installed storage generated or used a model identity")
			}
			assertNoLegacyStorageIdentity(t, f.legacyRoot)
		})
	}
}

func TestInstalledStorageInvalidIdentityIsNotReplaced(t *testing.T) {
	f := newInstalledStorageFixture(t)
	path := filepath.Join(f.data, "Desktop", "credential-device.json")
	writeCredentialIdentityFixture(t, path, "invalid existing identity")
	if _, err := cloudCredentialKey(); err == nil {
		t.Error("invalid registered identity did not fail")
	}
	if _, err := modelCredentialKey(); err == nil {
		t.Error("invalid registered model identity did not fail")
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "invalid existing identity" {
		t.Errorf("invalid existing identity was replaced: %v", err)
	}
	assertNoLegacyStorageIdentity(t, f.legacyRoot)
}

func TestInstalledStorageMissingIdentityWithPersistedCredentialsFailsBeforeServiceStart(t *testing.T) {
	for _, kind := range []string{"auth", "model"} {
		t.Run(kind, func(t *testing.T) {
			f := newInstalledStorageFixture(t)
			opts := f.options("local")
			opts.RuntimeRoot = f.data
			cfg, paths, err := newRuntimeConfigWithOptions(opts, true)
			if err != nil {
				t.Fatal(err)
			}
			identityPath := filepath.Join(f.data, "Desktop", "credential-device.json")
			writeCredentialIdentityFixture(t, identityPath, fixtureDesktopIdentity)
			stored := writeStoredCredentialFixture(t, paths, kind, true)
			if err := os.Remove(identityPath); err != nil {
				t.Fatal(err)
			}

			if _, err := cloudCredentialKey(); err == nil {
				t.Error("existing encrypted credentials did not block creation of a replacement cloud identity")
			}
			if _, err := modelCredentialKey(); err == nil {
				t.Error("existing encrypted credentials did not block creation of a replacement model identity")
			}
			// These are the real pre-start boundaries: AuthServiceManager.Run
			// returns on authServiceEnv failure before preparing dependencies,
			// and CoreServiceManager.Run cannot exec Core without coreServiceEnv.
			// Do not launch either manager/product process to prove this guard.
			if env, err := authServiceEnv(cfg, paths); err == nil || env != nil {
				t.Error("auth-service startup environment was produced after losing an identity protecting durable credentials")
			}
			if env, err := coreServiceEnv(cfg, paths); err == nil || env != nil {
				t.Error("Core startup environment was produced after losing an identity protecting durable credentials")
			}
			if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
				t.Errorf("missing identity was replaced: %v", err)
			}
			assertNoLegacyStorageIdentity(t, f.legacyRoot)
			stored.assertUnchanged(t)
		})
	}
}

func TestInstalledStorageFirstIdentityAllowsEmptyDataAndEmptyCredentialSchemas(t *testing.T) {
	for _, kind := range []string{"no database", "auth", "model"} {
		t.Run(kind, func(t *testing.T) {
			f := newInstalledStorageFixture(t)
			opts := f.options("local")
			opts.RuntimeRoot = f.data
			cfg, paths, err := newRuntimeConfigWithOptions(opts, true)
			if err != nil {
				t.Fatal(err)
			}
			var stored *storedCredentialFixture
			if kind != "no database" {
				value := writeStoredCredentialFixture(t, paths, kind, false)
				stored = &value
			}
			identityPath := filepath.Join(f.data, "Desktop", "credential-device.json")
			if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
				t.Fatalf("first-start identity already exists: %v", err)
			}
			cloud, err := cloudCredentialKey()
			if err != nil || cloud == "" {
				t.Fatalf("empty application state cannot create its first identity: %v", err)
			}
			raw, err := os.ReadFile(identityPath)
			if err != nil {
				t.Fatalf("first identity was not created at the selected data root: %v", err)
			}
			if _, err := readCredentialDevice(identityPath); err != nil {
				t.Fatalf("first identity is invalid: %v", err)
			}
			model, err := modelCredentialKey()
			if err != nil || model == "" {
				t.Fatalf("first identity cannot provide the model key: %v", err)
			}
			if env, err := authServiceEnv(cfg, paths); err != nil || env == nil {
				t.Fatalf("empty initialized Auth schema was treated as existing encrypted credentials: %v", err)
			}
			if env, err := coreServiceEnv(cfg, paths); err != nil || env == nil {
				t.Fatalf("empty initialized Core schema was treated as existing encrypted credentials: %v", err)
			}
			after, err := os.ReadFile(identityPath)
			if err != nil || !bytes.Equal(raw, after) {
				t.Errorf("first identity rotated while preparing startup environments: %v", err)
			}
			assertNoLegacyStorageIdentity(t, f.legacyRoot)
			if stored != nil {
				stored.assertUnchanged(t)
			}
		})
	}
}

func TestInstalledStorageKeepsDataAfterProgramRegistrationIsRemoved(t *testing.T) {
	f := newInstalledStorageFixture(t)
	f.registry.DeleteValue(teststorage.InstallKey, "InstallLocation")
	if err := os.RemoveAll(f.app); err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(f.data, "Desktop", "credential-device.json")
	writeCredentialIdentityFixture(t, identityPath, fixtureDesktopIdentity)
	sourceRepo := t.TempDir()
	writeComposeFixture(t, sourceRepo)
	_, paths, err := newRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: "local", RepoRoot: sourceRepo}, true)
	if err != nil {
		t.Fatal(err)
	}
	if paths.RuntimeRoot != f.data {
		t.Errorf("retained runtime root = %q, want %q", paths.RuntimeRoot, f.data)
	}
	if got, err := cloudCredentialKey(); err != nil || got != fixtureDesktopCloudKey {
		t.Errorf("retained identity was not reused: %q, %v", got, err)
	}
	assertNoLegacyStorageIdentity(t, f.legacyRoot)
}

func TestInstalledStoragePreservesLocalExplicitRootButRejectsDesktopConflict(t *testing.T) {
	f := newInstalledStorageFixture(t)
	explicit := t.TempDir()
	opts := f.options("local")
	opts.RuntimeRoot = explicit
	if _, paths, err := newRuntimeConfigWithOptions(opts, true); err != nil || paths.RuntimeRoot != explicit {
		t.Errorf("Local explicit root = %q, %v; want %q", paths.RuntimeRoot, err, explicit)
	}
	opts.Profile = "desktop"
	if _, _, err := newRuntimeConfigWithOptions(opts, true); err == nil {
		t.Error("Desktop accepted a root conflicting with the registered new layout")
	}
	t.Setenv(runtimeRootEnvVar, explicit)
	opts.RuntimeRoot = ""
	opts.Profile = "local"
	if _, paths, err := newRuntimeConfigWithOptions(opts, true); err != nil || paths.RuntimeRoot != explicit {
		t.Errorf("Local environment root = %q, %v; want %q", paths.RuntimeRoot, err, explicit)
	}
	opts.Profile = "desktop"
	if _, _, err := newRuntimeConfigWithOptions(opts, true); err == nil {
		t.Error("Desktop accepted an inherited root conflicting with the registered new layout")
	}
}

func TestInstalledStorageLegacyDefaultRemainsUnregistered(t *testing.T) {
	teststorage.New(t)
	home := t.TempDir()
	localAppData := filepath.Join(home, "AppData", "Local")
	t.Setenv(localHostHomeEnvVar, home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv(runtimeRootEnvVar, "")
	t.Setenv(localBuildRootEnvVar, "")
	t.Setenv(runtimeResourcesRootEnvVar, "")
	t.Setenv(localSQLiteDirEnvVar, "")
	t.Setenv("LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR", "")
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := newRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: "desktop", RepoRoot: repo}, true)
	if err != nil {
		t.Fatal(err)
	}
	if paths.RuntimeRoot != filepath.Join(localAppData, "LazyMind") ||
		paths.CacheDir != filepath.Join(localAppData, "LazyMind", "Cache") ||
		paths.LogsDir != filepath.Join(localAppData, "LazyMind", "Logs") ||
		cfg.FileWatcher.WatchHostDir != filepath.Join(home, "Documents", "LazyMind") {
		t.Fatalf("legacy default directories changed: paths=%+v watcher=%q", paths, cfg.FileWatcher.WatchHostDir)
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, teststorage.DataKey, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err == nil {
		key.Close()
		t.Fatal("legacy lookup wrote a new storage registration")
	}
	if err != registry.ErrNotExist {
		t.Fatal(err)
	}
}

func assertNoLegacyStorageIdentity(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "Desktop", "credential-device.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("unexpected legacy identity at %q: %v", path, err)
	}
}
