package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/teststorage"
	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/winprocess"
)

// Override HKCU in every test-binary process, including the existing process
// helper tests. Never launch the production maintenance executable from here:
// RegOverridePredefKey is not inherited by a child process.
func TestMain(m *testing.M) {
	flag.Parse()
	if flag.Lookup("test.run").Value.String() != "^TestStorage" {
		// The older force-stop tests scan real processes. HKCU remapping does
		// not isolate that inventory, so the full suite requires a deliberately
		// provisioned disposable account, not just a temporary directory.
		account, err := user.Current()
		if err != nil || os.Getenv("LAZYMIND_TEST_DISPOSABLE_ACCOUNT_SID") == "" ||
			os.Getenv("LAZYMIND_TEST_DISPOSABLE_ACCOUNT_SID") != account.Uid {
			fmt.Fprintln(os.Stderr, "Refusing process-stop tests: use -run '^TestStorage', or explicitly authorize the disposable account via LAZYMIND_TEST_DISPOSABLE_ACCOUNT_SID.")
			os.Exit(1)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		inventory, err := winprocess.Snapshot(ctx)
		cancel()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Cannot establish safe process inventory:", err)
			os.Exit(1)
		}
		for _, process := range inventory {
			name := filepath.Base(winprocess.Text(process.ExecutablePath))
			if strings.EqualFold(name, "LazyMind.exe") || strings.EqualFold(name, "LazyMindApp.exe") {
				fmt.Fprintln(os.Stderr, "Refusing process-stop tests while a real LazyMind process exists.")
				os.Exit(1)
			}
		}
	}
	os.Exit(teststorage.Run(m))
}

func storageFixture(t *testing.T) (*teststorage.Registry, string, string, string) {
	t.Helper()
	r := teststorage.New(t)
	base := t.TempDir()
	legacy := filepath.Join(base, "Local", "LazyMind")
	root := filepath.Join(base, "QA Tools", "中文")
	app, data := filepath.Join(root, "App"), filepath.Join(root, "Data")
	for _, dir := range []string{legacy, app, data} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeStorageFile(t, filepath.Join(app, teststorage.AppMarker), "")
	writeStorageFile(t, filepath.Join(data, teststorage.OwnerMarker), teststorage.SID(t))
	r.SetString(teststorage.DataKey, "DataRoot", data)
	r.SetString(teststorage.InstallKey, "InstallLocation", app)
	return r, legacy, app, data
}

func writeStorageFile(t *testing.T, name, value string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

// This is a native Windows path/registry unit test, not a real second-volume
// installation. The separate lifecycle script checks a genuinely different D.
func TestStorageRegisteredCustomAppCanBeAuthenticated(t *testing.T) {
	_, legacy, app, _ := storageFixture(t)
	got, err := trustedInstallDir(legacy, app)
	if err != nil || !filepath.IsAbs(got) || got != app {
		t.Fatalf("registered App should be authenticated: got=%q err=%v", got, err)
	}
}

func TestStoragePlanReusesRequestedStorageRoot(t *testing.T) {
	for _, mode := range []string{"installed", "restore"} {
		t.Run(mode, func(t *testing.T) {
			registry, _, app, data := storageFixture(t)
			if mode == "restore" {
				registry.DeleteValue(teststorage.InstallKey, "InstallLocation")
			}
			root := filepath.Dir(app)
			for _, requested := range []string{root, app} {
				layout, err := planInstallation(commandOptions{InstallDir: requested, RequestedDir: requested}, false)
				if err != nil || layout.Mode != mode || layout.App != app || layout.Data != data || layout.Storage != root {
					t.Fatalf("plan for %q = %+v, %v; want %s at %q", requested, layout, err, mode, root)
				}
			}
			other := filepath.Join(filepath.Dir(root), "other-storage")
			if _, err := planInstallation(commandOptions{InstallDir: other, RequestedDir: other}, false); err == nil {
				t.Fatal("plan accepted a different storage root")
			}
		})
	}
}

func TestStorageUnregisteredCandidateDoesNotAuthorizeProcesses(t *testing.T) {
	r, legacy, app, _ := storageFixture(t)
	r.DeleteValue(teststorage.InstallKey, "InstallLocation")
	if _, err := trustedInstallDir(legacy, app); err == nil {
		t.Fatal("a candidate path became a trusted executable root before product registration")
	}
	// Do not feed a rejected candidate back to compactRoots. This must stay a
	// pure classification check and must not kill any actual process.
	process := processInfo(910001, filepath.Join(app, "unrelated.exe"))
	if reason, matched := matchLazyMindProcess(process, processRecord{}, false, true, nil); matched {
		t.Fatalf("unregistered candidate process was selected: %s", reason)
	}
}

func TestStorageUnownedSameNameExecutableIsNotLazyMind(t *testing.T) {
	teststorage.New(t)
	process := processInfo(910002, filepath.Join(t.TempDir(), "LazyMind.exe"))
	if reason, matched := matchLazyMindProcess(process, processRecord{}, false, true, nil); matched {
		t.Fatalf("same-session filename alone authorized an unrelated process: %s", reason)
	}
}

func TestStorageInvalidOwnershipDoesNotAuthenticateApp(t *testing.T) {
	for _, invalid := range []string{"wrong owner", "missing owner", "missing app marker", "conflicting location", "wrong value type"} {
		t.Run(invalid, func(t *testing.T) {
			r, legacy, app, data := storageFixture(t)
			switch invalid {
			case "wrong owner":
				writeStorageFile(t, filepath.Join(data, teststorage.OwnerMarker), "S-1-0-0")
			case "missing owner":
				if err := os.Remove(filepath.Join(data, teststorage.OwnerMarker)); err != nil {
					t.Fatal(err)
				}
			case "missing app marker":
				if err := os.Remove(filepath.Join(app, teststorage.AppMarker)); err != nil {
					t.Fatal(err)
				}
			case "conflicting location":
				r.SetString(teststorage.InstallKey, "InstallLocation", filepath.Dir(app))
			case "wrong value type":
				r.SetDWord(teststorage.DataKey, "DataRoot", 42)
			}
			if _, err := trustedInstallDir(legacy, app); err == nil {
				t.Fatalf("invalid layout authenticated App: %s", invalid)
			}
		})
	}
}

func TestStorageLegacyFixedRootStillAuthenticatesWithoutNewRecord(t *testing.T) {
	teststorage.New(t)
	legacy := filepath.Join(t.TempDir(), "Local", "LazyMind")
	app := filepath.Join(filepath.Dir(legacy), "Programs", "LazyMind")
	if got, err := trustedInstallDir(legacy, app); err != nil || got != app {
		t.Fatalf("legacy authentication changed: got=%q err=%v", got, err)
	}
}

func TestStoragePurgeRemovesOnlyRegisteredOwnedData(t *testing.T) {
	_, _, app, data := storageFixture(t)
	appWitness := filepath.Join(app, "keep-program.txt")
	sibling := filepath.Join(filepath.Dir(app), "user-document.txt")
	writeStorageFile(t, appWitness, "program")
	writeStorageFile(t, sibling, "user document")
	writeStorageFile(t, filepath.Join(data, "attachment.txt"), "owned test attachment")
	if err := purgeLocalData(context.Background(), data); err != nil {
		t.Fatalf("purging registered owned Data failed: %v", err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("Data remains or unexpected stat error: %v", err)
	}
	for file, want := range map[string]string{appWitness: "program", sibling: "user document"} {
		if got, err := os.ReadFile(file); err != nil || string(got) != want {
			t.Fatalf("purge crossed Data boundary: file=%q content=%q err=%v", file, got, err)
		}
	}
}

func TestStoragePurgeRejectsWrongOwnerWithoutChangingData(t *testing.T) {
	_, _, _, data := storageFixture(t)
	writeStorageFile(t, filepath.Join(data, teststorage.OwnerMarker), "S-1-0-0")
	witness := filepath.Join(data, "keep.txt")
	writeStorageFile(t, witness, "keep")
	if err := purgeLocalData(context.Background(), data); err == nil {
		t.Fatal("purge accepted another user's ownership marker")
	}
	if got, err := os.ReadFile(witness); err != nil || string(got) != "keep" {
		t.Fatalf("rejected purge modified Data: content=%q err=%v", got, err)
	}
}

func TestStoragePurgeRejectsJunctionWithoutTouchingItsTarget(t *testing.T) {
	r, _, _, data := storageFixture(t)
	if err := os.RemoveAll(data); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	witness := filepath.Join(target, "keep.txt")
	writeStorageFile(t, witness, "outside Data")
	writeStorageFile(t, filepath.Join(target, teststorage.OwnerMarker), teststorage.SID(t))
	// Junctions do not require Developer Mode or symbolic-link privilege.
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/c", "mklink", "/J", data, target)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create test-only junction: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = os.Remove(data) })
	r.SetString(teststorage.DataKey, "DataRoot", data)
	if err := purgeLocalData(context.Background(), data); err == nil {
		t.Fatal("purge accepted a reparse-point Data root")
	}
	if got, err := os.ReadFile(witness); err != nil || string(got) != "outside Data" {
		t.Fatalf("purge followed the junction: content=%q err=%v", got, err)
	}
}

func TestStoragePreflightRejectsFinalPathThatCannotFitPayload(t *testing.T) {
	teststorage.New(t)
	base := t.TempDir()
	witness := filepath.Join(base, "unrelated.txt")
	writeStorageFile(t, witness, "keep")
	err := installerPreflight(commandOptions{
		InstallDir: filepath.Join(base, "QA Tools", "中文", "App"), TempDir: base,
		MinimumFreeSpaceMB: 1, MaximumRelativeLength: 260,
	})
	if err == nil {
		t.Fatal("final target path was accepted although a packaged path cannot fit")
	}
	if !strings.Contains(err.Error(), "installation path is too long") {
		t.Fatalf("expected the payload path-length rejection, not an unrelated setup failure: %v", err)
	}
	if got, err := os.ReadFile(witness); err != nil || string(got) != "keep" {
		t.Fatalf("preflight changed an unrelated file: content=%q err=%v", got, err)
	}
}
