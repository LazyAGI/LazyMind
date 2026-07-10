package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"
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
	return "file://" + filepath.Join(s.root, filepath.FromSlash(key))
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
	if err := tx.Create(&row).Error; err != nil {
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
