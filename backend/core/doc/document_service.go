package doc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
)

const documentContentMaxBytes int64 = 1024 * 1024

type DocumentServiceErrorCode string

const (
	DocumentServiceInvalidArgument DocumentServiceErrorCode = "INVALID_ARGUMENT"
	DocumentServiceNotFound        DocumentServiceErrorCode = "NOT_FOUND"
	DocumentServiceForbidden       DocumentServiceErrorCode = "FORBIDDEN"
	DocumentServiceUnavailable     DocumentServiceErrorCode = "UNAVAILABLE"
	DocumentServiceUnsupported     DocumentServiceErrorCode = "UNSUPPORTED"
	DocumentServiceInternal        DocumentServiceErrorCode = "INTERNAL"
)

type DocumentServiceError struct {
	Code    DocumentServiceErrorCode
	Message string
	Err     error
}

func (e *DocumentServiceError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *DocumentServiceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type DocumentServiceDeps struct {
	DB     *gorm.DB
	LazyDB *gorm.DB
}

type DocumentService struct {
	db     *gorm.DB
	lazyDB *gorm.DB
}

func NewDocumentService(deps DocumentServiceDeps) (*DocumentService, error) {
	if deps.DB == nil {
		return nil, &DocumentServiceError{Code: DocumentServiceInternal, Message: "gorm db is required"}
	}
	lazyDB := deps.LazyDB
	if lazyDB == nil {
		lazyDB = deps.DB
	}
	return &DocumentService{db: deps.DB, lazyDB: lazyDB}, nil
}

type DocumentGetRequest struct {
	UserID     string
	DatasetID  string
	DocumentID string
	Caller     DatasetCatalogCaller
}

type DocumentContentRequest struct {
	UserID     string
	DatasetID  string
	DocumentID string
	Caller     DatasetCatalogCaller
}

type DocumentChunksRequest struct {
	UserID     string
	DatasetID  string
	DocumentID string
	PageToken  string
	PageSize   int
	Caller     DatasetCatalogCaller
}

type DocumentMetadata struct {
	ID           string
	DatasetID    string
	Name         string
	Source       string
	Tags         []string
	ParseStatus  string
	MIMEType     string
	SizeBytes    int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	CreatedBy    string
	OriginalFile *DocumentFileRef
}

type DocumentFileRef struct {
	FileName    string
	DownloadURL string
}

type DocumentContent struct {
	Text      string
	MIMEType  string
	Truncated bool
}

type DocumentChunk struct {
	ID     string
	Text   string
	Number int32
}

type DocumentChunksResult struct {
	Chunks        []DocumentChunk
	TotalSize     int32
	NextPageToken string
}

type documentServiceRecord struct {
	dataset  orm.Dataset
	row      orm.Document
	ext      documentExt
	lazy     *readonlyorm.LazyLLMDocRow
	taskData string
	taskStat string
}

func (s *DocumentService) GetDocumentMetadata(ctx context.Context, req DocumentGetRequest) (DocumentMetadata, error) {
	rec, err := s.loadRecord(ctx, req.UserID, req.DatasetID, req.DocumentID, req.Caller)
	if err != nil {
		return DocumentMetadata{}, err
	}
	return rec.metadata(), nil
}

func (s *DocumentService) ReadDocumentContent(ctx context.Context, req DocumentContentRequest) (DocumentContent, error) {
	rec, err := s.loadRecord(ctx, req.UserID, req.DatasetID, req.DocumentID, req.Caller)
	if err != nil {
		return DocumentContent{}, err
	}
	storedPath := previewPathForContent(rec.ext)
	mimeType := previewContentTypeForContent(rec.ext)
	filename := previewFilenameForContent(rec.ext)
	if filename == "" {
		filename = strings.TrimSpace(rec.row.DisplayName)
	}
	if mimeType == "" {
		mimeType = detectDocumentContentType(filename, storedPath, "")
	}
	if !isSafeTextDocumentContent(mimeType, filename, storedPath) {
		return DocumentContent{MIMEType: mimeType}, nil
	}
	if strings.TrimSpace(storedPath) == "" {
		return DocumentContent{}, &DocumentServiceError{Code: DocumentServiceNotFound, Message: "document file not found"}
	}
	text, truncated, err := readDocumentTextFile(storedPath, documentContentMaxBytes)
	if err != nil {
		return DocumentContent{}, err
	}
	return DocumentContent{Text: text, MIMEType: mimeType, Truncated: truncated}, nil
}

func (s *DocumentService) ListDocumentChunks(ctx context.Context, req DocumentChunksRequest) (DocumentChunksResult, error) {
	rec, err := s.loadRecord(ctx, req.UserID, req.DatasetID, req.DocumentID, req.Caller)
	if err != nil {
		return DocumentChunksResult{}, err
	}
	lazyDocID := strings.TrimSpace(rec.row.LazyllmDocID)
	if lazyDocID == "" {
		return DocumentChunksResult{Chunks: []DocumentChunk{}, TotalSize: 0}, nil
	}
	pageSize := normalizeDocumentChunkPageSize(req.PageSize)
	offset, err := parseDatasetPageToken(req.PageToken)
	if err != nil {
		return DocumentChunksResult{}, &DocumentServiceError{Code: DocumentServiceInvalidArgument, Message: "invalid page_token", Err: err}
	}
	if offset%pageSize != 0 {
		return DocumentChunksResult{}, &DocumentServiceError{Code: DocumentServiceInvalidArgument, Message: "page_token offset must align with page_size", Err: fmt.Errorf("unaligned page token offset")}
	}
	page := offset/pageSize + 1
	kbID := strings.TrimSpace(rec.dataset.KbID)
	if kbID == "" {
		return DocumentChunksResult{}, &DocumentServiceError{Code: DocumentServiceInternal, Message: "knowledge backend id is empty"}
	}
	algoID := parseDatasetAlgo(rec.dataset.Ext).AlgoID
	group := resolveDefaultChunkSegmentGroup(ctx, algoID, "DocumentService.ListDocumentChunks")
	queryURL := buildChunksURL(kbID, algoID, lazyDocID, group, page, pageSize)
	raw, err := fetchDocumentChunkPayload(ctx, queryURL)
	if err != nil {
		fallbackURL := buildParserChunksURL(kbID, algoID, lazyDocID, group, page, pageSize)
		fallbackRaw, fallbackErr := fetchDocumentChunkPayload(ctx, fallbackURL)
		if fallbackErr != nil {
			return DocumentChunksResult{}, &DocumentServiceError{Code: DocumentServiceUnavailable, Message: "query document chunks failed", Err: fmt.Errorf("%w; fallback parser chunks failed: %v", err, fallbackErr)}
		}
		raw = fallbackRaw
	}
	segments, total, next := parseChunkSearchResponse(req.DatasetID, req.DocumentID, raw, page, pageSize)
	chunks := make([]DocumentChunk, 0, len(segments))
	for _, segment := range segments {
		chunks = append(chunks, DocumentChunk{ID: segment.SegmentID, Text: segment.Text, Number: segment.Number})
	}
	return DocumentChunksResult{Chunks: chunks, TotalSize: total, NextPageToken: next}, nil
}

func (s *DocumentService) loadRecord(ctx context.Context, userID, datasetID, documentID string, caller DatasetCatalogCaller) (documentServiceRecord, error) {
	if s == nil || s.db == nil {
		return documentServiceRecord{}, &DocumentServiceError{Code: DocumentServiceInternal, Message: "document service is not configured"}
	}
	userID = strings.TrimSpace(userID)
	datasetID = strings.TrimSpace(datasetID)
	documentID = strings.TrimSpace(documentID)
	if userID == "" {
		return documentServiceRecord{}, &DocumentServiceError{Code: DocumentServiceInvalidArgument, Message: "user_id is required"}
	}
	if datasetID == "" {
		return documentServiceRecord{}, &DocumentServiceError{Code: DocumentServiceInvalidArgument, Message: "dataset_id is required"}
	}
	if documentID == "" {
		return documentServiceRecord{}, &DocumentServiceError{Code: DocumentServiceInvalidArgument, Message: "document_id is required"}
	}
	caller.UserID = firstNonEmpty(strings.TrimSpace(caller.UserID), userID)
	ds, err := s.requireDatasetRead(ctx, userID, datasetID, caller)
	if err != nil {
		return documentServiceRecord{}, err
	}
	var row orm.Document
	if err := s.db.WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", documentID, datasetID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return documentServiceRecord{}, &DocumentServiceError{Code: DocumentServiceNotFound, Message: "document not found", Err: err}
		}
		return documentServiceRecord{}, &DocumentServiceError{Code: DocumentServiceUnavailable, Message: "query document failed", Err: err}
	}
	var ext documentExt
	_ = json.Unmarshal(row.Ext, &ext)
	rec := documentServiceRecord{dataset: ds, row: row, ext: ext}
	lazyDocID := strings.TrimSpace(row.LazyllmDocID)
	if lazyDocID != "" {
		if lazy, err := s.loadLazyDocument(ctx, lazyDocID); err != nil {
			return documentServiceRecord{}, err
		} else if lazy != nil {
			rec.lazy = lazy
		}
		rec.taskData = s.latestTaskDataSource(ctx, row.ID)
		rec.taskStat = s.latestLazyTaskStatus(ctx, lazyDocID)
	}
	return rec, nil
}

func (s *DocumentService) requireDatasetRead(ctx context.Context, userID, datasetID string, caller DatasetCatalogCaller) (orm.Dataset, error) {
	var ds orm.Dataset
	if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).Take(&ds).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return orm.Dataset{}, &DocumentServiceError{Code: DocumentServiceNotFound, Message: "dataset not found", Err: err}
		}
		return orm.Dataset{}, &DocumentServiceError{Code: DocumentServiceUnavailable, Message: "query dataset failed", Err: err}
	}
	if !canAccessDataset(&ds, userID, acl.PermissionDatasetRead) {
		return orm.Dataset{}, &DocumentServiceError{Code: DocumentServiceForbidden, Message: "dataset forbidden"}
	}
	if !datasetAllowedByScanSourceForCaller(ctx, caller, ds.ID, acl.PermissionDatasetRead) {
		return orm.Dataset{}, &DocumentServiceError{Code: DocumentServiceForbidden, Message: "dataset forbidden"}
	}
	return ds, nil
}

func (s *DocumentService) loadLazyDocument(ctx context.Context, lazyDocID string) (*readonlyorm.LazyLLMDocRow, error) {
	if s.lazyDB == nil {
		return nil, nil
	}
	var row readonlyorm.LazyLLMDocRow
	err := s.lazyDB.WithContext(ctx).Table((readonlyorm.LazyLLMDocRow{}).TableName()).Where("doc_id = ?", lazyDocID).Take(&row).Error
	if err == nil {
		return &row, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return nil, &DocumentServiceError{Code: DocumentServiceUnavailable, Message: "query readonly document failed", Err: err}
}

func (s *DocumentService) latestTaskDataSource(ctx context.Context, docID string) string {
	var task orm.Task
	if err := s.db.WithContext(ctx).Where("doc_id = ? AND deleted_at IS NULL", docID).Order("updated_at DESC").Take(&task).Error; err != nil {
		return ""
	}
	var ext taskExt
	_ = json.Unmarshal(task.Ext, &ext)
	return strings.TrimSpace(ext.DataSourceType)
}

func (s *DocumentService) latestLazyTaskStatus(ctx context.Context, lazyDocID string) string {
	if s.lazyDB == nil {
		return ""
	}
	var task readonlyorm.LazyLLMDocServiceTaskRow
	err := s.lazyDB.WithContext(ctx).Table((readonlyorm.LazyLLMDocServiceTaskRow{}).TableName()).Where("doc_id = ?", lazyDocID).Order("updated_at DESC").Take(&task).Error
	if err != nil {
		return ""
	}
	return strings.TrimSpace(task.Status)
}

func (r documentServiceRecord) metadata() DocumentMetadata {
	row := r.row
	ext := r.ext
	displayName := firstNonEmpty(strings.TrimSpace(row.DisplayName), strings.TrimSpace(ext.OriginalFilename), strings.TrimSpace(ext.StoredName))
	if r.lazy != nil {
		displayName = firstNonEmpty(strings.TrimSpace(row.DisplayName), strings.TrimSpace(r.lazy.Filename), displayName)
	}
	if displayName == "" {
		displayName = row.ID
	}
	size := ext.FileSize
	if size == 0 && r.lazy != nil && r.lazy.SizeBytes != nil {
		size = int64(*r.lazy.SizeBytes)
	}
	source := firstNonEmpty(r.taskData, "LOCAL_FILE")
	if r.lazy != nil && r.taskData == "" {
		source = dataSourceTypeFromSourceType(r.lazy.SourceType)
		if source == "" || source == "DATA_SOURCE_TYPE_UNSPECIFIED" {
			source = "LOCAL_FILE"
		}
	}
	parseStatus := firstNonEmpty(strings.TrimSpace(row.PDFConvertResult), strings.TrimSpace(ext.ConvertStatus), r.taskStat)
	if r.lazy != nil {
		parseStatus = firstNonEmpty(parseStatus, strings.TrimSpace(r.lazy.UploadStatus))
	}
	var tags []string
	_ = json.Unmarshal(row.Tags, &tags)
	if tags == nil {
		tags = []string{}
	}
	mimeType := previewContentTypeForContent(ext)
	if mimeType == "" {
		mimeType = detectDocumentContentType(previewFilenameForContent(ext), previewPathForContent(ext), "")
	}
	filename := firstNonEmpty(strings.TrimSpace(ext.OriginalFilename), strings.TrimSpace(ext.StoredName), displayName)
	return DocumentMetadata{
		ID:          row.ID,
		DatasetID:   row.DatasetID,
		Name:        displayName,
		Source:      source,
		Tags:        append([]string(nil), tags...),
		ParseStatus: parseStatus,
		MIMEType:    mimeType,
		SizeBytes:   size,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		CreatedBy:   row.CreateUserName,
		OriginalFile: &DocumentFileRef{
			FileName:    filename,
			DownloadURL: documentDownloadPath(row.DatasetID, row.ID),
		},
	}
}

func normalizeDocumentChunkPageSize(pageSize int) int {
	if pageSize <= 0 {
		return 20
	}
	if pageSize > 100 {
		return 100
	}
	return pageSize
}

func isSafeTextDocumentContent(contentType, filename, storedPath string) bool {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = detectDocumentContentType(filename, storedPath, "")
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	}
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json", "application/x-ndjson", "application/xml", "application/javascript", "application/x-yaml":
		return true
	}
	return strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml")
}

func readDocumentTextFile(fullPath string, maxBytes int64) (string, bool, error) {
	realPath, err := resolveAllowedDocumentFilePath(fullPath)
	if err != nil {
		return "", false, err
	}
	f, err := os.Open(realPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, &DocumentServiceError{Code: DocumentServiceNotFound, Message: "document file not found", Err: err}
		}
		return "", false, &DocumentServiceError{Code: DocumentServiceUnavailable, Message: "open document file failed", Err: err}
	}
	defer f.Close()
	limit := maxBytes
	if limit <= 0 {
		limit = documentContentMaxBytes
	}
	reader := io.LimitReader(f, limit+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", false, &DocumentServiceError{Code: DocumentServiceUnavailable, Message: "read document file failed", Err: err}
	}
	truncated := int64(len(data)) > limit
	if truncated {
		data = data[:limit]
	}
	data = bytes.ToValidUTF8(data, nil)
	return string(data), truncated, nil
}

func resolveAllowedDocumentFilePath(fullPath string) (string, error) {
	raw := strings.TrimSpace(fullPath)
	if raw == "" {
		return "", &DocumentServiceError{Code: DocumentServiceNotFound, Message: "document file not found"}
	}
	candidateAbs, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", &DocumentServiceError{Code: DocumentServiceForbidden, Message: "document file forbidden"}
	}
	candidateReal, err := filepath.EvalSymlinks(candidateAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &DocumentServiceError{Code: DocumentServiceNotFound, Message: "document file not found", Err: err}
		}
		return "", &DocumentServiceError{Code: DocumentServiceForbidden, Message: "document file forbidden", Err: err}
	}
	candidateReal, err = filepath.Abs(filepath.Clean(candidateReal))
	if err != nil {
		return "", &DocumentServiceError{Code: DocumentServiceForbidden, Message: "document file forbidden"}
	}
	for _, root := range []string{uploadRoot(), subagentWorkspaceRoot()} {
		if rootReal, ok := resolveAllowedDocumentRoot(root); ok && pathWithinRoot(candidateReal, rootReal) {
			return candidateReal, nil
		}
	}
	return "", &DocumentServiceError{Code: DocumentServiceForbidden, Message: "document file forbidden"}
}

func resolveAllowedDocumentRoot(root string) (string, bool) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", false
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", false
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", false
	}
	rootReal, err = filepath.Abs(filepath.Clean(rootReal))
	if err != nil {
		return "", false
	}
	return rootReal, true
}

func pathWithinRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func fetchDocumentChunkPayload(ctx context.Context, queryURL string) (map[string]any, error) {
	var raw map[string]any
	if err := common.ApiGet(ctx, queryURL, nil, &raw, 10_000_000_000); err != nil {
		return nil, err
	}
	if err := validateDocumentChunkBusinessResponse(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func validateDocumentChunkBusinessResponse(raw map[string]any) error {
	if raw == nil {
		return nil
	}
	if err := validateDocumentChunkCode(raw["code"]); err != nil {
		return err
	}
	if err := validateDocumentChunkStatus(raw["status"]); err != nil {
		return err
	}
	if err := validateDocumentChunkSuccess(raw["success"]); err != nil {
		return err
	}
	return nil
}

func validateDocumentChunkCode(value any) error {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case float64:
		if v == 0 || v == 200 {
			return nil
		}
	case int:
		if v == 0 || v == 200 {
			return nil
		}
	case int32:
		if v == 0 || v == 200 {
			return nil
		}
	case int64:
		if v == 0 || v == 200 {
			return nil
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && (n == 0 || n == 200) {
			return nil
		}
	case string:
		text := strings.ToLower(strings.TrimSpace(v))
		if text == "" || text == "0" || text == "200" || text == "ok" || text == "success" {
			return nil
		}
		if n, err := strconv.Atoi(text); err == nil && (n == 0 || n == 200) {
			return nil
		}
	default:
		return nil
	}
	return errors.New("chunk backend business code failed")
}

func validateDocumentChunkStatus(value any) error {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case bool:
		if v {
			return nil
		}
	case float64:
		if v == 0 || v == 200 {
			return nil
		}
	case int:
		if v == 0 || v == 200 {
			return nil
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "", "0", "200", "ok", "success", "succeeded", "true", "done", "ready":
			return nil
		}
	default:
		return nil
	}
	return errors.New("chunk backend business status failed")
}

func validateDocumentChunkSuccess(value any) error {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case bool:
		if v {
			return nil
		}
	case float64:
		if v != 0 {
			return nil
		}
	case int:
		if v != 0 {
			return nil
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "ok", "success", "succeeded":
			return nil
		}
	default:
		return nil
	}
	return errors.New("chunk backend business success failed")
}
