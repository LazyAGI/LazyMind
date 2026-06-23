package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	internal "github.com/lazymind/file_watcher/internal"
)

// PathValidator validates filesystem paths.
type PathValidator interface {
	EnsureAllowed(path string) error
	AllowedRoots() []string
}

type pathValidator struct {
	allowedRoots []string
}

func NewPathValidator(allowedRoots []string) PathValidator {
	cleaned := make([]string, 0, len(allowedRoots))
	for _, r := range allowedRoots {
		canonical, err := canonicalize(r)
		if err != nil {
			canonical = filepath.Clean(r)
		}
		cleaned = append(cleaned, canonical)
	}
	return &pathValidator{allowedRoots: cleaned}
}

func (v *pathValidator) EnsureAllowed(path string) error {
	clean, err := canonicalize(path)
	if err != nil {
		return fmt.Errorf("%s: %w", internal.ErrInvalidPath, err)
	}
	if !v.isAllowed(clean) {
		return fmt.Errorf("%s: %s", internal.ErrPathNotAllowed, clean)
	}
	return nil
}

func (v *pathValidator) AllowedRoots() []string {
	return append([]string(nil), v.allowedRoots...)
}

func (v *pathValidator) isAllowed(clean string) bool {
	for _, root := range v.allowedRoots {
		if strings.HasPrefix(clean, root+string(filepath.Separator)) || clean == root {
			return true
		}
	}
	return false
}

// canonicalize applies Clean and Abs, and resolves symlinks where the path exists.
func canonicalize(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}

	if _, err := os.Lstat(abs); err == nil {
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return "", err
		}
		return filepath.Clean(resolved), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	parent := filepath.Dir(abs)
	if parent == abs {
		return abs, nil
	}

	resolvedParent, err := canonicalize(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(abs)), nil
}
