package evalset

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

const (
	importPreviewStatusReady    = "ready"
	importPreviewStatusConsumed = "consumed"
	importPreviewStatusExpired  = "expired"
)

func (s *Service) PreviewImport(ctx context.Context, fileName, fileType string, data []byte, userID, userName string) (*ImportPreviewResponse, error) {
	fileType = normalizeImportFileType(fileType, fileName)
	if fileType != importFileTypeCSV && fileType != importFileTypeJSON {
		return nil, errors.New("unsupported file_type")
	}

	parsed, err := parseImportRows(fileType, data)
	if err != nil {
		return nil, err
	}

	token := newImportToken()
	tempPath := tempPathForImportToken(token)
	if err := os.MkdirAll(filepath.Dir(tempPath), 0700); err != nil {
		return nil, err
	}

	normalizedJSON, err := json.Marshal(parsed.rows)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(tempPath, normalizedJSON, 0600); err != nil {
		_ = os.Remove(tempPath)
		return nil, err
	}

	invalidRowsDownloadURL := ""
	if len(parsed.invalidRows) > 0 {
		invalidRowsDownloadURL, err = writeInvalidRowsCSV(token, parsed)
		if err != nil {
			_ = os.Remove(tempPath)
			_ = os.Remove(invalidRowsCSVPathForImportToken(token))
			return nil, err
		}
	}

	previewRows := previewImportRows(parsed.rows)
	previewRowsJSON, err := json.Marshal(previewRows)
	if err != nil {
		_ = os.Remove(tempPath)
		_ = os.Remove(invalidRowsCSVPathForImportToken(token))
		return nil, err
	}
	errorDetailsJSON, err := json.Marshal(parsed.errorDetails)
	if err != nil {
		_ = os.Remove(tempPath)
		_ = os.Remove(invalidRowsCSVPathForImportToken(token))
		return nil, err
	}

	now := time.Now().UTC()
	expiresAt := now.Add(importPreviewTTL())
	row := orm.EvalSetImportPreview{
		Token:            token,
		Status:           importPreviewStatusReady,
		FileName:         strings.TrimSpace(fileName),
		FileType:         fileType,
		TempPath:         tempPath,
		TotalRows:        parsed.totalRows,
		EmptyRows:        parsed.emptyRows,
		ValidRows:        int64(len(parsed.rows)),
		PreviewRowsJSON:  previewRowsJSON,
		ErrorDetailsJSON: errorDetailsJSON,
		CreateUserID:     userID,
		CreateUserName:   userName,
		CreatedAt:        now,
		ExpiresAt:        expiresAt,
	}
	if err := s.repo.CreateImportPreview(ctx, row); err != nil {
		_ = os.Remove(tempPath)
		_ = os.Remove(invalidRowsCSVPathForImportToken(token))
		return nil, err
	}

	return &ImportPreviewResponse{
		ImportToken:            token,
		FileName:               row.FileName,
		FileType:               fileType,
		TotalRows:              parsed.totalRows,
		EmptyRows:              parsed.emptyRows,
		ValidRows:              int64(len(parsed.rows)),
		InvalidRows:            int64(len(parsed.invalidRows)),
		PreviewRows:            previewRows,
		InvalidPreviewRows:     previewInvalidRows(parsed.invalidRows),
		ErrorDetails:           parsed.errorDetails,
		ErrorsTruncated:        parsed.errorsTruncated,
		InvalidRowsDownloadURL: invalidRowsDownloadURL,
		ExpiresAt:              expiresAt,
	}, nil
}

func (r *Repository) CreateImportPreview(ctx context.Context, row orm.EvalSetImportPreview) error {
	return r.db.WithContext(ctx).Create(&row).Error
}

func CleanupExpiredImportPreviews(ctx context.Context, db *gorm.DB, now time.Time) error {
	var previews []orm.EvalSetImportPreview
	if err := db.WithContext(ctx).
		Where("status = ? AND expires_at < ?", importPreviewStatusReady, now).
		Find(&previews).Error; err != nil {
		return err
	}

	for _, preview := range previews {
		if strings.TrimSpace(preview.TempPath) != "" {
			if err := os.Remove(preview.TempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := os.Remove(invalidRowsCSVPathForImportToken(preview.Token)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := db.WithContext(ctx).Model(&orm.EvalSetImportPreview{}).
			Where("token = ? AND status = ?", preview.Token, importPreviewStatusReady).
			Update("status", importPreviewStatusExpired).Error; err != nil {
			return err
		}
	}
	return nil
}

func previewImportRows(rows []ImportNormalizedRow) []ImportNormalizedRow {
	if len(rows) <= importPreviewRowCount {
		return rows
	}
	return rows[:importPreviewRowCount]
}

func previewInvalidRows(rows []importInvalidRow) []ImportInvalidPreviewRow {
	if len(rows) > importPreviewRowCount {
		rows = rows[:importPreviewRowCount]
	}
	out := make([]ImportInvalidPreviewRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, ImportInvalidPreviewRow{
			Row:    row.rowNumber,
			Values: cloneStringMap(row.values),
			Errors: append([]ImportValidationErrorDetail(nil), row.errors...),
		})
	}
	return out
}

func writeInvalidRowsCSV(token string, parsed *importParseResult) (string, error) {
	path := invalidRowsCSVPathForImportToken(token)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}

	header := parsed.invalidCSVHeader
	if len(header) == 0 {
		header = importTemplateFields
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	if err := writer.Write(header); err != nil {
		return "", err
	}
	for _, row := range parsed.invalidRows {
		fields := row.fields
		if len(fields) == 0 {
			fields = valuesByHeader(header, row.values)
		}
		if err := writer.Write(fields); err != nil {
			return "", err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return "", err
	}
	fileURL := doc.StaticFileURLFromFullPath(path)
	if fileURL == "" {
		return "", fmt.Errorf("build invalid rows csv url failed")
	}
	return signedStaticDownloadURL(fileURL), nil
}

func invalidRowsCSVPathForImportToken(token string) string {
	return filepath.Join(doc.UploadRoot(), "eval-set-import", "invalid-rows", token+".csv")
}

func signedStaticDownloadURL(fileURL string) string {
	if strings.TrimSpace(fileURL) == "" {
		return ""
	}
	if strings.Contains(fileURL, "?") {
		return fileURL + "&download=1"
	}
	return fileURL + "?download=1"
}

func newImportToken() string {
	return "import_tmp_" + common.GenerateID()
}

func tempPathForImportToken(token string) string {
	return filepath.Join(importTempDir(), token+".json")
}

func normalizeImportFileType(fileType, fileName string) string {
	fileType = strings.ToLower(strings.TrimSpace(fileType))
	if fileType != "" {
		return strings.TrimPrefix(fileType, ".")
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fileName), "."))
	return strings.TrimSpace(ext)
}
