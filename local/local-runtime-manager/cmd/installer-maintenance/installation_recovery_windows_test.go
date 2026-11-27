package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/teststorage"
	"golang.org/x/sys/windows/registry"
)

const (
	recoveryExecutable  = "verified packaged application"
	recoveryUninstaller = "verified embedded uninstaller"
)

func TestStorageFreshInstallRecoveryPreservesExistingDirectories(t *testing.T) {
	_, layout := freshRecoverySession(t, true)
	writeStorageFile(t, filepath.Join(layout.Stage, "unfinished-package.7z"), "partial package")

	if err := rollbackRecoverySession(t, layout); err != nil {
		t.Fatalf("roll back interrupted extraction: %v", err)
	}
	requireRecoveryPathAbsent(t, layout.Stage)
	for _, dir := range []string{layout.App, layout.Data} {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Errorf("existing directory %s should still be empty: %v, %v", dir, entries, err)
		}
	}
	requireRecoveryRegistryValue(t, teststorage.DataKey, "DataRoot", "")
	requireRecoveryFile(t, filepath.Join(layout.Storage, "personal.txt"), "existing user file")
}

func TestStorageFreshInstallRecoveryClearsPartialRegistration(t *testing.T) {
	r, layout := freshRecoverySession(t, false)
	verifiedRecoveryPayload(t, layout)
	// NSIS emits the uninstaller before it begins writing these values.
	r.SetString(teststorage.InstallKey, "InstallLocation", layout.App)
	r.SetString(teststorage.InstallKey, "KeepShortcuts", "true")
	r.SetString(teststorage.InstallKey, "ShortcutName", "LazyMind")
	r.SetString(teststorage.UninstallKey, "DisplayName", "LazyMind")
	r.SetString(teststorage.UninstallKey, "UninstallString", recoveryUninstallCommand(layout.App))

	if err := rollbackRecoverySession(t, layout); err != nil {
		t.Fatalf("roll back partial program registration: %v", err)
	}
	requireRecoveryPathAbsent(t, layout.Stage)
	requireRecoveryPathAbsent(t, layout.App)
	requireRecoveryPathAbsent(t, layout.Data)
	requireRecoveryRegistryValue(t, teststorage.DataKey, "DataRoot", "")
	requireRecoveryRegistryValue(t, teststorage.InstallKey, "InstallLocation", "")
	requireRecoveryRegistryValue(t, teststorage.InstallKey, "KeepShortcuts", "")
	requireRecoveryRegistryValue(t, teststorage.InstallKey, "ShortcutName", "")
	requireRecoveryRegistryValue(t, teststorage.UninstallKey, "DisplayName", "")
	requireRecoveryRegistryValue(t, teststorage.UninstallKey, "UninstallString", "")
	requireRecoveryFile(t, filepath.Join(layout.Storage, "personal.txt"), "existing user file")
}

func TestStoragePublishedInstallRecoveryKeepsProgramAndData(t *testing.T) {
	r, layout := freshRecoverySession(t, false)
	verifiedRecoveryPayload(t, layout)
	command := recoveryUninstallCommand(layout.App)
	uninstaller := filepath.Join(layout.App, "Uninstall LazyMind.exe")
	r.SetString(teststorage.InstallKey, "InstallLocation", layout.App)
	r.SetString(teststorage.UninstallKey, "DisplayName", "LazyMind")
	r.SetString(teststorage.UninstallKey, "UninstallString", command)
	r.SetString(teststorage.UninstallKey, "QuietUninstallString", command+" /S")
	// Publication reached the registry before the session recorded it.
	r.SetString(teststorage.DataKey, "DataRoot", layout.Data)
	dataFile := filepath.Join(layout.Data, "user-data.txt")
	writeStorageFile(t, dataFile, "data written after publication")

	if err := rollbackRecoverySession(t, layout); err != nil {
		t.Fatalf("recover published installation: %v", err)
	}
	requireRecoveryPathAbsent(t, layout.Stage)
	requireRecoveryFile(t, filepath.Join(layout.App, "LazyMind.exe"), recoveryExecutable)
	requireRecoveryFile(t, uninstaller, recoveryUninstaller)
	requireRecoveryFile(t, dataFile, "data written after publication")
	requireRecoveryRegistryValue(t, teststorage.InstallKey, "InstallLocation", layout.App)
	requireRecoveryRegistryValue(t, teststorage.UninstallKey, "UninstallString", command)
	requireRecoveryRegistryValue(t, teststorage.UninstallKey, "QuietUninstallString", command+" /S")
	requireRecoveryRegistryValue(t, teststorage.DataKey, "DataRoot", layout.Data)
}

func TestStorageInstallRecoveryRejectsUnattributedChanges(t *testing.T) {
	t.Run("unexpected program file", func(t *testing.T) {
		_, layout := freshRecoverySession(t, false)
		verifiedRecoveryPayload(t, layout)
		unknown := filepath.Join(layout.App, "personal.txt")
		writeStorageFile(t, unknown, "file outside the installation payload")

		if err := rollbackRecoverySession(t, layout); err == nil {
			t.Fatal("rollback accepted a file whose ownership it could not prove")
		}
		requireRecoveryFile(t, unknown, "file outside the installation payload")
		if _, err := os.Stat(layout.Stage); err != nil {
			t.Errorf("recovery stage should remain available: %v", err)
		}
	})

	t.Run("program registration points elsewhere", func(t *testing.T) {
		r, layout := freshRecoverySession(t, false)
		other := t.TempDir()
		r.SetString(teststorage.InstallKey, "InstallLocation", other)
		witness := filepath.Join(other, "personal.txt")
		writeStorageFile(t, witness, "file from another directory")

		if err := rollbackRecoverySession(t, layout); err == nil {
			t.Fatal("rollback accepted a conflicting program registration")
		}
		requireRecoveryRegistryValue(t, teststorage.InstallKey, "InstallLocation", other)
		requireRecoveryFile(t, witness, "file from another directory")
		if _, err := os.Stat(layout.Stage); err != nil {
			t.Errorf("recovery stage should remain available: %v", err)
		}
	})
}

func TestStorageUpgradeRecoveryRestoresBackupAndRegistration(t *testing.T) {
	r, _, app, data := storageFixture(t)
	command := recoveryUninstallCommand(app)
	r.SetString(teststorage.UninstallKey, "UninstallString", command)
	r.SetString(teststorage.UninstallKey, "DisplayVersion", "1.0.0")
	writeStorageFile(t, filepath.Join(app, "LazyMind.exe"), "previous program")
	identity := filepath.Join(data, "credential-device.json")
	writeStorageFile(t, identity, "previous device identity")
	layout := recoverySession(t, installLayout{Mode: "installed", App: app, Data: data, Storage: filepath.Dir(app), Registered: true})
	if err := os.Rename(app, filepath.Join(layout.Stage, "old-app")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(app, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStorageFile(t, filepath.Join(app, "LazyMind.exe"), "incomplete upgrade")
	r.SetString(teststorage.UninstallKey, "DisplayVersion", "2.0.0")

	if err := rollbackRecoverySession(t, layout); err != nil {
		t.Fatalf("roll back upgrade: %v", err)
	}
	requireRecoveryPathAbsent(t, layout.Stage)
	requireRecoveryFile(t, filepath.Join(app, "LazyMind.exe"), "previous program")
	requireRecoveryFile(t, identity, "previous device identity")
	requireRecoveryRegistryValue(t, teststorage.DataKey, "DataRoot", data)
	requireRecoveryRegistryValue(t, teststorage.InstallKey, "InstallLocation", app)
	requireRecoveryRegistryValue(t, teststorage.UninstallKey, "UninstallString", command)
	requireRecoveryRegistryValue(t, teststorage.UninstallKey, "DisplayVersion", "1.0.0")
}

func freshRecoverySession(t *testing.T, existingDirectories bool) (*teststorage.Registry, installLayout) {
	t.Helper()
	r := teststorage.New(t)
	root := t.TempDir()
	layout := installLayout{Mode: "new", Storage: root, App: filepath.Join(root, "App"), Data: filepath.Join(root, "Data")}
	writeStorageFile(t, filepath.Join(root, "personal.txt"), "existing user file")
	sid := teststorage.SID(t)
	createDirectories := func() {
		t.Helper()
		for _, dir := range []string{layout.App, layout.Data} {
			if err := createPrivateDirectory(dir, sid); err != nil {
				t.Fatal(err)
			}
		}
	}
	if existingDirectories {
		createDirectories()
	}
	// Record the session before writing any markers or registry values and
	// without consulting the real account's legacy application data.
	layout = recoverySession(t, layout)
	if !existingDirectories {
		createDirectories()
	}
	if err := writeAppFlag(layout.App, sid); err != nil {
		t.Fatal(err)
	}
	writeStorageFile(t, filepath.Join(layout.Data, teststorage.OwnerMarker), sid+"\n")
	return r, layout
}

func recoverySession(t *testing.T, layout installLayout) installLayout {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	layout.Stage = filepath.Join(layout.Storage, ".install-tmp", "install-recovery-test")
	if err := createPrivateDirectory(layout.Stage, teststorage.SID(t)); err != nil {
		t.Fatal(err)
	}
	if err := createSession(layout); err != nil {
		t.Fatal(err)
	}
	return layout
}

func rollbackRecoverySession(t *testing.T, layout installLayout) error {
	t.Helper()
	opts, err := parseCommandOptions([]string{
		"rollback-install", "--install-dir", layout.App, "--stage-dir", layout.Stage,
	})
	if err != nil {
		t.Fatalf("parse rollback command: %v", err)
	}
	return runInstallerRollback(opts)
}

func verifiedRecoveryPayload(t *testing.T, layout installLayout) {
	t.Helper()
	// The installer knows the bytes of both the packaged executable and the
	// embedded uninstaller it writes before creating the registry entries.
	manifest := payloadManifest{Version: 1}
	for _, file := range []struct{ name, contents string }{
		{"LazyMind.exe", recoveryExecutable},
		{"Uninstall LazyMind.exe", recoveryUninstaller},
	} {
		writeStorageFile(t, filepath.Join(layout.App, file.name), file.contents)
		manifest.Files = append(manifest.Files, payloadFile{
			Path: file.name, Size: int64(len(file.contents)), SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(file.contents))),
		})
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "payload.json")
	writeStorageFile(t, filename, string(content))
	// The product's verification call must supply the installed App and its
	// private session to persist verified evidence for recovery.
	opts, err := parseCommandOptions([]string{
		"verify-payload", "--payload-dir", layout.App, "--manifest", filename,
		"--install-dir", layout.App, "--stage-dir", layout.Stage,
	})
	if err != nil {
		t.Fatalf("parse payload verification command: %v", err)
	}
	if err := verifyPayload(opts); err != nil {
		t.Fatalf("verify installation payload fixture: %v", err)
	}
	// NSIS plugin files are unavailable when recovering an interrupted run.
	if err := os.Remove(filename); err != nil {
		t.Fatalf("remove transient verification input: %v", err)
	}
}

func recoveryUninstallCommand(app string) string {
	return "\"" + filepath.Join(app, "Uninstall LazyMind.exe") + "\" /currentuser"
}

func requireRecoveryPathAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("path %s should be absent: %v", path, err)
	}
}

func requireRecoveryFile(t *testing.T, path, want string) {
	t.Helper()
	if got, err := os.ReadFile(path); err != nil || string(got) != want {
		t.Errorf("file %s = %q, %v; want %q", path, got, err, want)
	}
}

func requireRecoveryRegistryValue(t *testing.T, path, name, want string) {
	t.Helper()
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if want == "" && errors.Is(err, registry.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	got, kind, err := key.GetStringValue(name)
	if want == "" {
		if !errors.Is(err, registry.ErrNotExist) {
			t.Errorf("registry value %s\\%s should be absent: %q, %v", path, name, got, err)
		}
		return
	}
	if err != nil || got != want || kind != registry.SZ {
		t.Errorf("registry value %s\\%s = %q (kind %d), %v; want REG_SZ %q", path, name, got, kind, err, want)
	}
}
