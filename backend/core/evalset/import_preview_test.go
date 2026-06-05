package evalset

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

func TestDownloadCSVImportTemplateFieldOrder(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/core/eval-set-import-templates/csv", nil)
	req = mux.SetURLVars(req, map[string]string{"file_type": "csv"})

	DownloadImportTemplate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Fatalf("expected csv content type, got %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="eval_set_template.csv"` {
		t.Fatalf("unexpected content disposition: %q", got)
	}
	rows, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("read csv template: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected only header row, got %d rows", len(rows))
	}
	if got := strings.Join(rows[0], ","); got != strings.Join(importTemplateFields, ",") {
		t.Fatalf("unexpected header order:\nwant %v\ngot  %v", importTemplateFields, rows[0])
	}
}

func TestDownloadJSONImportTemplate(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/core/eval-set-import-templates/json", nil)
	req = mux.SetURLVars(req, map[string]string{"file_type": "json"})

	DownloadImportTemplate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected json content type, got %q", got)
	}
	var rows []ImportNormalizedRow
	if err := json.NewDecoder(rec.Body).Decode(&rows); err != nil {
		t.Fatalf("decode json template: %v", err)
	}
	if len(rows) != 1 || rows[0] != (ImportNormalizedRow{}) {
		t.Fatalf("unexpected json template: %#v", rows)
	}
}

func TestDownloadXLSXImportTemplateRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/core/eval-set-import-templates/xlsx", nil)
	req = mux.SetURLVars(req, map[string]string{"file_type": "xlsx"})

	DownloadImportTemplate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCSVImportPreviewMissingRequiredHeader(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)

	rec, req := multipartImportRequest(t, "missing.csv", "", "question,ground_truth\nq,a\n", "user_1")
	PreviewEvalSetImport(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	errData := decodeErrorData[ImportValidationErrorResponse](t, rec)
	if len(errData.Errors) == 0 || errData.Errors[0].Column != "question_type" {
		t.Fatalf("expected question_type header error, got %#v", errData)
	}
	assertNoImportPreviewState(t, db)
}

func TestCSVImportPreviewMissingQuestionFailsWithoutToken(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)

	rec, req := multipartImportRequest(t, "missing-question.csv", "", "question,ground_truth,question_type\n,answer,1\n", "user_1")
	PreviewEvalSetImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[ImportPreviewResponse](t, rec)
	if resp.ValidRows != 0 || resp.InvalidRows != 1 || resp.ImportToken == "" {
		t.Fatalf("unexpected partial preview response: %#v", resp)
	}
	if len(resp.ErrorDetails) != 1 || resp.ErrorDetails[0].Row != 2 || resp.ErrorDetails[0].Column != "question" {
		t.Fatalf("expected row 2 question error, got %#v", resp.ErrorDetails)
	}
	if resp.InvalidRowsDownloadURL == "" {
		t.Fatalf("expected invalid rows download url")
	}
	assertImportPreviewStored(t, db, resp)
}

func TestCSVImportPreviewSkipsEmptyRows(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)

	body := "question,ground_truth,question_type\n,,\nq,a,1\n   ,   ,   \n"
	rec, req := multipartImportRequest(t, "empty-rows.csv", "", body, "user_1")
	PreviewEvalSetImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[ImportPreviewResponse](t, rec)
	if resp.TotalRows != 3 || resp.EmptyRows != 2 || resp.ValidRows != 1 {
		t.Fatalf("unexpected row counters: %#v", resp)
	}
	assertImportPreviewStored(t, db, resp)
}

func TestCSVImportPreviewPartialRowsWritesOnlyInvalidOriginalRowsToCSV(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)

	body := "case_id,question,ground_truth,question_type\ncase_good,q,a,1\ncase_bad,,bad answer,2\n"
	rec, req := multipartImportRequest(t, "partial.csv", "", body, "user_1")
	PreviewEvalSetImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[ImportPreviewResponse](t, rec)
	if resp.TotalRows != 2 || resp.ValidRows != 1 || resp.InvalidRows != 1 || resp.EmptyRows != 0 {
		t.Fatalf("unexpected row counters: %#v", resp)
	}
	if resp.InvalidRowsDownloadURL == "" || !strings.Contains(resp.InvalidRowsDownloadURL, "/static-files/") || !strings.Contains(resp.InvalidRowsDownloadURL, "download=1") {
		t.Fatalf("unexpected invalid rows download url: %q", resp.InvalidRowsDownloadURL)
	}
	if len(resp.InvalidPreviewRows) != 1 || resp.InvalidPreviewRows[0].Values["case_id"] != "case_bad" {
		t.Fatalf("unexpected invalid preview rows: %#v", resp.InvalidPreviewRows)
	}

	var preview orm.EvalSetImportPreview
	if err := db.First(&preview, "token = ?", resp.ImportToken).Error; err != nil {
		t.Fatalf("query preview: %v", err)
	}
	raw, err := os.ReadFile(preview.TempPath)
	if err != nil {
		t.Fatalf("read valid temp file: %v", err)
	}
	var validRows []ImportNormalizedRow
	if err := json.Unmarshal(raw, &validRows); err != nil {
		t.Fatalf("decode valid temp rows: %v", err)
	}
	if len(validRows) != 1 || validRows[0].CaseID != "case_good" {
		t.Fatalf("expected only valid row in temp json, got %#v", validRows)
	}

	csvRaw, err := os.ReadFile(invalidRowsCSVPathForImportToken(resp.ImportToken))
	if err != nil {
		t.Fatalf("read invalid rows csv: %v", err)
	}
	records, err := csv.NewReader(bytes.NewReader(csvRaw)).ReadAll()
	if err != nil {
		t.Fatalf("decode invalid rows csv: %v; raw=%s", err, string(csvRaw))
	}
	if len(records) != 2 {
		t.Fatalf("expected header plus one invalid row, got %#v", records)
	}
	if got := strings.Join(records[0], ","); got != "case_id,question,ground_truth,question_type" {
		t.Fatalf("unexpected invalid csv header: %#v", records[0])
	}
	if got := strings.Join(records[1], ","); got != "case_bad,,bad answer,2" {
		t.Fatalf("unexpected invalid csv row: %#v", records[1])
	}
}

func TestCSVImportPreviewAcceptsBOM(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)

	rec, req := multipartImportRequest(t, "bom.csv", "", "\ufeffquestion,ground_truth,question_type\nq,a,1\n", "user_1")
	PreviewEvalSetImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[ImportPreviewResponse](t, rec)
	if resp.ValidRows != 1 || len(resp.PreviewRows) != 1 || resp.PreviewRows[0].Question != "q" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	assertImportPreviewStored(t, db, resp)
}

func TestJSONImportPreviewArraySuccess(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)

	body := `[{"case_id":"case_1","question":"q","ground_truth":"a","question_type":"1","is_deleted":false}]`
	rec, req := multipartImportRequest(t, "items.json", "", body, "user_1")
	PreviewEvalSetImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[ImportPreviewResponse](t, rec)
	if resp.FileType != importFileTypeJSON || resp.ValidRows != 1 || resp.PreviewRows[0].CaseID != "case_1" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	assertImportPreviewStored(t, db, resp)
}

func TestJSONImportPreviewItemsSuccess(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)

	body := `{"items":[{"question":"q","ground_truth":"a","question_type":"1"}]}`
	rec, req := multipartImportRequest(t, "items.json", "", body, "user_1")
	PreviewEvalSetImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[ImportPreviewResponse](t, rec)
	if resp.TotalRows != 1 || resp.ValidRows != 1 {
		t.Fatalf("unexpected row counters: %#v", resp)
	}
	assertImportPreviewStored(t, db, resp)
}

func TestJSONImportPreviewMissingGroundTruth(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)

	body := `[{"question":"q","question_type":"1"}]`
	rec, req := multipartImportRequest(t, "missing-ground-truth.json", "", body, "user_1")
	PreviewEvalSetImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[ImportPreviewResponse](t, rec)
	if resp.ValidRows != 0 || resp.InvalidRows != 1 {
		t.Fatalf("unexpected partial preview response: %#v", resp)
	}
	if len(resp.ErrorDetails) != 1 || resp.ErrorDetails[0].Row != 1 || resp.ErrorDetails[0].Column != "ground_truth" {
		t.Fatalf("expected row 1 ground_truth error, got %#v", resp.ErrorDetails)
	}
	assertImportPreviewStored(t, db, resp)
}

func TestXLSXImportPreviewRejected(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)

	rec, req := multipartImportRequest(t, "items.xlsx", "xlsx", "not parsed", "user_1")
	PreviewEvalSetImport(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	assertNoImportPreviewState(t, db)
}

func TestSuccessfulImportPreviewWritesNormalizedTempAndRecordOnly(t *testing.T) {
	db := newEvalSetTestDB(t)
	withTempImportDir(t)

	body := "case_id,generate_reason,ground_truth,is_deleted,key_points,question,question_type,reference_chunk_ids,reference_context,reference_doc,reference_doc_ids\ncase_001,,标准答案,false,,问题,1,,,,\n"
	rec, req := multipartImportRequest(t, "3.csv", "", body, "user_1")
	PreviewEvalSetImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOKData[ImportPreviewResponse](t, rec)
	if !strings.HasPrefix(resp.ImportToken, "import_tmp_") {
		t.Fatalf("expected import_tmp_ token, got %q", resp.ImportToken)
	}

	var preview orm.EvalSetImportPreview
	if err := db.First(&preview, "token = ?", resp.ImportToken).Error; err != nil {
		t.Fatalf("query preview: %v", err)
	}
	if preview.Status != importPreviewStatusReady || preview.FileName != "3.csv" || preview.FileType != importFileTypeCSV {
		t.Fatalf("unexpected preview row: %#v", preview)
	}
	if preview.ValidRows != 1 || preview.TotalRows != 1 || preview.EmptyRows != 0 {
		t.Fatalf("unexpected preview counters: %#v", preview)
	}
	if preview.TempPath == "" || filepath.Base(preview.TempPath) != resp.ImportToken+".json" {
		t.Fatalf("unexpected temp path: %q", preview.TempPath)
	}

	raw, err := os.ReadFile(preview.TempPath)
	if err != nil {
		t.Fatalf("read temp file: %v", err)
	}
	var rows []ImportNormalizedRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode temp rows: %v; raw=%s", err, string(raw))
	}
	if len(rows) != 1 || rows[0].Question != "问题" || rows[0].GroundTruth != "标准答案" || rows[0].QuestionType != "1" {
		t.Fatalf("unexpected temp rows: %#v", rows)
	}
	if strings.Contains(string(raw), "case_id,generate_reason") {
		t.Fatalf("temp file must contain normalized JSON, got raw csv: %s", string(raw))
	}

	var itemCount int64
	if err := db.Model(&orm.EvalSetItem{}).Count(&itemCount).Error; err != nil {
		t.Fatalf("count eval set items: %v", err)
	}
	if itemCount != 0 {
		t.Fatalf("preview must not write eval_set_items, got count=%d", itemCount)
	}
}

func TestCleanupExpiredImportPreviews(t *testing.T) {
	db := newEvalSetTestDB(t)
	tempDir := withTempImportDir(t)
	now := time.Now().UTC()
	tempPath := filepath.Join(tempDir, "lazymind", "eval-set-import", "import_tmp_expired.json")
	if err := os.MkdirAll(filepath.Dir(tempPath), 0700); err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	if err := os.WriteFile(tempPath, []byte("[]"), 0600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	row := orm.EvalSetImportPreview{
		Token:          "import_tmp_expired",
		Status:         importPreviewStatusReady,
		FileName:       "expired.csv",
		FileType:       importFileTypeCSV,
		TempPath:       tempPath,
		CreateUserID:   "user_1",
		CreateUserName: "user_1 name",
		CreatedAt:      now.Add(-3 * time.Hour),
		ExpiresAt:      now.Add(-time.Minute),
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed preview: %v", err)
	}

	if err := CleanupExpiredImportPreviews(t.Context(), db.DB, now); err != nil {
		t.Fatalf("cleanup expired previews: %v", err)
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("expected temp file removed, stat err=%v", err)
	}
	var updated orm.EvalSetImportPreview
	if err := db.First(&updated, "token = ?", row.Token).Error; err != nil {
		t.Fatalf("query updated preview: %v", err)
	}
	if updated.Status != importPreviewStatusExpired {
		t.Fatalf("expected expired status, got %q", updated.Status)
	}
}

func multipartImportRequest(t *testing.T, fileName, fileType, content, userID string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if fileType != "" {
		if err := writer.WriteField("file_type", fileType); err != nil {
			t.Fatalf("write file_type: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/eval-sets/imports:preview", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
		req.Header.Set("X-User-Name", userID+" name")
	}
	return httptest.NewRecorder(), req
}

func withTempImportDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", filepath.Join(dir, "uploads"))
	return dir
}

func decodeErrorData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var envelope common.APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, rec.Body.String())
	}
	raw, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode data: %v; raw=%s", err, string(raw))
	}
	return out
}

func assertNoImportPreviewState(t *testing.T, db *orm.DB) {
	t.Helper()
	var count int64
	if err := db.Model(&orm.EvalSetImportPreview{}).Count(&count).Error; err != nil {
		t.Fatalf("count import previews: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no import preview rows, got %d", count)
	}
	dir := filepath.Join(os.TempDir(), "lazymind", "eval-set-import")
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("expected no temp files, got %v", names)
	}
}

func assertImportPreviewStored(t *testing.T, db *orm.DB, resp ImportPreviewResponse) {
	t.Helper()
	if resp.ImportToken == "" {
		t.Fatalf("expected import token")
	}
	var preview orm.EvalSetImportPreview
	if err := db.First(&preview, "token = ?", resp.ImportToken).Error; err != nil {
		t.Fatalf("query preview: %v", err)
	}
	if preview.ValidRows != resp.ValidRows || preview.EmptyRows != resp.EmptyRows || preview.TotalRows != resp.TotalRows {
		t.Fatalf("preview counters do not match response: row=%#v resp=%#v", preview, resp)
	}
	if len(resp.ErrorDetails) > 0 {
		var details []ImportValidationErrorDetail
		if err := json.Unmarshal(preview.ErrorDetailsJSON, &details); err != nil {
			t.Fatalf("decode preview error details: %v", err)
		}
		if len(details) != len(resp.ErrorDetails) {
			t.Fatalf("preview error details do not match response: row=%#v resp=%#v", details, resp.ErrorDetails)
		}
	}
	if _, err := os.Stat(preview.TempPath); err != nil {
		t.Fatalf("expected temp file %q: %v", preview.TempPath, err)
	}
	if !strings.HasPrefix(preview.TempPath, fmt.Sprintf("%s%c", os.TempDir(), os.PathSeparator)) {
		t.Fatalf("expected temp path under os.TempDir, got %q", preview.TempPath)
	}
}
