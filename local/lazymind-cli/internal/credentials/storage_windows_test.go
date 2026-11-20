//go:build windows

package credentials

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"lazymind/agentconnector/internal/connectorconfig"
)

const (
	storageTestDataKey = `Software\LazyMind\DesktopStorage`
	// app-builder-lib 24.13.3 UUIDv5 for ai.lazymind.desktop; x64 NSIS uses RegView 64.
	storageTestInstallKey = `Software\412e5ec7-d7ee-55e2-b3e8-1ea8840a484e`
)

// RegOverridePredefKey is process-local. It isolates both product keys for all
// credentials tests, including legacy tests that only replace LOCALAPPDATA.
// These tests must not run parallel or launch registry-reading child processes.
func TestMain(m *testing.M) {
	os.Exit(runWithPrivateStorageRegistry(m))
}

func runWithPrivateStorageRegistry(m *testing.M) (code int) {
	code = 1
	home, err := os.MkdirTemp("", "lazymind-credentials-storage-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create private test home:", err)
		return
	}
	defer os.RemoveAll(home)
	for name, value := range map[string]string{
		"HOME": home, "USERPROFILE": home, "LAZYMIND_HOME": filepath.Join(home, ".lazymind"),
		"LOCALAPPDATA": filepath.Join(home, "AppData", "Local"), "APPDATA": filepath.Join(home, "AppData", "Roaming"),
		"LAZYMIND_CONNECTOR_CONFIG": "", "LAZYMIND_RUNTIME_PROFILE": "", "LAZYMIND_SERVER_URL": "",
		"LAZYMIND_RUNTIME_STATE_FILE": "", "LAZYMIND_DESKTOP_DATA_ROOT": "", "LAZYMIND_RUNTIME_ROOT": "",
	} {
		if err := os.Setenv(name, value); err != nil {
			fmt.Fprintln(os.Stderr, "isolate test environment:", err)
			return
		}
	}
	realHKCU, err := registry.OpenKey(registry.CURRENT_USER, "", registry.ALL_ACCESS|registry.WOW64_64KEY)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open HKCU for isolated tests:", err)
		return
	}
	defer realHKCU.Close()
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		fmt.Fprintln(os.Stderr, "name private HKCU:", err)
		return
	}
	privatePath := `Software\LazyMindStorageTests-` + hex.EncodeToString(nonce[:])
	privateHKCU, existed, err := registry.CreateKey(realHKCU, privatePath, registry.ALL_ACCESS|registry.WOW64_64KEY)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create private HKCU:", err)
		return
	}
	if existed {
		privateHKCU.Close()
		fmt.Fprintln(os.Stderr, "private HKCU unexpectedly existed; refusing to use or delete it")
		return
	}
	defer func() {
		privateHKCU.Close()
		if err := deleteStorageTestKey(realHKCU, privatePath); err != nil {
			fmt.Fprintln(os.Stderr, "remove private HKCU:", err)
			code = 1
		}
	}()
	override := windows.NewLazySystemDLL("advapi32.dll").NewProc("RegOverridePredefKey")
	if err := override.Find(); err != nil {
		fmt.Fprintln(os.Stderr, "resolve RegOverridePredefKey:", err)
		return
	}
	if status, _, _ := override.Call(uintptr(registry.CURRENT_USER), uintptr(privateHKCU)); status != 0 {
		fmt.Fprintln(os.Stderr, "override HKCU:", syscall.Errno(status))
		return
	}
	code = m.Run()
	if status, _, _ := override.Call(uintptr(registry.CURRENT_USER), 0); status != 0 {
		// Leave the private key intact if the mapping could not be restored.
		fmt.Fprintln(os.Stderr, "restore HKCU:", syscall.Errno(status))
		privatePath = ""
		return 1
	}
	return
}

func deleteStorageTestKey(parent registry.Key, path string) error {
	if path == "" {
		return nil
	}
	key, err := registry.OpenKey(parent, path, registry.ALL_ACCESS|registry.WOW64_64KEY)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return err
	}
	names, err := key.ReadSubKeyNames(-1)
	if err == nil {
		for _, name := range names {
			if err = deleteStorageTestKey(key, name); err != nil {
				break
			}
		}
	}
	key.Close()
	if err != nil {
		return err
	}
	return registry.DeleteKey(parent, path)
}

type storageFixture struct {
	dataRoot, appRoot, legacyRoot, connectorHome string
}

func newStorageFixture(t *testing.T) storageFixture {
	t.Helper()
	for _, key := range []string{storageTestDataKey, storageTestInstallKey} {
		if err := deleteStorageTestKey(registry.CURRENT_USER, key); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, key := range []string{storageTestDataKey, storageTestInstallKey} {
			if err := deleteStorageTestKey(registry.CURRENT_USER, key); err != nil {
				t.Error(err)
			}
		}
	})
	base := t.TempDir()
	local := filepath.Join(base, "legacy-local")
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("USERPROFILE", filepath.Join(base, "user"))
	for _, name := range []string{
		"LAZYMIND_CONNECTOR_CONFIG", "LAZYMIND_RUNTIME_PROFILE", "LAZYMIND_SERVER_URL",
		"LAZYMIND_RUNTIME_STATE_FILE", "LAZYMIND_DESKTOP_DATA_ROOT", "LAZYMIND_RUNTIME_ROOT",
	} {
		t.Setenv(name, "")
	}
	f := storageFixture{
		dataRoot:      filepath.Join(base, "Selected 中文 Tools", "Data"),
		appRoot:       filepath.Join(base, "Selected 中文 Tools", "App"),
		legacyRoot:    filepath.Join(local, "LazyMind"),
		connectorHome: filepath.Join(base, "connector"),
	}
	for _, dir := range []string{f.dataRoot, f.appRoot} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string]string{
		filepath.Join(f.appRoot, ".lazymind-installed"):   "",
		filepath.Join(f.dataRoot, ".lazymind-data-owner"): user.User.Sid.String() + "\n",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func setStorageTestString(t *testing.T, path, name, value string) {
	t.Helper()
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS|registry.WOW64_64KEY)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	if err := key.SetStringValue(name, value); err != nil {
		t.Fatal(err)
	}
}

func (f storageFixture) register(t *testing.T, withProgram bool) {
	t.Helper()
	setStorageTestString(t, storageTestDataKey, "DataRoot", f.dataRoot)
	if withProgram {
		setStorageTestString(t, storageTestInstallKey, "InstallLocation", f.appRoot)
	}
}

func writeStorageTestState(t *testing.T, root string, port int) string {
	t.Helper()
	path := filepath.Join(root, "state", "runtime-state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	contents := fmt.Sprintf(`{"config":{"localProxy":{"port":%d}}}`, port)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func storageTestStore(t *testing.T, home string, requested *[]string) *Store {
	t.Helper()
	store, err := NewStore(home, "")
	if err != nil {
		t.Fatal(err)
	}
	store.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*requested = append(*requested, request.URL.String())
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: request,
			Body: io.NopCloser(strings.NewReader(`{"token":"synthetic-access","refreshToken":"synthetic-refresh","username":"alice","tenantId":"tenant"}`))}, nil
	})}
	return store
}

func TestWindowsStorageDiscoversRegisteredStateWithoutInheritedRoot(t *testing.T) {
	for _, mode := range []string{"unmanaged", "managed", "data-kept-after-uninstall"} {
		t.Run(mode, func(t *testing.T) {
			f := newStorageFixture(t)
			f.register(t, mode != "data-kept-after-uninstall")
			if mode == "data-kept-after-uninstall" {
				if err := os.RemoveAll(f.appRoot); err != nil {
					t.Fatal(err)
				}
			}
			writeStorageTestState(t, f.dataRoot, 28473)
			writeStorageTestState(t, f.legacyRoot, 28474)
			if mode == "managed" {
				configPath, err := connectorconfig.Save(connectorconfig.Config{Home: f.connectorHome, Provider: "codex", HostID: "test-host"})
				if err != nil {
					t.Fatal(err)
				}
				t.Setenv(connectorconfig.EnvironmentKey, configPath)
				// An already-open Agent has no newly inherited Desktop root.
				// Its inherited state-file override must remain filtered.
				t.Setenv("LAZYMIND_RUNTIME_STATE_FILE", writeStorageTestState(t, filepath.Join(t.TempDir(), "untrusted"), 28475))
			}
			var requested []string
			store := storageTestStore(t, f.connectorHome, &requested)
			got, err := store.ServerURL(context.Background())
			want := "http://127.0.0.1:28473"
			if err != nil || got != want {
				t.Fatalf("ServerURL=%q err=%v, want registered state %q; requests=%v", got, err, want, requested)
			}
			if len(requested) != 1 || requested[0] != want+authPath+"/health" {
				t.Fatalf("probed a legacy, inherited or fixed-port server: %v", requested)
			}
		})
	}
}

func TestWindowsStorageInvalidLocationDoesNotFallBack(t *testing.T) {
	for _, fault := range []string{"empty", "wrong-type", "relative", "missing-directory", "missing-owner", "wrong-owner", "program-mismatch", "marked-app-without-DataRoot"} {
		t.Run(fault, func(t *testing.T) {
			f := newStorageFixture(t)
			f.register(t, true)
			writeStorageTestState(t, f.legacyRoot, 28474)
			switch fault {
			case "empty":
				setStorageTestString(t, storageTestDataKey, "DataRoot", "")
			case "wrong-type":
				key, err := registry.OpenKey(registry.CURRENT_USER, storageTestDataKey, registry.SET_VALUE|registry.WOW64_64KEY)
				if err != nil {
					t.Fatal(err)
				}
				err = key.SetDWordValue("DataRoot", 7)
				key.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "relative":
				setStorageTestString(t, storageTestDataKey, "DataRoot", `relative\Data`)
			case "missing-directory":
				if err := os.RemoveAll(f.dataRoot); err != nil {
					t.Fatal(err)
				}
			case "missing-owner":
				if err := os.Remove(filepath.Join(f.dataRoot, ".lazymind-data-owner")); err != nil {
					t.Fatal(err)
				}
			case "wrong-owner":
				user, err := windows.GetCurrentProcessToken().GetTokenUser()
				if err != nil {
					t.Fatal(err)
				}
				otherSID := user.User.Sid.String() + "-9999\n"
				if err := os.WriteFile(filepath.Join(f.dataRoot, ".lazymind-data-owner"), []byte(otherSID), 0o600); err != nil {
					t.Fatal(err)
				}
			case "program-mismatch":
				setStorageTestString(t, storageTestInstallKey, "InstallLocation", t.TempDir())
			case "marked-app-without-DataRoot":
				if err := deleteStorageTestKey(registry.CURRENT_USER, storageTestDataKey); err != nil {
					t.Fatal(err)
				}
			}
			for _, operation := range []string{"ServerURL", "AccessToken", "SaveDesktop"} {
				t.Run(operation, func(t *testing.T) {
					var requested []string
					store := storageTestStore(t, t.TempDir(), &requested)
					var err error
					switch operation {
					case "ServerURL":
						_, err = store.ServerURL(context.Background())
					case "AccessToken":
						_, err = store.AccessToken(context.Background())
					case "SaveDesktop":
						err = store.SaveDesktop(Credentials{ServerURL: "http://127.0.0.1:28473", AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh"}, "")
					}
					if err == nil || errors.Is(err, ErrAuthenticationRequired) {
						t.Errorf("%s must surface the storage-location error, got %v", operation, err)
					}
					if len(requested) != 0 {
						t.Errorf("%s requested fallback servers despite invalid location: %v", operation, requested)
					}
					if _, err := os.Stat(store.path()); !errors.Is(err, os.ErrNotExist) {
						t.Errorf("%s wrote replacement credentials: %v", operation, err)
					}
				})
			}
		})
	}
}

func TestWindowsStorageLegacyAndExplicitStateRemainSupported(t *testing.T) {
	for _, mode := range []string{"legacy", "explicit-state"} {
		t.Run(mode, func(t *testing.T) {
			f := newStorageFixture(t)
			writeStorageTestState(t, f.legacyRoot, 28474)
			port := 28474
			if mode == "explicit-state" {
				// This existing unmanaged override precedes automatic discovery.
				setStorageTestString(t, storageTestDataKey, "DataRoot", "")
				t.Setenv("LAZYMIND_RUNTIME_STATE_FILE", writeStorageTestState(t, filepath.Join(t.TempDir(), "explicit"), 28476))
				port = 28476
			}
			var requested []string
			store := storageTestStore(t, f.connectorHome, &requested)
			want := fmt.Sprintf("http://127.0.0.1:%d", port)
			if got, err := store.ServerURL(context.Background()); err != nil || got != want {
				t.Fatalf("ServerURL=%q err=%v, want %q", got, err, want)
			}
			if len(requested) != 1 || requested[0] != want+authPath+"/health" {
				t.Fatalf("legacy/explicit discovery changed: %v", requested)
			}
		})
	}
}

func TestWindowsStoragePreferredServerIgnoresBrokenLocalRegistration(t *testing.T) {
	f := newStorageFixture(t)
	setStorageTestString(t, storageTestDataKey, "DataRoot", "")
	writeStorageTestState(t, f.legacyRoot, 28474)
	var requested []string
	store := storageTestStore(t, f.connectorHome, &requested)
	preferred := "http://127.0.0.1:28477"
	value, err := store.bootstrapLocalSessionForServerUnlocked(context.Background(), preferred, true)
	if err != nil || value.ServerURL != preferred || value.AccessToken != "synthetic-access" {
		t.Fatalf("explicit server recovery was blocked by unrelated storage: value=%#v err=%v", value, err)
	}
	if len(requested) != 1 || requested[0] != preferred+localSessionPath+"?force=true" {
		t.Fatalf("explicit recovery requested another server: %v", requested)
	}
}

func TestWindowsStorageDesktopIdentityUsesDiscoveredDataRoot(t *testing.T) {
	f := newStorageFixture(t)
	f.register(t, true)
	var requested []string
	store := storageTestStore(t, f.connectorHome, &requested)
	value := Credentials{ServerURL: "http://127.0.0.1:28473", Username: "alice", AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh"}
	if err := store.SaveDesktop(value, ""); err != nil {
		t.Fatal(err)
	}
	discovered, err := store.loadUnlocked()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDesktop(value, f.dataRoot); err != nil {
		t.Fatal(err)
	}
	explicit, err := store.loadUnlocked()
	if err != nil || discovered.LocalRuntimeID == "" || discovered.LocalRuntimeID != explicit.LocalRuntimeID {
		t.Fatalf("discovered identity must bind actual DataRoot: discovered=%q explicit=%q err=%v", discovered.LocalRuntimeID, explicit.LocalRuntimeID, err)
	}
	if len(requested) != 0 {
		t.Fatalf("SaveDesktop unexpectedly made HTTP requests: %v", requested)
	}
	// Ensure the saved payload retains the existing wire field used by Agents.
	body, err := os.ReadFile(store.path())
	var payload map[string]any
	if err != nil || json.Unmarshal(body, &payload) != nil || payload["local_runtime_id"] != explicit.LocalRuntimeID {
		t.Fatal("actual-root identity was not persisted in local_runtime_id")
	}
}
