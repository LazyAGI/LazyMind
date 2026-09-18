package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/subagent"
)

const maxWriterDownloadConversionSize int64 = 20 * 1024 * 1024
const maxWriterDownloadConversionRequestSize int64 = 64 * 1024 * 1024

var writerDownloadSourceHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type writerDownloadFormatSpec struct {
	extension string
	mimeType  string
}

type writerDownloadConvertRequest struct {
	SourceFormat string `json:"source_format"`
	TargetFormat string `json:"target_format"`
	Content      string `json:"content"`
	DocumentID   string `json:"document_id,omitempty"`
}

func writerDownloadSpec(targetFormat string) (writerDownloadFormatSpec, bool) {
	switch strings.ToLower(strings.TrimSpace(targetFormat)) {
	case "markdown":
		return writerDownloadFormatSpec{extension: ".md", mimeType: "text/markdown; charset=utf-8"}, true
	case "lmd":
		return writerDownloadFormatSpec{extension: ".lmd", mimeType: "application/json; charset=utf-8"}, true
	default:
		return writerDownloadFormatSpec{}, false
	}
}

func writerDownloadSourceFormat(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "markdown", "lmd", "writer_document":
		return strings.ToLower(strings.TrimSpace(value)), true
	default:
		return "", false
	}
}

func safeWriterDownloadUserPart(userID string) string {
	if userID != "" && userID != "." && userID != ".." && filepath.Base(userID) == userID &&
		!strings.ContainsAny(userID, `/\\`) {
		return userID
	}
	hash := sha256.Sum256([]byte(userID))
	return "user_" + hex.EncodeToString(hash[:8])
}

func writerDownloadStoragePath(userID, sourceHash, targetFormat string) string {
	spec, _ := writerDownloadSpec(targetFormat)
	return filepath.Join(
		subagent.WorkspaceRoot(),
		safeWriterDownloadUserPart(userID),
		"_writer_download_conversions",
		sourceHash,
		"converted"+spec.extension,
	)
}

func validateWriterDownloadRequest(r *http.Request) (string, string, string, writerDownloadFormatSpec, bool) {
	userID := store.UserID(r)
	if userID == "" {
		return "", "", "", writerDownloadFormatSpec{}, false
	}
	sourceHash := strings.ToLower(strings.TrimSpace(common.PathVar(r, "source_hash")))
	targetFormat := strings.ToLower(strings.TrimSpace(common.PathVar(r, "target_format")))
	spec, formatOK := writerDownloadSpec(targetFormat)
	if !writerDownloadSourceHashPattern.MatchString(sourceHash) || !formatOK {
		return userID, sourceHash, targetFormat, writerDownloadFormatSpec{}, false
	}
	return userID, sourceHash, targetFormat, spec, true
}

func writerDownloadFilename(raw string, spec writerDownloadFormatSpec) (string, bool) {
	name := strings.TrimSpace(raw)
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, "/\\\x00\r\n") {
		return "", false
	}
	lower := strings.ToLower(name)
	if spec.extension == ".md" {
		if !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".markdown") {
			return "", false
		}
	} else if !strings.HasSuffix(lower, spec.extension) {
		return "", false
	}
	return name, true
}

func writeWriterDownloadFile(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".converted-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// ConvertWriterDownload delegates canonical Writer conversion to LazyLLM.
func ConvertWriterDownload(w http.ResponseWriter, r *http.Request) {
	if store.UserID(r) == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusUnauthorized)
		return
	}
	var request writerDownloadConvertRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWriterDownloadConversionRequestSize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		common.ReplyErr(w, "invalid writer download conversion request", http.StatusBadRequest)
		return
	}
	sourceFormat, ok := writerDownloadSourceFormat(request.SourceFormat)
	if !ok {
		common.ReplyErr(w, "invalid writer download source format", http.StatusBadRequest)
		return
	}
	targetFormat := strings.ToLower(strings.TrimSpace(request.TargetFormat))
	spec, ok := writerDownloadSpec(targetFormat)
	if !ok {
		common.ReplyErr(w, "invalid writer download target format", http.StatusBadRequest)
		return
	}
	if int64(len([]byte(request.Content))) > maxWriterDownloadConversionSize {
		common.ReplyErr(w, "writer download conversion is too large", http.StatusRequestEntityTooLarge)
		return
	}
	request.SourceFormat = sourceFormat
	request.TargetFormat = targetFormat
	request.DocumentID = strings.TrimSpace(request.DocumentID)
	if len(request.DocumentID) > 255 {
		common.ReplyErr(w, "invalid writer download conversion request", http.StatusBadRequest)
		return
	}
	body, err := json.Marshal(request)
	if err != nil {
		common.ReplyErr(w, "encode writer download conversion request failed", http.StatusInternalServerError)
		return
	}
	converted, status, err := common.HTTPPost(
		r.Context(),
		common.JoinURL(common.ChatServiceEndpoint(), "/api/writer/documents:convert"),
		"application/json",
		body,
	)
	if err != nil {
		common.ReplyErr(w, "writer download conversion service unavailable", http.StatusBadGateway)
		return
	}
	if status != http.StatusOK {
		responseStatus := http.StatusBadGateway
		if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
			responseStatus = http.StatusUnprocessableEntity
		}
		common.ReplyErr(w, "writer download conversion failed", responseStatus)
		return
	}
	if int64(len(converted)) > maxWriterDownloadConversionSize {
		common.ReplyErr(w, "writer download conversion is too large", http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", spec.mimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(converted)
}

// GetWriterDownloadConversion returns an already converted Writer download.
func GetWriterDownloadConversion(w http.ResponseWriter, r *http.Request) {
	userID, sourceHash, targetFormat, spec, ok := validateWriterDownloadRequest(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusUnauthorized)
		return
	}
	if !ok {
		common.ReplyErr(w, "invalid writer download conversion key", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	var row orm.WriterDownloadConversion
	err := db.WithContext(r.Context()).Where(
		"user_id = ? AND source_hash = ? AND target_format = ?", userID, sourceHash, targetFormat,
	).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "writer download conversion not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query writer download conversion failed", http.StatusInternalServerError)
		return
	}
	expectedPath := writerDownloadStoragePath(userID, sourceHash, targetFormat)
	if filepath.Clean(row.StoragePath) != filepath.Clean(expectedPath) {
		common.ReplyErr(w, "writer download conversion path is invalid", http.StatusInternalServerError)
		return
	}
	file, err := os.Open(expectedPath)
	if err != nil {
		if os.IsNotExist(err) {
			_ = db.WithContext(r.Context()).Delete(&row).Error
			common.ReplyErr(w, "writer download conversion not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "open writer download conversion failed", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		common.ReplyErr(w, "read writer download conversion failed", http.StatusInternalServerError)
		return
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": row.Filename})
	w.Header().Set("Content-Type", spec.mimeType)
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	if row.ContentHash != "" {
		w.Header().Set("ETag", fmt.Sprintf(`"%s"`, row.ContentHash))
	}
	http.ServeContent(w, r, row.Filename, stat.ModTime(), file)
}

// PutWriterDownloadConversion persists a newly converted Writer download.
func PutWriterDownloadConversion(w http.ResponseWriter, r *http.Request) {
	userID, sourceHash, targetFormat, spec, ok := validateWriterDownloadRequest(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusUnauthorized)
		return
	}
	if !ok {
		common.ReplyErr(w, "invalid writer download conversion key", http.StatusBadRequest)
		return
	}
	filename, ok := writerDownloadFilename(r.URL.Query().Get("filename"), spec)
	if !ok {
		common.ReplyErr(w, "invalid writer download filename", http.StatusBadRequest)
		return
	}
	content, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWriterDownloadConversionSize+1))
	if err != nil || int64(len(content)) > maxWriterDownloadConversionSize {
		common.ReplyErr(w, "writer download conversion is too large", http.StatusRequestEntityTooLarge)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	storagePath := writerDownloadStoragePath(userID, sourceHash, targetFormat)
	if err := writeWriterDownloadFile(storagePath, content); err != nil {
		common.ReplyErr(w, "save writer download conversion failed", http.StatusInternalServerError)
		return
	}
	contentHashBytes := sha256.Sum256(content)
	now := time.Now().UTC()
	row := orm.WriterDownloadConversion{
		ID:           uuid.NewString(),
		UserID:       userID,
		SourceHash:   sourceHash,
		TargetFormat: targetFormat,
		Filename:     filename,
		MIMEType:     spec.mimeType,
		StoragePath:  storagePath,
		Size:         int64(len(content)),
		ContentHash:  hex.EncodeToString(contentHashBytes[:]),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.WithContext(r.Context()).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "source_hash"}, {Name: "target_format"}},
		DoUpdates: clause.Assignments(map[string]any{
			"filename": filename, "mime_type": spec.mimeType, "storage_path": storagePath,
			"size": row.Size, "content_hash": row.ContentHash, "updated_at": now,
		}),
	}).Create(&row).Error; err != nil {
		common.ReplyErr(w, "index writer download conversion failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"source_hash": sourceHash, "target_format": targetFormat,
		"filename": filename, "size": row.Size, "content_hash": row.ContentHash,
	})
}
