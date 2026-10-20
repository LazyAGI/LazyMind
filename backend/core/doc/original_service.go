package doc

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

const originalMaxBytes int64 = OriginalFileMaxBytes
const originalRetention = 24 * time.Hour

// OriginalRef identifies immutable bytes; it is never a filesystem capability.
type OriginalRef struct {
	FileRef       string    `json:"file_ref"`
	Filename      string    `json:"filename"`
	ContentType   string    `json:"content_type"`
	Size          int64     `json:"size"`
	ContentSHA256 string    `json:"content_sha256"`
	FinalURL      string    `json:"final_url,omitempty"`
	RevisionID    string    `json:"revision_id,omitempty"`
	ExpiresAt     time.Time `json:"expires_at"`
}
type AcquireOriginalRequest struct {
	Filename, ContentType, ContentSHA256, FinalURL, RevisionID string
	Reader                                                     io.Reader
}
type AcquireRemoteOriginalRequest struct{ URL, DisplayName, ExpectedMIME string }
type OriginalArtifact struct {
	Reader                                           io.ReadCloser
	Filename, ContentType, ContentSHA256, RevisionID string
}
type OriginalServiceDeps struct {
	DB               *gorm.DB
	SubmitProcessing func(context.Context, DatasetCatalogCaller, string, string) (StartTaskResult, error)
	// Resolver belongs to the artifact module, which already depends on doc.
	ResolveWorkArtifact func(context.Context, DatasetCatalogCaller, string, string) (OriginalArtifact, error)
}
type OriginalService struct {
	db       *gorm.DB
	submit   func(context.Context, DatasetCatalogCaller, string, string) (StartTaskResult, error)
	artifact func(context.Context, DatasetCatalogCaller, string, string) (OriginalArtifact, error)
	now      func() time.Time
}
type originalMetadata struct {
	uploadedFileExt
	ExpiresAt  time.Time `json:"original_expires_at,omitempty"`
	FinalURL   string    `json:"original_final_url,omitempty"`
	RevisionID string    `json:"original_revision_id,omitempty"`
}
type ImportOriginalRequest struct {
	DocumentID, TaskID, RelativePath                          string // Internal page request identity; not part of MCP upload schema.
	FileRef, DatasetID, ParentID, DisplayName, DataSourceType string
	Tags                                                      []string
	ConflictPolicy                                            string
}
type OriginalImportResult struct {
	Document         orm.Document    `json:"document"`
	Task             orm.Task        `json:"task"`
	ContentSHA256    string          `json:"content_sha256"`
	Start            StartTaskResult `json:"processing"`
	SubmissionStatus string          `json:"submission_status"`
	SubmissionError  string          `json:"submission_error,omitempty"`
	Skipped          bool            `json:"skipped,omitempty"`
}

// PreparedOriginalBinding is private storage plus an authorization snapshot.
// Call CleanupPreparedBinding only if the caller's transaction did not commit.
type PreparedOriginalBinding struct {
	request                   ImportOriginalRequest
	source                    orm.UploadedFile
	sourceDocument            *orm.Document
	metadata                  uploadedFileExt
	uploadID, hash, directory string
}

// SetParentID allows a move transaction to bind beneath its newly created folder.
func (p *PreparedOriginalBinding) SetParentID(parentID string) { p.request.ParentID = parentID }

func (p *PreparedOriginalBinding) AuthorizationRequests() []ResourceAuthorizationRequest {
	requests := []ResourceAuthorizationRequest{{DatasetID: p.request.DatasetID, Action: acl.PermissionDatasetUpload}}
	if p.sourceDocument != nil {
		requests = append(requests, ResourceAuthorizationRequest{DatasetID: p.sourceDocument.DatasetID, Action: acl.PermissionDatasetWrite})
	} else if p.source.DatasetID != "" {
		requests = append(requests, ResourceAuthorizationRequest{DatasetID: p.source.DatasetID, Action: acl.PermissionDatasetRead})
	}
	return requests
}

func NewOriginalService(deps OriginalServiceDeps) (*OriginalService, error) {
	if deps.DB == nil {
		return nil, errors.New("original service requires database")
	}
	return &OriginalService{db: deps.DB, submit: deps.SubmitProcessing, artifact: deps.ResolveWorkArtifact, now: func() time.Time { return time.Now().UTC() }}, nil
}
func originalError(code DatasetServiceErrorCode, message string) error {
	return &DatasetServiceError{Code: code, Message: message}
}
func validOriginalCaller(caller DatasetCatalogCaller) error {
	if strings.TrimSpace(caller.UserID) == "" {
		return originalError(DatasetServiceForbidden, "original owner is required")
	}
	return nil
}
func originalFilename(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return "", originalError(DatasetServiceInvalidArgument, "a plain filename is required")
	}
	return name, nil
}

// AcquireOriginal accepts bytes from a trusted host transfer or source adapter.
// Remote clients cannot pass local paths into this API.
func (s *OriginalService) AcquireOriginal(ctx context.Context, caller DatasetCatalogCaller, req AcquireOriginalRequest) (OriginalRef, error) {
	if err := validOriginalCaller(caller); err != nil {
		return OriginalRef{}, err
	}
	if err := s.purgeExpiredOriginals(ctx, caller); err != nil {
		return OriginalRef{}, err
	}
	name, err := originalFilename(req.Filename)
	if err != nil {
		return OriginalRef{}, err
	}
	if req.Reader == nil {
		return OriginalRef{}, originalError(DatasetServiceInvalidArgument, "original reader is required")
	}
	id := newUploadID()
	dir := buildTempUploadFileDir(caller.UserID, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return OriginalRef{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(dir)
		}
	}()
	path := filepath.Join(dir, storedFileName(name, id))
	size, hash, mime, err := writeOriginal(ctx, path, name, req.ContentType, req.Reader, req.ContentSHA256)
	if err != nil {
		return OriginalRef{}, err
	}
	now := s.now()
	expires := now.Add(originalRetention)
	meta := originalMetadata{uploadedFileExt: uploadedFileExt{StoredPath: path, StoredName: filepath.Base(path), OriginalFilename: name, FileSize: size, ContentType: mime}, ExpiresAt: expires, FinalURL: req.FinalURL, RevisionID: req.RevisionID}
	row := orm.UploadedFile{UploadFileID: id, TenantID: caller.TenantID, ContentHash: hash, Status: UploadedFileStateUploaded, Ext: mustJSON(meta), BaseModel: orm.BaseModel{CreateUserID: caller.UserID, CreateUserName: firstNonEmpty(caller.UserName, caller.UserID), CreatedAt: now, UpdatedAt: now}}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return OriginalRef{}, err
	}
	committed = true
	return originalRef(row, meta), nil
}
func originalRef(row orm.UploadedFile, meta originalMetadata) OriginalRef {
	return OriginalRef{FileRef: row.UploadFileID, Filename: meta.OriginalFilename, ContentType: meta.ContentType, Size: meta.FileSize, ContentSHA256: row.ContentHash, FinalURL: meta.FinalURL, RevisionID: meta.RevisionID, ExpiresAt: meta.ExpiresAt}
}
func (s *OriginalService) ResolveOriginal(ctx context.Context, caller DatasetCatalogCaller, id string) (OriginalRef, error) {
	row, meta, err := s.resolve(ctx, s.db, caller, id)
	if err != nil {
		return OriginalRef{}, err
	}
	return originalRef(row, meta), nil
}
func (s *OriginalService) resolve(ctx context.Context, db *gorm.DB, caller DatasetCatalogCaller, id string) (orm.UploadedFile, originalMetadata, error) {
	var row orm.UploadedFile
	var meta originalMetadata
	if err := validOriginalCaller(caller); err != nil {
		return row, meta, err
	}
	if strings.HasPrefix(id, "fr_") || strings.TrimSpace(id) == "" {
		return row, meta, originalError(DatasetServiceInvalidArgument, "controlled uploaded file reference required")
	}
	if err := db.WithContext(ctx).Where("upload_file_id = ? AND deleted_at IS NULL", id).Take(&row).Error; err != nil {
		return row, meta, originalError(DatasetServiceNotFound, "original not found")
	}
	if row.CreateUserID != caller.UserID || resourceTenant(row.TenantID) != resourceTenant(caller.TenantID) {
		return row, meta, originalError(DatasetServiceForbidden, "original belongs to another owner or tenant")
	}
	if row.Status != UploadedFileStateUploaded && row.Status != UploadedFileStateBound {
		return row, meta, originalError(DatasetServiceInvalidArgument, "original is not complete")
	}
	if err := json.Unmarshal(row.Ext, &meta); err != nil {
		return row, meta, originalError(DatasetServiceInvalidArgument, "invalid original metadata")
	}
	// Legacy temporary uploads have the same bounded lifetime even without expiry metadata.
	if row.DatasetID == "" && meta.ExpiresAt.IsZero() {
		meta.ExpiresAt = row.CreatedAt.Add(originalRetention)
	}
	if !meta.ExpiresAt.IsZero() && !s.now().Before(meta.ExpiresAt) {
		return row, meta, originalError("SOURCE_EXPIRED", "original reference has expired")
	}
	if row.DatasetID != "" {
		if _, err := ResolveResourceDataset(ctx, db, caller, row.DatasetID, acl.PermissionDatasetRead); err != nil {
			return row, meta, err
		}
	}
	return row, meta, nil
}
func (s *OriginalService) AcquireWorkOriginal(ctx context.Context, caller DatasetCatalogCaller, artifactID, revisionID string) (OriginalRef, error) {
	if err := validOriginalCaller(caller); err != nil {
		return OriginalRef{}, err
	}
	if s.artifact == nil {
		return OriginalRef{}, originalError(DatasetServiceUnavailable, "work artifact resolver unavailable")
	}
	resolved, err := s.artifact(ctx, caller, artifactID, revisionID)
	if err != nil {
		return OriginalRef{}, err
	}
	if resolved.Reader == nil {
		return OriginalRef{}, errors.New("artifact original unavailable")
	}
	defer resolved.Reader.Close()
	if resolved.RevisionID == "" || (revisionID != "" && resolved.RevisionID != revisionID) {
		return OriginalRef{}, errors.New("artifact revision was not fixed")
	}
	return s.AcquireOriginal(ctx, caller, AcquireOriginalRequest{Filename: resolved.Filename, ContentType: resolved.ContentType, ContentSHA256: resolved.ContentSHA256, RevisionID: resolved.RevisionID, Reader: resolved.Reader})
}
func (s *OriginalService) PrepareOriginalBinding(ctx context.Context, caller DatasetCatalogCaller, req ImportOriginalRequest) (*PreparedOriginalBinding, error) {
	row, meta, err := s.resolve(ctx, s.db, caller, req.FileRef)
	if err != nil {
		return nil, err
	}
	return s.prepare(ctx, caller, req, row, meta.uploadedFileExt, nil)
}
func (s *OriginalService) PrepareDocumentOriginalBinding(ctx context.Context, caller DatasetCatalogCaller, source orm.Document, targetDatasetID, parentID string) (*PreparedOriginalBinding, error) {
	if _, err := ResolveResourceDataset(ctx, s.db, caller, source.DatasetID, acl.PermissionDatasetWrite); err != nil {
		return nil, err
	}
	var current orm.Document
	if err := s.db.WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", source.ID, source.DatasetID).Take(&current).Error; err != nil {
		return nil, err
	}
	if current.DocumentType == "FOLDER" {
		return nil, errors.New("folders have no original")
	}
	var meta documentExt
	if err := json.Unmarshal(current.Ext, &meta); err != nil {
		return nil, err
	}
	var tags []string
	_ = json.Unmarshal(current.Tags, &tags)
	req := ImportOriginalRequest{DatasetID: targetDatasetID, ParentID: parentID, DisplayName: current.DisplayName, Tags: tags, DataSourceType: "LOCAL_FILE"}
	return s.prepare(ctx, caller, req, orm.UploadedFile{}, uploadedFileExt{StoredPath: meta.StoredPath, StoredName: meta.StoredName, OriginalFilename: meta.OriginalFilename, FileSize: meta.FileSize, ContentType: meta.ContentType, RelativePath: meta.RelativePath}, &current)
}
func (s *OriginalService) prepare(ctx context.Context, caller DatasetCatalogCaller, req ImportOriginalRequest, row orm.UploadedFile, meta uploadedFileExt, source *orm.Document) (*PreparedOriginalBinding, error) {
	dataset, err := ResolveResourceDataset(ctx, s.db, caller, req.DatasetID, acl.PermissionDatasetUpload)
	if err != nil {
		return nil, err
	}
	if req.ConflictPolicy != "" && req.ConflictPolicy != "ask" && req.ConflictPolicy != "skip_same_content" {
		return nil, originalError(DatasetServiceInvalidArgument, "invalid conflict policy")
	}
	name, err := originalFilename(firstNonEmpty(req.DisplayName, meta.OriginalFilename))
	if err != nil {
		return nil, err
	}
	req.DisplayName = name
	req.Tags = normalizeBatchDocumentTags(req.Tags)
	if len(req.Tags) > 10 {
		req.Tags = req.Tags[:10]
	}
	if req.RelativePath != "" {
		meta.RelativePath = req.RelativePath
	}
	input, err := openControlledOriginal(meta.StoredPath)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	id := newUploadID()
	dir := buildDatasetDocFileDir(dataset.TenantID, dataset.ID, "", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	target := filepath.Join(dir, storedFileName(meta.OriginalFilename, id))
	size, hash, mime, err := writeOriginal(ctx, target, meta.OriginalFilename, meta.ContentType, input, row.ContentHash)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if size != meta.FileSize {
		_ = os.RemoveAll(dir)
		return nil, originalError("SOURCE_CHANGED", "original size changed")
	}
	meta.StoredPath, meta.StoredName, meta.FileSize, meta.ContentType = target, filepath.Base(target), size, mime
	return &PreparedOriginalBinding{request: req, source: row, sourceDocument: source, metadata: meta, uploadID: id, hash: hash, directory: dir}, nil
}
func (s *OriginalService) CleanupPreparedBinding(p *PreparedOriginalBinding) error {
	if p == nil || p.directory == "" {
		return nil
	}
	return os.RemoveAll(p.directory)
}

// BindOriginalTx never starts a transaction or copies bytes. The caller owns commit.
func (s *OriginalService) BindOriginalTx(ctx context.Context, tx *gorm.DB, caller DatasetCatalogCaller, p *PreparedOriginalBinding) (OriginalImportResult, error) {
	if tx == nil || p == nil {
		return OriginalImportResult{}, errors.New("transaction and prepared original required")
	}
	req := p.request
	dataset, err := ResolveResourceDataset(ctx, tx, caller, req.DatasetID, acl.PermissionDatasetUpload)
	if err != nil {
		return OriginalImportResult{}, err
	}
	if err := lockResourceDataset(tx, dataset); err != nil {
		return OriginalImportResult{}, err
	}

	if p.sourceDocument != nil {
		src := p.sourceDocument
		if _, err := ResolveResourceDataset(ctx, tx, caller, src.DatasetID, acl.PermissionDatasetWrite); err != nil {
			return OriginalImportResult{}, err
		}
		var current orm.Document
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", src.ID, src.DatasetID).Take(&current).Error; err != nil {
			return OriginalImportResult{}, err
		}
		if !current.UpdatedAt.Equal(src.UpdatedAt) || !bytes.Equal(current.Ext, src.Ext) {
			return OriginalImportResult{}, originalError("SOURCE_CHANGED", "source document changed during preparation")
		}
	} else {
		source, _, err := s.resolve(ctx, tx, caller, p.source.UploadFileID)
		if err != nil {
			return OriginalImportResult{}, err
		}
		if source.ContentHash != p.source.ContentHash || !bytes.Equal(source.Ext, p.source.Ext) {
			return OriginalImportResult{}, originalError("SOURCE_CHANGED", "original changed during preparation")
		}
	}
	if req.ParentID != "" {
		var parent orm.Document
		if err := tx.Where("id = ? AND dataset_id = ? AND document_type = ? AND deleted_at IS NULL", req.ParentID, req.DatasetID, "FOLDER").Take(&parent).Error; err != nil {
			return OriginalImportResult{}, originalError(DatasetServiceInvalidArgument, "target folder not found")
		}
	}
	var conflicts []orm.Document
	if err := tx.Where("dataset_id = ? AND p_id = ? AND display_name = ? AND deleted_at IS NULL", req.DatasetID, req.ParentID, req.DisplayName).Find(&conflicts).Error; err != nil {
		return OriginalImportResult{}, err
	}
	if len(conflicts) > 0 {
		if req.ConflictPolicy == "skip_same_content" {
			for _, existing := range conflicts {
				var uploaded orm.UploadedFile
				if tx.Where("document_id = ? AND dataset_id = ? AND content_hash = ? AND deleted_at IS NULL", existing.ID, req.DatasetID, p.hash).Take(&uploaded).Error == nil {
					return OriginalImportResult{Document: existing, ContentSHA256: p.hash, SubmissionStatus: "skipped", Skipped: true}, nil
				}
			}
		}
		return OriginalImportResult{}, originalError("CONFLICT", "target already contains this name")
	}
	now := s.now()
	docID, taskID := firstNonEmpty(req.DocumentID, newDocID()), firstNonEmpty(req.TaskID, newTaskID())
	meta := p.metadata
	base := orm.BaseModel{CreateUserID: caller.UserID, CreateUserName: firstNonEmpty(caller.UserName, caller.UserID), CreatedAt: now, UpdatedAt: now}
	dExt := newDocumentExt(meta.StoredPath, meta.StoredName, meta.OriginalFilename, meta.FileSize, meta.ContentType, meta.RelativePath, req.Tags)
	d := orm.Document{ID: docID, ResourceVersion: 1, DatasetID: dataset.ID, DisplayName: req.DisplayName, DocumentType: fileDocumentTypeFromName(meta.OriginalFilename), PID: req.ParentID, Tags: mustJSON(req.Tags), FileID: docID, PDFConvertResult: dExt.ConvertStatus, Ext: mustJSON(dExt), BaseModel: base}
	te := taskExt{TaskType: string(TaskTypeParseUploaded), DocumentPID: req.ParentID, DisplayName: req.DisplayName, DataSourceType: firstNonEmpty(req.DataSourceType, "LOCAL_FILE"), DocumentTags: req.Tags, Files: []TaskFile{{DisplayName: req.DisplayName, StoredName: meta.StoredName, StoredPath: meta.StoredPath, FileSize: meta.FileSize, RelativePath: meta.RelativePath, ContentType: meta.ContentType}}}
	if effectiveProcessingLevel(dataset.ProcessingLevel) == ProcessingLevelStored {
		te.TaskState = string(TaskStateSucceeded)
		te.ProcessingLevel = ProcessingLevelStored
	}
	task := orm.Task{ID: taskID, DocID: docID, KbID: dataset.ID, AlgoID: parseDatasetAlgo(dataset.Ext).AlgoID, DatasetID: dataset.ID, TaskType: string(TaskTypeParseUploaded), DocumentPID: req.ParentID, DisplayName: req.DisplayName, Ext: mustJSON(te), BaseModel: base}
	upload := orm.UploadedFile{UploadFileID: p.uploadID, DatasetID: dataset.ID, TenantID: dataset.TenantID, ContentHash: p.hash, TaskID: taskID, DocumentID: docID, Status: UploadedFileStateBound, Ext: mustJSON(meta), BaseModel: base}
	for _, row := range []any{&d, &task, &upload} {
		if err := tx.Create(row).Error; err != nil {
			return OriginalImportResult{}, err
		}
	}
	if err := createDocumentProcessingState(tx, dataset.ID, docID, now); err != nil {
		return OriginalImportResult{}, err
	}
	return OriginalImportResult{Document: d, Task: task, ContentSHA256: p.hash, SubmissionStatus: "pending"}, nil
}
func (s *OriginalService) ImportOriginal(ctx context.Context, caller DatasetCatalogCaller, req ImportOriginalRequest) (OriginalImportResult, error) {
	p, err := s.PrepareOriginalBinding(ctx, caller, req)
	if err != nil {
		return OriginalImportResult{}, err
	}
	ctx, err = PrepareResourceAuthorization(ctx, caller, p.AuthorizationRequests())
	if err != nil {
		_ = s.CleanupPreparedBinding(p)
		return OriginalImportResult{}, err
	}
	var result OriginalImportResult
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var err error; result, err = s.BindOriginalTx(ctx, tx, caller, p); return err })
	if err != nil {
		_ = s.CleanupPreparedBinding(p)
		return OriginalImportResult{}, err
	}
	if result.Skipped {
		_ = s.CleanupPreparedBinding(p)
		return result, nil
	}
	return s.SubmitOriginalProcessing(ctx, caller, result), nil
}
func (s *OriginalService) SubmitOriginalProcessing(ctx context.Context, caller DatasetCatalogCaller, result OriginalImportResult) OriginalImportResult {
	submit := s.submit
	if submit == nil {
		submit = SubmitOriginalTask
	}
	start, err := submit(ctx, caller, result.Document.DatasetID, result.Task.ID)
	result.Start = start
	if err != nil || start.Status != "STARTED" {
		result.SubmissionStatus = "failed"
		result.SubmissionError = start.Message
		if err != nil {
			result.SubmissionError = err.Error()
		}
		var state taskExt
		if json.Unmarshal(result.Task.Ext, &state) == nil {
			state.TaskState = string(TaskStateFailed)
			state.ErrorMessage = result.SubmissionError
			if persistErr := s.db.WithContext(ctx).Model(&orm.Task{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", result.Task.ID, result.Document.DatasetID).Updates(map[string]any{"ext": mustJSON(state), "updated_at": s.now()}).Error; persistErr != nil {
				result.SubmissionError += "; failed to persist submission status: " + persistErr.Error()
			}
		}
	} else {
		result.SubmissionStatus = "submitted"
		if start.Message == "document stored without parsing" {
			result.SubmissionStatus = "not_requested"
		}
	}
	return result
}

// Storage is restricted to the configured upload root, including symlink resolution.
func openControlledOriginal(path string) (*os.File, error) {
	root, err := filepath.EvalSymlinks(uploadRoot())
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, originalError(DatasetServiceForbidden, "original path outside upload storage")
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("original must be a regular file")
	}
	return f, nil
}

type originalContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r originalContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
func writeOriginal(ctx context.Context, path, name, expectedMIME string, reader io.Reader, expectedHash string) (int64, string, string, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, "", "", err
	}
	defer f.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(originalContextReader{ctx, reader}, originalMaxBytes+1))
	if err != nil {
		return 0, "", "", err
	}
	if n == 0 || n > originalMaxBytes {
		return 0, "", "", originalError(DatasetServiceInvalidArgument, "original is empty or exceeds size limit")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if expectedHash != "" && strings.TrimPrefix(expectedHash, "sha256:") != digest {
		return 0, "", "", originalError("SOURCE_CHANGED", "original content hash mismatch")
	}
	mime, err := validateOriginalBytes(f, n, name, expectedMIME)
	if err != nil {
		return 0, "", "", err
	}
	if err := f.Sync(); err != nil {
		return 0, "", "", err
	}
	return n, digest, mime, nil
}
func validateOriginalBytes(f *os.File, size int64, name, expectedMIME string) (string, error) {
	header := make([]byte, 512)
	n, err := f.ReadAt(header, 0)
	if err != nil && err != io.EOF {
		return "", err
	}
	header = header[:n]
	ext := strings.ToLower(filepath.Ext(name))
	mime := http.DetectContentType(header)
	switch ext {
	case ".pdf":
		if !bytes.HasPrefix(header, []byte("%PDF-")) {
			return "", errors.New("original is not a PDF")
		}
		tail := make([]byte, min(size, int64(4096)))
		_, _ = f.ReadAt(tail, size-int64(len(tail)))
		if !bytes.Contains(tail, []byte("%%EOF")) {
			return "", errors.New("PDF is incomplete")
		}
		mime = "application/pdf"
	case ".docx", ".xlsx", ".pptx", ".pptm", ".epub", ".zip":
		zr, err := zip.NewReader(f, size)
		if err != nil {
			return "", errors.New("original is not a valid ZIP container")
		}
		if ext != ".zip" && ext != ".epub" {
			prefix := map[string]string{".docx": "word/", ".xlsx": "xl/", ".pptx": "ppt/", ".pptm": "ppt/"}[ext]
			types, content := false, false
			for _, entry := range zr.File {
				types = types || entry.Name == "[Content_Types].xml"
				content = content || strings.HasPrefix(entry.Name, prefix)
			}
			if !types || !content {
				return "", errors.New("Office container does not match filename")
			}
		}
		mime = map[string]string{".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation", ".zip": "application/zip", ".pptm": "application/vnd.ms-powerpoint.presentation.macroEnabled.12", ".epub": "application/epub+zip"}[ext]
	case ".txt", ".md", ".csv", ".tsv", ".html", ".htm", ".json", ".xml", ".ipynb", ".jsonl", ".yaml", ".yml", ".py", ".mbox":
		// Validate the full stream, not only a plausible prefix.
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		decoder := json.NewDecoder(f)
		if ext == ".json" || ext == ".ipynb" {
			var v any
			if err := decoder.Decode(&v); err != nil {
				return "", errors.New("invalid JSON original")
			}
			if err := decoder.Decode(&v); err != io.EOF {
				return "", errors.New("invalid trailing JSON content")
			}
		} else {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return "", err
			}
			buf := make([]byte, 64<<10)
			var pending []byte
			for {
				n, e := f.Read(buf)
				data := append(pending, buf[:n]...)
				pending = nil
				for len(data) > 0 {
					if !utf8.FullRune(data) {
						pending = append(pending, data...)
						break
					}
					r, k := utf8.DecodeRune(data)
					if r == utf8.RuneError && k == 1 || r == 0 {
						return "", errors.New("invalid text original")
					}
					data = data[k:]
				}
				if e == io.EOF {
					if len(pending) > 0 {
						return "", errors.New("invalid text original")
					}
					break
				}
				if e != nil {
					return "", e
				}
			}
		}
		mime = map[string]string{".md": "text/markdown", ".html": "text/html", ".htm": "text/html", ".json": "application/json", ".xml": "application/xml", ".csv": "text/csv", ".tsv": "text/tab-separated-values", ".txt": "text/plain"}[ext]
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp", ".tiff", ".tif":
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		_, format, err := image.DecodeConfig(f)
		if err != nil {
			return "", errors.New("invalid image original")
		}
		expected := strings.TrimPrefix(ext, ".")
		if expected == "jpg" {
			expected = "jpeg"
		}
		if expected == "tif" {
			expected = "tiff"
		}
		if format != expected {
			return "", errors.New("image format does not match filename")
		}
		mime = "image/" + format
	case ".mp3":
		if !bytes.HasPrefix(header, []byte("ID3")) && !(len(header) > 1 && header[0] == 0xff && header[1]&0xe0 == 0xe0) {
			return "", errors.New("invalid MP3 original")
		}
		mime = "audio/mpeg"
	case ".mp4":
		if len(header) < 12 || !bytes.Equal(header[4:8], []byte("ftyp")) {
			return "", errors.New("invalid MP4 original")
		}
		mime = "video/mp4"
	case ".doc", ".xls", ".ppt":
		mime = map[string]string{".doc": "application/msword", ".xls": "application/vnd.ms-excel", ".ppt": "application/vnd.ms-powerpoint"}[ext]
		if !bytes.HasPrefix(header, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}) {
			return "", errors.New("invalid Office original")
		}
	default:
		return "", originalError("UNSUPPORTED_FORMAT", "unsupported original file format")
	}
	if mime == "" {
		mime = "text/plain"
	}
	expectedMIME = strings.TrimSpace(strings.Split(expectedMIME, ";")[0])
	if expectedMIME != "" && expectedMIME != "application/octet-stream" && expectedMIME != mime && !(strings.HasPrefix(expectedMIME, "text/") && strings.HasPrefix(mime, "text/")) {
		return "", fmt.Errorf("original MIME mismatch: expected %s, got %s", expectedMIME, mime)
	}
	return mime, nil
}

// AcquireUploadedOriginal bridges existing complete attachment sessions to the
// immutable UploadedFile contract. A conversation fr_ reference is not a session ID.
func (s *OriginalService) AcquireUploadedOriginal(ctx context.Context, caller DatasetCatalogCaller, id string) (OriginalRef, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&orm.UploadedFile{}).Where("upload_file_id = ?", id).Count(&count).Error; err != nil {
		return OriginalRef{}, err
	}
	if count > 0 {
		return s.ResolveOriginal(ctx, caller, id)
	}
	if err := validOriginalCaller(caller); err != nil {
		return OriginalRef{}, err
	}
	if strings.HasPrefix(id, "fr_") || strings.TrimSpace(id) == "" {
		return OriginalRef{}, originalError(DatasetServiceInvalidArgument, "controlled upload reference required")
	}
	var session orm.UploadSession
	if err := s.db.WithContext(ctx).Where("upload_id = ? AND deleted_at IS NULL", id).Take(&session).Error; err != nil {
		return OriginalRef{}, originalError(DatasetServiceNotFound, "upload original not found")
	}
	if session.CreateUserID != caller.UserID || resourceTenant(session.TenantID) != resourceTenant(caller.TenantID) {
		return OriginalRef{}, originalError(DatasetServiceForbidden, "upload belongs to another owner or tenant")
	}
	if session.UploadState != string(TaskStateUploaded) {
		return OriginalRef{}, originalError(DatasetServiceInvalidArgument, "upload is incomplete")
	}
	if !s.now().Before(session.CreatedAt.Add(originalRetention)) {
		return OriginalRef{}, originalError("SOURCE_EXPIRED", "upload reference has expired")
	}
	var meta uploadMeta
	if err := json.Unmarshal(session.Ext, &meta); err != nil {
		return OriginalRef{}, err
	}
	if meta.UploadScope != uploadScopeTemp {
		return OriginalRef{}, originalError(DatasetServiceInvalidArgument, "not a temporary upload")
	}
	if _, err := originalFilename(meta.StoredName); err != nil {
		return OriginalRef{}, err
	}
	path := filepath.Join(buildTempUploadFileDir(session.CreateUserID, session.UploadID), meta.StoredName)
	input, err := openControlledOriginal(path)
	if err != nil {
		return OriginalRef{}, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return OriginalRef{}, err
	}
	if info.Size() != meta.FileSize {
		return OriginalRef{}, originalError("SOURCE_CHANGED", "upload size changed")
	}
	return s.AcquireOriginal(ctx, caller, AcquireOriginalRequest{Filename: meta.OriginalFilename, ContentType: meta.ContentType, Reader: input})
}

// purgeExpiredOriginals bounds retention opportunistically when the same owner
// acquires another original. It never removes legacy attachment sessions or any
// bound record, and each target document owns a separate durable copy.
func (s *OriginalService) purgeExpiredOriginals(ctx context.Context, caller DatasetCatalogCaller) error {
	var rows []orm.UploadedFile
	if err := s.db.WithContext(ctx).Where("create_user_id = ? AND tenant_id = ? AND dataset_id = '' AND document_id = '' AND status = ? AND created_at <= ? AND deleted_at IS NULL", caller.UserID, caller.TenantID, UploadedFileStateUploaded, s.now().Add(-originalRetention)).Order("created_at").Limit(20).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var meta originalMetadata
		if json.Unmarshal(row.Ext, &meta) != nil || meta.ExpiresAt.IsZero() || s.now().Before(meta.ExpiresAt) {
			continue
		}
		directory := buildTempUploadFileDir(row.CreateUserID, row.UploadFileID)
		if filepath.Clean(meta.StoredPath) != filepath.Join(directory, meta.StoredName) {
			continue
		}
		now := s.now()
		removed := s.db.WithContext(ctx).Model(&orm.UploadedFile{}).Where("upload_file_id = ? AND status = ? AND document_id = '' AND deleted_at IS NULL", row.UploadFileID, UploadedFileStateUploaded).Update("deleted_at", now)
		if removed.Error != nil {
			return removed.Error
		}
		if removed.RowsAffected == 1 {
			if err := os.RemoveAll(directory); err != nil {
				return err
			}
		}
	}
	return nil
}
