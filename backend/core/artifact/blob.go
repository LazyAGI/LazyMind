package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"lazymind/core/subagent"
)

type BlobRef struct {
	ID         string
	TenantID   string
	SHA256     string
	Size       int64
	MIMEType   string
	StorageKey string
}

func blobRoot() string {
	return filepath.Join(subagent.WorkspaceRoot(), "artifact-blobs")
}

func blobPath(tenant, digest string) string {
	safeTenant := strings.ReplaceAll(strings.TrimSpace(tenant), string(os.PathSeparator), "_")
	if safeTenant == "" {
		safeTenant = "default"
	}
	if len(digest) < 4 {
		digest = digest + "0000"
	}
	return filepath.Join(blobRoot(), safeTenant, digest[:2], digest)
}

func PutBlob(tenant, mimeType string, source io.Reader, expectedHash string, expectedSize int64) (BlobRef, error) {
	if err := os.MkdirAll(blobRoot(), 0o700); err != nil {
		return BlobRef{}, err
	}
	sum := sha256.New()
	reader := io.TeeReader(source, sum)
	tmp, err := os.CreateTemp(blobRoot(), ".blob-*.tmp")
	if err != nil {
		_ = os.MkdirAll(blobRoot(), 0o700)
		tmp, err = os.CreateTemp(blobRoot(), ".blob-*.tmp")
		if err != nil {
			return BlobRef{}, err
		}
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	written, err := io.Copy(tmp, reader)
	if err != nil {
		return BlobRef{}, err
	}
	if err := tmp.Sync(); err != nil {
		return BlobRef{}, err
	}
	digest := hex.EncodeToString(sum.Sum(nil))
	if expectedHash != "" && !strings.EqualFold(strings.TrimPrefix(expectedHash, "sha256:"), digest) {
		return BlobRef{}, ErrBlobHashMismatch
	}
	if expectedSize > 0 && written != expectedSize {
		return BlobRef{}, ErrBlobHashMismatch
	}
	final := blobPath(tenant, digest)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return BlobRef{}, err
	}
	if err := tmp.Close(); err != nil {
		return BlobRef{}, err
	}
	if _, err := os.Lstat(final); err == nil {
		resolved, err := filepath.EvalSymlinks(final)
		if err != nil || resolved != final {
			return BlobRef{}, ErrAccessDenied
		}
	}
	if err := os.Rename(tmp.Name(), final); err != nil && !os.IsExist(err) {
		return BlobRef{}, err
	}
	if info, err := os.Lstat(final); err != nil || !info.Mode().IsRegular() {
		return BlobRef{}, ErrAccessDenied
	}
	return BlobRef{
		ID:         digest,
		TenantID:   tenant,
		SHA256:     digest,
		Size:       written,
		MIMEType:   mimeType,
		StorageKey: final,
	}, nil
}

func OpenBlob(ref BlobRef) (*os.File, error) {
	path := ref.StorageKey
	if path == "" {
		path = blobPath(ref.TenantID, ref.SHA256)
	}
	cleaned := filepath.Clean(path)
	root := filepath.Clean(blobRoot())
	rel, err := filepath.Rel(root, cleaned)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, ErrAccessDenied
	}
	info, err := os.Lstat(cleaned)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrAccessDenied
	}
	return os.Open(cleaned)
}

func RangeRead(ref BlobRef, offset, length int64, dest io.Writer) error {
	file, err := OpenBlob(ref)
	if err != nil {
		return err
	}
	defer file.Close()
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}
	if length <= 0 {
		_, err = io.Copy(dest, file)
		return err
	}
	_, err = io.Copy(dest, io.LimitReader(file, length))
	return err
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func FormatBlobID(digest string) string {
	return fmt.Sprintf("blob_%s", digest)
}
