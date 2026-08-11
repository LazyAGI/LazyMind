package integration_test

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
	"unicode/utf8"

	"gorm.io/gorm"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/compat/contract"
	adaptercore "lazymind/core/compat/internal/adapters/core"
	compatknowledge "lazymind/core/compat/knowledge"
	compatruntime "lazymind/core/compat/runtime"
	"lazymind/core/doc"
)

func TestKnowledgeRuntimeWithRealPostgreSQLDocumentGet(t *testing.T) {
	if strings.TrimSpace(os.Getenv("COMPAT_INTEGRATION")) != "1" ||
		strings.TrimSpace(os.Getenv("COMPAT_KNOWLEDGE_DOCUMENT_INTEGRATION")) != "1" {
		t.Skip("set COMPAT_INTEGRATION=1 and COMPAT_KNOWLEDGE_DOCUMENT_INTEGRATION=1 to run knowledge document integration tests")
	}

	coreDriver, coreDSN := dbConfigFromCoreEnv(t)
	if coreDriver != orm.DriverPostgres {
		t.Fatalf("Knowledge document integration requires ACL_DB_DRIVER=%q, got %q", orm.DriverPostgres, coreDriver)
	}
	lazyDriver, lazyDSN := readonlyDBConfigFromCoreEnv(t)
	if lazyDriver != orm.DriverPostgres {
		t.Fatalf("Knowledge document integration requires readonly driver=%q, got %q", orm.DriverPostgres, lazyDriver)
	}
	if coreDriver == lazyDriver && coreDSN == lazyDSN {
		t.Fatal("Knowledge document integration requires explicit separated core and lazy DB configuration")
	}

	coreDB, err := orm.Connect(coreDriver, coreDSN)
	if err != nil {
		t.Fatalf("connect core db: %v", err)
	}
	coreSQL, err := coreDB.DB.DB()
	if err != nil {
		t.Fatalf("get core sql db: %v", err)
	}
	t.Cleanup(func() { _ = coreSQL.Close() })
	lazyDB, err := orm.Connect(lazyDriver, lazyDSN)
	if err != nil {
		t.Fatalf("connect lazy db: %v", err)
	}
	lazySQL, err := lazyDB.DB.DB()
	if err != nil {
		t.Fatalf("get lazy sql db: %v", err)
	}
	t.Cleanup(func() { _ = lazySQL.Close() })
	if coreSQL == lazySQL {
		t.Fatal("core and lazy sql.DB handles must be different")
	}

	acl.InitStore(coreDB)
	coreTx := coreDB.Begin()
	if coreTx.Error != nil {
		t.Fatalf("begin core tx: %v", coreTx.Error)
	}
	t.Cleanup(func() { _ = coreTx.Rollback().Error })
	lazyTx := lazyDB.Begin()
	if lazyTx.Error != nil {
		t.Fatalf("begin lazy tx: %v", lazyTx.Error)
	}
	t.Cleanup(func() { _ = lazyTx.Rollback().Error })

	scanServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/scan/internal/source-access/by-dataset:batch" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(scanServer.Close)
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", scanServer.URL)

	seed := fmt.Sprintf("compat_doc_it_%d", time.Now().UnixNano())
	userID := seed + "_user"
	otherUserID := seed + "_other_user"
	datasetID := seed + "_ds"
	kbID := seed + "_kb"
	otherDatasetID := seed + "_other_ds"
	otherKBID := seed + "_other_kb"
	documentID := seed + "_doc"
	otherDocumentID := seed + "_other_doc"
	largeDocumentID := seed + "_large_doc"
	binaryDocumentID := seed + "_binary_doc"
	lazyDocID := seed + "_lazy_doc"
	lazyLargeDocID := seed + "_lazy_large_doc"
	lazyBinaryDocID := seed + "_lazy_binary_doc"
	now := time.Now().UTC().Truncate(time.Microsecond)

	uploadRoot := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", uploadRoot)
	textPath := filepath.Join(uploadRoot, "document.txt")
	if err := os.WriteFile(textPath, []byte("hello knowledge document"), 0o600); err != nil {
		t.Fatalf("write text file: %v", err)
	}
	largePath := filepath.Join(uploadRoot, "large.txt")
	largePayload := append([]byte(strings.Repeat("a", int(1024*1024)-1)), []byte("你b")...)
	if err := os.WriteFile(largePath, largePayload, 0o600); err != nil {
		t.Fatalf("write large file: %v", err)
	}
	binaryPath := filepath.Join(uploadRoot, "sample.pdf")
	if err := os.WriteFile(binaryPath, []byte("%PDF-1.4\n%\xff\xff\n"), 0o600); err != nil {
		t.Fatalf("write binary file: %v", err)
	}

	insertDataset(t, coreTx, datasetID, kbID, userID, now)
	insertDataset(t, coreTx, otherDatasetID, otherKBID, userID, now)
	insertDocument(t, coreTx, datasetID, documentID, lazyDocID, userID, "core display should lose to lazy", textPath, "text/plain; charset=utf-8", now)
	insertDocument(t, coreTx, otherDatasetID, otherDocumentID, seed+"_other_lazy", userID, "other doc", textPath, "text/plain; charset=utf-8", now)
	insertDocument(t, coreTx, datasetID, largeDocumentID, lazyLargeDocID, userID, "large doc", largePath, "text/plain; charset=utf-8", now)
	insertDocument(t, coreTx, datasetID, binaryDocumentID, lazyBinaryDocID, userID, "binary doc", binaryPath, "application/pdf", now)
	insertLazyDocument(t, lazyTx, lazyDocID, "lazy-title.md", "FILE_SYSTEM", "READY_FROM_LAZY", 321, now)
	insertLazyDocument(t, lazyTx, lazyLargeDocID, "large-title.txt", "LOCAL_FILE", "READY", len(largePayload), now)
	insertLazyDocument(t, lazyTx, lazyBinaryDocID, "binary-title.pdf", "LOCAL_FILE", "READY", 13, now)

	service, err := doc.NewDocumentService(doc.DocumentServiceDeps{DB: coreTx, LazyDB: lazyTx})
	if err != nil {
		t.Fatalf("NewDocumentService: %v", err)
	}
	adapter, err := adaptercore.NewKnowledgeDocumentAdapter(service)
	if err != nil {
		t.Fatalf("NewKnowledgeDocumentAdapter: %v", err)
	}
	rt, err := compatruntime.New(compatruntime.Dependencies{KnowledgeDocument: adapter})
	if err != nil {
		t.Fatalf("Runtime.New: %v", err)
	}
	if rt.Knowledge == nil {
		t.Fatal("Runtime.Knowledge is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	callCtx := contract.CallContext{UserID: userID}

	t.Run("metadata dual db", func(t *testing.T) {
		got, err := rt.Knowledge.GetDocument(ctx, callCtx, compatknowledge.GetDocumentInput{
			KnowledgeID: datasetID,
			DocumentID:  documentID,
		})
		if err != nil {
			t.Fatalf("GetDocument metadata: %v", err)
		}
		docDetail := got.Document
		if docDetail.ID != documentID || docDetail.KnowledgeID != datasetID {
			t.Fatalf("identity = %#v, want core document/dataset IDs", docDetail)
		}
		if docDetail.Name != "lazy-title.md" || docDetail.Source != "FILE_SYSTEM" || docDetail.SizeBytes != 321 || docDetail.ParseStatus != "READY_FROM_LAZY" {
			t.Fatalf("readonly metadata not mapped from lazy DB: %#v", docDetail)
		}
		if docDetail.Content != nil || docDetail.ChunksPage != nil || len(docDetail.Chunks) != 0 {
			t.Fatalf("metadata-only should not include content/chunks: %#v", docDetail)
		}
		assertNoInternalDocumentFields(t, docDetail, uploadRoot, lazyDocID)
	})

	t.Run("permission and ownership", func(t *testing.T) {
		result, err := rt.Knowledge.GetDocument(ctx, contract.CallContext{UserID: otherUserID}, compatknowledge.GetDocumentInput{KnowledgeID: datasetID, DocumentID: documentID})
		assertCompatNotFound(t, result, err)
		result, err = rt.Knowledge.GetDocument(ctx, callCtx, compatknowledge.GetDocumentInput{KnowledgeID: datasetID, DocumentID: otherDocumentID})
		assertCompatNotFound(t, result, err)
		result, err = rt.Knowledge.GetDocument(ctx, callCtx, compatknowledge.GetDocumentInput{KnowledgeID: datasetID, DocumentID: seed + "_missing_doc"})
		assertCompatNotFound(t, result, err)
		result, err = rt.Knowledge.GetDocument(ctx, callCtx, compatknowledge.GetDocumentInput{KnowledgeID: datasetID, DocumentID: lazyDocID})
		assertCompatNotFound(t, result, err)
	})

	t.Run("text content", func(t *testing.T) {
		got, err := rt.Knowledge.GetDocument(ctx, callCtx, compatknowledge.GetDocumentInput{
			KnowledgeID:    datasetID,
			DocumentID:     documentID,
			IncludeContent: true,
		})
		if err != nil {
			t.Fatalf("GetDocument content: %v", err)
		}
		if got.Document.Content == nil || got.Document.Content.Text != "hello knowledge document" || got.Document.Content.MIMEType != "text/plain; charset=utf-8" || got.Document.Content.Truncated {
			t.Fatalf("unexpected text content: %#v", got.Document.Content)
		}
		assertNoInternalDocumentFields(t, got.Document, uploadRoot, lazyDocID)
	})

	t.Run("large utf8 content", func(t *testing.T) {
		got, err := rt.Knowledge.GetDocument(ctx, callCtx, compatknowledge.GetDocumentInput{
			KnowledgeID:    datasetID,
			DocumentID:     largeDocumentID,
			IncludeContent: true,
		})
		if err != nil {
			t.Fatalf("GetDocument large content: %v", err)
		}
		if got.Document.Content == nil || !got.Document.Content.Truncated || !utf8.ValidString(got.Document.Content.Text) || len([]byte(got.Document.Content.Text)) > 1024*1024 {
			t.Fatalf("unexpected large content: len=%d content=%#v", len([]byte(got.Document.Content.Text)), got.Document.Content)
		}
	})

	t.Run("binary content", func(t *testing.T) {
		got, err := rt.Knowledge.GetDocument(ctx, callCtx, compatknowledge.GetDocumentInput{
			KnowledgeID:    datasetID,
			DocumentID:     binaryDocumentID,
			IncludeContent: true,
		})
		if err != nil {
			t.Fatalf("GetDocument binary content: %v", err)
		}
		if got.Document.Content == nil || got.Document.Content.MIMEType != "application/pdf" || got.Document.Content.Text != "" || got.Document.Content.Truncated {
			t.Fatalf("unexpected binary content: %#v", got.Document.Content)
		}
		if got.Document.OriginalFile == nil || strings.TrimSpace(got.Document.OriginalFile.FileName) == "" ||
			!strings.HasPrefix(got.Document.OriginalFile.DownloadURL, "/datasets/"+datasetID+"/documents/"+binaryDocumentID+":download") {
			t.Fatalf("unexpected original file ref: %#v", got.Document.OriginalFile)
		}
		assertNoInternalDocumentFields(t, got.Document, uploadRoot, lazyBinaryDocID)
	})

	t.Run("chunks httptest", func(t *testing.T) {
		var gotKBID, gotDocID string
		chunkServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/chunks" {
				http.NotFound(w, r)
				return
			}
			gotKBID = r.URL.Query().Get("kb_id")
			gotDocID = r.URL.Query().Get("doc_id")
			if r.URL.Query().Get("page_size") != "1" || r.URL.Query().Get("page") != "1" {
				t.Errorf("unexpected chunk query: %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"chunk_id":"chunk-1","content":"chunk text","number":1}],"total":2}`))
		}))
		defer chunkServer.Close()
		t.Setenv("LAZYMIND_ALGO_SERVICE_URL", chunkServer.URL)

		got, err := rt.Knowledge.GetDocument(ctx, callCtx, compatknowledge.GetDocumentInput{
			KnowledgeID:    datasetID,
			DocumentID:     documentID,
			IncludeChunks:  true,
			ChunksPage:     contract.PageRequest{PageSize: 1},
			IncludeContent: false,
		})
		if err != nil {
			t.Fatalf("GetDocument chunks: %v", err)
		}
		if gotKBID != kbID || gotDocID != lazyDocID {
			t.Fatalf("chunk backend got kb_id=%q doc_id=%q, want %q/lazy doc", gotKBID, gotDocID, kbID)
		}
		if len(got.Document.Chunks) != 1 || got.Document.Chunks[0].ID != "chunk-1" || got.Document.Chunks[0].Text != "chunk text" ||
			got.Document.ChunksPage == nil || got.Document.ChunksPage.NextPageToken == "" || got.Document.ChunksPage.Total == nil || *got.Document.ChunksPage.Total != 2 {
			t.Fatalf("unexpected chunks result: chunks=%#v page=%#v", got.Document.Chunks, got.Document.ChunksPage)
		}
		assertNoInternalDocumentFields(t, got.Document, uploadRoot, lazyDocID)
	})

	t.Run("chunks unavailable", func(t *testing.T) {
		badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "down", http.StatusBadGateway)
		}))
		defer badServer.Close()
		t.Setenv("LAZYMIND_ALGO_SERVICE_URL", badServer.URL)
		t.Setenv("LAZYMIND_PARSING_SERVICE_URL", badServer.URL)
		_, err := rt.Knowledge.GetDocument(ctx, callCtx, compatknowledge.GetDocumentInput{
			KnowledgeID:   datasetID,
			DocumentID:    documentID,
			IncludeChunks: true,
		})
		if code, ok := contract.CodeOf(err); !ok || code != contract.BackendUnavailable {
			t.Fatalf("chunk unavailable code=%v ok=%v err=%v, want BACKEND_UNAVAILABLE", code, ok, err)
		}
	})

	t.Logf("Knowledge Document integration core_driver=%s lazy_driver=%s dataset=%s document=%s", coreDriver, lazyDriver, datasetID, documentID)
}

func readonlyDBConfigFromCoreEnv(t *testing.T) (string, string) {
	t.Helper()
	driver := strings.TrimSpace(os.Getenv("LAZYMIND_READONLY_DB_DRIVER"))
	dsn := strings.TrimSpace(os.Getenv("LAZYMIND_READONLY_DB_DSN"))
	if driver == "" {
		driver = strings.TrimSpace(os.Getenv("LAZYMIND_LAZYLLM_DB_DRIVER"))
	}
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("LAZYMIND_LAZYLLM_DB_DSN"))
	}
	if driver == "" {
		t.Fatal("LAZYMIND_READONLY_DB_DRIVER or LAZYMIND_LAZYLLM_DB_DRIVER is required")
	}
	if dsn == "" {
		t.Fatal("LAZYMIND_READONLY_DB_DSN or LAZYMIND_LAZYLLM_DB_DSN is required")
	}
	return driver, dsn
}

func insertDataset(t *testing.T, db *gorm.DB, datasetID, kbID, userID string, now time.Time) {
	t.Helper()
	if err := db.Create(&orm.Dataset{
		ID:           datasetID,
		KbID:         kbID,
		DisplayName:  "Dataset " + datasetID,
		Desc:         "compat document integration",
		DatasetState: 0,
		ShareType:    0,
		Type:         1,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID + " name",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("insert dataset %s: %v", datasetID, err)
	}
}

func insertDocument(t *testing.T, db *gorm.DB, datasetID, documentID, lazyDocID, userID, displayName, storedPath, contentType string, now time.Time) {
	t.Helper()
	ext, err := json.Marshal(map[string]any{
		"stored_path":       storedPath,
		"stored_name":       filepath.Base(storedPath),
		"original_filename": filepath.Base(storedPath),
		"file_size":         fileSize(t, storedPath),
		"content_type":      contentType,
	})
	if err != nil {
		t.Fatalf("marshal document ext: %v", err)
	}
	if err := db.Create(&orm.Document{
		ID:           documentID,
		LazyllmDocID: lazyDocID,
		DatasetID:    datasetID,
		DisplayName:  displayName,
		Tags:         []byte(`["integration"]`),
		Ext:          ext,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID + " name",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("insert document %s: %v", documentID, err)
	}
}

func insertLazyDocument(t *testing.T, db *gorm.DB, lazyDocID, filename, sourceType, uploadStatus string, size int, now time.Time) {
	t.Helper()
	if err := db.Table((readonlyorm.LazyLLMDocRow{}).TableName()).Create(&readonlyorm.LazyLLMDocRow{
		DocID:        lazyDocID,
		Filename:     filename,
		Path:         "/readonly/" + filename,
		UploadStatus: uploadStatus,
		SourceType:   sourceType,
		SizeBytes:    &size,
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("insert lazy document %s: %v", lazyDocID, err)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

func assertCompatNotFound(t *testing.T, result compatknowledge.GetDocumentResult, err error) {
	t.Helper()
	if code, ok := contract.CodeOf(err); !ok || code != contract.NotFound {
		t.Fatalf("result=%#v code=%v ok=%v err=%v, want NOT_FOUND", result, code, ok, err)
	}
}

func assertNoInternalDocumentFields(t *testing.T, detail compatknowledge.DocumentDetail, uploadRoot, lazyDocID string) {
	t.Helper()
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal document detail: %v", err)
	}
	text := string(raw)
	for _, forbidden := range []string{
		uploadRoot,
		lazyDocID,
		"lazyllm_doc_id",
		"FileSystemPath",
		"file_system_path",
		"StoredPath",
		"stored_path",
		"ParseStoredPath",
		"parse_stored_path",
		"SourceStoredPath",
		"source_stored_path",
	} {
		if strings.TrimSpace(forbidden) != "" && strings.Contains(text, forbidden) {
			t.Fatalf("document detail leaks forbidden value %q: %s", forbidden, text)
		}
	}
}
