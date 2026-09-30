package showcase

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lazymind/core/systemdeps"
)

type assetFile struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"sizeBytes"`
}
type assetBundle struct {
	Filename    string               `json:"filename"`
	URL         string               `json:"url"`
	FallbackURL string               `json:"fallbackUrl"`
	SHA256      string               `json:"sha256"`
	Size        int64                `json:"sizeBytes"`
	Files       map[string]assetFile `json:"files"`
}
type assetDownloads struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Bundles       map[string]assetBundle `json:"bundles"`
	Local         map[string]assetFile   `json:"local"`
}

var assetLocks sync.Map

func validAssetFile(name string) bool {
	return name != "" && path.Clean(name) == name && !strings.HasPrefix(name, "/") &&
		!strings.Contains(name, "\\") && !strings.Contains(name, ":") && !strings.HasPrefix(name, "../") && name != ".."
}

func assetMatches(file string, expected assetFile) bool {
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() || info.Size() != expected.Size {
		return false
	}
	stream, err := os.Open(file)
	if err != nil {
		return false
	}
	defer stream.Close()
	hash := sha256.New()
	_, err = io.Copy(hash, stream)
	return err == nil && hex.EncodeToString(hash.Sum(nil)) == expected.SHA256
}

func ensureAssetBundle(ctx context.Context, root string, bundle assetBundle) (string, error) {
	if len(bundle.SHA256) != 64 || bundle.Size <= 0 || bundle.Size > 256<<20 {
		return "", fmt.Errorf("invalid asset bundle")
	}
	if _, err := hex.DecodeString(bundle.SHA256); err != nil {
		return "", err
	}
	target := filepath.Join(root, bundle.SHA256)
	lock, _ := assetLocks.LoadOrStore(target, make(chan struct{}, 1))
	gate := lock.(chan struct{})
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	valid := len(bundle.Files) > 0
	for name, file := range bundle.Files {
		if !validAssetFile(name) || file.Size <= 0 || file.Size > 32<<20 {
			return "", fmt.Errorf("invalid asset manifest")
		}
		valid = valid && assetMatches(filepath.Join(target, filepath.FromSlash(name)), file)
	}
	if valid {
		return target, nil
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	temp, err := os.MkdirTemp(root, ".asset-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temp)
	archive := filepath.Join(temp, "bundle.zip")
	if err := systemdeps.DownloadVerifiedAsset(ctx, bundle.URL, bundle.FallbackURL, bundle.Filename, archive, bundle.Size, bundle.SHA256); err != nil {
		return "", err
	}
	if err := extractAssets(archive, filepath.Join(temp, "payload"), bundle.Files); err != nil {
		return "", err
	}
	// The old cache is removed only after a complete verified replacement exists.
	if err := os.RemoveAll(target); err != nil {
		return "", err
	}
	if err := os.Rename(filepath.Join(temp, "payload"), target); err != nil {
		return "", err
	}
	return target, nil
}

func extractAssets(archive, target string, expected map[string]assetFile) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	seen := map[string]bool{}
	var total int64
	for _, file := range reader.File {
		entry, ok := expected[file.Name]
		if !ok || !validAssetFile(file.Name) || seen[file.Name] || !file.Mode().IsRegular() || file.UncompressedSize64 != uint64(entry.Size) {
			return fmt.Errorf("unexpected ZIP entry %q", file.Name)
		}
		total += entry.Size
		if total > 256<<20 {
			return fmt.Errorf("asset archive exceeds expanded limit")
		}
		seen[file.Name] = true
		destination := filepath.Join(target, filepath.FromSlash(file.Name))
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		source, err := file.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			source.Close()
			return err
		}
		_, copyErr := io.Copy(output, io.LimitReader(source, entry.Size+1))
		source.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if !assetMatches(destination, entry) {
			return fmt.Errorf("asset hash mismatch: %s", file.Name)
		}
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("asset archive is incomplete")
	}
	return nil
}

// ServeAsset exposes only pinned public showcase resources; it cannot fetch a
// caller-provided URL or serve arbitrary paths from the runtime directory.
func ServeAsset(w http.ResponseWriter, r *http.Request) {
	catalog := CatalogPath()
	runtimeRoot := os.Getenv("LAZYMIND_RUNTIME_ROOT")
	if catalog == "" || runtimeRoot == "" {
		http.NotFound(w, r)
		return
	}
	serveAssetFrom(w, r, filepath.Dir(catalog), filepath.Join(runtimeRoot, "cache", "featured-assets"))
}

func serveAssetFrom(w http.ResponseWriter, r *http.Request, featured, cache string) {
	name := strings.TrimPrefix(r.URL.Path, "/showcase-assets/")
	if !validAssetFile(name) {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(featured, "downloads.json"))
	var downloads assetDownloads
	if err != nil || json.Unmarshal(data, &downloads) != nil || downloads.SchemaVersion != 1 {
		http.NotFound(w, r)
		return
	}
	destination := ""
	if local, ok := downloads.Local[name]; ok {
		destination = filepath.Join(featured, "assets", filepath.FromSlash(name))
		if !assetMatches(destination, local) {
			http.Error(w, "Preview unavailable", http.StatusServiceUnavailable)
			return
		}
	} else {
		parts := strings.Split(name, "/")
		if len(parts) < 3 {
			http.NotFound(w, r)
			return
		}
		bundle, ok := downloads.Bundles[strings.Join(parts[:2], "/")]
		if _, allowed := bundle.Files[name]; !ok || !allowed {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		root, err := ensureAssetBundle(ctx, cache, bundle)
		if err != nil {
			w.Header().Set("Retry-After", "5")
			http.Error(w, "Preview download failed; please retry", http.StatusBadGateway)
			return
		}
		destination = filepath.Join(root, filepath.FromSlash(name))
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, destination)
}
