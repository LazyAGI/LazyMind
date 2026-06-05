package coreclient

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPCoreClientResourceRoutesUseCoreDocumentAPIs(t *testing.T) {
	t.Parallel()

	var datasetReqs []CreateDatasetRequest
	var rootDocBody map[string]any
	var deleted []string
	var rootUserID string
	var deleteDocumentUserID string
	var deleteDatasetUserID string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/datasets":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected dataset method %s", r.Method)
			}
			var req CreateDatasetRequest
			decodeJSON(t, r, &req)
			datasetReqs = append(datasetReqs, req)
			writeJSON(t, w, http.StatusOK, CreateDatasetResponse{DatasetID: "dataset-1", Created: len(datasetReqs) == 1})
		case "/datasets/dataset-1/documents":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected root document method %s", r.Method)
			}
			rootUserID = r.Header.Get("X-User-Id")
			if got := r.URL.Query().Get("document_id"); got != "" {
				t.Fatalf("binding root must not guess document_id from idempotency key, got %q", got)
			}
			decodeJSON(t, r, &rootDocBody)
			writeJSON(t, w, http.StatusOK, map[string]any{"document_id": "core-folder-1"})
		case "/datasets/dataset-1/documents/core-folder-1":
			if r.Method != http.MethodDelete {
				t.Fatalf("unexpected delete method %s", r.Method)
			}
			deleteDocumentUserID = r.Header.Get("X-User-Id")
			deleted = append(deleted, "core-folder-1")
			w.WriteHeader(http.StatusOK)
		case "/datasets/dataset-1":
			if r.Method != http.MethodDelete {
				t.Fatalf("unexpected dataset delete method %s", r.Method)
			}
			deleteDatasetUserID = r.Header.Get("X-User-Id")
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	client := newHTTPTestCoreClient(t, handler)
	dataset, err := client.CreateDataset(context.Background(), CreateDatasetRequest{
		IdempotencyKey: "create-source:user-1:req-1:dataset",
		Name:           "Docs",
		CreatedBy:      "user-1",
		TenantID:       "tenant-1",
	})
	if err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	root, err := client.CreateBindingRootDocument(context.Background(), CreateBindingRootDocumentRequest{
		IdempotencyKey: "binding-root-1",
		DatasetID:      dataset.DatasetID,
		Name:           "Binding",
		UserID:         "user-1",
	})
	if err != nil {
		t.Fatalf("create binding root: %v", err)
	}
	if !dataset.Created || !root.Created || root.DocumentID != "core-folder-1" {
		t.Fatalf("unexpected resource responses: dataset=%+v root=%+v", dataset, root)
	}
	if len(datasetReqs) != 1 || datasetReqs[0].DisplayName != "Docs" {
		t.Fatalf("dataset create did not carry Core display_name: %+v", datasetReqs)
	}
	if rootDocBody["display_name"] != "Binding" || rootDocBody["p_id"] != "" || rootDocBody["idempotency_key"] != "binding-root-1" {
		t.Fatalf("binding root request should create a top-level Core document: %+v", rootDocBody)
	}
	if rootUserID != "user-1" {
		t.Fatalf("binding root request should carry caller user id, got %q", rootUserID)
	}
	if err := client.DeleteDocument(context.Background(), DeleteDocumentRequest{DatasetID: "dataset-1", DocumentID: "core-folder-1", UserID: "user-1"}); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	if len(deleted) != 1 {
		t.Fatalf("expected one real document delete, got %v", deleted)
	}
	if deleteDocumentUserID != "user-1" {
		t.Fatalf("document delete request should carry caller user id, got %q", deleteDocumentUserID)
	}
	if err := client.DeleteDataset(context.Background(), DeleteDatasetRequest{DatasetID: "dataset-1", UserID: "user-1"}); err != nil {
		t.Fatalf("delete dataset: %v", err)
	}
	if deleteDatasetUserID != "user-1" {
		t.Fatalf("dataset delete request should carry caller user id, got %q", deleteDatasetUserID)
	}
}

func TestHTTPCoreClientCreateDatasetCarriesAlgo(t *testing.T) {
	t.Parallel()

	var datasetReq CreateDatasetRequest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/datasets" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		decodeJSON(t, r, &datasetReq)
		writeJSON(t, w, http.StatusOK, CreateDatasetResponse{DatasetID: "dataset-1", Created: true})
	})
	client := newHTTPTestCoreClient(t, handler)

	_, err := client.CreateDataset(context.Background(), CreateDatasetRequest{
		IdempotencyKey: "create-source:user-1:req-1:dataset",
		Name:           "Docs",
		CreatedBy:      "user-1",
		Algo: &DatasetAlgo{
			AlgoID:      "general_algo",
			DisplayName: "General",
		},
	})
	if err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	if datasetReq.DisplayName != "Docs" {
		t.Fatalf("expected display_name fallback from name, got %+v", datasetReq)
	}
	if datasetReq.Algo == nil || datasetReq.Algo.AlgoID != "general_algo" || datasetReq.Algo.DisplayName != "General" {
		t.Fatalf("dataset create lost algo payload: %+v", datasetReq)
	}
}

func TestHTTPCoreClientSubmitCreateUsesUploadTaskAndStart(t *testing.T) {
	t.Parallel()

	var paths []string
	var uploadedFilename string
	var uploadUserID string
	var taskUserID string
	var startUserID string
	var createTaskReq map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/datasets/dataset-1/uploads":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected upload method %s", r.Method)
			}
			uploadUserID = r.Header.Get("X-User-Id")
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("parse multipart: %v", err)
			}
			file, header, err := r.FormFile("files")
			if err != nil {
				t.Fatalf("uploaded file missing: %v", err)
			}
			defer file.Close()
			uploadedFilename = header.Filename
			content, _ := io.ReadAll(file)
			if string(content) != "hello" {
				t.Fatalf("unexpected upload content %q", string(content))
			}
			writeJSON(t, w, http.StatusOK, map[string]any{"files": []map[string]string{{"upload_file_id": "upload-1"}}})
		case "/datasets/dataset-1/tasks":
			taskUserID = r.Header.Get("X-User-Id")
			decodeJSON(t, r, &createTaskReq)
			writeJSON(t, w, http.StatusOK, map[string]any{"tasks": []map[string]string{{"task_id": "core-task-1", "document_id": "core-doc-1"}}})
		case "/datasets/dataset-1/tasks:start":
			startUserID = r.Header.Get("X-User-Id")
			var req map[string]any
			decodeJSON(t, r, &req)
			writeJSON(t, w, http.StatusOK, map[string]any{"tasks": []map[string]any{{"task_id": "core-task-1", "status": "STARTED"}}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	client := newHTTPTestCoreClient(t, handler)
	resp, err := client.SubmitParseTask(context.Background(), SubmitParseTaskRequest{
		IdempotencyKey:   "idem-create",
		DatasetID:        "dataset-1",
		ParentDocumentID: "binding-root-1",
		DisplayName:      "a.md",
		ContentURI:       "scan-temp://token",
		Content:          strings.NewReader("hello"),
		MimeType:         "text/markdown",
		FileExtension:    ".md",
		UserID:           "user-1",
		Action:           ActionCreate,
	})
	if err != nil {
		t.Fatalf("submit create: %v", err)
	}
	if resp.Status != StatusSubmitted || resp.CoreTaskID != "core-task-1" || resp.CoreDocumentID != "core-doc-1" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	assertNoPath(t, paths, "/parse/tasks")
	task := firstTaskPayload(t, createTaskReq)
	if task["task_type"] != "TASK_TYPE_PARSE_UPLOADED" || task["document_pid"] != "binding-root-1" || task["display_name"] != "a.md" {
		t.Fatalf("create task payload does not match Core contract: %+v", createTaskReq)
	}
	if createTaskReq["idempotency_key"] != "idem-create" {
		t.Fatalf("create task did not carry idempotency key: %+v", createTaskReq)
	}
	if uploadedFilename != "a.md" {
		t.Fatalf("unexpected multipart filename %q", uploadedFilename)
	}
	if uploadUserID != "user-1" || taskUserID != "user-1" || startUserID != "user-1" {
		t.Fatalf("submit create should carry owner user id, upload=%q task=%q start=%q", uploadUserID, taskUserID, startUserID)
	}
}

func TestHTTPCoreClientSubmitCreateUsesExportedExtensionForMultipartFilename(t *testing.T) {
	t.Parallel()

	var uploadedFilename string
	var createTaskReq map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/datasets/dataset-1/uploads":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("parse multipart: %v", err)
			}
			file, header, err := r.FormFile("files")
			if err != nil {
				t.Fatalf("uploaded file missing: %v", err)
			}
			defer file.Close()
			uploadedFilename = header.Filename
			writeJSON(t, w, http.StatusOK, map[string]any{"files": []map[string]string{{"upload_file_id": "upload-1"}}})
		case "/datasets/dataset-1/tasks":
			decodeJSON(t, r, &createTaskReq)
			writeJSON(t, w, http.StatusOK, map[string]any{"tasks": []map[string]string{{"task_id": "core-task-1", "document_id": "core-doc-1"}}})
		case "/datasets/dataset-1/tasks:start":
			writeJSON(t, w, http.StatusOK, map[string]any{"tasks": []map[string]any{{"task_id": "core-task-1", "status": "STARTED"}}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	client := newHTTPTestCoreClient(t, handler)
	_, err := client.SubmitParseTask(context.Background(), SubmitParseTaskRequest{
		IdempotencyKey:   "idem-create",
		DatasetID:        "dataset-1",
		ParentDocumentID: "binding-root-1",
		DisplayName:      "perm2",
		Content:          strings.NewReader("hello"),
		MimeType:         "text/markdown",
		FileExtension:    ".md",
		Action:           ActionCreate,
	})
	if err != nil {
		t.Fatalf("submit create: %v", err)
	}
	if uploadedFilename != "perm2.md" {
		t.Fatalf("expected multipart filename to use exported extension, got %q", uploadedFilename)
	}
	task := firstTaskPayload(t, createTaskReq)
	if task["display_name"] != "perm2.md" {
		t.Fatalf("core display_name should use exported extension, got %+v", createTaskReq)
	}
}

func TestHTTPCoreClientRecoversIdempotentConflicts(t *testing.T) {
	t.Parallel()

	var calls []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		switch r.URL.Path {
		case "/datasets":
			writeJSON(t, w, http.StatusConflict, map[string]any{
				"code":       "already_exists",
				"dataset_id": "dataset-existing",
			})
		case "/datasets/dataset-existing/documents":
			writeJSON(t, w, http.StatusConflict, map[string]any{
				"code":        "already_exists",
				"document_id": "folder-existing",
			})
		case "/datasets/dataset-existing/uploads":
			writeJSON(t, w, http.StatusOK, map[string]any{"files": []map[string]string{{"upload_file_id": "upload-1"}}})
		case "/datasets/dataset-existing/tasks":
			writeJSON(t, w, http.StatusConflict, map[string]any{
				"code":        "already_exists",
				"task_id":     "task-existing",
				"document_id": "doc-existing",
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	client := newHTTPTestCoreClient(t, handler)
	dataset, err := client.CreateDataset(context.Background(), CreateDatasetRequest{
		IdempotencyKey: "idem-dataset",
		Name:           "Docs",
		CreatedBy:      "user-1",
	})
	if err != nil || dataset.DatasetID != "dataset-existing" || dataset.Created {
		t.Fatalf("unexpected dataset=%+v err=%v", dataset, err)
	}
	root, err := client.CreateBindingRootDocument(context.Background(), CreateBindingRootDocumentRequest{
		IdempotencyKey: "idem-folder",
		DatasetID:      dataset.DatasetID,
		Name:           "Binding",
	})
	if err != nil || root.DocumentID != "folder-existing" || root.Created {
		t.Fatalf("unexpected root=%+v err=%v", root, err)
	}
	resp, err := client.SubmitParseTask(context.Background(), SubmitParseTaskRequest{
		IdempotencyKey:   "idem-task",
		DatasetID:        dataset.DatasetID,
		ParentDocumentID: root.DocumentID,
		DisplayName:      "a.md",
		Content:          strings.NewReader("hello"),
		Action:           ActionCreate,
	})
	if err != nil || resp.CoreTaskID != "task-existing" || resp.CoreDocumentID != "doc-existing" || resp.Created {
		t.Fatalf("unexpected submit=%+v err=%v", resp, err)
	}
	assertNoPath(t, calls, "/datasets/dataset-existing/tasks:start")
}

func TestHTTPCoreClientSubmitCreateCanOpenContentURI(t *testing.T) {
	t.Parallel()

	var uploaded string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/datasets/dataset-1/uploads":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("parse multipart: %v", err)
			}
			file, _, err := r.FormFile("files")
			if err != nil {
				t.Fatalf("uploaded file missing: %v", err)
			}
			defer file.Close()
			content, _ := io.ReadAll(file)
			uploaded = string(content)
			writeJSON(t, w, http.StatusOK, map[string]any{"files": []map[string]string{{"upload_file_id": "upload-1"}}})
		case "/datasets/dataset-1/tasks":
			writeJSON(t, w, http.StatusOK, map[string]any{"tasks": []map[string]string{{"task_id": "core-task-1", "document_id": "core-doc-1"}}})
		case "/datasets/dataset-1/tasks:start":
			writeJSON(t, w, http.StatusOK, map[string]any{"tasks": []map[string]any{{"task_id": "core-task-1", "status": "STARTED"}}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	client := newHTTPTestCoreClient(t, handler)
	client.UseContentStore(coreTestContentStore{"scan-temp://token": "from temp store"})
	_, err := client.SubmitParseTask(context.Background(), SubmitParseTaskRequest{
		IdempotencyKey:   "idem-create",
		DatasetID:        "dataset-1",
		ParentDocumentID: "folder-1",
		DisplayName:      "a.md",
		ContentURI:       "scan-temp://token",
		Action:           ActionCreate,
	})
	if err != nil {
		t.Fatalf("submit create from uri: %v", err)
	}
	if uploaded != "from temp store" {
		t.Fatalf("content_uri was not opened through content store, uploaded=%q", uploaded)
	}
}

func TestHTTPCoreClientSubmitReparseUsesDocumentPathTaskAndStart(t *testing.T) {
	t.Parallel()

	var paths []string
	var uploaded string
	var uploadedFilename string
	var taskUserID string
	var startUserID string
	var deleteDocumentUserID string
	var createTaskReq map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/datasets/dataset-1/uploads":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("parse multipart: %v", err)
			}
			files := r.MultipartForm.File["files"]
			if len(files) != 1 {
				t.Fatalf("expected one uploaded file, got %d", len(files))
			}
			uploadedFilename = files[0].Filename
			file, err := files[0].Open()
			if err != nil {
				t.Fatalf("open uploaded file: %v", err)
			}
			body, err := io.ReadAll(file)
			_ = file.Close()
			if err != nil {
				t.Fatalf("read uploaded file: %v", err)
			}
			uploaded = string(body)
			writeJSON(t, w, http.StatusOK, map[string]any{"files": []map[string]string{{"upload_file_id": "upload-reparse"}}})
		case "/datasets/dataset-1/tasks":
			taskUserID = r.Header.Get("X-User-Id")
			decodeJSON(t, r, &createTaskReq)
			writeJSON(t, w, http.StatusOK, map[string]any{"tasks": []map[string]string{{"task_id": "core-task-reparse", "document_id": "core-doc-new"}}})
		case "/datasets/dataset-1/tasks:start":
			startUserID = r.Header.Get("X-User-Id")
			writeJSON(t, w, http.StatusOK, map[string]any{"tasks": []map[string]any{{"task_id": "core-task-reparse", "status": "STARTED"}}})
		case "/datasets/dataset-1/documents/core-doc-1":
			if r.Method != http.MethodDelete {
				t.Fatalf("unexpected document method %s", r.Method)
			}
			deleteDocumentUserID = r.Header.Get("X-User-Id")
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	client := newHTTPTestCoreClient(t, handler)
	resp, err := client.SubmitParseTask(context.Background(), SubmitParseTaskRequest{
		IdempotencyKey:   "idem-reparse",
		DatasetID:        "dataset-1",
		ParentDocumentID: "folder-1",
		SourceDocumentID: "core-doc-1",
		DisplayName:      "a.md",
		ContentURI:       "scan-temp://token",
		Content:          strings.NewReader("new content"),
		UserID:           "user-1",
		Action:           ActionReparse,
	})
	if err != nil {
		t.Fatalf("submit reparse: %v", err)
	}
	if resp.Status != StatusSubmitted || resp.CoreTaskID != "core-task-reparse" || resp.CoreDocumentID != "core-doc-new" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	assertNoPath(t, paths, "/datasets/dataset-1/documents/core-doc-1:content")
	if uploaded != "new content" || uploadedFilename != "a.md" {
		t.Fatalf("reparse did not upload replacement content, filename=%q content=%q", uploadedFilename, uploaded)
	}
	task := firstTaskPayload(t, createTaskReq)
	if task["task_type"] != "TASK_TYPE_PARSE_UPLOADED" || task["document_pid"] != "folder-1" {
		t.Fatalf("reparse task payload does not match Core contract: %+v", createTaskReq)
	}
	if itemUploadID(t, createTaskReq) != "upload-reparse" {
		t.Fatalf("reparse task should bind replacement upload: %+v", createTaskReq)
	}
	if taskUserID != "user-1" || startUserID != "user-1" || deleteDocumentUserID != "user-1" {
		t.Fatalf("submit reparse should carry owner user id, task=%q start=%q delete=%q", taskUserID, startUserID, deleteDocumentUserID)
	}
}

func TestHTTPCoreClientSubmitDeleteUsesDocumentDelete(t *testing.T) {
	t.Parallel()

	var paths []string
	var deleteUserID string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/datasets/dataset-1/documents/core-doc-1" || r.Method != http.MethodDelete {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		deleteUserID = r.Header.Get("X-User-Id")
		w.WriteHeader(http.StatusOK)
	})

	client := newHTTPTestCoreClient(t, handler)
	resp, err := client.SubmitParseTask(context.Background(), SubmitParseTaskRequest{
		IdempotencyKey:   "idem-delete",
		DatasetID:        "dataset-1",
		SourceDocumentID: "core-doc-1",
		DisplayName:      "a.md",
		UserID:           "user-1",
		Action:           ActionDelete,
	})
	if err != nil {
		t.Fatalf("submit delete: %v", err)
	}
	if resp.Status != StatusSucceeded || resp.CoreDocumentID != "core-doc-1" {
		t.Fatalf("unexpected delete response: %+v", resp)
	}
	if deleteUserID != "user-1" {
		t.Fatalf("submit delete should carry owner user id, got %q", deleteUserID)
	}
	assertNoPath(t, paths, "/parse/tasks")
}

func TestHTTPCoreClientGetCoreTaskResultUsesCoreTaskRoute(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/datasets/dataset-1/tasks/core-task-1" || r.Method != http.MethodGet {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, map[string]string{"task_id": "core-task-1", "document_id": "core-doc-1", "task_state": "SUCCEEDED"})
	})

	client := newHTTPTestCoreClient(t, handler)
	result, err := client.GetCoreTaskResult(context.Background(), GetCoreTaskResultRequest{DatasetID: "dataset-1", CoreTaskID: "core-task-1"})
	if err != nil || result.Status != ResultStatusSucceeded || result.CoreDocumentID != "core-doc-1" {
		t.Fatalf("unexpected result=%+v err=%v", result, err)
	}
}

func newHTTPTestCoreClient(t *testing.T, handler http.Handler) *HTTPCoreClient {
	t.Helper()
	client, err := NewHTTPCoreClient("http://core.test", &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Result(), nil
	})})
	if err != nil {
		t.Fatalf("new http core client: %v", err)
	}
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func decodeJSON(t *testing.T, r *http.Request, out any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		t.Fatalf("decode request: %v", err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

func assertNoPath(t *testing.T, paths []string, forbidden string) {
	t.Helper()
	for _, path := range paths {
		if path == forbidden {
			t.Fatalf("unexpected forbidden Core path %s in %v", forbidden, paths)
		}
	}
}

func firstTaskPayload(t *testing.T, req map[string]any) map[string]any {
	t.Helper()
	items, ok := req["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("unexpected items payload: %+v", req)
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected item payload: %+v", items[0])
	}
	task, ok := item["task"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected task payload: %+v", item)
	}
	return task
}

func itemUploadID(t *testing.T, req map[string]any) string {
	t.Helper()
	items, ok := req["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("unexpected items payload: %+v", req)
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected item payload: %+v", items[0])
	}
	uploadID, _ := item["upload_file_id"].(string)
	return uploadID
}

type coreTestContentStore map[string]string

func (s coreTestContentStore) Open(_ context.Context, uri string) (io.ReadCloser, error) {
	if content, ok := s[uri]; ok {
		return io.NopCloser(strings.NewReader(content)), nil
	}
	return nil, fs.ErrNotExist
}
