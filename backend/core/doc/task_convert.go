package doc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"

	"lazymind/core/common"
	"lazymind/core/log"
)

// Office text PDF：text（text documents.ext）
const (
	ConvertStatusNone       = "NONE"
	ConvertStatusPending    = "PENDING"
	ConvertStatusProcessing = "PROCESSING"
	ConvertStatusSucceeded  = "SUCCEEDED"
	ConvertStatusFailed     = "FAILED"
)

const convertProviderHTTP = "http"

const officeConvertRetryCount = 2
const defaultOfficeConvertWorkers = 4

// env: LAZYMIND_OFFICE_CONVERT_URL — text URL，POST JSON {"source_path":"..."}，text {"pdf_path":"..."} text {"data":{"pdf_path":"..."}}
// Office text tasks:start text；Failedtext，text。

func newDocumentExt(storedPath, storedName, originalFilename string, fileSize int64, contentType, relativePath string, tags []string) documentExt {
	d := documentExt{StoredPath: storedPath, StoredName: storedName, OriginalFilename: originalFilename, FileSize: fileSize, ContentType: contentType, RelativePath: relativePath, Tags: append([]string(nil), tags...)}
	if isOfficeDocument(storedPath, contentType, originalFilename) {
		d.ConvertRequired = true
		d.ConvertStatus = ConvertStatusPending
		d.SourceStoredPath = strings.TrimSpace(storedPath)
		return d
	}
	d.ConvertRequired = false
	d.ConvertStatus = ConvertStatusNone
	return d
}

// parsePathForAdd text /v1/docs/add text（text）；Office Successtext PDF。
func parsePathForAdd(d documentExt) string {
	if v := strings.TrimSpace(d.ParseStoredPath); v != "" {
		return v
	}
	return strings.TrimSpace(d.StoredPath)
}

func previewPathForContent(d documentExt) string {
	if v := strings.TrimSpace(d.ParseStoredPath); v != "" {
		return v
	}
	return strings.TrimSpace(d.StoredPath)
}

func previewFilenameForContent(d documentExt) string {
	if v := strings.TrimSpace(d.ParseStoredName); v != "" {
		return v
	}
	if v := strings.TrimSpace(d.OriginalFilename); v != "" {
		return v
	}
	if v := strings.TrimSpace(d.StoredName); v != "" {
		return v
	}
	return ""
}

func previewContentTypeForContent(d documentExt) string {
	if v := strings.TrimSpace(d.ParseContentType); v != "" {
		return v
	}
	return strings.TrimSpace(d.ContentType)
}

// isOfficeDocument text Content-Type text Office Document
func isOfficeDocument(storedPath, contentType, originalFilename string) bool {
	name := originalFilename
	if name == "" {
		name = filepath.Base(storedPath)
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".pptm":
		return true
	}
	ct := strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.Contains(ct, "wordprocessingml"),
		strings.Contains(ct, "msword"),
		strings.Contains(ct, "spreadsheetml"),
		strings.Contains(ct, "excel"),
		strings.Contains(ct, "presentationml"),
		strings.Contains(ct, "powerpoint"):
		return true
	}
	return false
}

func isPresentationDocument(storedPath, contentType, originalFilename string) bool {
	name := originalFilename
	if name == "" {
		name = filepath.Base(storedPath)
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ppt", ".pptx", ".pptm":
		return true
	}
	ct := strings.ToLower(strings.TrimSpace(contentType))
	return strings.Contains(ct, "presentationml") || strings.Contains(ct, "powerpoint")
}

func ocrTypeFromConfig(ocrConfig map[string]any) string {
	if ocrConfig == nil {
		return ""
	}
	raw, _ := ocrConfig["ocr_type"].(string)
	return strings.ToLower(strings.TrimSpace(raw))
}

func ocrURLFromConfig(ocrConfig map[string]any) string {
	if ocrConfig == nil {
		return ""
	}
	raw, _ := ocrConfig["ocr_url"].(string)
	return strings.TrimSpace(raw)
}

// isOfficialMinerU mirrors lazyllm resolve_ocr_variant: empty URL or mineru.net host means online official API.
func isOfficialMinerU(ocrConfig map[string]any) bool {
	if ocrTypeFromConfig(ocrConfig) != "mineru" {
		return false
	}
	url := strings.ToLower(ocrURLFromConfig(ocrConfig))
	return url == "" || strings.Contains(url, "mineru.net")
}

// needsOfficeConvertBeforeParse decides whether office-convert-service runs before parsing.
// PPT/PPTX/PPTM skip conversion only when official MinerU (mineru.net) is configured so
// DynamicPDFReader can route them to MineruPPTReader; self-hosted MinerU still converts to PDF.
func needsOfficeConvertBeforeParse(d documentExt, ocrConfig map[string]any) bool {
	if !d.ConvertRequired {
		return false
	}
	src := strings.TrimSpace(d.StoredPath)
	if !isOfficeDocument(src, d.ContentType, d.OriginalFilename) {
		return false
	}
	if isPresentationDocument(src, d.ContentType, d.OriginalFilename) && isOfficialMinerU(ocrConfig) {
		return false
	}
	return true
}

// parsePathForIngestion returns the file path passed to the parsing service.
func parsePathForIngestion(d documentExt, ocrConfig map[string]any) string {
	if needsOfficeConvertBeforeParse(d, ocrConfig) {
		return parsePathForAdd(d)
	}
	if isOfficeDocument(d.StoredPath, d.ContentType, d.OriginalFilename) {
		if src := strings.TrimSpace(d.StoredPath); src != "" {
			return src
		}
	}
	return parsePathForAdd(d)
}

// expectedParseOutputPath text：stem.pdf
func expectedParseOutputPath(sourcePath string) string {
	dir := filepath.Dir(sourcePath)
	base := filepath.Base(sourcePath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	return filepath.Join(dir, stem+".pdf")
}

func expectedParseOutputPathByStoredName(sourcePath, storedName string) string {
	if v := parseStoredNameFromSource(storedName); strings.TrimSpace(v) != "" {
		return filepath.Join(filepath.Dir(sourcePath), v)
	}
	return expectedParseOutputPath(sourcePath)
}

func officeConvertTimeout() time.Duration {
	// Default 15 text，textDocumenttext
	return 15 * time.Minute
}

func officeConvertWorkers() int {
	raw := strings.TrimSpace(os.Getenv("LAZYMIND_OFFICE_CONVERT_WORKERS"))
	if raw == "" {
		return defaultOfficeConvertWorkers
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return defaultOfficeConvertWorkers
	}
	return v
}

// applyOfficeConversion text d text StoredPath/StoredName text；Failedtext ConvertStatus=FAILED，text error（UploadtextSuccess）。
func applyOfficeConversion(ctx context.Context, d *documentExt) {
	src := strings.TrimSpace(d.StoredPath)
	if src == "" {
		return
	}
	if !isOfficeDocument(src, d.ContentType, d.OriginalFilename) {
		d.ConvertRequired = false
		if strings.TrimSpace(d.ConvertStatus) == "" {
			d.ConvertStatus = ConvertStatusNone
		}
		return
	}

	d.ConvertRequired = true
	d.SourceStoredPath = src

	outPath := expectedParseOutputPathByStoredName(src, d.StoredName)
	d.ConvertStatus = ConvertStatusProcessing
	d.ConvertError = ""
	d.ConvertProvider = convertProviderHTTP

	// text：text PDF text
	if ok, sz := reuseExistingPDFIfFresh(src, outPath); ok {
		fillParseFields(d, outPath, sz)
		d.ConvertStatus = ConvertStatusSucceeded
		d.ConvertError = ""
		log.Logger.Info().Str("source", src).Str("pdf", outPath).Msg("office convert skipped, reused existing pdf")
		return
	}

	url := strings.TrimSpace(os.Getenv("LAZYMIND_OFFICE_CONVERT_URL"))
	if url == "" {
		d.ConvertStatus = ConvertStatusFailed
		d.ConvertError = "LAZYMIND_OFFICE_CONVERT_URL is not configured"
		log.Logger.Warn().Str("source", src).Msg("office convert: service URL missing")
		return
	}

	pdfPath, err := callOfficeConvertHTTP(ctx, url, src)
	if err != nil {
		d.ConvertStatus = ConvertStatusFailed
		d.ConvertError = err.Error()
		log.Logger.Error().Err(err).Str("source", src).Msg("office convert failed")
		return
	}
	pdfPath = strings.TrimSpace(pdfPath)
	if pdfPath == "" {
		pdfPath = outPath
	}
	st, err := os.Stat(pdfPath)
	if err != nil || st.IsDir() {
		d.ConvertStatus = ConvertStatusFailed
		d.ConvertError = fmt.Sprintf("converted pdf not found: %v", err)
		return
	}
	fillParseFields(d, pdfPath, st.Size())
	d.ConvertStatus = ConvertStatusSucceeded
	d.ConvertError = ""
	log.Logger.Info().Str("source", src).Str("pdf", pdfPath).Int64("size", st.Size()).Msg("office convert succeeded")
}

func fillParseFields(d *documentExt, pdfPath string, size int64) {
	d.ParseStoredPath = pdfPath
	d.ParseStoredName = filepath.Base(pdfPath)
	d.ParseContentType = "application/pdf"
	d.ParseFileSize = size
}

func reuseExistingPDFIfFresh(sourcePath, pdfPath string) (bool, int64) {
	srcSt, err := os.Stat(sourcePath)
	if err != nil || srcSt.IsDir() {
		return false, 0
	}
	pdfSt, err := os.Stat(pdfPath)
	if err != nil || pdfSt.IsDir() || pdfSt.Size() == 0 {
		return false, 0
	}
	// text PDF text
	if pdfSt.ModTime().Before(srcSt.ModTime()) {
		return false, 0
	}
	return true, pdfSt.Size()
}

func callOfficeConvertWithRetry(ctx context.Context, d *documentExt) {
	if d == nil {
		return
	}
	src := strings.TrimSpace(d.StoredPath)
	if src == "" || !isOfficeDocument(src, d.ContentType, d.OriginalFilename) {
		d.ConvertRequired = false
		if strings.TrimSpace(d.ConvertStatus) == "" {
			d.ConvertStatus = ConvertStatusNone
		}
		return
	}
	for attempt := 0; attempt < officeConvertRetryCount; attempt++ {
		applyOfficeConversion(ctx, d)
		if strings.TrimSpace(d.ConvertStatus) == ConvertStatusSucceeded {
			return
		}
	}
}

func persistDocumentConvertState(ctx context.Context, datasetID, documentID string, d documentExt) {
	updates := map[string]any{
		"ext":                mustJSON(d),
		"pdf_convert_result": strings.TrimSpace(d.ConvertStatus),
		"updated_at":         time.Now().UTC(),
	}
	if err := store.DB().WithContext(ctx).Model(&orm.Document{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", documentID, datasetID).Updates(updates).Error; err != nil {
		log.Logger.Error().Err(err).Str("dataset_id", datasetID).Str("document_id", documentID).Msg("persist document convert state failed")
	}
}

func cloneDocumentExt(d documentExt) documentExt {
	cloned := d
	if len(d.Tags) > 0 {
		cloned.Tags = append([]string(nil), d.Tags...)
	}
	return cloned
}

func buildAddFileItem(datasetID string, taskRow orm.Task, docRow orm.Document, dExt documentExt, parsePath string) addFileItem {
	externalPath := strings.TrimSpace(parsePath)
	return addFileItem{FilePath: externalPath, DocID: firstNonEmpty(strings.TrimSpace(docRow.LazyllmDocID), docRow.ID), Metadata: map[string]any{
		"dataset_id":                 datasetID,
		"document_pid":               docRow.PID,
		"display_name":               docRow.DisplayName,
		"core_task_id":               taskRow.ID,
		"core_document_id":           docRow.ID,
		"core_stored_path":           dExt.StoredPath,
		"core_parse_stored_path":     parsePath,
		"core_original_content_type": dExt.ContentType,
		"core_parse_content_type":    firstNonEmpty(dExt.ParseContentType, dExt.ContentType, "application/octet-stream"),
		"core_convert_required":      dExt.ConvertRequired,
		"core_convert_status":        dExt.ConvertStatus,
		"external_file_path":         externalPath,
	}}
}

func marshalConvertMeta(d documentExt) string {
	b, _ := json.Marshal(map[string]any{
		"convert_required":  d.ConvertRequired,
		"convert_status":    d.ConvertStatus,
		"convert_error":     d.ConvertError,
		"parse_stored_path": d.ParseStoredPath,
	})
	return string(b)
}

func callOfficeConvertHTTP(ctx context.Context, serviceURL, sourcePath string) (string, error) {
	body := map[string]string{"source_path": sourcePath}
	var raw map[string]any
	if err := common.ApiPost(ctx, serviceURL, body, nil, &raw, officeConvertTimeout()); err != nil {
		return "", err
	}
	// text data
	if p := extractPDFPath(raw); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("convert response missing pdf_path")
}

func extractPDFPath(m map[string]any) string {
	if m == nil {
		return ""
	}
	for _, k := range []string{"pdf_path", "pdfPath", "output_path", "outputPath", "path"} {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	if data, ok := m["data"].(map[string]any); ok {
		if p := extractPDFPath(data); p != "" {
			return p
		}
	}
	if data, ok := m["result"].(map[string]any); ok {
		if p := extractPDFPath(data); p != "" {
			return p
		}
	}
	return ""
}
