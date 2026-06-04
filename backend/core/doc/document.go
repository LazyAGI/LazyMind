package doc

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/log"
	"lazymind/core/store"

	"github.com/gorilla/mux"
)

// DocumentService implements document APIs by joining:
// - schema A (core-owned diff): orm.documents / orm.tasks
// - schema B (readonly, maintained by lazy-llm-server): lazy_llm_server.lazyllm_*

var fuzzyPunctRe = regexp.MustCompile(`[._\-\s（）()]+`)

// fuzzySearchMaxCandidates caps DB rows loaded for in-memory fuzzy scoring (avoids OOM).
const fuzzySearchMaxCandidates = 5000

func requireDatasetPermission(r *http.Request, datasetID string, action string) (*orm.Dataset, string, bool) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		return nil, userID, false
	}
	var ds orm.Dataset
	if err := store.DB().WithContext(r.Context()).
		Where("id = ? AND deleted_at IS NULL", datasetID).
		First(&ds).Error; err != nil {
		return nil, userID, false
	}
	if !canAccessDataset(&ds, userID, action) {
		return &ds, userID, false
	}
	return &ds, userID, true
}

func replyDatasetForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(common.ForbiddenBody))
}

func publicBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("LAZYMIND_PUBLIC_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://localhost:8000/api/core"
}

func absoluteCoreURL(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}

func signedFileSecret() string {
	if v := strings.TrimSpace(os.Getenv("LAZYMIND_FILE_URL_SIGN_SECRET")); v != "" {
		return v
	}
	return "lazymind-file-url-secret"
}

func signedFileExpireSeconds() int64 {
	if v := strings.TrimSpace(os.Getenv("LAZYMIND_FILE_URL_EXPIRE_SECONDS")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 3600
}

func fileRelativePath(fullPath string) string {
	p := strings.TrimSpace(fullPath)
	if p == "" {
		return ""
	}
	cleanPath := filepath.Clean(p)
	roots := []string{strings.TrimSpace(uploadRoot())}
	for _, root := range roots {
		if root == "" {
			continue
		}
		cleanRoot := filepath.Clean(root)
		rel, err := filepath.Rel(cleanRoot, cleanPath)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return filepath.ToSlash(rel)
	}
	return ""
}

func signStaticFile(rel string, expires int64) string {
	mac := hmac.New(sha256.New, []byte(signedFileSecret()))
	_, _ = mac.Write([]byte(rel))
	_, _ = mac.Write([]byte("\n"))
	_, _ = mac.Write([]byte(strconv.FormatInt(expires, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

func staticFileURLFromFullPath(fullPath string) string {
	rel := fileRelativePath(fullPath)
	if rel == "" {
		return ""
	}
	expires := time.Now().UTC().Unix() + signedFileExpireSeconds()
	sig := signStaticFile(rel, expires)
	return fmt.Sprintf("/static-files/%s?expires=%d&sig=%s", encodeStaticFilePath(rel), expires, sig)
}

// UploadRoot returns the configured local root used by the signed static file service.
func UploadRoot() string {
	return uploadRoot()
}

// StaticFileURLFromFullPath returns a signed, time-limited URL for a file below UploadRoot.
func StaticFileURLFromFullPath(fullPath string) string {
	return staticFileURLFromFullPath(fullPath)
}

func encodeStaticFilePath(rel string) string {
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func documentContentPath(datasetID, docID string) string {
	return absoluteCoreURL(fmt.Sprintf("/datasets/%s/documents/%s:content", datasetID, docID))
}

func documentDownloadPath(datasetID, docID string) string {
	return absoluteCoreURL(fmt.Sprintf("/datasets/%s/documents/%s:download", datasetID, docID))
}

func uploadedFileContentPath(datasetID, uploadFileID string) string {
	return absoluteCoreURL(fmt.Sprintf("/datasets/%s/uploads/%s:content", datasetID, uploadFileID))
}

func uploadedFileDownloadPath(datasetID, uploadFileID string) string {
	return absoluteCoreURL(fmt.Sprintf("/datasets/%s/uploads/%s:download", datasetID, uploadFileID))
}

func setDocumentURI(doc *Doc) {
	if doc == nil {
		return
	}
	if strings.TrimSpace(doc.DocumentID) == "" || strings.TrimSpace(doc.DatasetID) == "" {
		return
	}
	doc.URI = documentContentPath(doc.DatasetID, doc.DocumentID)
}

func streamLocalFile(w http.ResponseWriter, fullPath, filename, fallbackContentType string, inline bool) {
	cleanPath := filepath.Clean(strings.TrimSpace(fullPath))
	root := filepath.Clean(uploadRoot())
	rel, relErr := filepath.Rel(root, cleanPath)
	if cleanPath == "" || relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		common.ReplyErr(w, "file path is invalid", http.StatusBadRequest)
		return
	}
	f, err := os.Open(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "file not found", err), http.StatusNotFound)
			return
		}
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "open file failed", err), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "read file failed", err), http.StatusInternalServerError)
		return
	}
	name := strings.TrimSpace(filename)
	if name == "" {
		name = filepath.Base(cleanPath)
	}
	contentType := detectDocumentContentType(name, cleanPath, fallbackContentType)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Del("ETag")
	w.Header().Del("Last-Modified")
	if inline {
		w.Header().Set("Content-Disposition", contentDispositionHeader("inline", name))
	} else {
		w.Header().Set("Content-Disposition", contentDispositionHeader("attachment", name))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

func contentDispositionHeader(disposition, filename string) string {
	name := strings.TrimSpace(filename)
	if name == "" {
		name = "download"
	}
	fallback := strings.NewReplacer("\\", "\\\\", `"`, `\"`, "\r", "", "\n", "").Replace(name)
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disposition, fallback, url.PathEscape(name))
}

type signStaticFilesRequest struct {
	Paths []string `json:"paths"`
}

type signStaticFilesResponse struct {
	URLs map[string]string `json:"urls"`
}

// SignStaticFiles returns signed /static-files URLs for upload-root paths.
func SignStaticFiles(w http.ResponseWriter, r *http.Request) {
	var req signStaticFilesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid request body", err), http.StatusBadRequest)
		return
	}
	urls := make(map[string]string, len(req.Paths))
	for _, raw := range req.Paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		if strings.HasPrefix(path, "/static-files/") {
			urls[path] = path
			continue
		}
		if signed := staticFileURLFromFullPath(path); signed != "" {
			urls[path] = signed
		}
	}
	common.ReplyJSON(w, signStaticFilesResponse{URLs: urls})
}

func GetSignedStaticFile(w http.ResponseWriter, r *http.Request) {
	rawPath := strings.TrimSpace(common.PathVar(r, "path"))
	if rawPath == "" {
		common.ReplyErr(w, "missing path", http.StatusBadRequest)
		return
	}
	decodedPath, err := url.PathUnescape(rawPath)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid path encoding", err), http.StatusBadRequest)
		return
	}
	relPath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+decodedPath)), "/")
	if relPath == "" || relPath == "." || strings.HasPrefix(relPath, "../") {
		common.ReplyErr(w, "invalid path", http.StatusBadRequest)
		return
	}
	expiresStr := strings.TrimSpace(r.URL.Query().Get("expires"))
	sig := strings.TrimSpace(r.URL.Query().Get("sig"))
	if expiresStr == "" || sig == "" {
		common.ReplyErr(w, "missing signature", http.StatusForbidden)
		return
	}
	expires, err := strconv.ParseInt(expiresStr, 10, 64)
	if err != nil || expires <= 0 || time.Now().UTC().Unix() > expires {
		common.ReplyErr(w, "url expired", http.StatusForbidden)
		return
	}
	expected := signStaticFile(relPath, expires)
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		common.ReplyErr(w, "invalid signature", http.StatusForbidden)
		return
	}
	fullPath := filepath.Join(uploadRoot(), filepath.FromSlash(relPath))
	inline := !strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("download")), "1") &&
		!strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("download")), "true")
	streamLocalFile(w, fullPath, filepath.Base(fullPath), "", inline)
}

func detectDocumentContentType(name, storedPath, fallback string) string {
	if v := strings.TrimSpace(fallback); v != "" {
		return v
	}
	if ext := strings.TrimSpace(filepath.Ext(name)); ext != "" {
		if ct := knownDocumentContentType(ext); ct != "" {
			return ct
		}
		if ct := mime.TypeByExtension(strings.ToLower(ext)); ct != "" {
			return ct
		}
	}
	if ext := strings.TrimSpace(filepath.Ext(storedPath)); ext != "" {
		if ct := knownDocumentContentType(ext); ct != "" {
			return ct
		}
		if ct := mime.TypeByExtension(strings.ToLower(ext)); ct != "" {
			return ct
		}
	}
	return "application/octet-stream"
}

func knownDocumentContentType(ext string) string {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".tsv":
		return "text/tab-separated-values; charset=utf-8"
	default:
		return ""
	}
}

func loadDocumentFileMeta(ctx context.Context, datasetID, docID string) (orm.Document, documentExt, error) {
	var row orm.Document
	if err := store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", docID, datasetID).Take(&row).Error; err != nil {
		return orm.Document{}, documentExt{}, err
	}
	var ext documentExt
	_ = json.Unmarshal(row.Ext, &ext)
	return row, ext, nil
}

func streamDocumentFile(w http.ResponseWriter, r *http.Request, inline bool) {
	datasetID := datasetIDFromPath(r)
	docID := documentIDFromPath(r)
	if datasetID == "" || docID == "" {
		common.ReplyErr(w, "missing dataset or document", http.StatusBadRequest)
		return
	}
	if _, userID, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetRead); !ok {
		if userID == "" {
			common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		} else {
			replyDatasetForbidden(w)
		}
		return
	}
	row, ext, err := loadDocumentFileMeta(r.Context(), datasetID, docID)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "document not found", err), http.StatusNotFound)
		return
	}
	storedPath := previewPathForContent(ext)
	if storedPath == "" {
		common.ReplyErr(w, "document file not found", http.StatusNotFound)
		return
	}
	filename := previewFilenameForContent(ext)
	if filename == "" {
		filename = strings.TrimSpace(row.DisplayName)
	}
	streamLocalFile(w, storedPath, filename, previewContentTypeForContent(ext), inline)
}

func GetDocumentContent(w http.ResponseWriter, r *http.Request) {
	streamDocumentFile(w, r, true)
}

func DownloadDocument(w http.ResponseWriter, r *http.Request) {
	streamDocumentFile(w, r, false)
}

func GetUploadedFileContent(w http.ResponseWriter, r *http.Request) {
	streamUploadedFile(w, r, true)
}

func DownloadUploadedFile(w http.ResponseWriter, r *http.Request) {
	streamUploadedFile(w, r, false)
}

func ListDocuments(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "missing dataset", http.StatusBadRequest)
		return
	}
	if _, userID, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetRead); !ok {
		if userID == "" {
			common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		} else {
			replyDatasetForbidden(w)
		}
		return
	}

	q := r.URL.Query()
	pageToken := strings.TrimSpace(q.Get("page_token"))
	pageSizeStr := strings.TrimSpace(q.Get("page_size"))
	pid := firstNonEmpty(
		strings.TrimSpace(q.Get("p_id")),
		strings.TrimSpace(q.Get("document_pid")),
		strings.TrimSpace(q.Get("pid")),
		parseDocumentPIDFromParentName(strings.TrimSpace(q.Get("parent"))),
	)

	pageSize := 20
	if pageSizeStr != "" {
		if v, err := strconv.Atoi(pageSizeStr); err == nil && v > 0 {
			pageSize = v
		}
	}
	if pageSize > 1000 {
		pageSize = 1000
	}
	offset := 0
	if pageToken != "" {
		if v, err := strconv.Atoi(pageToken); err == nil && v >= 0 {
			offset = v
		}
	}

	rows, total, err := loadDatasetDocuments(r.Context(), datasetID, "", pid, true, pageSize, offset)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "query documents failed", err), http.StatusInternalServerError)
		return
	}

	next := ""
	if offset+len(rows) < int(total) {
		next = encodeDatasetPageToken(offset+len(rows), int(pageSize), int(total))
	}

	relPaths := buildDocumentTreeRelPaths(r.Context(), rows)
	out := make([]Doc, 0, len(rows))
	for _, rr := range rows {
		rr.RelPath = relPaths[rr.DocID]
		doc := docFromRow(rr)
		setDocumentURI(&doc)
		out = append(out, doc)
	}
	common.ReplyJSON(w, ListDocumentsResponse{Documents: out, TotalSize: int32(total), NextPageToken: next})
}
func CreateDocument(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "missing dataset", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	userName := store.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	if _, _, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetUpload); !ok {
		replyDatasetForbidden(w)
		return
	}

	docID := strings.TrimSpace(r.URL.Query().Get("document_id"))
	if docID == "" {
		docID = newDocID()
	}

	var body Doc
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	display := strings.TrimSpace(body.DisplayName)
	if display == "" {
		display = docID
	}
	pid := strings.TrimSpace(body.PID)
	fileID := strings.TrimSpace(body.FileID)
	tagsBytes, _ := json.Marshal(body.Tags)
	now := time.Now().UTC()

	row := orm.Document{
		ID:           docID,
		LazyllmDocID: "",
		DatasetID:    datasetID,
		DisplayName:  display,
		PID:          pid,
		Tags:         tagsBytes,
		FileID:       fileID,
		Ext:          json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := store.DB().WithContext(r.Context()).Create(&row).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "create document failed", err), http.StatusInternalServerError)
		return
	}

	common.ReplyJSON(w, docFromRow(mergedDocRow{
		DocID:         docID,
		DatasetID:     datasetID,
		DisplayName:   display,
		PID:           pid,
		Tags:          tagsBytes,
		FileID:        fileID,
		Creator:       userName,
		BaseCreatedAt: now,
		BaseUpdatedAt: now,
	}))
}
func GetDocument(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	docID := documentIDFromPath(r)
	if datasetID == "" || docID == "" {
		common.ReplyErr(w, "missing dataset or document", http.StatusBadRequest)
		return
	}
	if _, userID, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetRead); !ok {
		if userID == "" {
			common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		} else {
			replyDatasetForbidden(w)
		}
		return
	}

	rr, err := loadDocumentByID(r.Context(), datasetID, docID)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "document not found", err), http.StatusNotFound)
		return
	}
	doc := docFromRow(rr)
	setDocumentURI(&doc)
	common.ReplyJSON(w, doc)
}
func DeleteDocument(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	docID := documentIDFromPath(r)
	userID := store.UserID(r)
	if datasetID == "" || docID == "" {
		common.ReplyErr(w, "missing dataset or document", http.StatusBadRequest)
		return
	}
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	if _, _, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetWrite); !ok {
		replyDatasetForbidden(w)
		return
	}
	var row orm.Document
	if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", docID, datasetID).Take(&row).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "document not found", err), http.StatusNotFound)
		return
	}
	if err := deleteExternalDocs(r, datasetID, []orm.Document{row}); err != nil {
		common.ReplyErr(w, "external delete failed", http.StatusBadGateway)
		return
	}
	now := time.Now().UTC()
	if err := store.DB().WithContext(r.Context()).
		Model(&orm.Document{}).
		Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", docID, datasetID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error; err != nil {
		common.ReplyErr(w, "delete document failed", http.StatusInternalServerError)
		return
	}
	recalcAffectedFolderStats(r.Context(), datasetID, row.PID)
	w.WriteHeader(http.StatusOK)
}
func UpdateDocument(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	docID := documentIDFromPath(r)
	userID := store.UserID(r)
	userName := store.UserName(r)
	if datasetID == "" || docID == "" {
		common.ReplyErr(w, "missing dataset or document", http.StatusBadRequest)
		return
	}
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	if _, _, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetWrite); !ok {
		replyDatasetForbidden(w)
		return
	}
	var body Doc
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	updates := map[string]any{}
	if s := strings.TrimSpace(body.DisplayName); s != "" {
		updates["display_name"] = s
	}
	if s := strings.TrimSpace(body.PID); s != "" {
		updates["p_id"] = s
	}
	if body.Tags != nil {
		b, _ := json.Marshal(body.Tags)
		updates["tags"] = b
	}
	if s := strings.TrimSpace(body.FileID); s != "" {
		updates["file_id"] = s
	}
	now := time.Now().UTC()
	updates["updated_at"] = now

	db := store.DB().WithContext(r.Context())
	var cd orm.Document
	err := db.Where("id = ? AND dataset_id = ?", docID, datasetID).Take(&cd).Error
	if err != nil {
		row := orm.Document{
			ID:           docID,
			LazyllmDocID: "",
			DatasetID:    datasetID,
			DisplayName:  strings.TrimSpace(body.DisplayName),
			PID:          strings.TrimSpace(body.PID),
			FileID:       strings.TrimSpace(body.FileID),
			Tags:         func() []byte { b, _ := json.Marshal(body.Tags); return b }(),
			Ext:          json.RawMessage(`{}`),
			BaseModel: orm.BaseModel{
				CreateUserID:   userID,
				CreateUserName: userName,
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		}
		if err := db.Create(&row).Error; err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "update document failed", err), http.StatusInternalServerError)
			return
		}
		common.ReplyJSON(w, docFromRow(mergedDocRow{
			DocID:         docID,
			DatasetID:     datasetID,
			DisplayName:   row.DisplayName,
			PID:           row.PID,
			Tags:          row.Tags,
			FileID:        row.FileID,
			Creator:       userName,
			BaseCreatedAt: now,
			BaseUpdatedAt: now,
		}))
		return
	}
	if err := db.Model(&orm.Document{}).Where("id = ? AND deleted_at IS NULL", cd.ID).Updates(updates).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "update document failed", err), http.StatusInternalServerError)
		return
	}
	// return refreshed
	r2 := r.Clone(r.Context())
	mux.Vars(r2)["document"] = docID
	GetDocument(w, r2)
}
func SearchDocuments(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "missing dataset", http.StatusBadRequest)
		return
	}
	if _, userID, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetRead); !ok {
		if userID == "" {
			common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		} else {
			replyDatasetForbidden(w)
		}
		return
	}
	var req SearchDocumentsRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			common.ReplyErr(w, "invalid body", http.StatusBadRequest)
			return
		}
	}

	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 1000 {
		pageSize = 1000
	}
	offset := 0
	if strings.TrimSpace(req.PageToken) != "" {
		v, err := parseDatasetPageToken(req.PageToken)
		if err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid page_token", err), http.StatusBadRequest)
			return
		}
		offset = v
	}
	keyword := strings.TrimSpace(req.Keyword)
	pid := firstNonEmpty(strings.TrimSpace(req.PID), parseDocumentPIDFromParentName(strings.TrimSpace(req.Parent)))

	rows, total, err := loadDatasetDocuments(r.Context(), datasetID, keyword, pid, true, int(pageSize), offset)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "search documents failed", err), http.StatusInternalServerError)
		return
	}
	relPaths := buildDocumentTreeRelPaths(r.Context(), rows)
	out := make([]Doc, 0, len(rows))
	for _, rr := range rows {
		rr.RelPath = relPaths[rr.DocID]
		doc := docFromRow(rr)
		setDocumentURI(&doc)
		out = append(out, doc)
	}
	next := ""
	if offset+len(rows) < int(total) {
		next = encodeDatasetPageToken(offset+len(rows), int(pageSize), int(total))
	}
	common.ReplyJSON(w, ListDocumentsResponse{Documents: out, TotalSize: int32(total), NextPageToken: next})
}
func SearchAllDocuments(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	var req SearchDocumentsRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			common.ReplyErr(w, "invalid body", http.StatusBadRequest)
			return
		}
	}

	datasetIDs, err := accessibleDatasetIDs(r.Context(), userID)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "search documents failed", err), http.StatusInternalServerError)
		return
	}
	if len(datasetIDs) == 0 {
		common.ReplyJSON(w, ListDocumentsResponse{Documents: []Doc{}, TotalSize: 0, NextPageToken: ""})
		return
	}

	offset := 0
	if strings.TrimSpace(req.PageToken) != "" {
		v, err := parseDatasetPageToken(req.PageToken)
		if err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid page_token", err), http.StatusBadRequest)
			return
		}
		offset = v
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	rows, total, err := searchAllDocumentsMerged(r.Context(), searchAllDocumentsParams{
		DatasetIDs:  datasetIDs,
		Keyword:     strings.TrimSpace(req.Keyword),
		KeywordList: req.KeywordList,
		PageSize:    int(pageSize),
		Offset:      offset,
	})
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "search documents failed", err), http.StatusInternalServerError)
		return
	}
	relPaths := buildDocumentTreeRelPaths(r.Context(), rows)
	out := make([]Doc, 0, len(rows))
	for _, rr := range rows {
		rr.RelPath = relPaths[rr.DocID]
		doc := docFromRow(rr)
		setDocumentURI(&doc)
		out = append(out, doc)
	}
	next := ""
	if offset+len(rows) < int(total) {
		next = encodeDatasetPageToken(offset+len(rows), int(pageSize), int(total))
	}
	common.ReplyJSON(w, ListDocumentsResponse{Documents: out, TotalSize: int32(total), NextPageToken: next})
}

func ListDocumentsByDatasets(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	var req ListDatasetDocumentsRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			common.ReplyErr(w, "invalid body", http.StatusBadRequest)
			return
		}
	}

	datasetIDs := normalizeDocumentDatasetIDs(req.DatasetIDs)
	if len(datasetIDs) == 0 {
		common.ReplyErr(w, "dataset_ids required", http.StatusBadRequest)
		return
	}
	readableDatasetIDs, err := readableRequestedDatasetIDs(r.Context(), userID, datasetIDs)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "query datasets failed", err), http.StatusInternalServerError)
		return
	}
	if len(readableDatasetIDs) == 0 {
		common.ReplyJSON(w, ListDocumentsResponse{Documents: []Doc{}, TotalSize: 0, NextPageToken: ""})
		return
	}

	cursor, err := parseListDatasetDocumentsPageToken(req.PageToken)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid page_token", err), http.StatusBadRequest)
		return
	}
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	rows, total, hasMore, snapshotUpdatedAt, err := loadDocumentsByDatasetIDs(r.Context(), readableDatasetIDs, strings.TrimSpace(req.Keyword), cursor, pageSize)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "query documents failed", err), http.StatusInternalServerError)
		return
	}
	relPaths := buildDocumentTreeRelPaths(r.Context(), rows)
	out := make([]Doc, 0, len(rows))
	for _, rr := range rows {
		rr.RelPath = relPaths[rr.DocID]
		doc := docFromRow(rr)
		setDocumentURI(&doc)
		out = append(out, doc)
	}
	next := ""
	if hasMore && len(rows) > 0 {
		next = encodeListDatasetDocumentsPageToken(rows[len(rows)-1], snapshotUpdatedAt, listDatasetDocumentsSeenIDs(cursor, rows))
	}
	common.ReplyJSON(w, ListDocumentsResponse{Documents: out, TotalSize: int32(total), NextPageToken: next})
}

func BatchUpdateDocumentTags(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "missing dataset", http.StatusBadRequest)
		return
	}
	if _, userID, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetWrite); !ok {
		if userID == "" {
			common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		} else {
			replyDatasetForbidden(w)
		}
		return
	}

	var req BatchUpdateDocumentTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	parent := strings.TrimSpace(req.Parent)
	if parent == "" {
		common.ReplyErr(w, "parent required", http.StatusBadRequest)
		return
	}
	parentDatasetID := strings.TrimPrefix(parent, "datasets/")
	if parentDatasetID == "" || parentDatasetID != datasetID {
		common.ReplyErr(w, "parent does not match dataset", http.StatusBadRequest)
		return
	}

	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = "UPDATE_MODE_UNSPECIFIED"
	}
	if mode != "UPDATE_MODE_UNSPECIFIED" && mode != "APPEND" && mode != "OVERWRITE" {
		common.ReplyErr(w, "invalid mode", http.StatusBadRequest)
		return
	}

	targetIDs := map[string]struct{}{}
	for _, id := range req.DocumentIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			targetIDs[id] = struct{}{}
		}
	}
	for _, folderID := range req.FolderIDs {
		folderID = strings.TrimSpace(folderID)
		if folderID == "" {
			continue
		}
		subtree, err := loadDocumentSubtree(r.Context(), datasetID, folderID)
		if err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "folder not found", err), http.StatusBadRequest)
			return
		}
		for _, row := range subtree {
			if isFolderLikeDocument(row) {
				continue
			}
			targetIDs[strings.TrimSpace(row.ID)] = struct{}{}
		}
	}

	if len(targetIDs) == 0 {
		common.ReplyJSON(w, BatchUpdateDocumentTagsResponse{AffectedFiles: 0, TruncatedDocs: 0})
		return
	}

	ids := make([]string, 0, len(targetIDs))
	for id := range targetIDs {
		ids = append(ids, id)
	}

	var docs []orm.Document
	if err := store.DB().WithContext(r.Context()).
		Where("dataset_id = ? AND id IN ? AND deleted_at IS NULL", datasetID, ids).
		Find(&docs).Error; err != nil {
		common.ReplyErr(w, "query documents failed", http.StatusInternalServerError)
		return
	}

	requestTags := normalizeBatchDocumentTags(req.Tags)
	now := time.Now().UTC()
	affected := int32(0)
	truncated := int32(0)

	for _, docRow := range docs {
		if isFolderLikeDocument(docRow) {
			continue
		}
		affected++
		finalTags := append([]string(nil), requestTags...)
		if mode == "APPEND" || mode == "UPDATE_MODE_UNSPECIFIED" {
			var existing []string
			_ = json.Unmarshal(docRow.Tags, &existing)
			finalTags = mergeAppendDocumentTags(existing, requestTags, 10)
		} else if len(finalTags) > 10 {
			finalTags = finalTags[:10]
			truncated++
		}
		if len(finalTags) > 10 {
			finalTags = finalTags[:10]
			truncated++
		}
		tagsBytes, _ := json.Marshal(finalTags)
		if err := store.DB().WithContext(r.Context()).
			Model(&orm.Document{}).
			Where("dataset_id = ? AND id = ? AND deleted_at IS NULL", datasetID, docRow.ID).
			Updates(map[string]any{"tags": tagsBytes, "updated_at": now}).Error; err != nil {
			common.ReplyErr(w, "update document tags failed", http.StatusInternalServerError)
			return
		}
	}

	common.ReplyJSON(w, BatchUpdateDocumentTagsResponse{
		AffectedFiles: affected,
		TruncatedDocs: truncated,
	})
}

func normalizeBatchDocumentTags(tags []string) []string {
	if len(tags) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		t := strings.TrimSpace(tag)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

func mergeAppendDocumentTags(existing, incoming []string, limit int) []string {
	existing = normalizeBatchDocumentTags(existing)
	incoming = normalizeBatchDocumentTags(incoming)
	if limit <= 0 {
		return []string{}
	}
	if len(incoming) == 0 {
		if len(existing) > limit {
			return existing[:limit]
		}
		return existing
	}
	keptExisting := make([]string, 0, len(existing))
	incomingSet := make(map[string]struct{}, len(incoming))
	for _, tag := range incoming {
		incomingSet[tag] = struct{}{}
	}
	for _, tag := range existing {
		if _, ok := incomingSet[tag]; ok {
			continue
		}
		keptExisting = append(keptExisting, tag)
	}
	available := limit - len(incoming)
	if available < 0 {
		available = 0
	}
	if len(keptExisting) > available {
		keptExisting = keptExisting[len(keptExisting)-available:]
	}
	out := append(keptExisting, incoming...)
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func BatchDeleteDocument(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	userID := store.UserID(r)
	if datasetID == "" {
		common.ReplyErr(w, "missing dataset", http.StatusBadRequest)
		return
	}
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	if _, _, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetWrite); !ok {
		replyDatasetForbidden(w)
		return
	}
	var req BatchDeleteDocumentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if len(req.Names) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}
	var rows []orm.Document
	if err := store.DB().WithContext(r.Context()).Where("dataset_id = ? AND id IN ? AND deleted_at IS NULL", datasetID, req.Names).Find(&rows).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "query documents failed", err), http.StatusInternalServerError)
		return
	}
	if err := deleteExternalDocs(r, datasetID, rows); err != nil {
		common.ReplyErr(w, "external delete failed", http.StatusBadGateway)
		return
	}
	now := time.Now().UTC()
	if err := store.DB().WithContext(r.Context()).
		Model(&orm.Document{}).
		Where("dataset_id = ? AND id IN ? AND deleted_at IS NULL", datasetID, req.Names).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error; err != nil {
		common.ReplyErr(w, "batch delete document failed", http.StatusInternalServerError)
		return
	}
	pids := make([]string, 0, len(rows))
	for _, row := range rows {
		pids = append(pids, row.PID)
	}
	recalcAffectedFolderStats(r.Context(), datasetID, pids...)
	w.WriteHeader(http.StatusOK)
}
func AllDocumentCreators(w http.ResponseWriter, r *http.Request) {
	type resp struct {
		Creators []UserInfo `json:"creators"`
	}
	type creatorRow struct {
		ID   string
		Name string
	}
	var rows []creatorRow
	_ = store.DB().WithContext(r.Context()).
		Model(&orm.Document{}).
		Select("create_user_id AS id", "create_user_name AS name").
		Where("deleted_at IS NULL").
		Group("create_user_id, create_user_name").
		Order("create_user_name ASC").
		Find(&rows).Error
	out := make([]UserInfo, 0, len(rows))
	for _, row := range rows {
		id := strings.TrimSpace(row.ID)
		name := strings.TrimSpace(row.Name)
		if id == "" && name == "" {
			continue
		}
		out = append(out, UserInfo{ID: id, Name: name})
	}
	common.ReplyJSON(w, resp{Creators: out})
}
func AllDocumentTags(w http.ResponseWriter, r *http.Request) {
	type resp struct {
		Tags []string `json:"tags"`
	}
	var docs []orm.Document
	_ = store.DB().WithContext(r.Context()).
		Select("tags").
		Where("deleted_at IS NULL").
		Find(&docs).Error
	seen := map[string]struct{}{}
	var tags []string
	for _, d := range docs {
		var ts []string
		_ = json.Unmarshal(d.Tags, &ts)
		for _, t := range ts {
			tt := strings.TrimSpace(t)
			if tt == "" {
				continue
			}
			if _, ok := seen[tt]; ok {
				continue
			}
			seen[tt] = struct{}{}
			tags = append(tags, tt)
		}
	}
	sort.Strings(tags)
	if tags == nil {
		tags = []string{}
	}
	common.ReplyJSON(w, resp{Tags: tags})
}

// --- types (aligned to document-apis-and-tables.md; minimal subset for now) ---

type DocumentTableColumn struct {
	ID           int32  `json:"id"`
	DisplayName  string `json:"display_name"`
	Type         string `json:"type"`
	Desc         string `json:"desc"`
	Sample       string `json:"sample"`
	SourceColumn string `json:"source_column"`
	IndexType    string `json:"index_type"`
}

type Doc struct {
	Name                   string                `json:"name"`
	DocumentID             string                `json:"document_id"`
	DisplayName            string                `json:"display_name"`
	DocumentSize           int64                 `json:"document_size"`
	DatasetID              string                `json:"dataset_id"`
	DatasetDisplay         string                `json:"dataset_display"`
	PID                    string                `json:"p_id"`
	Creator                string                `json:"creator"`
	URI                    string                `json:"uri"`
	FileURL                string                `json:"file_url,omitempty"`
	DownloadFileURL        string                `json:"download_file_url,omitempty"`
	Columns                []DocumentTableColumn `json:"columns"`
	CreateTime             string                `json:"create_time"`
	UpdateTime             string                `json:"update_time"`
	Tags                   []string              `json:"tags"`
	FileID                 string                `json:"file_id"`
	DataSourceType         string                `json:"data_source_type"`
	FileSystemPath         string                `json:"file_system_path"`
	Type                   string                `json:"type"`
	ConvertFileURI         string                `json:"convert_file_uri"`
	RelPath                string                `json:"rel_path"`
	DocumentStage          string                `json:"document_stage"`
	PDFConvertResult       string                `json:"pdf_convert_result,omitempty"`
	ChildDocumentCount     int64                 `json:"child_document_count,omitempty"`
	ChildFolderCount       int64                 `json:"child_folder_count,omitempty"`
	RecursiveDocumentCount int64                 `json:"recursive_document_count,omitempty"`
	RecursiveFolderCount   int64                 `json:"recursive_folder_count,omitempty"`
	RecursiveFileSize      int64                 `json:"recursive_file_size,omitempty"`
	Children               []Doc                 `json:"children"`
}

type ListDocumentsResponse struct {
	Documents     []Doc  `json:"documents"`
	TotalSize     int32  `json:"total_size,omitempty"`
	NextPageToken string `json:"next_page_token,omitempty"`
}

type SearchDocumentsRequest struct {
	Parent      string   `json:"parent,omitempty"`
	PID         string   `json:"p_id,omitempty"`
	DirPath     string   `json:"dir_path,omitempty"`
	OrderBy     string   `json:"order_by,omitempty"`
	PageToken   string   `json:"page_token,omitempty"`
	PageSize    int32    `json:"page_size,omitempty"`
	Keyword     string   `json:"keyword,omitempty"`
	KeywordList []string `json:"keyword_list,omitempty"`
	Recursive   bool     `json:"recursive,omitempty"`
}

type ListDatasetDocumentsRequest struct {
	DatasetIDs []string `json:"dataset_ids"`
	PageToken  string   `json:"page_token,omitempty"`
	PageSize   int32    `json:"page_size,omitempty"`
	Keyword    string   `json:"keyword,omitempty"`
}

type BatchUpdateDocumentTagsRequest struct {
	Parent      string   `json:"parent"`
	DocumentIDs []string `json:"document_ids,omitempty"`
	FolderIDs   []string `json:"folder_ids,omitempty"`
	Mode        string   `json:"mode"`
	Tags        []string `json:"tags"`
}

type BatchUpdateDocumentTagsResponse struct {
	AffectedFiles int32 `json:"affected_files,omitempty"`
	TruncatedDocs int32 `json:"truncated_docs,omitempty"`
}

type BatchDeleteDocumentRequest struct {
	Parent string   `json:"parent"`
	Names  []string `json:"names"`
}

type externalDeleteDocsRequest struct {
	DocIDs         []string `json:"doc_ids"`
	KbID           string   `json:"kb_id,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
}

func deleteExternalDocs(r *http.Request, datasetID string, rows []orm.Document) error {
	docIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		lazyDocID := strings.TrimSpace(row.LazyllmDocID)
		if lazyDocID == "" {
			continue
		}
		docIDs = append(docIDs, lazyDocID)
	}
	if len(docIDs) == 0 {
		return nil
	}
	req := externalDeleteDocsRequest{
		DocIDs:         docIDs,
		KbID:           datasetKbIDByID(datasetID),
		IdempotencyKey: newDocID(),
	}
	url := common.JoinURL(parsingServiceEndpoint(), "/v1/docs/delete")
	log.Logger.Info().
		Str("handler", "DeleteDocument").
		Str("dataset_id", datasetID).
		Str("external_url", url).
		Int("doc_count", len(docIDs)).
		Any("request_body", req).
		Msg("calling external delete-docs request")
	var resp map[string]any
	if err := common.ApiPost(requestContext(r), url, req, nil, &resp, 15*time.Second); err != nil {
		log.Logger.Error().
			Err(err).
			Str("handler", "DeleteDocument").
			Str("dataset_id", datasetID).
			Str("external_url", url).
			Int("doc_count", len(docIDs)).
			Any("request_body", req).
			Msg("external delete-docs request failed")
		return err
	}
	log.Logger.Info().
		Str("handler", "DeleteDocument").
		Str("dataset_id", datasetID).
		Str("external_url", url).
		Int("doc_count", len(docIDs)).
		Any("request_body", req).
		Any("response_body", resp).
		Msg("external delete-docs request succeeded")
	return nil
}

type UserInfo struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

func newDocID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "doc_" + fmtHex(b[:])
}

func fmtHex(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0x0f]
	}
	return string(out)
}

type mergedDocRow struct {
	DocID            string
	Filename         string
	Path             string
	Ext              json.RawMessage
	DatasetID        string
	DatasetDisplay   string
	BaseCreatedAt    time.Time
	BaseUpdatedAt    time.Time
	DisplayName      string
	PID              string
	Tags             []byte
	FileID           string
	Creator          string
	DocumentSize     int64
	DataSourceType   string
	Type             string
	RelPath          string
	DocumentStage    string
	PDFConvertResult string
}

func latestTime(values ...time.Time) time.Time {
	var out time.Time
	for _, value := range values {
		if value.IsZero() {
			continue
		}
		if out.IsZero() || value.After(out) {
			out = value
		}
	}
	return out
}

func loadDatasetDocuments(ctx context.Context, datasetID, keyword, pid string, applyPIDFilter bool, limit, offset int) ([]mergedDocRow, int64, error) {
	if limit <= 0 {
		limit = 20
	}

	keyTrim := strings.TrimSpace(keyword)
	// Listing uses direct children only; keyword search must include nested documents under pid.
	mergedPIDFilter := applyPIDFilter
	var docs []orm.Document
	if applyPIDFilter && keyTrim != "" {
		mergedPIDFilter = false
		var err error
		if pid == "" {
			err = store.DB().WithContext(ctx).
				Where("dataset_id = ? AND deleted_at IS NULL", datasetID).
				Order("updated_at DESC").
				Find(&docs).Error
		} else {
			docs, err = loadDocumentSubtree(ctx, datasetID, pid)
		}
		if err != nil {
			return nil, 0, err
		}
	} else {
		db := store.DB().WithContext(ctx).
			Where("dataset_id = ? AND deleted_at IS NULL", datasetID)
		if applyPIDFilter {
			db = db.Where("COALESCE(p_id, '') = ?", pid)
		}
		if err := db.Order("updated_at DESC").Find(&docs).Error; err != nil {
			return nil, 0, err
		}
	}
	if len(docs) == 0 {
		return []mergedDocRow{}, 0, nil
	}

	docIDs := make([]string, 0, len(docs))
	for _, doc := range docs {
		docIDs = append(docIDs, doc.ID)
	}
	return loadMergedDocumentsByDocIDs(ctx, docIDs, datasetID, keyword, pid, mergedPIDFilter, limit, offset)
}

func mergedDocRowFromCoreOnlyWithDatasetDisplay(row orm.Document, datasetDisplay string) mergedDocRow {
	var dExt documentExt
	_ = json.Unmarshal(row.Ext, &dExt)
	documentSize := dExt.FileSize
	relPath := firstNonEmpty(strings.TrimSpace(dExt.RelativePath), relativePathFromFullPath(dExt.StoredPath))
	docType := documentTypeFromName(firstNonEmpty(strings.TrimSpace(row.DisplayName), dExt.OriginalFilename))
	return mergedDocRow{
		DocID:            row.ID,
		Filename:         row.DisplayName,
		Path:             dExt.StoredPath,
		Ext:              row.Ext,
		DatasetID:        row.DatasetID,
		DatasetDisplay:   datasetDisplay,
		BaseCreatedAt:    row.CreatedAt,
		BaseUpdatedAt:    row.UpdatedAt,
		DisplayName:      row.DisplayName,
		PID:              row.PID,
		Tags:             row.Tags,
		FileID:           row.FileID,
		Creator:          row.CreateUserName,
		DocumentSize:     documentSize,
		DataSourceType:   "LOCAL_FILE",
		Type:             docType,
		RelPath:          relPath,
		DocumentStage:    "",
		PDFConvertResult: strings.TrimSpace(row.PDFConvertResult),
	}
}

func loadDocumentByID(ctx context.Context, datasetID, docID string) (mergedDocRow, error) {
	var row orm.Document
	if err := store.DB().WithContext(ctx).Where("(id = ? OR lazyllm_doc_id = ?) AND dataset_id = ? AND deleted_at IS NULL", docID, docID, datasetID).Take(&row).Error; err != nil {
		return mergedDocRow{}, err
	}
	if strings.TrimSpace(row.LazyllmDocID) == "" {
		return mergedDocRowFromCoreOnlyWithDatasetDisplay(row, ""), nil
	}
	rows, _, err := loadMergedDocumentsByDocIDs(ctx, []string{row.LazyllmDocID}, datasetID, "", "", false, 1, 0)
	if err != nil {
		return mergedDocRow{}, err
	}
	for _, rr := range rows {
		if rr.DocID == row.ID {
			return rr, nil
		}
	}
	return mergedDocRowFromCoreOnlyWithDatasetDisplay(row, ""), nil
}

type searchAllDocumentsParams struct {
	DatasetIDs  []string
	Keyword     string
	KeywordList []string
	PageSize    int
	Offset      int
}

func searchAllDocumentsMerged(ctx context.Context, params searchAllDocumentsParams) ([]mergedDocRow, int64, error) {
	if len(params.DatasetIDs) == 0 {
		return []mergedDocRow{}, 0, nil
	}
	enableFuzzy := strings.TrimSpace(params.Keyword) != ""
	searchKeyword := strings.TrimSpace(params.Keyword)
	searchKeywordList := normalizeKeywordList(params.KeywordList)
	queryKeyword := searchKeyword
	queryKeywordList := searchKeywordList
	offset := params.Offset
	limit := params.PageSize
	if limit <= 0 {
		limit = 20
	}
	if enableFuzzy {
		queryKeyword = ""
		queryKeywordList = nil
		offset = 0
		limit = fuzzySearchMaxCandidates
	}

	rows, total, err := loadMergedDocumentsBySearch(ctx, params.DatasetIDs, queryKeyword, queryKeywordList, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	if enableFuzzy {
		scored := scoreMergedDocuments(searchKeyword, rows)
		return pageScoredMergedDocuments(scored, params.Offset, params.PageSize), int64(len(scored)), nil
	}
	return rows, total, nil
}

func normalizeKeywordList(keywordList []string) []string {
	if len(keywordList) == 0 {
		return nil
	}
	out := make([]string, 0, len(keywordList))
	seen := map[string]struct{}{}
	for _, keyword := range keywordList {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		if _, ok := seen[keyword]; ok {
			continue
		}
		seen[keyword] = struct{}{}
		out = append(out, keyword)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func accessibleDatasetIDs(ctx context.Context, userID string) ([]string, error) {
	var datasets []orm.Dataset
	if err := store.DB().WithContext(ctx).Where("deleted_at IS NULL").Find(&datasets).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(datasets))
	for _, ds := range datasets {
		if canAccessDataset(&ds, userID, acl.PermissionDatasetRead) {
			ids = append(ids, ds.ID)
		}
	}
	return ids, nil
}

func loadMergedDocumentsBySearch(ctx context.Context, datasetIDs []string, keyword string, keywordList []string, limit, offset int) ([]mergedDocRow, int64, error) {
	if len(datasetIDs) == 0 {
		return []mergedDocRow{}, 0, nil
	}

	var docs []orm.Document
	if err := store.DB().WithContext(ctx).
		Where("dataset_id IN ? AND deleted_at IS NULL", datasetIDs).
		Order("updated_at DESC").
		Find(&docs).Error; err != nil {
		return nil, 0, err
	}
	if len(docs) == 0 {
		return []mergedDocRow{}, 0, nil
	}

	datasetDisplayByID := make(map[string]string, len(datasetIDs))
	var datasets []orm.Dataset
	if err := store.DB().WithContext(ctx).
		Where("id IN ? AND deleted_at IS NULL", datasetIDs).
		Find(&datasets).Error; err != nil {
		return nil, 0, err
	}
	for _, ds := range datasets {
		datasetDisplayByID[ds.ID] = strings.TrimSpace(ds.DisplayName)
	}

	externalIDs := make([]string, 0, len(docs))
	coreIDs := make([]string, 0, len(docs))
	for _, doc := range docs {
		coreIDs = append(coreIDs, doc.ID)
		if extID := strings.TrimSpace(doc.LazyllmDocID); extID != "" {
			externalIDs = append(externalIDs, extID)
		}
	}

	baseByExternalID := make(map[string]readonlyorm.LazyLLMDocRow, len(externalIDs))
	if len(externalIDs) > 0 {
		var baseRows []readonlyorm.LazyLLMDocRow
		if err := store.LazyLLMDB().WithContext(ctx).
			Table((readonlyorm.LazyLLMDocRow{}).TableName()).
			Where("doc_id IN ?", externalIDs).
			Find(&baseRows).Error; err != nil {
			return nil, 0, err
		}
		for _, row := range baseRows {
			baseByExternalID[row.DocID] = row
		}
	}

	latestTaskStatusByExternalID := make(map[string]string, len(externalIDs))
	if len(externalIDs) > 0 {
		var extTasks []readonlyorm.LazyLLMDocServiceTaskRow
		if err := store.LazyLLMDB().WithContext(ctx).
			Table((readonlyorm.LazyLLMDocServiceTaskRow{}).TableName()).
			Where("doc_id IN ?", externalIDs).
			Order("updated_at DESC").
			Find(&extTasks).Error; err != nil {
			return nil, 0, err
		}
		for _, task := range extTasks {
			if _, ok := latestTaskStatusByExternalID[task.DocID]; !ok {
				latestTaskStatusByExternalID[task.DocID] = strings.TrimSpace(task.Status)
			}
		}
	}

	latestTaskDataSourceByExternalID := make(map[string]string, len(externalIDs))
	if len(coreIDs) > 0 {
		var coreTasks []orm.Task
		if err := store.DB().WithContext(ctx).
			Where("doc_id IN ? AND deleted_at IS NULL", coreIDs).
			Order("updated_at DESC").
			Find(&coreTasks).Error; err != nil {
			return nil, 0, err
		}
		docByID := make(map[string]orm.Document, len(docs))
		for _, doc := range docs {
			docByID[doc.ID] = doc
		}
		for _, task := range coreTasks {
			doc, ok := docByID[task.DocID]
			if !ok {
				continue
			}
			extID := strings.TrimSpace(doc.LazyllmDocID)
			if extID == "" {
				continue
			}
			if _, ok := latestTaskDataSourceByExternalID[extID]; ok {
				continue
			}
			var ext taskExt
			_ = json.Unmarshal(task.Ext, &ext)
			if s := strings.TrimSpace(ext.DataSourceType); s != "" {
				latestTaskDataSourceByExternalID[extID] = s
			}
		}
	}

	rows := make([]mergedDocRow, 0, len(docs))
	for _, doc := range docs {
		datasetDisplay := datasetDisplayByID[doc.DatasetID]
		extID := strings.TrimSpace(doc.LazyllmDocID)
		if extID == "" {
			rows = append(rows, mergedDocRowFromCoreOnlyWithDatasetDisplay(doc, datasetDisplay))
			continue
		}

		base, ok := baseByExternalID[extID]
		if !ok {
			rows = append(rows, mergedDocRowFromCoreOnlyWithDatasetDisplay(doc, datasetDisplay))
			continue
		}

		displayName := strings.TrimSpace(base.Filename)
		if strings.TrimSpace(doc.DisplayName) != "" {
			displayName = strings.TrimSpace(doc.DisplayName)
		}
		documentSize := int64(0)
		if base.SizeBytes != nil {
			documentSize = int64(*base.SizeBytes)
		}
		docType := documentTypeFromName(base.Filename)
		relPath := relativePathFromFullPath(base.Path)
		documentStage := strings.TrimSpace(base.UploadStatus)
		if taskStatus := strings.TrimSpace(latestTaskStatusByExternalID[extID]); taskStatus != "" {
			documentStage = taskStatus
		}
		dataSourceType := strings.TrimSpace(latestTaskDataSourceByExternalID[extID])
		if dataSourceType == "" && strings.TrimSpace(base.SourceType) != "" {
			dataSourceType = dataSourceTypeFromSourceType(base.SourceType)
		}
		if dataSourceType == "" {
			dataSourceType = "LOCAL_FILE"
		}
		var dExt documentExt
		_ = json.Unmarshal(doc.Ext, &dExt)
		if dExt.FileSize > 0 {
			documentSize = dExt.FileSize
		}
		if strings.TrimSpace(dExt.RelativePath) != "" {
			relPath = strings.TrimSpace(dExt.RelativePath)
		}
		if strings.TrimSpace(dExt.OriginalFilename) != "" {
			docType = documentTypeFromName(dExt.OriginalFilename)
		}
		rows = append(rows, mergedDocRow{
			DocID:            doc.ID,
			Filename:         base.Filename,
			Path:             firstNonEmpty(strings.TrimSpace(dExt.StoredPath), strings.TrimSpace(base.Path)),
			Ext:              doc.Ext,
			DatasetID:        doc.DatasetID,
			DatasetDisplay:   datasetDisplay,
			BaseCreatedAt:    base.CreatedAt,
			BaseUpdatedAt:    latestTime(base.UpdatedAt, doc.UpdatedAt),
			DisplayName:      displayName,
			PID:              doc.PID,
			Tags:             doc.Tags,
			FileID:           doc.FileID,
			Creator:          doc.CreateUserName,
			DocumentSize:     documentSize,
			DataSourceType:   dataSourceType,
			Type:             docType,
			RelPath:          relPath,
			DocumentStage:    documentStage,
			PDFConvertResult: strings.TrimSpace(doc.PDFConvertResult),
		})
	}

	if kw := strings.TrimSpace(keyword); kw != "" {
		filtered := rows[:0]
		for _, row := range rows {
			if mergedDocMatchesKeyword(row, kw) {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	if kws := normalizeKeywordList(keywordList); len(kws) > 0 {
		filtered := rows[:0]
		for _, row := range rows {
			for _, kw := range kws {
				if mergedDocMatchesKeyword(row, kw) {
					filtered = append(filtered, row)
					break
				}
			}
		}
		rows = filtered
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].BaseUpdatedAt.After(rows[j].BaseUpdatedAt) })
	total := int64(len(rows))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 20
	}
	if offset >= len(rows) {
		return []mergedDocRow{}, total, nil
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[offset:end], total, nil
}

type scoredMergedDoc struct {
	row   mergedDocRow
	score int
}

type listDatasetDocumentsCursor struct {
	UpdatedAt         time.Time
	DatasetID         string
	DocumentID        string
	SnapshotUpdatedAt time.Time
	SeenDocumentIDs   []string
}

type listDatasetDocumentsPageToken struct {
	V                 int      `json:"v"`
	UpdatedAt         string   `json:"updated_at"`
	DatasetID         string   `json:"dataset_id"`
	DocumentID        string   `json:"document_id"`
	SnapshotUpdatedAt string   `json:"snapshot_updated_at"`
	SeenDocumentIDs   []string `json:"seen_document_ids,omitempty"`
}

func scoreMergedDocuments(keyword string, rows []mergedDocRow) []scoredMergedDoc {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" || len(rows) == 0 {
		return nil
	}
	normalizedKw := normalizeForFuzzy(keyword)
	if normalizedKw == "" {
		return nil
	}
	scored := make([]scoredMergedDoc, 0, len(rows))
	for _, row := range rows {
		bestScore := -1
		if strings.Contains(normalizeForFuzzy(row.DisplayName), normalizedKw) {
			bestScore = 0
		}
		if bestScore < 0 && strings.Contains(normalizeForFuzzy(row.Creator), normalizedKw) {
			bestScore = 2
		}
		if bestScore < 0 {
			var tags []string
			_ = json.Unmarshal(row.Tags, &tags)
			for _, tag := range tags {
				if strings.Contains(normalizeForFuzzy(tag), normalizedKw) {
					bestScore = 1
					break
				}
			}
		}
		if bestScore >= 0 {
			scored = append(scored, scoredMergedDoc{row: row, score: bestScore})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score < scored[j].score
		}
		if !scored[i].row.BaseUpdatedAt.Equal(scored[j].row.BaseUpdatedAt) {
			return scored[i].row.BaseUpdatedAt.After(scored[j].row.BaseUpdatedAt)
		}
		return strings.ToLower(scored[i].row.DisplayName) < strings.ToLower(scored[j].row.DisplayName)
	})
	return scored
}

func pageScoredMergedDocuments(scored []scoredMergedDoc, offset, pageSize int) []mergedDocRow {
	if len(scored) == 0 {
		return []mergedDocRow{}
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(scored) {
		return []mergedDocRow{}
	}
	end := offset + pageSize
	if end > len(scored) {
		end = len(scored)
	}
	out := make([]mergedDocRow, 0, end-offset)
	for _, item := range scored[offset:end] {
		out = append(out, item.row)
	}
	return out
}

func loadDocumentsByDatasetIDs(ctx context.Context, datasetIDs []string, keyword string, cursor *listDatasetDocumentsCursor, limit int) ([]mergedDocRow, int64, bool, time.Time, error) {
	if len(datasetIDs) == 0 {
		return []mergedDocRow{}, 0, false, time.Time{}, nil
	}
	if limit <= 0 {
		limit = 10
	}
	maxInt := int(^uint(0) >> 1)
	rows, _, err := loadMergedDocumentsBySearch(ctx, datasetIDs, "", nil, maxInt, 0)
	if err != nil {
		return nil, 0, false, time.Time{}, err
	}
	if keyword = strings.TrimSpace(keyword); keyword != "" {
		filtered := rows[:0]
		for _, row := range rows {
			if mergedDocNameMatchesKeyword(row, keyword) {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	sortMergedDocumentsForDatasetList(rows)

	snapshotUpdatedAt := time.Time{}
	snapshotSet := false
	if cursor != nil {
		snapshotUpdatedAt = cursor.SnapshotUpdatedAt
		snapshotSet = true
	} else if len(rows) > 0 {
		snapshotUpdatedAt = rows[0].BaseUpdatedAt
		snapshotSet = true
	}
	if snapshotSet {
		filtered := rows[:0]
		for _, row := range rows {
			if !row.BaseUpdatedAt.After(snapshotUpdatedAt) {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}

	total := int64(len(rows))
	if cursor != nil {
		filtered := rows[:0]
		for _, row := range rows {
			if mergedDocRowAfterCursor(row, *cursor) {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	if cursor != nil && len(cursor.SeenDocumentIDs) > 0 {
		seen := make(map[string]struct{}, len(cursor.SeenDocumentIDs))
		for _, docID := range cursor.SeenDocumentIDs {
			if docID = strings.TrimSpace(docID); docID != "" {
				seen[docID] = struct{}{}
			}
		}
		filtered := rows[:0]
		for _, row := range rows {
			if _, ok := seen[row.DocID]; ok {
				continue
			}
			filtered = append(filtered, row)
		}
		rows = filtered
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	out := make([]mergedDocRow, 0, len(rows))
	out = append(out, rows...)
	return out, total, hasMore, snapshotUpdatedAt, nil
}

func sortMergedDocumentsForDatasetList(rows []mergedDocRow) {
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].BaseUpdatedAt.Equal(rows[j].BaseUpdatedAt) {
			return rows[i].BaseUpdatedAt.After(rows[j].BaseUpdatedAt)
		}
		if rows[i].DatasetID != rows[j].DatasetID {
			return rows[i].DatasetID < rows[j].DatasetID
		}
		return rows[i].DocID < rows[j].DocID
	})
}

func mergedDocRowAfterCursor(row mergedDocRow, cursor listDatasetDocumentsCursor) bool {
	if !row.BaseUpdatedAt.Equal(cursor.UpdatedAt) {
		return row.BaseUpdatedAt.Before(cursor.UpdatedAt)
	}
	if row.DatasetID != cursor.DatasetID {
		return row.DatasetID > cursor.DatasetID
	}
	return row.DocID > cursor.DocumentID
}

func mergedDocNameMatchesKeyword(row mergedDocRow, keyword string) bool {
	kw := strings.ToLower(strings.TrimSpace(keyword))
	if kw == "" {
		return true
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(row.DisplayName)), kw) {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(row.Filename)), kw)
}

func encodeListDatasetDocumentsPageToken(row mergedDocRow, snapshotUpdatedAt time.Time, seenDocumentIDs []string) string {
	payload := listDatasetDocumentsPageToken{
		V:                 1,
		UpdatedAt:         row.BaseUpdatedAt.UTC().Format(time.RFC3339Nano),
		DatasetID:         row.DatasetID,
		DocumentID:        row.DocID,
		SnapshotUpdatedAt: snapshotUpdatedAt.UTC().Format(time.RFC3339Nano),
		SeenDocumentIDs:   seenDocumentIDs,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return base64.RawStdEncoding.EncodeToString(b)
}

func parseListDatasetDocumentsPageToken(token string) (*listDatasetDocumentsCursor, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil
	}
	decoders := []*base64.Encoding{
		base64.RawStdEncoding,
		base64.StdEncoding,
		base64.RawURLEncoding,
		base64.URLEncoding,
	}
	var payload listDatasetDocumentsPageToken
	for _, decoder := range decoders {
		b, err := decoder.DecodeString(token)
		if err != nil {
			continue
		}
		if err := json.Unmarshal(b, &payload); err != nil {
			continue
		}
		break
	}
	if strings.TrimSpace(payload.UpdatedAt) == "" || strings.TrimSpace(payload.DatasetID) == "" || strings.TrimSpace(payload.DocumentID) == "" || strings.TrimSpace(payload.SnapshotUpdatedAt) == "" {
		return nil, fmt.Errorf("invalid cursor")
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, payload.UpdatedAt)
	if err != nil {
		return nil, err
	}
	snapshotUpdatedAt, err := time.Parse(time.RFC3339Nano, payload.SnapshotUpdatedAt)
	if err != nil {
		return nil, err
	}
	return &listDatasetDocumentsCursor{
		UpdatedAt:         updatedAt,
		DatasetID:         strings.TrimSpace(payload.DatasetID),
		DocumentID:        strings.TrimSpace(payload.DocumentID),
		SnapshotUpdatedAt: snapshotUpdatedAt,
		SeenDocumentIDs:   normalizeDocumentDatasetIDs(payload.SeenDocumentIDs),
	}, nil
}

func listDatasetDocumentsSeenIDs(cursor *listDatasetDocumentsCursor, rows []mergedDocRow) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(rows))
	if cursor != nil {
		for _, docID := range cursor.SeenDocumentIDs {
			docID = strings.TrimSpace(docID)
			if docID == "" {
				continue
			}
			if _, ok := seen[docID]; ok {
				continue
			}
			seen[docID] = struct{}{}
			out = append(out, docID)
		}
	}
	for _, row := range rows {
		docID := strings.TrimSpace(row.DocID)
		if docID == "" {
			continue
		}
		if _, ok := seen[docID]; ok {
			continue
		}
		seen[docID] = struct{}{}
		out = append(out, docID)
	}
	return out
}

func normalizeDocumentDatasetIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func readableRequestedDatasetIDs(ctx context.Context, userID string, datasetIDs []string) ([]string, error) {
	if len(datasetIDs) == 0 {
		return nil, nil
	}
	var datasets []orm.Dataset
	if err := store.DB().WithContext(ctx).Where("id IN ? AND deleted_at IS NULL", datasetIDs).Find(&datasets).Error; err != nil {
		return nil, err
	}
	byID := make(map[string]orm.Dataset, len(datasets))
	for _, ds := range datasets {
		byID[ds.ID] = ds
	}
	out := make([]string, 0, len(datasetIDs))
	for _, id := range datasetIDs {
		ds, ok := byID[id]
		if !ok {
			continue
		}
		if canAccessDataset(&ds, userID, acl.PermissionDatasetRead) {
			out = append(out, id)
		}
	}
	return out, nil
}

func boundedLevenshtein(a, b string, _ int) int {
	la, lb := len(a), len(b)
	if a == b {
		return 0
	}
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	if la > lb {
		a, b = b, a
		la, lb = lb, la
	}
	prev := make([]int, la+1)
	curr := make([]int, la+1)
	for i := 0; i <= la; i++ {
		prev[i] = i
	}
	for j := 1; j <= lb; j++ {
		curr[0] = j
		bj := b[j-1]
		for i := 1; i <= la; i++ {
			cost := 0
			if a[i-1] != bj {
				cost = 1
			}
			deletion := prev[i] + 1
			insertion := curr[i-1] + 1
			substitution := prev[i-1] + cost
			curr[i] = deletion
			if insertion < curr[i] {
				curr[i] = insertion
			}
			if substitution < curr[i] {
				curr[i] = substitution
			}
		}
		prev, curr = curr, prev
	}
	return prev[la]
}

func normalizeForFuzzy(s string) string {
	s = strings.ToLower(s)
	s = fuzzyPunctRe.ReplaceAllString(s, "")
	s = strings.TrimSuffix(s, ".docx")
	s = strings.TrimSuffix(s, ".doc")
	s = strings.TrimSuffix(s, ".pdf")
	return s
}

func loadMergedDocumentsByDocIDs(ctx context.Context, docIDs []string, datasetID, keyword, pid string, applyPIDFilter bool, limit, offset int) ([]mergedDocRow, int64, error) {
	if len(docIDs) == 0 {
		return []mergedDocRow{}, 0, nil
	}

	var docs []orm.Document
	docQuery := store.DB().WithContext(ctx).
		Where("deleted_at IS NULL").
		Where("(id IN ? OR lazyllm_doc_id IN ?)", docIDs, docIDs)
	if datasetID != "" {
		docQuery = docQuery.Where("dataset_id = ?", datasetID)
	}
	if applyPIDFilter {
		docQuery = docQuery.Where("COALESCE(p_id, '') = ?", pid)
	}
	if err := docQuery.Order("updated_at DESC").Find(&docs).Error; err != nil {
		return nil, 0, err
	}
	if len(docs) == 0 {
		return []mergedDocRow{}, 0, nil
	}

	datasetIDs := make([]string, 0, len(docs))
	datasetSeen := make(map[string]struct{}, len(docs))
	externalIDs := make([]string, 0, len(docs))
	for _, doc := range docs {
		if doc.DatasetID != "" {
			if _, ok := datasetSeen[doc.DatasetID]; !ok {
				datasetSeen[doc.DatasetID] = struct{}{}
				datasetIDs = append(datasetIDs, doc.DatasetID)
			}
		}
		if extID := strings.TrimSpace(doc.LazyllmDocID); extID != "" {
			externalIDs = append(externalIDs, extID)
		}
	}

	datasetDisplayByID := make(map[string]string, len(datasetIDs))
	if len(datasetIDs) > 0 {
		var datasets []orm.Dataset
		if err := store.DB().WithContext(ctx).
			Where("id IN ? AND deleted_at IS NULL", datasetIDs).
			Find(&datasets).Error; err != nil {
			return nil, 0, err
		}
		for _, ds := range datasets {
			datasetDisplayByID[ds.ID] = strings.TrimSpace(ds.DisplayName)
		}
	}

	baseByExternalID := make(map[string]readonlyorm.LazyLLMDocRow, len(externalIDs))
	if len(externalIDs) > 0 {
		var baseRows []readonlyorm.LazyLLMDocRow
		baseQuery := store.LazyLLMDB().WithContext(ctx).
			Table((readonlyorm.LazyLLMDocRow{}).TableName()).
			Where("doc_id IN ?", externalIDs)
		if keyword != "" && strings.TrimSpace(datasetID) == "" {
			like := "%" + strings.ToLower(strings.ReplaceAll(keyword, "%", "\\%")) + "%"
			baseQuery = baseQuery.Where("LOWER(filename) LIKE ? OR LOWER(path) LIKE ?", like, like)
		}
		if err := baseQuery.Find(&baseRows).Error; err != nil {
			return nil, 0, err
		}
		for _, row := range baseRows {
			baseByExternalID[row.DocID] = row
		}
	}

	latestTaskStatusByExternalID := make(map[string]string, len(externalIDs))
	if len(externalIDs) > 0 {
		var extTasks []readonlyorm.LazyLLMDocServiceTaskRow
		if err := store.LazyLLMDB().WithContext(ctx).
			Table((readonlyorm.LazyLLMDocServiceTaskRow{}).TableName()).
			Where("doc_id IN ?", externalIDs).
			Order("updated_at DESC").
			Find(&extTasks).Error; err != nil {
			return nil, 0, err
		}
		for _, task := range extTasks {
			if _, ok := latestTaskStatusByExternalID[task.DocID]; !ok {
				latestTaskStatusByExternalID[task.DocID] = strings.TrimSpace(task.Status)
			}
		}
	}

	coreIDs := make([]string, 0, len(docs))
	docByID := make(map[string]orm.Document, len(docs))
	for _, doc := range docs {
		coreIDs = append(coreIDs, doc.ID)
		docByID[doc.ID] = doc
	}
	latestTaskDataSourceByExternalID := make(map[string]string, len(externalIDs))
	if len(coreIDs) > 0 {
		var coreTasks []orm.Task
		if err := store.DB().WithContext(ctx).
			Where("doc_id IN ? AND deleted_at IS NULL", coreIDs).
			Order("updated_at DESC").
			Find(&coreTasks).Error; err != nil {
			return nil, 0, err
		}
		for _, task := range coreTasks {
			doc, ok := docByID[task.DocID]
			if !ok {
				continue
			}
			extID := strings.TrimSpace(doc.LazyllmDocID)
			if extID == "" {
				continue
			}
			if _, ok := latestTaskDataSourceByExternalID[extID]; ok {
				continue
			}
			var ext taskExt
			_ = json.Unmarshal(task.Ext, &ext)
			if s := strings.TrimSpace(ext.DataSourceType); s != "" {
				latestTaskDataSourceByExternalID[extID] = s
			}
		}
	}

	rows := make([]mergedDocRow, 0, len(docs))
	likeKeyword := strings.ToLower(strings.TrimSpace(keyword))
	for _, doc := range docs {
		datasetDisplay := datasetDisplayByID[doc.DatasetID]
		extID := strings.TrimSpace(doc.LazyllmDocID)
		if extID == "" {
			row := mergedDocRowFromCoreOnlyWithDatasetDisplay(doc, datasetDisplay)
			if likeKeyword != "" && !mergedDocMatchesKeyword(row, likeKeyword) {
				continue
			}
			rows = append(rows, row)
			continue
		}

		base, ok := baseByExternalID[extID]
		if !ok {
			row := mergedDocRowFromCoreOnlyWithDatasetDisplay(doc, datasetDisplay)
			if likeKeyword != "" && !mergedDocMatchesKeyword(row, likeKeyword) {
				continue
			}
			rows = append(rows, row)
			continue
		}

		displayName := strings.TrimSpace(base.Filename)
		if strings.TrimSpace(doc.DisplayName) != "" {
			displayName = strings.TrimSpace(doc.DisplayName)
		}
		documentSize := int64(0)
		if base.SizeBytes != nil {
			documentSize = int64(*base.SizeBytes)
		}
		docType := documentTypeFromName(base.Filename)
		relPath := relativePathFromFullPath(base.Path)
		documentStage := strings.TrimSpace(base.UploadStatus)
		if taskStatus, ok := latestTaskStatusByExternalID[extID]; ok && strings.TrimSpace(taskStatus) != "" {
			documentStage = strings.TrimSpace(taskStatus)
		}
		dataSourceType := strings.TrimSpace(latestTaskDataSourceByExternalID[extID])
		if dataSourceType == "" && strings.TrimSpace(base.SourceType) != "" {
			dataSourceType = dataSourceTypeFromSourceType(base.SourceType)
		}
		if dataSourceType == "" {
			dataSourceType = "LOCAL_FILE"
		}
		var dExt documentExt
		_ = json.Unmarshal(doc.Ext, &dExt)
		if dExt.FileSize > 0 {
			documentSize = dExt.FileSize
		}
		if strings.TrimSpace(dExt.RelativePath) != "" {
			relPath = strings.TrimSpace(dExt.RelativePath)
		}
		if strings.TrimSpace(dExt.OriginalFilename) != "" {
			docType = documentTypeFromName(dExt.OriginalFilename)
		}
		if strings.TrimSpace(dExt.StoredPath) != "" {
			base.Path = strings.TrimSpace(dExt.StoredPath)
		}
		row := mergedDocRow{
			DocID:            doc.ID,
			Filename:         base.Filename,
			Path:             base.Path,
			Ext:              doc.Ext,
			DatasetID:        doc.DatasetID,
			DatasetDisplay:   datasetDisplay,
			BaseCreatedAt:    base.CreatedAt,
			BaseUpdatedAt:    latestTime(base.UpdatedAt, doc.UpdatedAt),
			DisplayName:      displayName,
			PID:              doc.PID,
			Tags:             doc.Tags,
			FileID:           doc.FileID,
			Creator:          doc.CreateUserName,
			DocumentSize:     documentSize,
			DataSourceType:   dataSourceType,
			Type:             docType,
			RelPath:          relPath,
			DocumentStage:    documentStage,
			PDFConvertResult: strings.TrimSpace(doc.PDFConvertResult),
		}
		if row.BaseCreatedAt.IsZero() {
			row.BaseCreatedAt = doc.CreatedAt
		}
		if row.BaseUpdatedAt.IsZero() {
			row.BaseUpdatedAt = doc.UpdatedAt
		}
		if likeKeyword != "" && !mergedDocMatchesKeyword(row, likeKeyword) {
			continue
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].BaseUpdatedAt.After(rows[j].BaseUpdatedAt) })
	total := int64(len(rows))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 20
	}
	if offset >= len(rows) {
		return []mergedDocRow{}, total, nil
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[offset:end], total, nil
}

func mergedDocMatchesKeyword(row mergedDocRow, keyword string) bool {
	kw := strings.ToLower(strings.TrimSpace(keyword))
	if kw == "" {
		return true
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(row.DisplayName)), kw) {
		return true
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(row.Filename)), kw) {
		return true
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(row.Path)), kw) {
		return true
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(row.Creator)), kw) {
		return true
	}

	var tags []string
	_ = json.Unmarshal(row.Tags, &tags)
	for _, t := range tags {
		if strings.Contains(strings.ToLower(strings.TrimSpace(t)), kw) {
			return true
		}
	}
	return false
}

func docFromRow(row mergedDocRow) Doc {
	var tags []string
	_ = json.Unmarshal(row.Tags, &tags)
	if tags == nil {
		tags = []string{}
	}
	stats := folderStatsFromExt(row.Ext)
	pdfConvertResult := strings.TrimSpace(row.PDFConvertResult)
	displayName := strings.TrimSpace(row.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(row.Filename)
	}
	if displayName == "" {
		displayName = row.DocID
	}
	originalPath := originalStoredPathFromRow(row)
	previewPath := strings.TrimSpace(row.Path)
	if previewPath == "" {
		previewPath = originalPath
	}
	if extPath := parseStoredPathFromExt(row.Ext); extPath != "" {
		previewPath = extPath
	}
	ct := ""
	ut := ""
	if !row.BaseCreatedAt.IsZero() {
		ct = row.BaseCreatedAt.UTC().Format(time.RFC3339)
	}
	if !row.BaseUpdatedAt.IsZero() {
		ut = row.BaseUpdatedAt.UTC().Format(time.RFC3339)
	}
	documentSize := row.DocumentSize
	if strings.EqualFold(strings.TrimSpace(row.Type), "FOLDER") && stats.RecursiveFileSize > 0 {
		documentSize = stats.RecursiveFileSize
	}
	return Doc{
		Name:                   "datasets/" + row.DatasetID + "/documents/" + row.DocID,
		DocumentID:             row.DocID,
		DisplayName:            displayName,
		DocumentSize:           documentSize,
		DatasetID:              row.DatasetID,
		DatasetDisplay:         row.DatasetDisplay,
		PID:                    row.PID,
		Creator:                row.Creator,
		URI:                    "",
		FileURL:                staticFileURLFromFullPath(previewPath),
		DownloadFileURL:        staticFileURLFromFullPath(originalPath),
		Columns:                []DocumentTableColumn{},
		CreateTime:             ct,
		UpdateTime:             ut,
		Tags:                   tags,
		FileID:                 row.FileID,
		DataSourceType:         row.DataSourceType,
		FileSystemPath:         row.Path,
		Type:                   row.Type,
		ConvertFileURI:         "",
		RelPath:                row.RelPath,
		DocumentStage:          row.DocumentStage,
		PDFConvertResult:       pdfConvertResult,
		ChildDocumentCount:     stats.ChildDocumentCount,
		ChildFolderCount:       stats.ChildFolderCount,
		RecursiveDocumentCount: stats.RecursiveDocumentCount,
		RecursiveFolderCount:   stats.RecursiveFolderCount,
		RecursiveFileSize:      stats.RecursiveFileSize,
		Children:               []Doc{},
	}
}

func buildDocumentTreeRelPaths(ctx context.Context, rows []mergedDocRow) map[string]string {
	paths := make(map[string]string, len(rows))
	if len(rows) == 0 {
		return paths
	}
	byID := make(map[string]mergedDocRow, len(rows))
	for _, row := range rows {
		byID[row.DocID] = row
	}
	getDisplayName := func(row mergedDocRow) string {
		name := strings.TrimSpace(row.DisplayName)
		if name == "" {
			name = strings.TrimSpace(row.Filename)
		}
		if name == "" {
			name = row.DocID
		}
		return name
	}
	var build func(docID string) string
	build = func(docID string) string {
		if p, ok := paths[docID]; ok {
			return p
		}
		row, ok := byID[docID]
		if !ok {
			return ""
		}
		selfName := getDisplayName(row)
		pid := strings.TrimSpace(row.PID)
		if pid == "" {
			paths[docID] = selfName
			return selfName
		}
		parent, ok := byID[pid]
		if !ok {
			var parentRow orm.Document
			if err := store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", pid, row.DatasetID).Take(&parentRow).Error; err != nil {
				paths[docID] = selfName
				return selfName
			}
			parent = mergedDocRow{DocID: parentRow.ID, DatasetID: parentRow.DatasetID, PID: parentRow.PID, DisplayName: parentRow.DisplayName}
			byID[pid] = parent
		}
		parentPath := build(pid)
		if strings.TrimSpace(parentPath) == "" {
			paths[docID] = selfName
		} else {
			paths[docID] = parentPath + "/" + selfName
		}
		return paths[docID]
	}
	for _, row := range rows {
		_ = build(row.DocID)
	}
	return paths
}

type folderStats struct {
	ChildDocumentCount     int64
	ChildFolderCount       int64
	RecursiveDocumentCount int64
	RecursiveFolderCount   int64
	RecursiveFileSize      int64
}

func folderStatsFromExt(raw json.RawMessage) folderStats {
	if len(raw) == 0 {
		return folderStats{}
	}
	var extMap map[string]any
	if err := json.Unmarshal(raw, &extMap); err != nil {
		return folderStats{}
	}
	return folderStats{
		ChildDocumentCount:     int64FromAny(extMap["child_document_count"]),
		ChildFolderCount:       int64FromAny(extMap["child_folder_count"]),
		RecursiveDocumentCount: int64FromAny(extMap["recursive_document_count"]),
		RecursiveFolderCount:   int64FromAny(extMap["recursive_folder_count"]),
		RecursiveFileSize:      int64FromAny(extMap["recursive_file_size"]),
	}
}

func int64FromAny(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case int64:
		return x
	case float32:
		return int64(x)
	case float64:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n
	default:
		return 0
	}
}

func parseStoredPathFromExt(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var ext documentExt
	if err := json.Unmarshal(raw, &ext); err != nil {
		return ""
	}
	return strings.TrimSpace(ext.ParseStoredPath)
}

func originalStoredPathFromRow(row mergedDocRow) string {
	if len(row.Ext) > 0 {
		var ext documentExt
		if err := json.Unmarshal(row.Ext, &ext); err == nil {
			if v := strings.TrimSpace(ext.SourceStoredPath); v != "" {
				return v
			}
			if v := strings.TrimSpace(ext.StoredPath); v != "" {
				return v
			}
		}
	}
	return strings.TrimSpace(row.Path)
}

func relativePathFromFullPath(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	dir := filepath.Dir(p)
	if dir == "." || dir == "/" {
		return ""
	}
	marker := string(filepath.Separator) + "docs" + string(filepath.Separator)
	idx := strings.Index(dir, marker)
	if idx >= 0 {
		rel := strings.TrimPrefix(dir[idx+len(marker):], string(filepath.Separator))
		parts := strings.Split(rel, string(filepath.Separator))
		for i := 0; i < len(parts); i++ {
			if parts[i] == "files" {
				if i == 0 {
					return ""
				}
				return filepath.Join(parts[:i]...)
			}
		}
		return rel
	}
	return dir
}

func documentTypeFromName(name string) string {
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(name)))
	switch ext {
	case "":
		return "FOLDER"
	case ".txt":
		return "TXT"
	case ".pdf":
		return "PDF"
	case ".html", ".htm":
		return "HTML"
	case ".xlsx":
		return "XLSX"
	case ".xls":
		return "XLS"
	case ".docx", ".doc":
		return "DOCX"
	case ".csv":
		return "CSV"
	case ".pptx":
		return "PPTX"
	case ".ppt":
		return "PPT"
	case ".xml":
		return "XML"
	case ".md", ".markdown":
		return "MARKDOWN"
	case ".json", ".jsonl":
		return "JSON"
	default:
		return "DOCUMENT_TYPE_UNSPECIFIED"
	}
}

func dataSourceTypeFromSourceType(sourceType string) string {
	switch strings.ToUpper(strings.TrimSpace(sourceType)) {
	case "LOCAL_FILE", "FILE", "UPLOAD":
		return "LOCAL_FILE"
	case "FILE_SYSTEM", "FILESYSTEM":
		return "FILE_SYSTEM"
	default:
		return "DATA_SOURCE_TYPE_UNSPECIFIED"
	}
}
