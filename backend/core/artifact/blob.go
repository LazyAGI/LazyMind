package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
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
	if root := strings.TrimSpace(os.Getenv("LAZYMIND_ARTIFACT_STORAGE_ROOT")); root != "" {
		return root
	}
	return legacyBlobRoot()
}

func legacyBlobRoot() string { return filepath.Join(artifactWorkspaceRoot(), "artifact-blobs") }

func artifactWorkspaceRoot() string {
	if root := strings.TrimSpace(os.Getenv("LAZYMIND_SUBAGENT_WORKSPACE")); root != "" {
		return root
	}
	if root := strings.TrimSpace(os.Getenv("LAZYMIND_AGENTIC_WORKSPACE")); root != "" {
		return root
	}
	return "/data/subagent"
}

func safeTenant(tenant string) string {
	value := strings.NewReplacer("/", "_", "\\", "_").Replace(strings.TrimSpace(tenant))
	if value == "" || value == "." || value == ".." {
		return "default"
	}
	return value
}

func blobPath(tenant, digest string) string {
	if len(digest) < 4 {
		digest = digest + "0000"
	}
	return filepath.Join(blobRoot(), safeTenant(tenant), digest[:2], digest)
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
	allowed := false
	for _, root := range []string{blobRoot(), legacyBlobRoot()} {
		rel, err := filepath.Rel(filepath.Clean(root), cleaned)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			allowed = true
		}
	}
	if !allowed {
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

func persistBlob(db *gorm.DB, ref BlobRef, createdAt time.Time) (string, error) {
	blob := orm.ArtifactBlob{
		ID: uuid.NewString(), TenantID: ref.TenantID, SHA256: ref.SHA256, Size: ref.Size,
		MIMEType: firstNonEmpty(ref.MIMEType, "application/octet-stream"), StorageBackend: "local",
		StorageKey: ref.StorageKey, State: "ready", CreatedAt: createdAt,
	}
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "sha256"}, {Name: "size"}},
		DoNothing: true,
	}).Create(&blob).Error; err != nil {
		return "", err
	}
	var stored orm.ArtifactBlob
	if err := db.Where("tenant_id = ? AND sha256 = ? AND size = ?", ref.TenantID, ref.SHA256, ref.Size).
		Take(&stored).Error; err != nil {
		return "", err
	}
	return stored.ID, nil
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
