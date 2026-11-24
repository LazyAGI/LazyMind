package main

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/teststorage"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const uninstallTestExecutable = "the installed uninstaller"

var errUninstallTestInterrupted = errors.New("synthetic interruption after removing one quarantined file")

func TestStorageKeepUninstallRetainsRetryEntryThroughPartialUnregistration(t *testing.T) {
	f := newUninstallRecoveryFixture(t)
	if err := f.retry(t, "UninstallString"); err != nil {
		t.Fatalf("move the program while retaining data: %v", err)
	}
	requireRecoveryPathAbsent(t, filepath.Join(f.app, "LazyMind.exe"))
	f.requireRetryEntry(t)
	f.requirePreservedData(t)
	stage := f.keepRecoveryStage(t)

	f.registry.DeleteValue(teststorage.InstallKey, "InstallLocation")
	if err := verifyUninstalled(); err == nil {
		t.Fatal("partial registry removal was reported as complete")
	}
	if err := f.retry(t, "QuietUninstallString"); err != nil {
		t.Fatalf("retry uninstall after partial registry removal: %v", err)
	}
	f.completeUninstall(t, stage, false)
}

func TestStoragePurgeUninstallContinuesOnlyAfterConfirmation(t *testing.T) {
	f := newUninstallRecoveryFixture(t)
	quarantine := f.interruptPurge(t)

	// A retry without the explicit purge flag must not clear the remaining
	// data or tell NSIS to proceed with removal of the retry entry.
	for _, name := range []string{"UninstallString", "QuietUninstallString"} {
		if err := f.retry(t, name); err == nil {
			t.Fatalf("the default restart from %s did not stop for confirmation of the pending purge", name)
		}
	}
	f.requireRetryEntry(t)
	requireRecoveryPathAbsent(t, f.data)
	requireRecoveryFile(t, uninstallTestIdentityPath(quarantine), "the existing device identity")

	calls := 0
	if err := f.remove(t, true, func(target string) error {
		calls++
		if !samePath(target, quarantine) {
			t.Fatalf("resumed purge switched targets from %q to %q", quarantine, target)
		}
		requireRecoveryFile(t, uninstallTestIdentityPath(target), "the existing device identity")
		return os.RemoveAll(target)
	}); err != nil {
		t.Fatalf("resume the explicitly confirmed purge: %v", err)
	}
	if calls != 1 {
		t.Fatalf("resumed purge deleted %d times, want 1", calls)
	}
	requireRecoveryPathAbsent(t, quarantine)
	requireRecoveryPathAbsent(t, f.data)
	f.requireRetryEntry(t)

	// The process may exit after data is gone but before NSIS finishes
	// unregistering the program. Retrying must finish without more deletion.
	f.registry.DeleteValue(teststorage.InstallKey, "InstallLocation")
	if err := f.remove(t, true, rejectUnexpectedPurge(t)); err != nil {
		t.Fatalf("retry removal after the purge finished and unregistration was interrupted: %v", err)
	}
	f.completeUninstall(t, f.purgeRecoveryStage(t), true)
}

func TestStoragePurgeUninstallRejectsAReplacedQuarantine(t *testing.T) {
	f := newUninstallRecoveryFixture(t)
	quarantine := f.interruptPurge(t)
	displaced := filepath.Join(f.root, "displaced-data")
	if err := os.Rename(quarantine, displaced); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(quarantine, 0o700); err != nil {
		t.Fatal(err)
	}
	witness := filepath.Join(quarantine, "user.txt")
	writeStorageFile(t, witness, "unrelated replacement")
	writeStorageFile(t, filepath.Join(quarantine, teststorage.OwnerMarker), teststorage.SID(t)+"\n")

	if err := f.remove(t, true, rejectUnexpectedPurge(t)); err == nil {
		t.Fatal("confirmed purge accepted a different directory at the recorded path")
	}
	requireRecoveryFile(t, witness, "unrelated replacement")
	requireRecoveryFile(t, uninstallTestIdentityPath(displaced), "the existing device identity")
	f.requireRetryEntry(t)
}

type uninstallRecoveryFixture struct {
	registry        *teststorage.Registry
	root, app, data string
	output          string
}

func newUninstallRecoveryFixture(t *testing.T) uninstallRecoveryFixture {
	t.Helper()
	r, _, app, data := storageFixture(t)
	root := filepath.Dir(app)
	writeStorageFile(t, filepath.Join(app, "LazyMind.exe"), "installed program")
	writeStorageFile(t, filepath.Join(app, "Uninstall LazyMind.exe"), uninstallTestExecutable)
	writeStorageFile(t, filepath.Join(data, "first.txt"), "first file to delete")
	identity := uninstallTestIdentityPath(data)
	if err := os.Mkdir(filepath.Dir(identity), 0o700); err != nil {
		t.Fatal(err)
	}
	writeStorageFile(t, identity, "the existing device identity")
	writeStorageFile(t, filepath.Join(root, "neighbor.txt"), "neighboring user file")
	command := recoveryUninstallCommand(app)
	r.SetString(teststorage.UninstallKey, "UninstallString", command)
	r.SetString(teststorage.UninstallKey, "QuietUninstallString", command+" /S")
	r.SetString(teststorage.UninstallKey, "DisplayName", "LazyMind")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	return uninstallRecoveryFixture{registry: r, root: root, app: app, data: data, output: filepath.Join(root, "layout.ini")}
}

func (f uninstallRecoveryFixture) remove(t *testing.T, confirmedPurge bool, removeQuarantine func(string) error) error {
	t.Helper()
	// Every NSIS invocation gets a fresh plugin directory and layout result.
	if err := os.Remove(f.output); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	args := []string{"remove-program", "--install-dir", f.app, "--output", f.output}
	if confirmedPurge {
		args = append(args, "--purge")
	}
	opts, err := parseCommandOptions(args)
	if err != nil {
		t.Fatal(err)
	}
	return runInstallerRemoval(opts, func(root, app string) ([]runningProcess, error) {
		if !samePath(root, f.data) || !samePath(app, f.app) {
			t.Fatalf("stop requested outside the fixture: %q, %q", root, app)
		}
		return nil, nil
	}, removeQuarantine)
}

func (f uninstallRecoveryFixture) interruptPurge(t *testing.T) string {
	t.Helper()
	var quarantine string
	if err := f.remove(t, true, func(target string) error {
		relative, err := filepath.Rel(f.root, target)
		if err != nil || relative == "." || !filepath.IsLocal(relative) || samePath(target, f.data) {
			t.Fatalf("purge must isolate data in the same private fixture: %q, %v", target, err)
		}
		if quarantine != "" {
			t.Fatal("the synthetic interruption should not be retried in the same attempt")
		}
		quarantine = target
		if err := os.Remove(filepath.Join(target, "first.txt")); err != nil {
			t.Fatal(err)
		}
		return errUninstallTestInterrupted
	}); !errors.Is(err, errUninstallTestInterrupted) {
		t.Fatalf("expected the interruption inside the quarantine: %v", err)
	}
	requireRecoveryPathAbsent(t, f.data)
	requireRecoveryPathAbsent(t, filepath.Join(quarantine, "first.txt"))
	requireRecoveryFile(t, uninstallTestIdentityPath(quarantine), "the existing device identity")
	requireRecoveryFile(t, filepath.Join(f.root, "neighbor.txt"), "neighboring user file")
	f.requireRetryEntry(t)
	return quarantine
}

func (f uninstallRecoveryFixture) requireRetryEntry(t *testing.T) {
	t.Helper()
	for _, name := range []string{"UninstallString", "QuietUninstallString"} {
		f.retryCommand(t, name)
	}
}

func (f uninstallRecoveryFixture) retryCommand(t *testing.T, name string) []string {
	t.Helper()
	command, err := readInstallStringRaw(teststorage.UninstallKey, name)
	if err != nil {
		t.Fatal(err)
	}
	args, err := windows.DecomposeCommandLine(command)
	if err != nil || len(args) == 0 {
		t.Fatalf("Windows uninstall entry %s is unavailable: %q, %v", name, command, err)
	}
	want := []string{"/currentuser"}
	if name == "QuietUninstallString" {
		want = append(want, "/S")
	}
	if !slices.Equal(args[1:], want) {
		t.Fatalf("Windows uninstall entry %s must start in its default mode: got %q, want %q", name, args[1:], want)
	}
	entry := args[0]
	relative, err := filepath.Rel(f.root, entry)
	if err != nil || !filepath.IsAbs(entry) || relative == "." || !filepath.IsLocal(relative) {
		t.Fatalf("Windows uninstall entry %s leaves the fixture: %q, %v", name, entry, err)
	}
	requireRecoveryFile(t, entry, uninstallTestExecutable)
	return args
}

func (f uninstallRecoveryFixture) retry(t *testing.T, name string) error {
	t.Helper()
	// Exercise the current NSIS flag-to-helper mapping without running the
	// registered executable. Without a separate UI decision, only a token in
	// the invocation could select purge, so the token list is also checked.
	args := f.retryCommand(t, name)
	purge := slices.Contains(args[1:], "--purge-lazymind-local-data") && !slices.Contains(args[1:], "--updated")
	return f.remove(t, purge, rejectUnexpectedPurge(t))
}

func (f uninstallRecoveryFixture) keepRecoveryStage(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(f.root, ".install-tmp")
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("expected one retained program-only recovery session: %v, %v", entries, err)
	}
	stage := filepath.Join(parent, entries[0].Name())
	if _, err := loadPrivateSession(stage, f.app); err != nil {
		t.Fatalf("inspect the session created by remove-program: %v", err)
	}
	return stage
}

func (f uninstallRecoveryFixture) purgeRecoveryStage(t *testing.T) string {
	t.Helper()
	// Read the same UTF-16 layout result that NSIS consumes via ReadINIStr.
	encoded, err := os.ReadFile(f.output)
	if err != nil || len(encoded) < 2 || len(encoded)%2 != 0 || binary.LittleEndian.Uint16(encoded) != 0xfeff {
		t.Fatalf("read the layout returned by remove-program: %v", err)
	}
	units := make([]uint16, (len(encoded)-2)/2)
	for index := range units {
		units[index] = binary.LittleEndian.Uint16(encoded[2+index*2:])
	}
	for _, line := range strings.Split(windows.UTF16ToString(units), "\r\n") {
		stage, ok := strings.CutPrefix(line, "stage=")
		if !ok {
			continue
		}
		if !filepath.IsAbs(stage) || !samePath(filepath.Dir(stage), filepath.Join(f.root, ".install-tmp")) {
			t.Fatalf("layout returned a stage outside the fixture: %q", stage)
		}
		if _, err := loadPrivateSession(stage, f.app); err != nil {
			t.Fatalf("inspect the session returned by remove-program: %v", err)
		}
		return stage
	}
	t.Fatal("layout returned by remove-program omitted the stage")
	return ""
}

func (f uninstallRecoveryFixture) completeUninstall(t *testing.T, stage string, purged bool) {
	t.Helper()
	launchers := []string{f.retryCommand(t, "UninstallString")[0], f.retryCommand(t, "QuietUninstallString")[0]}
	// Simulate electron-builder's standard registry removal in the private HKCU.
	for _, key := range []string{teststorage.UninstallKey, teststorage.InstallKey} {
		if err := registry.DeleteKey(registry.CURRENT_USER, key); err != nil {
			t.Fatalf("remove standard uninstall registration %q: %v", key, err)
		}
	}
	foreign := filepath.Join(stage, "unknown.txt")
	writeStorageFile(t, foreign, "unattributed stage file")
	// Completion may refuse the unknown entry or clean only its own files.
	// Neither outcome may change that entry or the selected application data.
	_ = f.complete(stage, purged)
	requireRecoveryFile(t, foreign, "unattributed stage file")
	f.requireUninstallData(t, purged)
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}

	otherSession := filepath.Join(filepath.Dir(stage), "install-unrelated")
	if err := os.Mkdir(otherSession, 0o700); err != nil {
		t.Fatal(err)
	}
	otherFile := filepath.Join(otherSession, "other.txt")
	writeStorageFile(t, otherFile, "another recovery session")
	for attempt := 1; attempt <= 2; attempt++ {
		if err := f.complete(stage, purged); err != nil {
			t.Fatalf("complete uninstall on attempt %d: %v", attempt, err)
		}
		requireRecoveryPathAbsent(t, stage)
		for _, launcher := range launchers {
			requireRecoveryPathAbsent(t, launcher)
		}
		requireRecoveryFile(t, otherFile, "another recovery session")
		f.requireUninstallData(t, purged)
	}
}

func (f uninstallRecoveryFixture) complete(stage string, purged bool) error {
	// Run the same completion functions, with the argv and data-root preflight
	// used by main. The test never enters the early data-deletion path.
	invoke := func(args ...string) error {
		opts, err := parseCommandOptions(args)
		if err != nil {
			return err
		}
		if _, err := resolveInstallerDataRoot(); err != nil {
			return err
		}
		if opts.Command == "verify-uninstalled" {
			return verifyUninstalled()
		}
		return completePurgeSession(opts)
	}
	if err := invoke("verify-uninstalled"); err != nil || !purged {
		return err
	}
	return invoke("purge-local-data", "--install-dir", f.app, "--stage-dir", stage)
}

func (f uninstallRecoveryFixture) requireUninstallData(t *testing.T, purged bool) {
	t.Helper()
	if !purged {
		f.requirePreservedData(t)
		return
	}
	requireRecoveryPathAbsent(t, f.data)
	requireRecoveryRegistryValue(t, teststorage.DataKey, "DataRoot", "")
	requireRecoveryFile(t, filepath.Join(f.root, "neighbor.txt"), "neighboring user file")
}

func (f uninstallRecoveryFixture) requirePreservedData(t *testing.T) {
	t.Helper()
	requireRecoveryRegistryValue(t, teststorage.DataKey, "DataRoot", f.data)
	requireRecoveryFile(t, uninstallTestIdentityPath(f.data), "the existing device identity")
	requireRecoveryFile(t, filepath.Join(f.data, "first.txt"), "first file to delete")
	requireRecoveryFile(t, filepath.Join(f.root, "neighbor.txt"), "neighboring user file")
}

func uninstallTestIdentityPath(data string) string {
	return filepath.Join(data, "Desktop", "credential-device.json")
}

func rejectUnexpectedPurge(t *testing.T) func(string) error {
	t.Helper()
	return func(target string) error {
		t.Fatalf("unexpected deletion of %q", target)
		return nil
	}
}
