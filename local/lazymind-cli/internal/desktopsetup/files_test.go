package desktopsetup

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFilesRestoreOriginalAndPreserveExternalChanges(t *testing.T) {
	root := t.TempDir()
	f, err := Load(filepath.Join(root, "ownership.json"))
	if err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(root, "existing")
	external := filepath.Join(root, "external")
	created := filepath.Join(root, "created")
	if err = os.WriteFile(original, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{original, external, created} {
		if err = f.Put(path, []byte("managed"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// A second installation retains the first backup.
	if err = f.Put(original, []byte("updated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = f.Check(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(external, []byte("user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = f.Check(); err == nil {
		t.Fatal("missed external modification")
	}
	if err = f.Put(external, []byte("overwrite"), 0600); err == nil {
		t.Fatal("overwrote external modification")
	}
	f, err = Load(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Restore(); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{original: "original", external: "user edit"} {
		body, err := os.ReadFile(path)
		if err != nil || string(body) != want {
			t.Fatalf("%s: %s %v", path, body, err)
		}
	}
	if _, err = os.Stat(created); !os.IsNotExist(err) {
		t.Fatal("left created startup file")
	}
	if err = f.Restore(); err != nil {
		t.Fatal(err)
	}
}

func TestFilesRecoverMissingManagedFile(t *testing.T) {
	root := t.TempDir()
	f, _ := Load(filepath.Join(root, "ownership.json"))
	path := filepath.Join(root, "launcher")
	if err := f.Put(path, []byte("launch"), 0700); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path)
	if err := f.Check(); err == nil {
		t.Fatal("missing launcher passed")
	}
	if err := f.Put(path, []byte("launch"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := f.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestFilesCheckLauncherPermissions(t *testing.T) {
	root := t.TempDir()
	f, err := Load(filepath.Join(root, "ownership.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "launcher")
	if err := f.Put(path, []byte("launch"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	err = f.Check()
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatalf("Windows launcher rejected for POSIX permissions: %v", err)
		}
	} else if err == nil {
		t.Fatal("non-executable POSIX launcher passed")
	}
	if err := f.Put(path, []byte("launch"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := f.Check(); err != nil {
		t.Fatalf("repaired launcher rejected: %v", err)
	}
}
