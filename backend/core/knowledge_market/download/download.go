package download

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxFetchTimeout bounds the whole package download (git clone + LFS objects +
// direct links + extraction + hashing). The underlying git/http calls already
// honor ctx cancellation, so a single deadline covers the entire pipeline and
// a hung external source can never occupy a job forever.
const MaxFetchTimeout = time.Hour

// Fetch downloads the package identified by packageURL into dstDir and returns
// the materialized file set with sizes and SHA-256 digests. A failed fetch is
// retried once after cleaning dstDir, which also re-resolves signed URLs.
func Fetch(ctx context.Context, packageURL, revision, dstDir string, progress ProgressFunc) ([]FetchedFile, error) {
	// Apply the overall deadline once at the entry point so every stage
	// (clone, LFS resolves, HTTP download, zip extraction) shares the budget.
	ctx, cancel := context.WithTimeout(ctx, MaxFetchTimeout)
	defer cancel()
	files, err := fetchOnce(ctx, packageURL, revision, dstDir, progress)
	if err == nil {
		return files, nil
	}
	// Retry once with a clean destination dir.
	if rmErr := os.RemoveAll(dstDir); rmErr != nil {
		return nil, fmt.Errorf("download retry cleanup failed: %w", rmErr)
	}
	if mkErr := os.MkdirAll(dstDir, 0o755); mkErr != nil {
		return nil, fmt.Errorf("download retry mkdir failed: %w", mkErr)
	}
	files, retryErr := fetchOnce(ctx, packageURL, revision, dstDir, progress)
	if retryErr != nil {
		return nil, fmt.Errorf("download failed after retry: %v; last error: %w", err, retryErr)
	}
	return files, nil
}

// fetchOnce performs a single fetch attempt.
func fetchOnce(ctx context.Context, packageURL, revision, dstDir string, progress ProgressFunc) ([]FetchedFile, error) {
	u, err := url.Parse(strings.TrimSpace(packageURL))
	if err != nil {
		return nil, fmt.Errorf("invalid package url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported package url scheme %q", u.Scheme)
	}
	if strings.HasSuffix(strings.ToLower(u.Path), ".git") {
		return fetchGit(ctx, u, revision, dstDir, progress)
	}
	return fetchHTTP(ctx, u, dstDir, progress)
}

// resolveOutputPath guards zip extraction against path traversal.
func resolveOutputPath(root, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid archive entry %q", name)
	}
	target := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry escapes destination: %q", name)
	}
	return target, nil
}
