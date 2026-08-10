package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
)

func TestDocumentServiceGetMetadataMapsFields(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, nil)
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	size := 42
	if err := db.Create(&readonlyorm.LazyLLMDocRow{
		DocID:        "lazy-doc-1",
		Filename:     "lazy-name.md",
		Path:         "/tmp/lazy-name.md",
		UploadStatus: "READY",
		SourceType:   "FILE_SYSTEM",
		SizeBytes:    &size,
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("create readonly doc: %v", err)
	}
	docExt := documentExt{
		StoredPath:       filepath.Join(t.TempDir(), "stored.md"),
		StoredName:       "stored.md",
		OriginalFilename: "original.md",
		FileSize:         100,
		ContentType:      "text/markdown; charset=utf-8",
		ConvertStatus:    ConvertStatusSucceeded,
	}
	if err := db.Create(&orm.Document{
		ID:           "doc-1",
		LazyllmDocID: "lazy-doc-1",
		DatasetID:    "dataset-1",
		DisplayName:  "Display Name",
		Tags:         []byte(`["alpha","beta"]`),
		Ext:          mustJSON(docExt),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-1",
			CreateUserName: "Alice",
			CreatedAt:      now,
			UpdatedAt:      now.Add(time.Minute),
		},
	}).Error; err != nil {
		t.Fatalf("create document: %v", err)
	}

	service := mustDocumentService(t, db)
	got, err := service.GetDocumentMetadata(context.Background(), DocumentGetRequest{
		UserID:     "user-1",
		DatasetID:  "dataset-1",
		DocumentID: "doc-1",
	})
	if err != nil {
		t.Fatalf("GetDocumentMetadata: %v", err)
	}
	if got.ID != "doc-1" || got.DatasetID != "dataset-1" || got.Name != "Display Name" {
		t.Fatalf("unexpected identity fields: %+v", got)
	}
	if got.Source != "FILE_SYSTEM" || got.ParseStatus != ConvertStatusSucceeded || got.MIMEType != "text/markdown; charset=utf-8" {
		t.Fatalf("unexpected source/status/mime: %+v", got)
	}
	if got.SizeBytes != 100 || got.CreatedBy != "Alice" || !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("unexpected metadata fields: %+v", got)
	}
	if strings.Join(got.Tags, ",") != "alpha,beta" {
		t.Fatalf("unexpected tags: %#v", got.Tags)
	}
	if got.OriginalFile == nil || got.OriginalFile.FileName != "original.md" || got.OriginalFile.DownloadURL != "/datasets/dataset-1/documents/doc-1:download" {
		t.Fatalf("unexpected original file ref: %#v", got.OriginalFile)
	}
}

func TestDocumentServiceGetMetadataChecksPermissionAndBelongsToDataset(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, nil)
	now := time.Date(2026, 7, 29, 11, 0, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "dataset-owned", "user-1", now)
	seedDocumentServiceDataset(t, db, "dataset-other", "user-2", now)
	seedDocumentServiceDocument(t, db, "dataset-owned", "doc-owned", "user-1", documentExt{ContentType: "text/plain"})
	seedDocumentServiceDocument(t, db, "dataset-other", "doc-other", "user-2", documentExt{ContentType: "text/plain"})
	service := mustDocumentService(t, db)

	if _, err := service.GetDocumentMetadata(context.Background(), DocumentGetRequest{UserID: "user-1", DatasetID: "dataset-other", DocumentID: "doc-other"}); documentServiceCode(err) != DocumentServiceForbidden {
		t.Fatalf("other user error = %v, want forbidden", err)
	}
	if _, err := service.GetDocumentMetadata(context.Background(), DocumentGetRequest{UserID: "user-1", DatasetID: "dataset-owned", DocumentID: "doc-other"}); documentServiceCode(err) != DocumentServiceNotFound {
		t.Fatalf("wrong dataset error = %v, want not found", err)
	}
	if _, err := service.GetDocumentMetadata(context.Background(), DocumentGetRequest{UserID: "", DatasetID: "dataset-owned", DocumentID: "doc-owned"}); documentServiceCode(err) != DocumentServiceInvalidArgument {
		t.Fatalf("missing user error = %v, want invalid argument", err)
	}
}

func TestDocumentServiceGetMetadataValidationNotFoundAndScanDeny(t *testing.T) {
	db := newDocumentTestDB(t)
	now := time.Date(2026, 7, 29, 11, 30, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	seedDocumentServiceDocument(t, db, "dataset-1", "doc-1", "user-1", documentExt{ContentType: "text/plain"})
	seedDocumentServiceDocument(t, db, "dataset-1", "doc-deleted", "user-1", documentExt{ContentType: "text/plain"})
	deletedAt := now.Add(time.Minute)
	if err := db.Model(&orm.Document{}).Where("id = ?", "doc-deleted").Update("deleted_at", deletedAt).Error; err != nil {
		t.Fatalf("mark deleted: %v", err)
	}
	if err := db.Model(&orm.Document{}).Where("id = ?", "doc-1").Update("lazyllm_doc_id", "lazy-doc-1").Error; err != nil {
		t.Fatalf("set lazy doc id: %v", err)
	}
	installDocumentServiceTransportWithScan(t, nil, `{"items":[]}`)
	service := mustDocumentService(t, db)

	cases := []struct {
		name string
		req  DocumentGetRequest
		code DocumentServiceErrorCode
	}{
		{name: "empty dataset", req: DocumentGetRequest{UserID: "user-1", DocumentID: "doc-1"}, code: DocumentServiceInvalidArgument},
		{name: "empty document", req: DocumentGetRequest{UserID: "user-1", DatasetID: "dataset-1"}, code: DocumentServiceInvalidArgument},
		{name: "missing dataset", req: DocumentGetRequest{UserID: "user-1", DatasetID: "dataset-missing", DocumentID: "doc-1"}, code: DocumentServiceNotFound},
		{name: "deleted document", req: DocumentGetRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-deleted"}, code: DocumentServiceNotFound},
		{name: "lazy id is not public id", req: DocumentGetRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "lazy-doc-1"}, code: DocumentServiceNotFound},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.GetDocumentMetadata(context.Background(), tt.req)
			if documentServiceCode(err) != tt.code {
				t.Fatalf("error = %v, want code %s", err, tt.code)
			}
		})
	}

	installDocumentServiceTransportWithScan(t, nil, `{"items":[{"dataset_id":"dataset-1","exists":true,"allowed":false}]}`)
	denyService := mustDocumentService(t, db)
	_, err := denyService.GetDocumentMetadata(context.Background(), DocumentGetRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-1"})
	if documentServiceCode(err) != DocumentServiceForbidden {
		t.Fatalf("scan deny error = %v, want forbidden", err)
	}
}

func TestDocumentServiceReadDocumentContentReturnsCappedTextAndSkipsBinary(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, nil)
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	textPath := filepath.Join(root, "doc.txt")
	text := strings.Repeat("a", int(documentContentMaxBytes)+10)
	if err := os.WriteFile(textPath, []byte(text), 0o600); err != nil {
		t.Fatalf("write text: %v", err)
	}
	seedDocumentServiceDocument(t, db, "dataset-1", "doc-text", "user-1", documentExt{
		StoredPath:       textPath,
		OriginalFilename: "doc.txt",
		FileSize:         int64(len(text)),
		ContentType:      "text/plain; charset=utf-8",
	})
	binaryPath := filepath.Join(root, "image.png")
	if err := os.WriteFile(binaryPath, []byte{0x89, 0x50, 0x4e, 0x47}, 0o600); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	seedDocumentServiceDocument(t, db, "dataset-1", "doc-image", "user-1", documentExt{
		StoredPath:       binaryPath,
		OriginalFilename: "image.png",
		FileSize:         4,
		ContentType:      "image/png",
	})
	service := mustDocumentService(t, db)

	content, err := service.ReadDocumentContent(context.Background(), DocumentContentRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-text"})
	if err != nil {
		t.Fatalf("ReadDocumentContent text: %v", err)
	}
	if len(content.Text) != int(documentContentMaxBytes) || !content.Truncated || content.MIMEType != "text/plain; charset=utf-8" {
		t.Fatalf("unexpected text content result: len=%d result=%+v", len(content.Text), content)
	}
	binary, err := service.ReadDocumentContent(context.Background(), DocumentContentRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-image"})
	if err != nil {
		t.Fatalf("ReadDocumentContent binary must not fail: %v", err)
	}
	if binary.Text != "" || binary.Truncated || binary.MIMEType != "image/png" {
		t.Fatalf("unexpected binary content result: %+v", binary)
	}
}

func TestDocumentServiceReadDocumentContentPathSafety(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, nil)
	root := t.TempDir()
	outsideRoot := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	now := time.Date(2026, 7, 29, 12, 30, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	insidePath := filepath.Join(root, "inside.txt")
	if err := os.WriteFile(insidePath, []byte("inside"), 0o600); err != nil {
		t.Fatalf("write inside: %v", err)
	}
	outsidePath := filepath.Join(outsideRoot, "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	linkInside := filepath.Join(root, "link-inside.txt")
	if err := os.Symlink(insidePath, linkInside); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	linkOutside := filepath.Join(root, "link-outside.txt")
	if err := os.Symlink(outsidePath, linkOutside); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	seedDocumentServiceDocument(t, db, "dataset-1", "doc-inside", "user-1", textDocumentExt(insidePath))
	seedDocumentServiceDocument(t, db, "dataset-1", "doc-outside", "user-1", textDocumentExt(outsidePath))
	seedDocumentServiceDocument(t, db, "dataset-1", "doc-link-inside", "user-1", textDocumentExt(linkInside))
	seedDocumentServiceDocument(t, db, "dataset-1", "doc-link-outside", "user-1", textDocumentExt(linkOutside))
	service := mustDocumentService(t, db)

	inside, err := service.ReadDocumentContent(context.Background(), DocumentContentRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-inside"})
	if err != nil || inside.Text != "inside" {
		t.Fatalf("inside file content=%+v err=%v", inside, err)
	}
	linkOK, err := service.ReadDocumentContent(context.Background(), DocumentContentRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-link-inside"})
	if err != nil || linkOK.Text != "inside" {
		t.Fatalf("inside symlink content=%+v err=%v", linkOK, err)
	}
	for _, docID := range []string{"doc-outside", "doc-link-outside"} {
		_, err := service.ReadDocumentContent(context.Background(), DocumentContentRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: docID})
		var svcErr *DocumentServiceError
		if !errors.As(err, &svcErr) || svcErr.Code != DocumentServiceForbidden {
			t.Fatalf("%s error = %v, want typed forbidden", docID, err)
		}
		if strings.Contains(svcErr.Error(), outsidePath) || strings.Contains(svcErr.Error(), outsideRoot) {
			t.Fatalf("error message leaks server path: %q", svcErr.Error())
		}
	}
}

func TestDocumentServiceReadDocumentContentMissingFilePreservesTypedCause(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, nil)
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	now := time.Date(2026, 7, 29, 12, 40, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	missingPath := filepath.Join(root, "missing.txt")
	seedDocumentServiceDocument(t, db, "dataset-1", "doc-missing", "user-1", textDocumentExt(missingPath))
	service := mustDocumentService(t, db)

	_, err := service.ReadDocumentContent(context.Background(), DocumentContentRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-missing"})
	var svcErr *DocumentServiceError
	if !errors.As(err, &svcErr) || svcErr.Code != DocumentServiceNotFound || svcErr.Unwrap() == nil || !os.IsNotExist(svcErr.Unwrap()) {
		t.Fatalf("missing file error = %#v, want typed not found with cause", err)
	}
	if strings.Contains(svcErr.Error(), missingPath) {
		t.Fatalf("error message leaks server path: %q", svcErr.Error())
	}
}

func TestDocumentServiceReadDocumentContentTruncatesAtValidUTF8Boundary(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, nil)
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	now := time.Date(2026, 7, 29, 12, 50, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	path := filepath.Join(root, "utf8.txt")
	payload := append([]byte(strings.Repeat("a", int(documentContentMaxBytes)-1)), []byte("你b")...)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write utf8: %v", err)
	}
	seedDocumentServiceDocument(t, db, "dataset-1", "doc-utf8", "user-1", textDocumentExt(path))
	service := mustDocumentService(t, db)

	content, err := service.ReadDocumentContent(context.Background(), DocumentContentRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-utf8"})
	if err != nil {
		t.Fatalf("ReadDocumentContent utf8: %v", err)
	}
	if !content.Truncated || len([]byte(content.Text)) > int(documentContentMaxBytes) || !utf8.ValidString(content.Text) {
		t.Fatalf("invalid utf8 truncation result: len=%d truncated=%t valid=%t", len([]byte(content.Text)), content.Truncated, utf8.ValidString(content.Text))
	}
}

func TestDocumentServiceGetMetadataDoesNotExposeLocalPath(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, nil)
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	now := time.Date(2026, 7, 29, 12, 55, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	storedPath := filepath.Join(root, "secret.txt")
	seedDocumentServiceDocument(t, db, "dataset-1", "doc-1", "user-1", textDocumentExt(storedPath))
	service := mustDocumentService(t, db)

	meta, err := service.GetDocumentMetadata(context.Background(), DocumentGetRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-1"})
	if err != nil {
		t.Fatalf("GetDocumentMetadata: %v", err)
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if strings.Contains(string(raw), storedPath) || strings.Contains(string(raw), root) {
		t.Fatalf("metadata leaks local path: %s", raw)
	}
}

func TestDocumentServiceListDocumentChunksMapsOnePage(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, func(r *http.Request) (int, string) {
		if r.URL.Host != "algo.test" || r.URL.Path != "/v1/chunks" {
			return http.StatusNotFound, `{"message":"not found"}`
		}
		if r.URL.Query().Get("kb_id") != "kb-dataset-1" {
			return http.StatusBadRequest, fmt.Sprintf(`{"query":%q}`, r.URL.RawQuery)
		}
		if r.URL.Query().Get("page_size") != "20" || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("doc_id") != "lazy-doc-1" {
			return http.StatusBadRequest, fmt.Sprintf(`{"query":%q}`, r.URL.RawQuery)
		}
		return http.StatusOK, `{"items":[{"chunk_id":"chunk-1","content":"hello","number":1}],"total":25}`
	})
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	now := time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	seedDocumentServiceDocument(t, db, "dataset-1", "doc-1", "user-1", documentExt{ContentType: "text/plain"})
	if err := db.Model(&orm.Document{}).Where("id = ?", "doc-1").Update("lazyllm_doc_id", "lazy-doc-1").Error; err != nil {
		t.Fatalf("set lazy doc id: %v", err)
	}
	service := mustDocumentService(t, db)

	result, err := service.ListDocumentChunks(context.Background(), DocumentChunksRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-1"})
	if err != nil {
		t.Fatalf("ListDocumentChunks: %v", err)
	}
	if result.TotalSize != 25 || result.NextPageToken == "" || len(result.Chunks) != 1 {
		t.Fatalf("unexpected chunk page: %+v", result)
	}
	if result.Chunks[0] != (DocumentChunk{ID: "chunk-1", Text: "hello", Number: 1}) {
		t.Fatalf("unexpected chunk mapping: %+v", result.Chunks[0])
	}
}

func TestDocumentServiceListDocumentChunksUsesDatasetKBIDAndResolvedChunkGroup(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v1/algo/algo-1/groups":
			return http.StatusOK, `{"code":200,"data":[{"type":"Line","name":"line_group"},{"type":"Block","name":"block_group"},{"type":"Chunk","name":"custom_chunk_group"}]}`
		case "/v1/chunks":
			if r.URL.Query().Get("kb_id") != "kb_backend_y" {
				return http.StatusBadRequest, fmt.Sprintf(`{"query":%q}`, r.URL.RawQuery)
			}
			if r.URL.Query().Get("group") != "custom_chunk_group" {
				return http.StatusBadRequest, fmt.Sprintf(`{"query":%q}`, r.URL.RawQuery)
			}
			if r.URL.Query().Get("algo_id") != "algo-1" {
				return http.StatusBadRequest, fmt.Sprintf(`{"query":%q}`, r.URL.RawQuery)
			}
			return http.StatusOK, `{"items":[{"chunk_id":"chunk-1","content":"hello","number":1}],"total":1}`
		default:
			return http.StatusNotFound, `{"message":"not found"}`
		}
	})
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "ds_core_x", "kb_backend_y", "algo-1", "user-1", now)
	seedDocumentServiceLazyDocument(t, db, "ds_core_x", "doc-1", "lazy-doc-1", "user-1")
	service := mustDocumentService(t, db)

	result, err := service.ListDocumentChunks(context.Background(), DocumentChunksRequest{UserID: "user-1", DatasetID: "ds_core_x", DocumentID: "doc-1"})
	if err != nil {
		t.Fatalf("ListDocumentChunks: %v", err)
	}
	if len(result.Chunks) != 1 || result.Chunks[0].ID != "chunk-1" {
		t.Fatalf("unexpected chunks: %+v", result.Chunks)
	}
}

func TestDocumentServiceGetDocumentLoadsRecordOnceForEveryExpansionCombination(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, func(r *http.Request) (int, string) {
		if r.URL.Path == "/v1/chunks" {
			return http.StatusOK, `{"items":[{"chunk_id":"chunk-1","content":"part","number":1}],"total":1}`
		}
		return http.StatusNotFound, `{"message":"not found"}`
	})
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	path := filepath.Join(root, "doc.txt")
	if err := os.WriteFile(path, []byte("document text"), 0o600); err != nil {
		t.Fatalf("write document: %v", err)
	}
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "ds-core", "user-1", now)
	seedDocumentServiceDocument(t, db, "ds-core", "doc-1", "user-1", documentExt{
		StoredPath:       path,
		OriginalFilename: "doc.txt",
		ContentType:      "text/plain",
	})
	if err := db.Model(&orm.Document{}).Where("id = ?", "doc-1").Update("lazyllm_doc_id", "lazy-doc-1").Error; err != nil {
		t.Fatalf("set lazy document id: %v", err)
	}
	service := mustDocumentService(t, db)
	var loadCount int
	service.loadRecordCounter = &loadCount

	cases := []struct {
		name           string
		includeContent bool
		includeChunks  bool
	}{
		{name: "metadata only"},
		{name: "content only", includeContent: true},
		{name: "chunks only", includeChunks: true},
		{name: "content and chunks", includeContent: true, includeChunks: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			loadCount = 0
			result, err := service.GetDocument(context.Background(), DocumentReadRequest{
				UserID:         "user-1",
				DatasetID:      "ds-core",
				DocumentID:     "doc-1",
				IncludeContent: tt.includeContent,
				IncludeChunks:  tt.includeChunks,
			})
			if err != nil {
				t.Fatalf("GetDocument: %v", err)
			}
			if loadCount != 1 {
				t.Fatalf("loadRecord calls = %d, want 1", loadCount)
			}
			if (result.Content != nil) != tt.includeContent || (result.Chunks != nil) != tt.includeChunks {
				t.Fatalf("optional result presence = content:%v chunks:%v, want content:%v chunks:%v", result.Content != nil, result.Chunks != nil, tt.includeContent, tt.includeChunks)
			}
		})
	}
}

func TestDocumentServiceListDocumentChunksSecondPageLastPageAndNoDuplicate(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, func(r *http.Request) (int, string) {
		if r.URL.Host != "algo.test" || r.URL.Path != "/v1/chunks" {
			return http.StatusNotFound, `{"message":"not found"}`
		}
		switch r.URL.Query().Get("page") {
		case "1":
			return http.StatusOK, `{"items":[{"chunk_id":"chunk-1","content":"first","number":1}],"total":2}`
		case "2":
			return http.StatusOK, `{"items":[{"chunk_id":"chunk-2","content":"second","number":2}],"total":2}`
		default:
			return http.StatusBadRequest, fmt.Sprintf(`{"query":%q}`, r.URL.RawQuery)
		}
	})
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	now := time.Date(2026, 7, 29, 13, 30, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	seedDocumentServiceLazyDocument(t, db, "dataset-1", "doc-1", "lazy-doc-1", "user-1")
	service := mustDocumentService(t, db)

	first, err := service.ListDocumentChunks(context.Background(), DocumentChunksRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-1", PageSize: 1})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	second, err := service.ListDocumentChunks(context.Background(), DocumentChunksRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-1", PageSize: 1, PageToken: first.NextPageToken})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if first.NextPageToken == "" || second.NextPageToken != "" {
		t.Fatalf("unexpected page tokens: first=%q second=%q", first.NextPageToken, second.NextPageToken)
	}
	if len(first.Chunks) != 1 || len(second.Chunks) != 1 || first.Chunks[0].ID == second.Chunks[0].ID || second.Chunks[0].ID != "chunk-2" {
		t.Fatalf("unexpected chunks first=%+v second=%+v", first.Chunks, second.Chunks)
	}
}

func TestDocumentServiceListDocumentChunksRejectsInvalidAndUnalignedToken(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, nil)
	now := time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	seedDocumentServiceLazyDocument(t, db, "dataset-1", "doc-1", "lazy-doc-1", "user-1")
	service := mustDocumentService(t, db)

	_, err := service.ListDocumentChunks(context.Background(), DocumentChunksRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-1", PageToken: "not-a-token"})
	if documentServiceCode(err) != DocumentServiceInvalidArgument {
		t.Fatalf("invalid token error = %v, want invalid argument", err)
	}
	_, err = service.ListDocumentChunks(context.Background(), DocumentChunksRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-1", PageSize: 20, PageToken: "1"})
	if documentServiceCode(err) != DocumentServiceInvalidArgument {
		t.Fatalf("unaligned token error = %v, want invalid argument", err)
	}
}

func TestDocumentServiceListDocumentChunksFallbacksOnBusinessFailure(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, func(r *http.Request) (int, string) {
		switch r.URL.Host {
		case "algo.test":
			return http.StatusOK, `{"code":500,"message":"business failed"}`
		case "parser.test":
			return http.StatusOK, `{"success":true,"items":[{"chunk_id":"chunk-fallback","content":"fallback","number":1}],"total":1}`
		default:
			return http.StatusNotFound, `{"message":"not found"}`
		}
	})
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	t.Setenv("LAZYMIND_PARSING_SERVICE_URL", "http://parser.test")
	now := time.Date(2026, 7, 29, 14, 10, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	seedDocumentServiceLazyDocument(t, db, "dataset-1", "doc-1", "lazy-doc-1", "user-1")
	service := mustDocumentService(t, db)

	result, err := service.ListDocumentChunks(context.Background(), DocumentChunksRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-1"})
	if err != nil {
		t.Fatalf("ListDocumentChunks fallback: %v", err)
	}
	if len(result.Chunks) != 1 || result.Chunks[0].ID != "chunk-fallback" {
		t.Fatalf("unexpected fallback result: %+v", result)
	}
}

func TestDocumentServiceListDocumentChunksDoubleBusinessFailureUnavailable(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, func(r *http.Request) (int, string) {
		return http.StatusOK, `{"success":false,"status":"failed","code":500,"message":"business failed"}`
	})
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	t.Setenv("LAZYMIND_PARSING_SERVICE_URL", "http://parser.test")
	now := time.Date(2026, 7, 29, 14, 20, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	seedDocumentServiceLazyDocument(t, db, "dataset-1", "doc-1", "lazy-doc-1", "user-1")
	service := mustDocumentService(t, db)

	_, err := service.ListDocumentChunks(context.Background(), DocumentChunksRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-1"})
	var svcErr *DocumentServiceError
	if !errors.As(err, &svcErr) || svcErr.Code != DocumentServiceUnavailable || svcErr.Unwrap() == nil {
		t.Fatalf("double business failure error = %#v, want unavailable with cause", err)
	}
}

func TestDocumentServiceListDocumentChunksClampsPageSizeToServiceLimit(t *testing.T) {
	db := newDocumentTestDB(t)
	installDocumentServiceTransport(t, func(r *http.Request) (int, string) {
		if r.URL.Query().Get("page_size") != "100" {
			return http.StatusBadRequest, fmt.Sprintf(`{"query":%q}`, r.URL.RawQuery)
		}
		return http.StatusOK, `{"items":[],"total":0}`
	})
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	now := time.Date(2026, 7, 29, 14, 30, 0, 0, time.UTC)
	seedDocumentServiceDataset(t, db, "dataset-1", "user-1", now)
	seedDocumentServiceLazyDocument(t, db, "dataset-1", "doc-1", "lazy-doc-1", "user-1")
	service := mustDocumentService(t, db)

	if _, err := service.ListDocumentChunks(context.Background(), DocumentChunksRequest{UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-1", PageSize: 1000}); err != nil {
		t.Fatalf("ListDocumentChunks clamp: %v", err)
	}
}

func mustDocumentService(t *testing.T, db *orm.DB) *DocumentService {
	t.Helper()
	service, err := NewDocumentService(DocumentServiceDeps{DB: db.DB, LazyDB: db.DB})
	if err != nil {
		t.Fatalf("NewDocumentService: %v", err)
	}
	return service
}

func seedDocumentServiceDataset(t *testing.T, db *orm.DB, id, userID string, now time.Time) {
	t.Helper()
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, id, "kb-"+id, "", userID, now)
}

func seedDocumentServiceDatasetWithKBAndAlgo(t *testing.T, db *orm.DB, id, kbID, algoID, userID string, now time.Time) {
	t.Helper()
	ext := json.RawMessage(nil)
	if strings.TrimSpace(algoID) != "" {
		ext = mustJSON(map[string]any{"algo_id": algoID})
	}
	if err := db.Create(&orm.Dataset{
		ID:          id,
		KbID:        kbID,
		DisplayName: id,
		Ext:         ext,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID + " name",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create dataset %s: %v", id, err)
	}
}

func seedDocumentServiceDocument(t *testing.T, db *orm.DB, datasetID, docID, userID string, ext documentExt) {
	t.Helper()
	now := time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC)
	if err := db.Create(&orm.Document{
		ID:          docID,
		DatasetID:   datasetID,
		DisplayName: docID + ".txt",
		Tags:        []byte(`[]`),
		Ext:         mustJSON(ext),
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID + " name",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create document %s: %v", docID, err)
	}
}

func seedDocumentServiceLazyDocument(t *testing.T, db *orm.DB, datasetID, docID, lazyDocID, userID string) {
	t.Helper()
	seedDocumentServiceDocument(t, db, datasetID, docID, userID, documentExt{ContentType: "text/plain"})
	if err := db.Model(&orm.Document{}).Where("id = ?", docID).Update("lazyllm_doc_id", lazyDocID).Error; err != nil {
		t.Fatalf("set lazy doc id: %v", err)
	}
}

func textDocumentExt(storedPath string) documentExt {
	return documentExt{
		StoredPath:       storedPath,
		OriginalFilename: filepath.Base(storedPath),
		ContentType:      "text/plain; charset=utf-8",
	}
}

func documentServiceCode(err error) DocumentServiceErrorCode {
	var svcErr *DocumentServiceError
	if errors.As(err, &svcErr) {
		return svcErr.Code
	}
	return ""
}

type documentServiceRoundTripper struct {
	handler  func(*http.Request) (int, string)
	scanBody string
}

func (rt documentServiceRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.Contains(r.URL.Host, "scan.test") {
		body := strings.TrimSpace(rt.scanBody)
		if body == "" {
			body = `{"items":[]}`
		}
		return documentServiceJSONResponse(http.StatusOK, body), nil
	}
	if rt.handler != nil {
		status, body := rt.handler(r)
		return documentServiceJSONResponse(status, body), nil
	}
	return documentServiceJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
}

func installDocumentServiceTransport(t *testing.T, handler func(*http.Request) (int, string)) {
	t.Helper()
	installDocumentServiceTransportWithScan(t, handler, `{"items":[]}`)
}

func installDocumentServiceTransportWithScan(t *testing.T, handler func(*http.Request) (int, string), scanBody string) {
	t.Helper()
	prev := http.DefaultTransport
	http.DefaultTransport = documentServiceRoundTripper{handler: handler, scanBody: scanBody}
	t.Cleanup(func() { http.DefaultTransport = prev })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
}

func documentServiceJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       ioNopCloser{strings.NewReader(body)},
	}
}

type ioNopCloser struct {
	*strings.Reader
}

func (c ioNopCloser) Close() error { return nil }
