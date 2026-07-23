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

func TestStreamLocalFileSubagentWorkspace(t *testing.T) {
	subRoot := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", subRoot)

	fullPath := filepath.Join(subRoot, "user-1", "task-1", "output.png")
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("png"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	recorder := httptest.NewRecorder()
	streamLocalFile(recorder, fullPath, "output.png", "image/png", true)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestGetSignedStaticFileSubagentPath(t *testing.T) {
	subRoot := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", subRoot)

	inner := filepath.Join("user-1", "task-1", "output.png")
	fullPath := filepath.Join(subRoot, inner)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("png"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	rel := "subagent/" + filepath.ToSlash(inner)
	expires := time.Now().UTC().Unix() + 3600
	sig := signStaticFile(rel, expires)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/static-files/%s?expires=%d&sig=%s", encodeStaticFilePath(rel), expires, sig),
		nil)
	req = mux.SetURLVars(req, map[string]string{"path": encodeStaticFilePath(rel)})

	recorder := httptest.NewRecorder()
	GetSignedStaticFile(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); body != "png" {
		t.Fatalf("expected body png, got %q", body)
	}
}

func TestStaticFileURLFromSubagentPathUsesSubagentPrefix(t *testing.T) {
	subRoot := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", subRoot)

	fullPath := filepath.Join(subRoot, "user-1", "task-1", "output.json")
	url := StaticFileURLFromAnyStoragePath(fullPath)
	if !strings.HasPrefix(url, "/static-files/subagent/user-1/task-1/output.json?") {
		t.Fatalf("expected subagent static file url, got %q", url)
	}
}

func TestAggregateDocumentsFiltersUnreadableDatasets(t *testing.T) {
	db := newDocumentTestDB(t)
	now := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&orm.Dataset{ID: "dataset-owned", DisplayName: "Owned", BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "Alice", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatalf("create owned dataset: %v", err)
	}
	if err := db.Create(&orm.Dataset{ID: "dataset-other", DisplayName: "Other", BaseModel: orm.BaseModel{CreateUserID: "user-2", CreateUserName: "Bob", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatalf("create other dataset: %v", err)
	}
	if err := db.Create(&orm.Document{ID: "doc-owned", DatasetID: "dataset-owned", DisplayName: "report.pdf", Tags: []byte(`["finance"]`), Ext: []byte(`{"file_size":12}`), BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "Alice", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatalf("create owned document: %v", err)
	}
	if err := db.Create(&orm.Document{ID: "doc-other", DatasetID: "dataset-other", DisplayName: "secret.pdf", Tags: []byte(`["finance"]`), Ext: []byte(`{"file_size":20}`), BaseModel: orm.BaseModel{CreateUserID: "user-2", CreateUserName: "Bob", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatalf("create other document: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/core/system-query/documents:aggregate", strings.NewReader(`{"dataset_ids":["dataset-owned","dataset-other"],"file_types":["pdf"],"group_by":["dataset_id","file_type"]}`))
	req.Header.Set("X-User-Id", "user-1")
	AggregateDocuments(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp AggregateDocumentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.TotalCount != 1 || resp.TotalSize != 12 {
		t.Fatalf("expected only readable owned doc, got count=%d size=%d", resp.TotalCount, resp.TotalSize)
	}
	if len(resp.Groups) != 1 || resp.Groups[0].Key["dataset_id"] != "dataset-owned" || resp.Groups[0].Key["file_type"] != "PDF" {
		t.Fatalf("unexpected groups: %#v", resp.Groups)
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
		&orm.UploadedFile{},
		&orm.DefaultDataset{},
		&orm.EvalSet{},
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

func TestLoadMergedDocumentsUsesCoreTimesWhenReadonlyWallClockLooksNewer(t *testing.T) {
	db := newDocumentTestDB(t)
	ctx := context.Background()

	coreCreatedAt := time.Date(2026, 6, 18, 7, 34, 56, 0, time.UTC)
	coreUpdatedAt := time.Date(2026, 6, 18, 7, 34, 57, 0, time.UTC)
	readonlyCreatedAt := coreCreatedAt.Add(8 * time.Hour)
	readonlyUpdatedAt := coreUpdatedAt.Add(8 * time.Hour)

	if err := db.Create(&orm.Document{
		ID:           "doc-core",
		LazyllmDocID: "doc-lazy",
		DatasetID:    "dataset-1",
		DisplayName:  "report.md",
		FileID:       "doc-core",
		Tags:         []byte(`[]`),
		Ext:          []byte(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-1",
			CreateUserName: "Alice",
			CreatedAt:      coreCreatedAt,
			UpdatedAt:      coreUpdatedAt,
		},
	}).Error; err != nil {
		t.Fatalf("create core document: %v", err)
	}
	if err := db.Table((readonlyorm.LazyLLMDocRow{}).TableName()).Create(&readonlyorm.LazyLLMDocRow{
		DocID:        "doc-lazy",
		Filename:     "report.md",
		Path:         "/uploads/report.md",
		UploadStatus: string(TaskStateUploaded),
		SourceType:   "LOCAL_FILE",
		CreatedAt:    readonlyCreatedAt,
		UpdatedAt:    readonlyUpdatedAt,
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
	if !rows[0].BaseCreatedAt.Equal(coreCreatedAt) {
		t.Fatalf("expected core created time %s, got %s", coreCreatedAt.Format(time.RFC3339), rows[0].BaseCreatedAt.Format(time.RFC3339))
	}
	if !rows[0].BaseUpdatedAt.Equal(coreUpdatedAt) {
		t.Fatalf("expected core updated time %s, got %s", coreUpdatedAt.Format(time.RFC3339), rows[0].BaseUpdatedAt.Format(time.RFC3339))
	}
	doc := docFromRow(rows[0])
	if doc.CreateTime != "2026-06-18T07:34:56Z" {
		t.Fatalf("expected core create_time, got %q", doc.CreateTime)
	}
	if doc.UpdateTime != "2026-06-18T07:34:57Z" {
		t.Fatalf("expected core update_time, got %q", doc.UpdateTime)
	}
}

func TestBuildTaskResponseDoesNotSucceedBeforeExternalTaskRowExists(t *testing.T) {
	db := newDocumentTestDB(t)
	now := time.Date(2026, 5, 2, 10, 30, 0, 0, time.UTC)

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
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create core document: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/datasets/dataset-1/tasks/task-core", nil)
	resp := buildTaskResponse(req, orm.Task{
		ID:            "task-core",
		LazyllmTaskID: "lazy-task-pending-row",
		DocID:         "doc-core",
		DatasetID:     "dataset-1",
		TaskType:      string(TaskTypeParse),
		DisplayName:   "report.pdf",
		Ext:           []byte(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-1",
			CreateUserName: "Alice",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	})

	if resp.TaskState != "WORKING" {
		t.Fatalf("expected task to keep polling before external row exists, got %+v", resp)
	}
}

func TestUITaskStatusRunningIncludesLazyllmActiveStates(t *testing.T) {
	states := uiTaskStatusToInternalStates("running")
	for _, want := range []string{"WAITING", "WORKING"} {
		found := false
		for _, state := range states {
			if state == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected running filter to include %s, got %v", want, states)
		}
	}
}

func TestTaskStateMatchesUIRunningFilter(t *testing.T) {
	for _, state := range []string{"WAITING", "WORKING"} {
		if !taskStateMatchesFilter(state, "running") {
			t.Fatalf("expected %s to match running filter", state)
		}
	}
	if taskStateMatchesFilter("SUCCESS", "running") {
		t.Fatalf("expected SUCCESS not to match running filter")
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

func TestListDocumentsByDatasetsExcludesFolders(t *testing.T) {
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "dataset-a", "user-1")
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&orm.Document{
		ID:          "folder-1",
		DatasetID:   "dataset-a",
		DisplayName: "folder",
		Tags:        []byte(`[]`),
		Ext:         json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-1",
			CreateUserName: "Alice",
			CreatedAt:      now.Add(-time.Hour),
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create folder: %v", err)
	}
	seedDocumentListDoc(t, db, "dataset-a", "doc-1", "report.pdf", now.Add(-time.Minute), "Alice", nil)

	rec := requestListDocumentsByDatasets(t, `{"dataset_ids":["dataset-a"]}`, "user-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body ListDocumentsResponse
	decodeRecorderJSON(t, rec, &body)
	if body.TotalSize != 1 || len(body.Documents) != 1 {
		t.Fatalf("expected only one document, total=%d len=%d body=%s", body.TotalSize, len(body.Documents), rec.Body.String())
	}
	if got, want := body.Documents[0].DocumentID, "doc-1"; got != want {
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

func TestListDocumentsByDatasetsFiltersScanDeniedDataset(t *testing.T) {
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "dataset-local", "user-1")
	seedDocumentListDataset(t, db, "dataset-manual", "user-1")
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	seedDocumentListDoc(t, db, "dataset-local", "doc-local", "local.txt", now, "user-1", nil)
	seedDocumentListDoc(t, db, "dataset-manual", "doc-manual", "manual.txt", now.Add(time.Minute), "user-1", nil)

	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/scan/internal/source-access/by-dataset:batch" {
			t.Errorf("unexpected scan request %s %q", r.Method, r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
		return testJSONResponse(http.StatusOK, `{"items":[{"dataset_id":"dataset-local","source_id":"source-local","exists":true,"allowed":false},{"dataset_id":"dataset-manual","exists":false,"allowed":true}]}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")

	req := httptest.NewRequest(http.MethodPost, "/api/core/documents:listByDatasets", strings.NewReader(`{"dataset_ids":["dataset-local","dataset-manual"]}`))
	req.Header.Set("X-User-Id", "user-1")
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	ListDocumentsByDatasets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body ListDocumentsResponse
	decodeRecorderJSON(t, rec, &body)
	if body.TotalSize != 1 || len(body.Documents) != 1 || body.Documents[0].DocumentID != "doc-manual" {
		t.Fatalf("expected only manual dataset document, got total=%d docs=%+v", body.TotalSize, body.Documents)
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

func TestDeleteFolderDeletesChildrenAndUpdatesDatasetStats(t *testing.T) {
	db := newDocumentTestDB(t)
	seedFolderWithSizedDoc(t, db, "dataset-1", "folder-1", "doc-1", 31744)
	assertDatasetStats(t, "dataset-1", 1, 31744)

	req := httptest.NewRequest(http.MethodDelete, "/api/core/datasets/dataset-1/documents/folder-1", nil)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"dataset": "dataset-1", "document": "folder-1"})
	rec := httptest.NewRecorder()

	DeleteDocument(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	assertDocumentSoftDeleted(t, db, "dataset-1", "folder-1")
	assertDocumentSoftDeleted(t, db, "dataset-1", "doc-1")
	assertDatasetStats(t, "dataset-1", 0, 0)
}

func TestBatchDeleteFolderDeletesChildrenAndUpdatesDatasetStats(t *testing.T) {
	db := newDocumentTestDB(t)
	seedFolderWithSizedDoc(t, db, "dataset-1", "folder-1", "doc-1", 31744)
	assertDatasetStats(t, "dataset-1", 1, 31744)

	req := httptest.NewRequest(http.MethodPost, "/api/core/datasets/dataset-1:batchDelete", strings.NewReader(`{"parent":"datasets/dataset-1","names":["folder-1"]}`))
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"dataset": "dataset-1"})
	rec := httptest.NewRecorder()

	BatchDeleteDocument(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	assertDocumentSoftDeleted(t, db, "dataset-1", "folder-1")
	assertDocumentSoftDeleted(t, db, "dataset-1", "doc-1")
	assertDatasetStats(t, "dataset-1", 0, 0)
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

func assertDatasetStats(t *testing.T, datasetID string, wantCount, wantSize int64) {
	t.Helper()
	stats := calcDatasetStats(context.Background(), datasetID)
	if stats.DocumentCount != wantCount || stats.DocumentSize != wantSize {
		t.Fatalf("expected dataset stats count=%d size=%d, got %+v", wantCount, wantSize, stats)
	}
}

func assertDocumentSoftDeleted(t *testing.T, db *orm.DB, datasetID, docID string) {
	t.Helper()
	var row orm.Document
	if err := db.Where("id = ? AND dataset_id = ?", docID, datasetID).Take(&row).Error; err != nil {
		t.Fatalf("query document %s: %v", docID, err)
	}
	if row.DeletedAt == nil {
		t.Fatalf("expected document %s to be soft deleted", docID)
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
