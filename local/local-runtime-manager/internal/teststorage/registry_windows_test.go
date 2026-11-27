//go:build windows

package teststorage

import (
	"os"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestMain(m *testing.M) { os.Exit(Run(m)) }

func TestCaseHKCUShadowsBothProductSourcesAndRestoresSuite(t *testing.T) {
	for _, path := range []string{DataKey, InstallKey} {
		key, _, err := registry.CreateKey(suiteRoot, path, registry.ALL_ACCESS|registry.WOW64_64KEY)
		if err != nil {
			t.Fatal(err)
		}
		if err := key.SetStringValue("fixture", "suite"); err != nil {
			key.Close()
			t.Fatal(err)
		}
		key.Close()
	}
	t.Cleanup(func() {
		if err := deleteTree(suiteRoot, "Software"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("isolated case", func(t *testing.T) {
		r := New(t)
		for _, path := range []string{DataKey, InstallKey} {
			key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
			if err == nil {
				key.Close()
				t.Fatalf("case exposed package-level key %q", path)
			}
			if err != registry.ErrNotExist {
				t.Fatalf("opening isolated key %q: %v", path, err)
			}
			r.SetString(path, "fixture", "case")
			assertHKCUValue(t, path, "case")
		}
	})

	for _, path := range []string{DataKey, InstallKey} {
		assertHKCUValue(t, path, "suite")
	}
}

func TestCaseHKCUDoesNotLeakIntoNextCase(t *testing.T) {
	t.Run("first", func(t *testing.T) {
		r := New(t)
		r.SetString(DataKey, "DataRoot", "fixture")
		r.SetDWord(InstallKey, "invalid fixture", 1)
		r.DeleteValue(DataKey, "DataRoot")
	})
	t.Run("second", func(t *testing.T) {
		New(t)
		for _, path := range []string{DataKey, InstallKey} {
			key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
			if err == nil {
				key.Close()
				t.Fatalf("previous case leaked key %q", path)
			}
			if err != registry.ErrNotExist {
				t.Fatalf("opening clean case key %q: %v", path, err)
			}
		}
	})
}

func assertHKCUValue(t *testing.T, path, want string) {
	t.Helper()
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	got, _, err := key.GetStringValue("fixture")
	if err != nil || got != want {
		t.Fatalf("HKCU %q fixture = %q, %v; want %q", path, got, err, want)
	}
}
