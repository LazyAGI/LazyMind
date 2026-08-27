package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	internal "github.com/lazymind/file_watcher/internal"
)

// PathValidator validates filesystem paths.
type PathValidator interface {
	EnsureAllowed(path string) error
	AllowedRoots() []string
	ReplaceAllowedRoots(roots []string) ([]string, error)
}

type pathValidator struct {
	mu           sync.RWMutex
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
	v.mu.RLock()
	allowed := v.isAllowed(clean)
	v.mu.RUnlock()
	if !allowed {
		return fmt.Errorf("%s: %s", internal.ErrPathNotAllowed, clean)
	}
	return nil
}

func (v *pathValidator) AllowedRoots() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return append([]string(nil), v.allowedRoots...)
}

func (v *pathValidator) ReplaceAllowedRoots(roots []string) ([]string, error) {
	candidates := make([]string, 0, len(roots))
	for _, root := range roots {
		canonical, err := canonicalize(strings.TrimSpace(root))
		if err != nil {
			return nil, fmt.Errorf("canonicalize allowed root %q: %w", root, err)
		}
		info, err := os.Stat(canonical)
		if err != nil {
			return nil, fmt.Errorf("stat allowed root %q: %w", canonical, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("allowed root is not a directory: %s", canonical)
		}
		candidates = append(candidates, canonical)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return len(candidates[i]) < len(candidates[j])
	})
	cleaned := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		covered := false
		for _, existing := range cleaned {
			if pathWithin(existing, candidate) {
				covered = true
				break
			}
		}
		if !covered {
			cleaned = append(cleaned, candidate)
		}
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("at least one allowed root is required")
	}
	v.mu.Lock()
	v.allowedRoots = cleaned
	v.mu.Unlock()
	return append([]string(nil), cleaned...), nil
}

func (v *pathValidator) isAllowed(clean string) bool {
	for _, root := range v.allowedRoots {
		if pathWithin(root, clean) {
			return true
		}
	}
	return false
}

func pathWithin(root, candidate string) bool {
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
		candidate = strings.ToLower(candidate)
	}
	return candidate == root || strings.HasPrefix(candidate, root+string(filepath.Separator))
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
