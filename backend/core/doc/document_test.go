package doc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/store"

	"github.com/gorilla/mux"
)

func TestDetectDocumentContentTypeCSV(t *testing.T) {
	if got := detectDocumentContentType("cases.csv", "", ""); got != "text/csv; charset=utf-8" {
		t.Fatalf("expected csv content type, got %q", got)
	}
}

func TestStreamLocalFileInlineUsesActualFilenameForCSV(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)

	fullPath := filepath.Join(root, "agent-results", "thr-1", "datasets", "cases.csv")
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte{0xEF, 0xBB, 0xBF, 'a', ',', 'b', '\n'}, 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	recorder := httptest.NewRecorder()
	streamLocalFile(recorder, fullPath, "cases.csv", "", true)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Fatalf("expected csv content type, got %q", got)
	}
	disposition := recorder.Header().Get("Content-Disposition")
	if !strings.Contains(disposition, "inline") || !strings.Contains(disposition, "cases.csv") {
		t.Fatalf("expected inline disposition with csv filename, got %q", disposition)
	}
	if strings.Contains(disposition, "preview.pdf") {
		t.Fatalf("disposition must not force preview.pdf: %q", disposition)
	}
}

func newDocumentTestDB(t *testing.T) *orm.DB {
	t.Helper()

	t.Setenv("LAZYMIND_READONLY_SCHEMA", "main")
	dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())
	db, err := orm.Connect(orm.DriverSQLite, dsn)
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.Dataset{},
		&orm.Document{},
		&orm.Task{},
		&readonlyorm.LazyLLMDocRow{},
		&readonlyorm.LazyLLMDocServiceTaskRow{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	store.Init(db.DB, db.DB, nil)
	return db
}

func TestLoadMergedDocumentsUsesCoreUpdatedAtWhenNewerThanReadonlyBase(t *testing.T) {
	db := newDocumentTestDB(t)
	ctx := context.Background()

	baseCreatedAt := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	baseUpdatedAt := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	coreUpdatedAt := time.Date(2026, 5, 2, 10, 30, 0, 0, time.UTC)

	if err := db.Create(&orm.Document{
		ID:           "doc-core",
		LazyllmDocID: "doc-lazy",
		DatasetID:    "dataset-1",
		DisplayName:  "report.pdf",
		FileID:       "doc-core",
		Tags:         []byte(`[]`),
		Ext:          []byte(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-1",
			CreateUserName: "Alice",
			CreatedAt:      baseCreatedAt,
			UpdatedAt:      coreUpdatedAt,
		},
	}).Error; err != nil {
		t.Fatalf("create core document: %v", err)
	}
	if err := db.Table((readonlyorm.LazyLLMDocRow{}).TableName()).Create(&readonlyorm.LazyLLMDocRow{
		DocID:        "doc-lazy",
		Filename:     "report.pdf",
		Path:         "/uploads/report.pdf",
		UploadStatus: string(TaskStateSucceeded),
		SourceType:   "LOCAL_FILE",
		CreatedAt:    baseCreatedAt,
		UpdatedAt:    baseUpdatedAt,
	}).Error; err != nil {
		t.Fatalf("create readonly document: %v", err)
	}

	rows, total, err := loadMergedDocumentsByDocIDs(ctx, []string{"doc-core"}, "dataset-1", "", "", false, 10, 0)
	if err != nil {
		t.Fatalf("load merged documents: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("expected one merged row, total=%d len=%d", total, len(rows))
	}
	if !rows[0].BaseUpdatedAt.Equal(coreUpdatedAt) {
		t.Fatalf("expected merged update time %s, got %s", coreUpdatedAt.Format(time.RFC3339), rows[0].BaseUpdatedAt.Format(time.RFC3339))
	}
	doc := docFromRow(rows[0])
	if doc.UpdateTime != coreUpdatedAt.Format(time.RFC3339) {
		t.Fatalf("expected document update_time %q, got %q", coreUpdatedAt.Format(time.RFC3339), doc.UpdateTime)
	}
}

func TestListDocumentsByDatasetsDefaultPaginationCursorNoDuplicates(t *testing.T) {
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "dataset-a", "user-1")
	seedDocumentListDataset(t, db, "dataset-b", "user-1")

	base := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		seedDocumentListDoc(t, db, "dataset-a", fmt.Sprintf("a-doc-%02d", i), fmt.Sprintf("A-%02d.pdf", i), base.Add(time.Duration(-i)*time.Minute), "Alice", nil)
		seedDocumentListDoc(t, db, "dataset-b", fmt.Sprintf("b-doc-%02d", i), fmt.Sprintf("B-%02d.pdf", i), base.Add(time.Duration(-i)*time.Minute), "Alice", nil)
	}

	first := requestListDocumentsByDatasets(t, `{"dataset_ids":["dataset-b","dataset-a","dataset-a","missing"]}`, "user-1")
	if first.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", first.Code, first.Body.String())
	}
	var firstBody ListDocumentsResponse
	decodeRecorderJSON(t, first, &firstBody)
	if len(firstBody.Documents) != 10 {
		t.Fatalf("expected default page size 10, got %d", len(firstBody.Documents))
	}
	if firstBody.TotalSize != 12 {
		t.Fatalf("expected total size 12, got %d", firstBody.TotalSize)
	}
	if strings.TrimSpace(firstBody.NextPageToken) == "" {
		t.Fatalf("expected next_page_token")
	}
	if got, want := firstBody.Documents[0].DocumentID, "a-doc-00"; got != want {
		t.Fatalf("expected stable dataset tie-break first doc %q, got %q", want, got)
	}
	if got, want := firstBody.Documents[1].DocumentID, "b-doc-00"; got != want {
		t.Fatalf("expected stable dataset tie-break second doc %q, got %q", want, got)
	}

	if err := db.Model(&orm.Document{}).
		Where("id = ?", firstBody.Documents[0].DocumentID).
		Update("updated_at", base.Add(-30*time.Minute)).Error; err != nil {
		t.Fatalf("move first page document behind cursor: %v", err)
	}

	second := requestListDocumentsByDatasets(t, fmt.Sprintf(`{"dataset_ids":["dataset-b","dataset-a"],"page_token":%q}`, firstBody.NextPageToken), "user-1")
	if second.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", second.Code, second.Body.String())
	}
	var secondBody ListDocumentsResponse
	decodeRecorderJSON(t, second, &secondBody)
	if len(secondBody.Documents) != 2 {
		t.Fatalf("expected remaining 2 documents, got %d", len(secondBody.Documents))
	}
	if secondBody.NextPageToken != "" {
		t.Fatalf("expected no next_page_token on final page, got %q", secondBody.NextPageToken)
	}

	seen := map[string]struct{}{}
	for _, doc := range firstBody.Documents {
		seen[doc.DocumentID] = struct{}{}
	}
	for _, doc := range secondBody.Documents {
		if _, ok := seen[doc.DocumentID]; ok {
			t.Fatalf("document %q repeated on next page", doc.DocumentID)
		}
	}
}

func TestListDocumentsByDatasetsKeywordMatchesDocumentNameOnly(t *testing.T) {
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "dataset-a", "user-1")
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	seedDocumentListDoc(t, db, "dataset-a", "doc-name", "needle-report.pdf", now, "Alice", nil)
	seedDocumentListDoc(t, db, "dataset-a", "doc-tag", "budget.pdf", now.Add(-time.Minute), "Alice", json.RawMessage(`{"stored_path":"/tmp/needle/budget.pdf"}`))
	seedDocumentListDoc(t, db, "dataset-a", "doc-creator", "notes.pdf", now.Add(-2*time.Minute), "needle-user", nil)

	rec := requestListDocumentsByDatasets(t, `{"dataset_ids":["dataset-a"],"keyword":"needle"}`, "user-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body ListDocumentsResponse
	decodeRecorderJSON(t, rec, &body)
	if body.TotalSize != 1 || len(body.Documents) != 1 {
		t.Fatalf("expected only name match, total=%d len=%d body=%s", body.TotalSize, len(body.Documents), rec.Body.String())
	}
	if got, want := body.Documents[0].DocumentID, "doc-name"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestListDocumentsByDatasetsSkipsInaccessibleAndMissingDatasets(t *testing.T) {
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "dataset-owned", "user-1")
	seedDocumentListDataset(t, db, "dataset-other", "user-2")
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	seedDocumentListDoc(t, db, "dataset-owned", "doc-owned", "owned.pdf", now, "Alice", nil)
	seedDocumentListDoc(t, db, "dataset-other", "doc-other", "other.pdf", now, "Bob", nil)

	rec := requestListDocumentsByDatasets(t, `{"dataset_ids":["dataset-other","dataset-owned","missing"]}`, "user-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body ListDocumentsResponse
	decodeRecorderJSON(t, rec, &body)
	if body.TotalSize != 1 || len(body.Documents) != 1 {
		t.Fatalf("expected one accessible document, total=%d len=%d body=%s", body.TotalSize, len(body.Documents), rec.Body.String())
	}
	if got, want := body.Documents[0].DocumentID, "doc-owned"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestListDocumentsByDatasetsRequiresDatasetIDs(t *testing.T) {
	_ = newDocumentTestDB(t)

	rec := requestListDocumentsByDatasets(t, `{}`, "user-1")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteDocumentRecalculatesParentFolderSize(t *testing.T) {
	db := newDocumentTestDB(t)
	seedFolderWithSizedDoc(t, db, "dataset-1", "folder-1", "doc-1", 31744)

	req := httptest.NewRequest(http.MethodDelete, "/api/core/datasets/dataset-1/documents/doc-1", nil)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"dataset": "dataset-1", "document": "doc-1"})
	rec := httptest.NewRecorder()

	DeleteDocument(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	assertFolderHasZeroSize(t, db, "dataset-1", "folder-1")
}

func TestBatchDeleteDocumentRecalculatesParentFolderSize(t *testing.T) {
	db := newDocumentTestDB(t)
	seedFolderWithSizedDoc(t, db, "dataset-1", "folder-1", "doc-1", 31744)

	req := httptest.NewRequest(http.MethodPost, "/api/core/datasets/dataset-1:batchDelete", strings.NewReader(`{"parent":"datasets/dataset-1","names":["doc-1"]}`))
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"dataset": "dataset-1"})
	rec := httptest.NewRecorder()

	BatchDeleteDocument(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	assertFolderHasZeroSize(t, db, "dataset-1", "folder-1")
}

func seedFolderWithSizedDoc(t *testing.T, db *orm.DB, datasetID, folderID, docID string, size int64) {
	t.Helper()
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)

	if err := db.Create(&orm.Dataset{
		ID:           datasetID,
		KbID:         "kb-" + datasetID,
		DisplayName:  "Dataset",
		DatasetState: 0,
		ShareType:    0,
		Type:         1,
		Ext:          json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-1",
			CreateUserName: "Alice",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	if err := db.Create(&orm.Document{
		ID:          folderID,
		DatasetID:   datasetID,
		DisplayName: "11111",
		Tags:        []byte(`[]`),
		Ext:         json.RawMessage(fmt.Sprintf(`{"file_size":%d,"child_document_count":1,"recursive_document_count":1,"recursive_file_size":%d}`, size, size)),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-1",
			CreateUserName: "Alice",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if err := db.Create(&orm.Document{
		ID:          docID,
		DatasetID:   datasetID,
		DisplayName: "perm_1.docx",
		PID:         folderID,
		Tags:        []byte(`[]`),
		Ext:         json.RawMessage(fmt.Sprintf(`{"file_size":%d,"original_filename":"perm_1.docx"}`, size)),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-1",
			CreateUserName: "Alice",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create child document: %v", err)
	}
}

func requestListDocumentsByDatasets(t *testing.T, body string, userID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/core/documents:listByDatasets", strings.NewReader(body))
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
	}
	rec := httptest.NewRecorder()
	ListDocumentsByDatasets(rec, req)
	return rec
}

func decodeRecorderJSON(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
}

func seedDocumentListDataset(t *testing.T, db *orm.DB, datasetID, ownerID string) {
	t.Helper()
	now := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	if err := db.Create(&orm.Dataset{
		ID:           datasetID,
		KbID:         "kb-" + datasetID,
		DisplayName:  "Dataset " + datasetID,
		DatasetState: 0,
		ShareType:    0,
		Type:         1,
		Ext:          json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   ownerID,
			CreateUserName: ownerID,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create dataset %s: %v", datasetID, err)
	}
}

func seedDocumentListDoc(t *testing.T, db *orm.DB, datasetID, docID, displayName string, updatedAt time.Time, creator string, ext json.RawMessage) {
	t.Helper()
	if ext == nil {
		ext = json.RawMessage(`{}`)
	}
	if err := db.Create(&orm.Document{
		ID:          docID,
		DatasetID:   datasetID,
		DisplayName: displayName,
		Tags:        []byte(`["needle"]`),
		Ext:         ext,
		BaseModel: orm.BaseModel{
			CreateUserID:   creator,
			CreateUserName: creator,
			CreatedAt:      updatedAt.Add(-time.Hour),
			UpdatedAt:      updatedAt,
		},
	}).Error; err != nil {
		t.Fatalf("create document %s: %v", docID, err)
	}
}

func assertFolderHasZeroSize(t *testing.T, db *orm.DB, datasetID, folderID string) {
	t.Helper()
	var folder orm.Document
	if err := db.Where("id = ? AND dataset_id = ?", folderID, datasetID).Take(&folder).Error; err != nil {
		t.Fatalf("query folder: %v", err)
	}
	stats := folderStatsFromExt(folder.Ext)
	if stats.RecursiveFileSize != 0 || stats.RecursiveDocumentCount != 0 || stats.ChildDocumentCount != 0 {
		t.Fatalf("expected empty folder stats after deleting child, got %+v ext=%s", stats, string(folder.Ext))
	}

	row, err := loadDocumentByID(context.Background(), datasetID, folderID)
	if err != nil {
		t.Fatalf("load folder document: %v", err)
	}
	if got := docFromRow(row).DocumentSize; got != 0 {
		t.Fatalf("expected folder document_size 0 after deleting child, got %d", got)
	}
}
