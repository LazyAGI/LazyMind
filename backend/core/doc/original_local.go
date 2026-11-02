package doc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"lazymind/core/common/orm"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const OriginalFileMaxBytes int64 = 500 << 20
const OriginalBatchMaxBytes int64 = 1 << 30

// KnowledgeFileTypes is the shared page/MCP import catalog. Storage support does
// not imply that a deployment has a parser for every format.
var KnowledgeFileTypes = []string{
	"pdf", "docx", "doc", "ppt", "pptx", "pptm", "jpg", "jpeg", "png", "gif",
	"bmp", "webp", "tiff", "tif", "ipynb", "epub", "md", "mbox", "csv", "xls",
	"xlsx", "mp3", "mp4", "txt", "xml", "json", "jsonl", "yaml", "yml", "html", "htm", "py",
}

// AcquireLocalOriginal copies from the same opened file that was validated.
// Dataset authorization belongs to the caller and must precede this operation.
func (s *OriginalService) AcquireLocalOriginal(ctx context.Context, caller DatasetCatalogCaller, path string, remaining int64) (OriginalRef, error) {
	if !filepath.IsAbs(path) {
		return OriginalRef{}, originalError("INVALID_ARGUMENT", "absolute local file path required")
	}
	if !slices.Contains(KnowledgeFileTypes, strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))) {
		return OriginalRef{}, originalError("UNSUPPORTED_FORMAT", "unsupported original file format")
	}
	f, err := openLocalOriginal(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return OriginalRef{}, originalError("FILE_NOT_FOUND", "local file does not exist")
		}
		return OriginalRef{}, originalError("FILE_NOT_ACCESSIBLE", "local file cannot be accessed")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return OriginalRef{}, originalError("INVALID_ARGUMENT", "local path must be a regular file")
	}
	if info.Size() <= 0 || info.Size() > OriginalFileMaxBytes || info.Size() > remaining {
		return OriginalRef{}, originalError("INVALID_ARGUMENT", "empty file or upload size limit exceeded")
	}
	ref, err := s.AcquireOriginal(ctx, caller, AcquireOriginalRequest{
		Filename: filepath.Base(path), Reader: io.LimitReader(f, min(remaining, OriginalFileMaxBytes)+1),
	})
	if err != nil {
		return ref, err
	}
	after, err := f.Stat()
	if err != nil || ref.Size != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return OriginalRef{}, originalError("CONFLICT", "local file changed while importing")
	}
	return ref, nil
}

// LocalOriginalSize validates a source without copying bytes or creating records.
func LocalOriginalSize(path string) (int64, error) {
	if !filepath.IsAbs(path) {
		return 0, originalError("INVALID_ARGUMENT", "absolute local file path required")
	}
	if !slices.Contains(KnowledgeFileTypes, strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))) {
		return 0, originalError("UNSUPPORTED_FORMAT", "unsupported original file format")
	}
	f, err := openLocalOriginal(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, originalError("FILE_NOT_FOUND", "local file does not exist")
		}
		return 0, originalError("FILE_NOT_ACCESSIBLE", "local file cannot be accessed")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, originalError("INVALID_ARGUMENT", "local path must be a regular file")
	}
	if info.Size() <= 0 || info.Size() > OriginalFileMaxBytes {
		return 0, originalError("INVALID_ARGUMENT", "empty file or per-file size limit exceeded")
	}
	return info.Size(), nil
}

// discardLocalAcquisition only removes a private, unbound transfer created by
// this batch. Durable bindings have separate copies and are never removed here.
func (s *OriginalService) discardLocalAcquisition(ctx context.Context, caller DatasetCatalogCaller, ref string) error {
	var row orm.UploadedFile
	if err := s.db.WithContext(ctx).Where("upload_file_id = ? AND create_user_id = ? AND tenant_id = ? AND dataset_id = '' AND document_id = '' AND status = ? AND deleted_at IS NULL", ref, caller.UserID, caller.TenantID, UploadedFileStateUploaded).Take(&row).Error; err != nil {
		return err
	}
	var meta originalMetadata
	if err := json.Unmarshal(row.Ext, &meta); err != nil {
		return err
	}
	dir := buildTempUploadFileDir(row.CreateUserID, row.UploadFileID)
	if filepath.Clean(meta.StoredPath) != filepath.Join(dir, meta.StoredName) {
		return originalError("INVALID_ARGUMENT", "invalid temporary original path")
	}
	deleted := s.db.WithContext(ctx).Model(&orm.UploadedFile{}).Where("upload_file_id = ? AND document_id = '' AND status = ? AND deleted_at IS NULL", ref, UploadedFileStateUploaded).Update("deleted_at", s.now())
	if deleted.Error != nil {
		return deleted.Error
	}
	if deleted.RowsAffected == 1 {
		return os.RemoveAll(dir)
	}
	return nil
}
