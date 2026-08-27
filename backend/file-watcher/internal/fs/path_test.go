package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureAllowedRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	allowedRoot := filepath.Join(base, "allowed")
	outsideRoot := filepath.Join(base, "outside")
	if err := os.MkdirAll(allowedRoot, 0o755); err != nil {
		t.Fatalf("mkdir allowed root: %v", err)
	}
	if err := os.MkdirAll(outsideRoot, 0o755); err != nil {
		t.Fatalf("mkdir outside root: %v", err)
	}

	targetFile := filepath.Join(outsideRoot, "secret.txt")
	if err := os.WriteFile(targetFile, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write target file: %v", err)
	}

	linkPath := filepath.Join(allowedRoot, "escape")
	if err := os.Symlink(outsideRoot, linkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	validator := NewPathValidator([]string{allowedRoot})
	if err := validator.EnsureAllowed(filepath.Join(linkPath, "secret.txt")); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestEnsureAllowedAcceptsDirectChild(t *testing.T) {
	t.Parallel()

	allowedRoot := t.TempDir()
	child := filepath.Join(allowedRoot, "docs", "a.txt")
	if err := os.MkdirAll(filepath.Dir(child), 0o755); err != nil {
		t.Fatalf("mkdir child dir: %v", err)
	}
	if err := os.WriteFile(child, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write child file: %v", err)
	}

	validator := NewPathValidator([]string{allowedRoot})
	if err := validator.EnsureAllowed(child); err != nil {
		t.Fatalf("expected allowed child path, got error: %v", err)
	}
}

func TestReplaceAllowedRootsAppliesAtomicallyAndCollapsesChildren(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	child := filepath.Join(second, "child")
	for _, root := range []string{first, child} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
	}
	validator := NewPathValidator([]string{first})

	roots, err := validator.ReplaceAllowedRoots([]string{first, second, child})
	if err != nil {
		t.Fatalf("replace roots: %v", err)
	}
	wantFirst, _ := filepath.EvalSymlinks(first)
	wantSecond, _ := filepath.EvalSymlinks(second)
	if len(roots) != 2 || roots[0] != wantFirst || roots[1] != wantSecond {
		t.Fatalf("roots = %#v", roots)
	}
	if err := validator.EnsureAllowed(filepath.Join(child, "a.md")); err != nil {
		t.Fatalf("new child should be allowed: %v", err)
	}

	if _, err := validator.ReplaceAllowedRoots([]string{filepath.Join(base, "missing")}); err == nil {
		t.Fatal("missing replacement root should fail")
	}
	if got := validator.AllowedRoots(); len(got) != 2 || got[1] != wantSecond {
		t.Fatalf("failed replacement changed roots: %#v", got)
	}
}
