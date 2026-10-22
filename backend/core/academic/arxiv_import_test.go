package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"lazymind/core/academic/arxivsource"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/doc"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func arxivImportTestService(t *testing.T) (*arxivImportService, *atomic.Int32) {
	t.Helper()
	db := orm.MigrateTestDB(t, &orm.AcademicWork{}, &orm.AcademicWorkDocument{}, &orm.AcademicDocumentState{}, &orm.AcademicReference{}, &orm.PaperImportBatch{}, &orm.PaperImportItem{}, &orm.Dataset{}, &orm.Document{}, &orm.Task{}, &orm.DocumentProcessingState{}).DB
	previous, previousLazy, previousState := store.DB(), store.LazyLLMDB(), store.State()
	store.Init(db, db, previousState)
	t.Cleanup(func() { store.Init(previous, previousLazy, previousState) })
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	for _, id := range []string{"dataset", "other"} {
		if err := db.Create(&orm.Dataset{ID: id, ProcessingLevel: "stored", BaseModel: orm.BaseModel{CreateUserID: "user"}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	count := &atomic.Int32{}
	svc := newArxivImportService(db)
	svc.lookup = func(_ context.Context, input string) (arxivsource.Metadata, error) {
		version := "v2"
		if strings.Contains(input, "v1") {
			version = "v1"
		}
		return arxivsource.Metadata{Title: "A reliable import", Authors: []string{"First Author", "Second Author"}, Year: 2024, Abstract: "Full abstract", DOI: "10.1234/test", Venue: "Journal", BaseID: "2404.06654", Version: version, AbsURL: "https://arxiv.org/abs/2404.06654" + version, PDFURL: "https://arxiv.org/pdf/2404.06654" + version}, nil
	}
	svc.acquire = func(_ context.Context, candidate FulltextCandidate) (string, int64, error) {
		count.Add(1)
		if !strings.HasSuffix(candidate.URL, "v1") && !strings.HasSuffix(candidate.URL, "v2") {
			return "", 0, errors.New("download must be version-pinned")
		}
		file, err := os.CreateTemp(t.TempDir(), "download-*.pdf")
		if err != nil {
			return "", 0, err
		}
		content := []byte("%PDF-1.7\nreal downloaded PDF")
		_, err = file.Write(content)
		_ = file.Close()
		return file.Name(), int64(len(content)), err
	}
	var sequence atomic.Int32
	svc.importFile = func(ctx context.Context, input doc.LocalFileImportRequest) (doc.LocalFileImportResult, error) {
		id := fmt.Sprintf("doc-%d", sequence.Add(1))
		bytes, err := os.ReadFile(input.SourcePath)
		if err != nil {
			return doc.LocalFileImportResult{}, err
		}
		stored := filepath.Join(t.TempDir(), id+".pdf")
		if err = os.WriteFile(stored, bytes, 0600); err != nil {
			return doc.LocalFileImportResult{}, err
		}
		ext, _ := json.Marshal(map[string]any{"stored_path": stored, "file_size": len(bytes), "content_type": "application/pdf"})
		tags, _ := json.Marshal(input.Tags)
		row := orm.Document{ID: id, DatasetID: input.DatasetID, DisplayName: input.DisplayName, DocumentType: "pdf", Ext: ext, Tags: tags, BaseModel: orm.BaseModel{CreateUserID: input.UserID}}
		if err = db.WithContext(ctx).Create(&row).Error; err != nil {
			return doc.LocalFileImportResult{}, err
		}
		taskExt, _ := json.Marshal(map[string]string{"task_state": "SUCCEEDED", "data_source_type": input.DataSourceType})
		task := orm.Task{ID: "task-" + id, DocID: id, DatasetID: input.DatasetID, Ext: taskExt, BaseModel: orm.BaseModel{CreateUserID: input.UserID}}
		if err = db.WithContext(ctx).Create(&task).Error; err != nil {
			return doc.LocalFileImportResult{}, err
		}
		return doc.LocalFileImportResult{DocumentID: id, TaskID: task.ID, ContentSHA256: strings.Repeat("a", 64), Start: doc.StartTaskResult{Status: "SUCCEEDED"}}, nil
	}
	return svc, count
}

func TestArxivImportStoresParentAndVersionedAttachmentAndReusesIt(t *testing.T) {
	svc, count := arxivImportTestService(t)
	first, err := svc.importPaper(context.Background(), "https://arxiv.org/pdf/2404.06654", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "imported" || first.WorkID == "" || first.DocumentID == "" || first.SourceVersion != "v2" || first.Size <= 0 || first.ProcessingStatus != "ready" {
		t.Fatalf("incomplete import: %#v", first)
	}
	second, err := svc.importPaper(context.Background(), "arXiv:2404.06654v2", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "already_present" || second.DocumentID != first.DocumentID || count.Load() != 1 {
		t.Fatalf("duplicate downloaded: %#v, downloads=%d", second, count.Load())
	}
	older, err := svc.importPaper(context.Background(), "2404.06654v1", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	if older.WorkID != first.WorkID || older.DocumentID == first.DocumentID || older.SourceVersion != "v1" {
		t.Fatalf("version identity lost: %#v", older)
	}
	other, err := svc.importPaper(context.Background(), "2404.06654v2", "other", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	if other.WorkID != first.WorkID || other.DocumentID == first.DocumentID {
		t.Fatalf("dataset attachment identity lost: %#v", other)
	}
	items, err := svc.library(context.Background(), "dataset")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Paper.Title != "A reliable import" || len(items[0].Paper.Authors) != 2 || items[0].Paper.Abstract != "Full abstract" {
		t.Fatalf("library cannot recover canonical metadata: %#v", items)
	}
}

func TestArxivImportConcurrentSameVersionCreatesOneAttachment(t *testing.T) {
	svc, count := arxivImportTestService(t)
	var wg sync.WaitGroup
	results := make(chan ArxivImportResult, 4)
	failures := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := svc.importPaper(context.Background(), "2404.06654v2", "dataset", "user", "User")
			results <- result
			failures <- err
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for result := range results {
		if id == "" {
			id = result.DocumentID
		}
		if result.DocumentID != id {
			t.Fatalf("duplicate document %s versus %s", id, result.DocumentID)
		}
	}
	if count.Load() != 1 {
		t.Fatalf("downloads=%d", count.Load())
	}
}

func TestArxivImportDownloadFailureDoesNotCreatePlaceholderWork(t *testing.T) {
	svc, _ := arxivImportTestService(t)
	svc.acquire = func(context.Context, FulltextCandidate) (string, int64, error) {
		return "", 0, errors.New("upstream returned HTTP 503")
	}
	_, err := svc.importPaper(context.Background(), "2404.06654", "dataset", "user", "User")
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("download error hidden: %v", err)
	}
	var count int64
	svc.db.Model(&orm.AcademicWork{}).Count(&count)
	if count != 0 {
		t.Fatalf("created %d placeholder works", count)
	}
}

func TestArxivImportProcessingFailureKeepsRecoverableAttachmentWithoutSuccess(t *testing.T) {
	svc, count := arxivImportTestService(t)
	save := svc.importFile
	svc.importFile = func(ctx context.Context, input doc.LocalFileImportRequest) (doc.LocalFileImportResult, error) {
		result, err := save(ctx, input)
		if err != nil {
			return result, err
		}
		return result, errors.New("parser unavailable")
	}
	result, err := svc.importPaper(context.Background(), "2404.06654", "dataset", "user", "User")
	if err == nil || !strings.Contains(err.Error(), "parser unavailable") || result.DocumentID == "" {
		t.Fatalf("processing failed but import pretended success: %#v %v", result, err)
	}
	items, err := svc.library(context.Background(), "dataset")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ProcessingStatus != "failed" || !strings.Contains(items[0].ProcessingError, "parser unavailable") {
		t.Fatalf("lost real attachment/error: %#v", items)
	}
	_, err = svc.importPaper(context.Background(), "2404.06654", "dataset", "user", "User")
	if err == nil || count.Load() != 1 {
		t.Fatalf("retry downloaded duplicate or hid failure: %v downloads=%d", err, count.Load())
	}
}

func TestArxivImportLibraryExcludesMissingAndDeletedFiles(t *testing.T) {
	svc, _ := arxivImportTestService(t)
	result, err := svc.importPaper(context.Background(), "2404.06654", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	var document orm.Document
	svc.db.Take(&document, "id = ?", result.DocumentID)
	var ext struct {
		StoredPath string `json:"stored_path"`
	}
	json.Unmarshal(document.Ext, &ext)
	if err = os.Remove(ext.StoredPath); err != nil {
		t.Fatal(err)
	}
	items, err := svc.library(context.Background(), "dataset")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("missing file exposed as attachment: %#v", items)
	}
	replacement, err := svc.importPaper(context.Background(), "2404.06654", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	if replacement.DocumentID == result.DocumentID || replacement.Status != "imported" {
		t.Fatalf("reused missing file: %#v", replacement)
	}
	svc.db.Model(&orm.Document{}).Where("id = ?", replacement.DocumentID).Update("deleted_at", gorm.Expr("CURRENT_TIMESTAMP"))
	items, err = svc.library(context.Background(), "dataset")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("deleted file exposed: %#v", items)
	}
}

func TestArxivLibraryLeavesGeneralWorkflowAttachmentsToDocumentSync(t *testing.T) {
	svc, _ := arxivImportTestService(t)
	result, err := svc.importPaper(context.Background(), "2404.06654", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	var original orm.Document
	if err = svc.db.Take(&original, "id=?", result.DocumentID).Error; err != nil {
		t.Fatal(err)
	}
	var ext arxivDocumentExt
	json.Unmarshal(original.Ext, &ext)
	ext.Marker = nil
	generic := orm.Document{ID: "workflow-pdf", DatasetID: "dataset", DisplayName: "workflow.pdf", Ext: marshal(ext)}
	if err = svc.db.Create(&generic).Error; err != nil {
		t.Fatal(err)
	}
	link := orm.AcademicWorkDocument{AcademicWorkID: result.WorkID, DatasetID: "dataset", DocumentID: generic.ID, SourceProvider: "arxiv"}
	if err = svc.db.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	items, err := svc.library(context.Background(), "dataset")
	if err != nil || len(items) != 1 || items[0].DocumentID != result.DocumentID {
		t.Fatalf("general attachment polluted versioned arXiv sync: %#v, %v", items, err)
	}
	if err = svc.db.Take(&generic, "id=?", generic.ID).Error; err != nil {
		t.Fatal("general attachment was removed: ", err)
	}
}

func TestArxivImportHandlersRejectMissingUserInvalidBodyAndForeignDataset(t *testing.T) {
	arxivImportTestService(t)
	cases := []struct {
		name, method, path, body, user string
		handler                        http.HandlerFunc
		status                         int
	}{
		{"user required", "POST", "/academic/arxiv:lookup", `{"input":"2404.06654"}`, "", LookupArxiv, http.StatusBadRequest},
		{"invalid JSON", "POST", "/academic/arxiv:import", `{"input":"2404.06654","title":"forged"}`, "user", ImportArxiv, http.StatusBadRequest},
		{"unknown dataset", "POST", "/academic/arxiv:import", `{"input":"2404.06654","target_dataset_id":"unknown"}`, "user", ImportArxiv, http.StatusForbidden},
		{"library denied", "GET", "/academic/arxiv/library?target_dataset_id=unknown", "", "user", ListArxivLibrary, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("X-User-Id", tc.user)
			rec := httptest.NewRecorder()
			tc.handler(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestArxivImportLookupRejectsInvalidIdentifierAsBadRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/academic/arxiv:lookup", strings.NewReader(`{"input":"https://example.com/not-arxiv"}`))
	req.Header.Set("X-User-Id", "user")
	rec := httptest.NewRecorder()
	LookupArxiv(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ARXIV_INVALID_INPUT") {
		t.Fatalf("bad identifier response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestArxivImportRetriesFailedAssociationWithoutDuplicateFile(t *testing.T) {
	svc, count := arxivImportTestService(t)
	if err := svc.db.Callback().Create().Before("gorm:create").Register("fail_arxiv_link", func(tx *gorm.DB) {
		if tx.Statement.Table == "academic_work_documents" {
			tx.AddError(errors.New("link database unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	failed, err := svc.importPaper(context.Background(), "2404.06654", "dataset", "user", "User")
	if err == nil || !strings.Contains(err.Error(), "link database unavailable") || failed.DocumentID == "" {
		t.Fatalf("link failure hidden: %#v %v", failed, err)
	}
	if err = svc.db.Callback().Create().Remove("fail_arxiv_link"); err != nil {
		t.Fatal(err)
	}
	recovered, err := svc.importPaper(context.Background(), "2404.06654", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.DocumentID != failed.DocumentID || recovered.Status != "already_present" || count.Load() != 1 {
		t.Fatalf("retry duplicated file: %#v downloads=%d", recovered, count.Load())
	}
	items, err := svc.library(context.Background(), "dataset")
	if err != nil || len(items) != 1 {
		t.Fatalf("missing recovered association: %#v %v", items, err)
	}
}

func TestArxivImportRecoveryAfterMarkerFailureRetainsHashAndTask(t *testing.T) {
	svc, count := arxivImportTestService(t)
	if err := svc.db.Callback().Update().Before("gorm:update").Register("fail_arxiv_marker", func(tx *gorm.DB) {
		if tx.Statement.Table == "documents" {
			tx.AddError(errors.New("marker unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	failed, err := svc.importPaper(context.Background(), "2404.06654", "dataset", "user", "User")
	if err == nil || failed.DocumentID == "" {
		t.Fatalf("marker failure hidden: %#v %v", failed, err)
	}
	if err = svc.db.Callback().Update().Remove("fail_arxiv_marker"); err != nil {
		t.Fatal(err)
	}
	recovered, err := svc.importPaper(context.Background(), "2404.06654", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.DocumentID != failed.DocumentID || recovered.DocumentTaskID == "" || len(recovered.ContentSHA256) != 64 || count.Load() != 1 {
		t.Fatalf("retry lost attachment identity: %#v downloads=%d", recovered, count.Load())
	}
}

func TestArxivImportPendingProcessingIsNeverReportedReady(t *testing.T) {
	svc, _ := arxivImportTestService(t)
	save := svc.importFile
	svc.importFile = func(ctx context.Context, input doc.LocalFileImportRequest) (doc.LocalFileImportResult, error) {
		result, err := save(ctx, input)
		if err == nil {
			err = svc.db.Model(&orm.Task{}).Where("id = ?", result.TaskID).Update("ext", json.RawMessage(`{"task_state":"RUNNING"}`)).Error
		}
		return result, err
	}
	imported, err := svc.importPaper(context.Background(), "2404.06654", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	items, err := svc.library(context.Background(), "dataset")
	if err != nil {
		t.Fatal(err)
	}
	if imported.ProcessingStatus != "parsing" || len(items) != 1 || items[0].ProcessingStatus != "parsing" {
		t.Fatalf("running parser reported ready: %#v %#v", imported, items)
	}
}

func TestArxivImportRejectedStartResultIsAnErrorEvenWithoutGoError(t *testing.T) {
	svc, _ := arxivImportTestService(t)
	save := svc.importFile
	svc.importFile = func(ctx context.Context, input doc.LocalFileImportRequest) (doc.LocalFileImportResult, error) {
		result, err := save(ctx, input)
		result.Start = doc.StartTaskResult{Status: "FAILED", SubmitStatus: "REJECTED", Message: "store document task failed"}
		return result, err
	}
	result, err := svc.importPaper(context.Background(), "2404.06654", "dataset", "user", "User")
	if err == nil || !strings.Contains(err.Error(), "store document task failed") || result.ProcessingStatus != "failed" {
		t.Fatalf("rejected task falsely succeeded: %#v %v", result, err)
	}
}

func TestArxivImportDifferentBaseNeverBorrowsOtherArxivAttachmentParent(t *testing.T) {
	svc, _ := arxivImportTestService(t)
	first, err := svc.importPaper(context.Background(), "2404.06654", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	original := svc.lookup
	svc.lookup = func(ctx context.Context, input string) (arxivsource.Metadata, error) {
		paper, err := original(ctx, input)
		paper.BaseID = "2404.06655"
		return paper, err
	}
	other, err := svc.importPaper(context.Background(), "2404.06655", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	if first.WorkID == other.WorkID || other.Paper.BaseID != "2404.06655" || other.Paper.DOI != "10.1234/test" {
		t.Fatalf("same DOI corrupted arxiv identity: %#v %#v", first, other)
	}
	items, err := svc.library(context.Background(), "dataset")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("lost distinct arxiv work: %#v", items)
	}
}

func TestArxivImportUsesRealDocumentBridgeAndRecoversFromServerLibrary(t *testing.T) {
	svc, count := arxivImportTestService(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	if err := svc.db.AutoMigrate(&orm.UploadedFile{}); err != nil {
		t.Fatal(err)
	}
	svc.importFile = doc.ImportLocalFile
	result, err := svc.importPaper(context.Background(), "2404.06654v2", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	if result.DocumentID == "" || result.DocumentTaskID == "" || len(result.ContentSHA256) != 64 || result.ProcessingStatus != "ready" {
		t.Fatalf("bridge did not persist real attachment: %#v", result)
	}
	items, err := newArxivImportService(svc.db).library(context.Background(), "dataset")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].DocumentID != result.DocumentID || items[0].Size != result.Size || items[0].ContentSHA256 != result.ContentSHA256 {
		t.Fatalf("server restart recovery failed: %#v", items)
	}
	again, err := svc.importPaper(context.Background(), "2404.06654v2", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	if again.DocumentID != result.DocumentID || count.Load() != 1 {
		t.Fatalf("real bridge repeated download: %#v", again)
	}
}

func TestArxivImportLibraryUsesAuthoritativeParserSuccess(t *testing.T) {
	t.Setenv("LAZYMIND_READONLY_SCHEMA", "")
	svc, _ := arxivImportTestService(t)
	result, err := svc.importPaper(context.Background(), "2404.06654v2", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.db.AutoMigrate(&readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
		t.Fatal(err)
	}
	if err = svc.db.Model(&orm.Dataset{}).Where("id = ?", "dataset").Update("processing_level", "indexed").Error; err != nil {
		t.Fatal(err)
	}
	if err = svc.db.Model(&orm.Document{}).Where("id = ?", result.DocumentID).Update("lazyllm_doc_id", "lazy-doc").Error; err != nil {
		t.Fatal(err)
	}
	if err = svc.db.Model(&orm.Task{}).Where("id = ?", result.DocumentTaskID).Updates(map[string]any{"lazyllm_task_id": "lazy-task", "ext": json.RawMessage(`{"task_state":"RUNNING"}`)}).Error; err != nil {
		t.Fatal(err)
	}
	row := readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "lazy-task", DocID: "lazy-doc", KbID: "dataset", Status: "SUCCEEDED", UpdatedAt: time.Now().UTC()}
	if err = svc.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	items, err := svc.library(context.Background(), "dataset")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ProcessingStatus != "ready" {
		t.Fatalf("schema-B success was ignored: %#v", items)
	}
}

func TestArxivImportLibraryReportsAuthoritativeParserFailureAndRejectsFalseRetrySuccess(t *testing.T) {
	t.Setenv("LAZYMIND_READONLY_SCHEMA", "")
	svc, count := arxivImportTestService(t)
	result, err := svc.importPaper(context.Background(), "2404.06654v2", "dataset", "user", "User")
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.db.AutoMigrate(&readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
		t.Fatal(err)
	}
	if err = svc.db.Model(&orm.Dataset{}).Where("id = ?", "dataset").Update("processing_level", "indexed").Error; err != nil {
		t.Fatal(err)
	}
	if err = svc.db.Model(&orm.Document{}).Where("id = ?", result.DocumentID).Update("lazyllm_doc_id", "lazy-doc").Error; err != nil {
		t.Fatal(err)
	}
	if err = svc.db.Model(&orm.Task{}).Where("id = ?", result.DocumentTaskID).Updates(map[string]any{"lazyllm_task_id": "lazy-task", "ext": json.RawMessage(`{"task_state":"RUNNING"}`)}).Error; err != nil {
		t.Fatal(err)
	}
	failure := "parser rejected corrupted PDF bytes"
	row := readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "lazy-task", DocID: "lazy-doc", KbID: "dataset", Status: "FAILED", ErrorMsg: &failure, UpdatedAt: time.Now().UTC()}
	if err = svc.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	items, err := svc.library(context.Background(), "dataset")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ProcessingStatus != "failed" || items[0].ProcessingError != failure {
		t.Fatalf("schema-B failure was hidden: %#v", items)
	}
	_, err = svc.importPaper(context.Background(), "2404.06654v2", "dataset", "user", "User")
	if err == nil || !strings.Contains(err.Error(), failure) || count.Load() != 1 {
		t.Fatalf("failed attachment falsely passed retry: %v downloads=%d", err, count.Load())
	}
}

func TestArxivImportLibraryDistinguishesParserAcknowledgementsFromErrors(t *testing.T) {
	for _, tt := range []struct{ name, state, code, message, want string }{
		{"real success callback", "SUCCESS", "200", "success", "ready"},
		{"working acknowledgement", "WORKING", "200", "success", "parsing"},
		{"real failure", "FAILED", "KeyError", "No source is configured for dynamic embedding source.", "failed"},
		{"success carrying actual error", "SUCCESS", "KeyError", "No source is configured for dynamic embedding source.", "failed"},
		{"success carrying error code only", "SUCCESS", "KeyError", "", "failed"},
		{"success carrying nonempty exception", "SUCCESS", "200", "upstream parse exception", "failed"},
		{"failed state with acknowledgement text", "FAILED", "200", "success", "failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LAZYMIND_READONLY_SCHEMA", "")
			svc, _ := arxivImportTestService(t)
			result, err := svc.importPaper(context.Background(), "2404.06654v2", "dataset", "user", "User")
			if err != nil {
				t.Fatal(err)
			}
			if err = svc.db.AutoMigrate(&readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
				t.Fatal(err)
			}
			if err = svc.db.Model(&orm.Document{}).Where("id = ?", result.DocumentID).Update("lazyllm_doc_id", "lazy-doc").Error; err != nil {
				t.Fatal(err)
			}
			if err = svc.db.Model(&orm.Task{}).Where("id = ?", result.DocumentTaskID).Updates(map[string]any{"lazyllm_task_id": "lazy-task", "ext": json.RawMessage(`{"task_state":"RUNNING"}`)}).Error; err != nil {
				t.Fatal(err)
			}
			row := readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "lazy-task", DocID: "lazy-doc", KbID: "dataset", Status: tt.state, ErrorCode: &tt.code, ErrorMsg: &tt.message, UpdatedAt: time.Now().UTC()}
			if err = svc.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			items, err := svc.library(context.Background(), "dataset")
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].ProcessingStatus != tt.want {
				t.Fatalf("parser acknowledgement/error misclassified: %#v", items)
			}
			if tt.want == "failed" && (items[0].ProcessingError == "" || items[0].ProcessingError == "success") {
				t.Fatalf("actual failure lacks meaningful message: %#v", items[0])
			}
			if tt.want != "failed" && items[0].ProcessingError != "" {
				t.Fatalf("success acknowledgement presented as failure: %#v", items[0])
			}
		})
	}
}
