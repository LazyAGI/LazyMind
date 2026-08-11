package service

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalObjectStoreURLUsesPortableFileURI(t *testing.T) {
	root := filepath.Join(t.TempDir(), "objects with space")
	key := "skillv2/ab/blob"
	wantPath := filepath.Join(root, filepath.FromSlash(key))

	rawURL := NewLocalObjectStore(root).URL(key)
	if strings.Contains(rawURL, `\`) {
		t.Fatalf("URL contains Windows path separators: %q", rawURL)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL %q: %v", rawURL, err)
	}
	if parsed.Scheme != "file" || parsed.Host != "" {
		t.Fatalf("URL = %q, want a local file URI", rawURL)
	}
	wantURIPath := filepath.ToSlash(wantPath)
	if filepath.VolumeName(wantPath) != "" && !strings.HasPrefix(wantURIPath, "/") {
		wantURIPath = "/" + wantURIPath
	}
	if parsed.Path != wantURIPath {
		t.Fatalf("URL path = %q, want %q", parsed.Path, wantURIPath)
	}
}
