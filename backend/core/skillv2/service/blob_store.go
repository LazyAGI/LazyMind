package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	skilldistribution "lazymind/core/skillv2/distribution"
)

type LocalObjectStore struct {
	root string
}

func NewLocalObjectStore(root string) *LocalObjectStore {
	return &LocalObjectStore{root: root}
}

func (s *LocalObjectStore) Put(ctx context.Context, key string, data []byte) error {
	if s == nil {
		return fmt.Errorf("object store is nil")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	path := filepath.Join(s.root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (s *LocalObjectStore) URL(key string) string {
	if s == nil {
		return ""
	}
	return localObjectFileURL(filepath.Join(s.root, filepath.FromSlash(key)))
}

func (s *LocalObjectStore) Get(ctx context.Context, key string) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("object store is nil")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return os.ReadFile(filepath.Join(s.root, filepath.FromSlash(key)))
}

func localObjectFileURL(localPath string) string {
	if absolutePath, err := filepath.Abs(localPath); err == nil {
		localPath = absolutePath
	}
	uriPath := filepath.ToSlash(localPath)
	if filepath.VolumeName(localPath) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath}).String()
}

type BlobStore struct {
	db      *gorm.DB
	objects *LocalObjectStore
}

func NewBlobStore(db *gorm.DB, objects *LocalObjectStore) *BlobStore {
	return &BlobStore{db: db, objects: objects}
}

type blobInfo struct {
	Hash           string
	Size           int64
	Mime           string
	FileType       string
	Binary         bool
	StorageBackend string
	StorageKey     *string
}

func (s *BlobStore) Put(ctx context.Context, tx *gorm.DB, path string, data []byte, nowProvider Clock) (blobInfo, error) {
	if tx == nil {
		tx = s.db
	}
	hashBytes := sha256.Sum256(data)
	hash := hex.EncodeToString(hashBytes[:])
	mime, fileType, binary := classifyFile(path, data)
	info := blobInfo{Hash: hash, Size: int64(len(data)), Mime: mime, FileType: fileType, Binary: binary}

	var existing skillBlobRow
	err := tx.Where("hash = ?", hash).Take(&existing).Error
	if err == nil {
		info.StorageBackend = existing.StorageBackend
		info.StorageKey = existing.StorageKey
		return info, nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return blobInfo{}, err
	}

	row := skillBlobRow{
		Hash:      hash,
		Size:      int64(len(data)),
		Mime:      mime,
		FileType:  fileType,
		Binary:    binary,
		CreatedAt: nowProvider.Now(),
	}
	if binary {
		key := strings.Join([]string{"skillv2", hash[:2], hash}, "/")
		if err := s.objects.Put(ctx, key, data); err != nil {
			return blobInfo{}, err
		}
		row.StorageBackend = "local_file"
		row.StorageKey = &key
		info.StorageBackend = row.StorageBackend
		info.StorageKey = row.StorageKey
	} else {
		row.StorageBackend = "postgres"
		row.Content = data
		info.StorageBackend = row.StorageBackend
	}
	create := tx
	if binary {
		// A nil []byte is encoded as an empty blob by the SQLite driver. Omit the
		// nullable column so the database stores SQL NULL as required by the
		// storage-shape constraint.
		create = create.Omit("content")
	}
	if err := create.Create(&row).Error; err != nil {
		return blobInfo{}, err
	}
	return info, nil
}

func (s *BlobStore) DownloadURL(key string) string {
	return s.objects.URL(key)
}

func (s *BlobStore) DeleteBlob(ctx context.Context, tx *gorm.DB, hash string) error {
	if tx == nil {
		tx = s.db
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return tx.Where("hash = ?", hash).Delete(&skillBlobRow{}).Error
}

func (s *BlobStore) StoreDistributionBlob(ctx context.Context, tx *gorm.DB, path string, data []byte, now time.Time) (skilldistribution.Blob, error) {
	info, err := s.Put(ctx, tx, path, data, distributionClock{now: now})
	if err != nil {
		return skilldistribution.Blob{}, err
	}
	return skilldistribution.Blob{Hash: info.Hash, Size: info.Size, Mime: info.Mime, FileType: info.FileType, Binary: info.Binary}, nil
}

func (s *BlobStore) ReadDistributionBlob(ctx context.Context, tx *gorm.DB, hash string) ([]byte, error) {
	if tx == nil {
		tx = s.db
	}
	var row skillBlobRow
	if err := tx.WithContext(ctx).Where("hash = ?", hash).Take(&row).Error; err != nil {
		return nil, err
	}
	if !row.Binary {
		return append([]byte(nil), row.Content...), nil
	}
	if row.StorageKey == nil || strings.TrimSpace(*row.StorageKey) == "" {
		return nil, fmt.Errorf("binary blob storage key missing for %s", hash)
	}
	return s.objects.Get(ctx, *row.StorageKey)
}

type distributionClock struct {
	now time.Time
}

func (clock distributionClock) Now() time.Time { return clock.now }
