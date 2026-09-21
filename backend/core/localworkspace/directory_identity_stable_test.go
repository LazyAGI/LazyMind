//go:build darwin || windows

package localworkspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentIdentityUsesOpenedObjectAndRejectsReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "workspace")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	before, err := observeIdentityPath(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := platformDirectoryIdentity(path, before)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := openedDirectoryIdentity(file)
	if err != nil || opened != first {
		t.Fatalf("path=%s opened=%s err=%v", first, opened, err)
	}
	file.Close()
	if _, err := openedDirectoryIdentity(file); err == nil {
		t.Fatal("closed handle accepted")
	}
	old := filepath.Join(root, "previous")
	if err := os.Rename(path, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := platformDirectoryIdentity(path, before); err == nil {
		t.Fatal("replacement accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(old, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := platformDirectoryIdentity(path, before); err == nil {
		t.Fatal("symlink replacement accepted")
	}
}
