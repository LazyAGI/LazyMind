// Package staticstorage connects domain-owned storage namespaces to the static
// file transport. Policies are registered at startup, including when a domain's
// write feature is disabled, so historical URLs retain their access controls.
package staticstorage

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
)

type Namespace struct {
	Prefix    string
	Root      func() string
	Authorize func(context.Context, string, string) bool
}

var registry struct {
	sync.RWMutex
	items []Namespace
}

func Register(n Namespace) {
	if n.Prefix == "" || n.Root == nil || n.Authorize == nil {
		panic("incomplete storage namespace")
	}
	registry.Lock()
	defer registry.Unlock()
	for _, existing := range registry.items {
		if existing.Prefix == n.Prefix {
			panic("duplicate storage namespace")
		}
	}
	registry.items = append(registry.items, n)
}

func namespaces() []Namespace {
	registry.RLock()
	defer registry.RUnlock()
	return append([]Namespace(nil), registry.items...)
}

func RelativePath(path string) string {
	for _, n := range namespaces() {
		root := filepath.Clean(n.Root())
		candidate := filepath.Clean(path)
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = resolved
		}
		if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
			candidate = resolved
		}
		rel, err := filepath.Rel(root, candidate)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return n.Prefix + filepath.ToSlash(rel)
		}
	}
	return ""
}

func Resolve(rel string) (string, bool) {
	for _, n := range namespaces() {
		if strings.HasPrefix(rel, n.Prefix) {
			inner := strings.TrimPrefix(rel, n.Prefix)
			if inner == "" || strings.Contains(inner, "\\") || filepath.IsAbs(inner) || filepath.ToSlash(filepath.Clean(inner)) != inner || inner == ".." || strings.HasPrefix(inner, "../") {
				return "", true
			}
			return filepath.Join(n.Root(), filepath.FromSlash(inner)), true
		}
	}
	return "", false
}

func Authorized(ctx context.Context, rel, userID string) bool {
	for _, n := range namespaces() {
		if strings.HasPrefix(rel, n.Prefix) {
			path, _ := Resolve(rel)
			return path != "" && n.Authorize(ctx, strings.TrimPrefix(rel, n.Prefix), userID)
		}
	}
	return true
}
