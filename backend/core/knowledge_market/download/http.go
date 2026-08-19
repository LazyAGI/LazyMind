package download

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// fetchHTTP downloads a single URL and, when the payload is a zip archive,
// extracts it so every file becomes part of the materialized package.
func fetchHTTP(ctx context.Context, u *url.URL, dstDir string, progress ProgressFunc) ([]FetchedFile, error) {
	buf := filepath.Join(dstDir, ".download.bin")
	if _, err := downloadURL(ctx, u.String(), buf, progress); err != nil {
		return nil, err
	}
	if isZipFile(buf) {
		if err := extractZip(buf, dstDir); err != nil {
			return nil, err
		}
		_ = os.Remove(buf)
	} else {
		name := filepath.Base(u.Path)
		if name == "" || name == "." || name == "/" {
			name = "download"
		}
		if err := os.Rename(buf, filepath.Join(dstDir, name)); err != nil {
			return nil, err
		}
	}

	paths, err := walkPackageFiles(dstDir)
	if err != nil {
		return nil, err
	}
	files := make([]FetchedFile, 0, len(paths))
	for _, rel := range paths {
		full := filepath.Join(dstDir, rel)
		sz, sha, err := hashFile(full)
		if err != nil {
			return nil, fmt.Errorf("hash %s failed: %w", rel, err)
		}
		files = append(files, FetchedFile{Path: rel, Size: sz, SHA256: sha})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("package %s produced no files", u)
	}
	return files, nil
}

// defaultHTTPClient is a package-level client so tests can inject a fake
// transport; the production default follows redirects.
var defaultHTTPClient = &http.Client{}

// downloadURL streams one URL into dst, following redirects, with an optional
// byte-progress callback. Timeouts come from ctx; no fixed client timeout.
func downloadURL(ctx context.Context, rawURL, dst string, progress ProgressFunc) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GET %s failed: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("GET %s failed: HTTP %s", rawURL, resp.Status)
	}
	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	body := io.Reader(resp.Body)
	if progress != nil && resp.ContentLength > 0 {
		body = &progressReader{r: resp.Body, done: 0, total: resp.ContentLength, cb: progress}
	}
	n, copyErr := io.Copy(out, body)
	closeErr := out.Close()
	if copyErr != nil {
		return 0, fmt.Errorf("write download %s failed: %w", rawURL, copyErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("write download %s failed: %w", rawURL, closeErr)
	}
	return n, nil
}

// progressReader reports byte progress in ~1% steps.
type progressReader struct {
	r     io.Reader
	done  int64
	total int64
	cb    ProgressFunc
	last  int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	step := p.total / 100
	if step <= 0 {
		step = 1
	}
	if p.done-p.last >= step || err != nil {
		p.last = p.done
		p.cb(p.done, p.total)
	}
	return n, err
}

// isZipFile detects zip payloads by magic bytes or file extension.
func isZipFile(path string) bool {
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	return magic[0] == 'P' && magic[1] == 'K' && (magic[2] == 3 || magic[2] == 5 || magic[2] == 7) && magic[3] == 4
}

// extractZip expands a zip archive into root with path-traversal guards.
func extractZip(zipPath, root string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip %s failed: %w", zipPath, err)
	}
	defer zr.Close()
	for _, entry := range zr.File {
		name := normalizeZipEntryName(entry.Name)
		target, err := resolveOutputPath(root, name)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if entry.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("zip entry is a symlink: %q", name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := entry.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if copyErr != nil {
			return fmt.Errorf("extract zip entry %s failed: %w", name, copyErr)
		}
	}
	return nil
}

// normalizeZipEntryName converts a zip entry name to valid UTF-8 so
// extraction works on filesystems that reject invalid UTF-8 filenames (for
// example macOS-hosted volumes) and never produces mojibake names.
// Windows-created archives commonly store Chinese names in GBK without the
// UTF-8 flag; anything that still cannot be decoded is scrubbed to a safe
// placeholder so extraction never fails on the name alone.
func normalizeZipEntryName(name string) string {
	if utf8.ValidString(name) {
		return name
	}
	if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes([]byte(name)); err == nil && utf8.Valid(decoded) {
		return string(decoded)
	}
	return strings.ToValidUTF8(name, "_")
}

// hashFile returns the file size and SHA-256 digest.
func hashFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// contentTypeByName guesses a MIME type from the file extension.
func contentTypeByName(name string) string {
	if ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
