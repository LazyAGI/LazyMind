package download

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const lfsPointerVersion = "version https://git-lfs.github.com/spec/v1"

// lfsRule resolves the real download URL of a git-lfs object on one platform.
// git clone only yields pointer files for LFS objects, so the adapter must
// fetch the real bytes from the platform-specific file endpoint.
type lfsRule interface {
	ResolveURL(ctx context.Context, repoURL *url.URL, revision, path, oid string, size int64) (string, error)
}

// modelscopeLFSRule supports ModelScope datasets/models:
// https://{host}/{repoType}s/{namespace}/{name}/resolve/{revision}/{path}
type modelscopeLFSRule struct{}

func (modelscopeLFSRule) ResolveURL(_ context.Context, repoURL *url.URL, revision, path, _ string, _ int64) (string, error) {
	segs := splitTrimmed(repoURL.Path, "/")
	if len(segs) != 3 {
		return "", fmt.Errorf("cannot parse ModelScope repo path %q", repoURL.Path)
	}
	repoName := strings.TrimSuffix(segs[2], ".git")
	if repoName == "" {
		return "", fmt.Errorf("cannot parse ModelScope repo path %q", repoURL.Path)
	}
	rev := strings.TrimSpace(revision)
	if rev == "" {
		rev = "master"
	}
	return fmt.Sprintf("https://%s/%s/%s/%s/resolve/%s/%s",
		repoURL.Host, segs[0], url.PathEscape(segs[1]), url.PathEscape(repoName),
		url.PathEscape(rev), escapePath(path)), nil
}

// lfsRules maps platform hosts to their LFS resolve rules; unknown hosts fail
// with a clear error when a pointer file is encountered.
var lfsRules = map[string]lfsRule{
	"modelscope.cn":     modelscopeLFSRule{},
	"www.modelscope.cn": modelscopeLFSRule{},
}

type lfsPointer struct {
	OID  string
	Size int64
}

// isLFSPointerFile reports whether the file is a git-lfs pointer.
func isLFSPointerFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return false
	}
	return strings.TrimSpace(scanner.Text()) == lfsPointerVersion
}

func parseLFSPointerFile(path string) (lfsPointer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return lfsPointer{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var oid, size string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "oid sha256:"):
			oid = strings.TrimPrefix(line, "oid sha256:")
		case strings.HasPrefix(line, "size "):
			size = strings.TrimPrefix(line, "size ")
		}
	}
	if oid == "" {
		return lfsPointer{}, fmt.Errorf("lfs pointer %s has no oid", path)
	}
	sz, err := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
	if err != nil {
		return lfsPointer{}, fmt.Errorf("lfs pointer %s has invalid size", path)
	}
	return lfsPointer{OID: oid, Size: sz}, nil
}

// resolveLFSPointer downloads the real content for one pointer file (rel is
// the file path relative to dstDir), verifies it against the pointer's
// oid/size and replaces the pointer in place.
func resolveLFSPointer(ctx context.Context, repoURL *url.URL, revision, dstDir, rel string) error {
	pointerPath := filepath.Join(dstDir, filepath.FromSlash(rel))
	ptr, err := parseLFSPointerFile(pointerPath)
	if err != nil {
		return err
	}
	rule, ok := lfsRules[strings.ToLower(repoURL.Host)]
	if !ok {
		return fmt.Errorf("LFS pointer found but host %q is not supported", repoURL.Host)
	}
	resolveURL, err := rule.ResolveURL(ctx, repoURL, revision, rel, ptr.OID, ptr.Size)
	if err != nil {
		return err
	}
	tmp := pointerPath + ".lfs.tmp"
	defer os.Remove(tmp)
	size, err := downloadURL(ctx, resolveURL, tmp, nil)
	if err != nil {
		return fmt.Errorf("resolve LFS object %s failed: %w", ptr.OID, err)
	}
	_, sha, err := hashFile(tmp)
	if err != nil {
		return err
	}
	if sha != ptr.OID {
		return fmt.Errorf("LFS object checksum mismatch: got %s want %s", sha, ptr.OID)
	}
	if size != ptr.Size {
		return fmt.Errorf("LFS object size mismatch: got %d want %d", size, ptr.Size)
	}
	return os.Rename(tmp, pointerPath)
}

func splitTrimmed(s, sep string) []string {
	parts := strings.Split(strings.Trim(s, "/"), sep)
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func escapePath(p string) string {
	segs := splitTrimmed(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}
