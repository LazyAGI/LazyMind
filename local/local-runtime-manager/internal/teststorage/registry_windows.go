//go:build windows

// Package teststorage is test-only registry support. It never resolves an
// application's storage location and must not be imported by production code.
//
// RegOverridePredefKey affects only this process. Tests using this package must
// not start product subprocesses; a test-binary child must run its own TestMain
// isolation before calling application code. New must not be used in parallel
// or nested tests because the HKCU mapping is process-wide.
package teststorage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	DataKey      = `Software\LazyMind\DesktopStorage`
	InstallKey   = `Software\412e5ec7-d7ee-55e2-b3e8-1ea8840a484e`
	UninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\412e5ec7-d7ee-55e2-b3e8-1ea8840a484e`
	AppMarker    = ".lazymind-installed"
	OwnerMarker  = ".lazymind-data-owner"
)

var (
	overridePredefinedKey = windows.NewLazySystemDLL("advapi32.dll").NewProc("RegOverridePredefKey")
	suiteRoot             registry.Key
	caseActive            bool
	mappingMu             sync.Mutex
)

// Run gives every test in this process an empty HKCU, including older tests that
// only isolate HOME/LOCALAPPDATA. It restores the real mapping before cleanup.
func Run(m *testing.M) (code int) {
	path, err := randomKeyName()
	if err != nil {
		fmt.Fprintln(os.Stderr, "teststorage:", err)
		return 1
	}
	realUser, err := registry.OpenKey(registry.CURRENT_USER, "", registry.ALL_ACCESS|registry.WOW64_64KEY)
	if err != nil {
		fmt.Fprintln(os.Stderr, "teststorage: open HKCU:", err)
		return 1
	}
	defer realUser.Close()
	root, existed, err := registry.CreateKey(realUser, path, registry.ALL_ACCESS|registry.WOW64_64KEY)
	if err != nil {
		fmt.Fprintln(os.Stderr, "teststorage: create private root:", err)
		return 1
	}
	if existed {
		root.Close()
		fmt.Fprintln(os.Stderr, "teststorage: private root already exists")
		return 1
	}
	if err := remap(root); err != nil {
		root.Close()
		_ = deleteTree(realUser, path)
		fmt.Fprintln(os.Stderr, "teststorage: isolate HKCU:", err)
		return 1
	}
	suiteRoot = root
	defer func() {
		if err := remap(0); err != nil {
			// Do not delete anything while the mapping's state is uncertain.
			fmt.Fprintln(os.Stderr, "teststorage: restore HKCU:", err)
			code = 1
			return
		}
		suiteRoot = 0
		root.Close()
		if err := deleteTree(realUser, path); err != nil {
			fmt.Fprintln(os.Stderr, "teststorage: remove private root:", err)
			code = 1
		}
	}()
	return m.Run()
}

type Registry struct {
	t    *testing.T
	root registry.Key
}

// New isolates one nonparallel test beneath Run's private root. Fixture writes
// use a concrete handle; application reads use the overridden predefined HKCU.
func New(t *testing.T) *Registry {
	t.Helper()
	mappingMu.Lock()
	defer mappingMu.Unlock()
	if suiteRoot == 0 {
		t.Fatal("teststorage.New requires teststorage.Run in TestMain")
	}
	if caseActive {
		t.Fatal("teststorage.New cannot be nested or used in parallel")
	}
	path, err := randomKeyName()
	if err != nil {
		t.Fatal(err)
	}
	root, existed, err := registry.CreateKey(suiteRoot, path, registry.ALL_ACCESS|registry.WOW64_64KEY)
	if err != nil {
		t.Fatal(err)
	}
	if existed {
		root.Close()
		t.Fatal("teststorage: private test key already exists")
	}
	if err := remap(root); err != nil {
		root.Close()
		_ = deleteTree(suiteRoot, path)
		t.Fatal(err)
	}
	caseActive = true
	t.Cleanup(func() {
		mappingMu.Lock()
		defer mappingMu.Unlock()
		if err := remap(suiteRoot); err != nil {
			t.Errorf("restore package HKCU: %v", err)
			return
		}
		caseActive = false
		root.Close()
		if err := deleteTree(suiteRoot, path); err != nil {
			t.Errorf("remove private test registry: %v", err)
		}
	})
	return &Registry{t: t, root: root}
}

func (r *Registry) SetString(path, name, value string) {
	r.t.Helper()
	key := r.create(path)
	defer key.Close()
	if err := key.SetStringValue(name, value); err != nil {
		r.t.Fatal(err)
	}
}

func (r *Registry) SetDWord(path, name string, value uint32) {
	r.t.Helper()
	key := r.create(path)
	defer key.Close()
	if err := key.SetDWordValue(name, value); err != nil {
		r.t.Fatal(err)
	}
}

func (r *Registry) SetExpandString(path, name, value string) {
	r.t.Helper()
	key := r.create(path)
	defer key.Close()
	if err := key.SetExpandStringValue(name, value); err != nil {
		r.t.Fatal(err)
	}
}

func (r *Registry) DeleteValue(path, name string) {
	r.t.Helper()
	key := r.create(path)
	defer key.Close()
	if err := key.DeleteValue(name); err != nil && err != registry.ErrNotExist {
		r.t.Fatal(err)
	}
}

func (r *Registry) create(path string) registry.Key {
	r.t.Helper()
	key, _, err := registry.CreateKey(r.root, path, registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		r.t.Fatal(err)
	}
	return key
}

func SID(t *testing.T) string {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return user.User.Sid.String()
}

func randomKeyName() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return "LazyMind-Storage-Test-" + hex.EncodeToString(token[:]), nil
}

func remap(key registry.Key) error {
	status, _, _ := overridePredefinedKey.Call(uintptr(registry.CURRENT_USER), uintptr(key))
	if status != 0 {
		return syscall.Errno(status)
	}
	return nil
}

func deleteTree(parent registry.Key, path string) error {
	key, err := registry.OpenKey(parent, path, registry.ALL_ACCESS|registry.WOW64_64KEY)
	if err != nil {
		return err
	}
	names, err := key.ReadSubKeyNames(-1)
	if err == nil {
		for _, name := range names {
			if err = deleteTree(key, name); err != nil {
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
