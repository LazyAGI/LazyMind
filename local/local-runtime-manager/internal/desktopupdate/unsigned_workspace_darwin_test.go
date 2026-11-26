//go:build darwin && lazymind_unsigned_test

package desktopupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnsignedWorkspaceSurvivesInterruptedRenameAndCleansOnlyItsOwnStage(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tx := strings.Repeat("a", 32)
	p := UnsignedTestPaths{Application: filepath.Join(base, "Applications", "LazyMind.app"), Updater: filepath.Join(base, "updater")}
	if err = os.MkdirAll(p.Application, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(p.Application, "source"), []byte("A"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := OpenUnsignedWorkspace(p, tx)
	if err != nil {
		t.Fatal(err)
	}
	first := w.Directory
	if err = os.MkdirAll(filepath.Join(first, "undo"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(p.Application, filepath.Join(first, "undo", "LazyMind.app")); err != nil {
		t.Fatal(err)
	}
	w.Close() // a dead helper releases the lock; the durable locator remains
	w, err = OpenUnsignedWorkspace(p, tx)
	if err != nil {
		t.Fatal(err)
	}
	if w.Directory != first {
		t.Fatal("retry abandoned the original same-volume directory")
	}
	if err = RestoreUnsignedWorkspaceSource(w, p.Application); err != nil {
		t.Fatal(err)
	}
	w.Close()
	if raw, e := os.ReadFile(filepath.Join(p.Application, "source")); e != nil || string(raw) != "A" {
		t.Fatalf("source was not recovered: %q %v", raw, e)
	}
	stage := filepath.Join(p.Updater, "installs", tx)
	neighbor := first + ".neighbor"
	if err = os.Rename(first, neighbor); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(first, 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(first, "unrelated")
	if err = os.WriteFile(foreign, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = removeUnsignedWorkspace(stage, tx, p.Application); err == nil {
		t.Fatal("cleanup accepted a substituted directory")
	}
	if raw, e := os.ReadFile(foreign); e != nil || string(raw) != "other" {
		t.Fatal("cleanup touched a substituted directory")
	}
	if err = os.Rename(first, first+".foreign"); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(neighbor, first); err != nil {
		t.Fatal(err)
	}
	if err = removeUnsignedWorkspace(stage, tx, p.Application); err != nil {
		t.Fatal(err)
	}
	if _, e := os.Lstat(first); !os.IsNotExist(e) {
		t.Fatalf("transaction workspace not retired: %v", e)
	}
	if _, e := os.Stat(filepath.Join(first+".foreign", "unrelated")); e != nil {
		t.Fatal(e)
	}
}
