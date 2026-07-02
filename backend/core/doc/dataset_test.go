package doc

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"lazymind/core/acl"
	"lazymind/core/common/orm"

	"github.com/gorilla/mux"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func testJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func testDatasetIDsJSON(t *testing.T, ids ...string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(ids)
	if err != nil {
		t.Fatalf("marshal dataset ids: %v", err)
	}
	return raw
}

func testParseDatasetIDs(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil {
		t.Fatalf("parse dataset ids: %v", err)
	}
	return ids
}

func TestListAlgosUsesDocServerAlgorithmsEndpoint(t *testing.T) {
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/algo/list" {
			t.Errorf("unexpected algo request path %q", r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"code":404,"msg":"not found"}`), nil
		}
		return testJSONResponse(http.StatusOK, `{"code":200,"msg":"success","data":{"items":[{"algo_id":"general","display_name":"General","description":"General algo"}]}}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")

	req := httptest.NewRequest(http.MethodGet, "/api/core/dataset/algos", nil)
	rec := httptest.NewRecorder()

	ListAlgos(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp ListAlgosResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Algos) != 1 || resp.Algos[0].AlgoID != "general" || resp.Algos[0].DisplayName != "General" {
		t.Fatalf("unexpected algos response: %#v", resp.Algos)
	}
}

func TestListDatasetsKeywordMatchesTags(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.DefaultDataset{}); err != nil {
		t.Fatalf("auto migrate default datasets: %v", err)
	}

	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	rows := []orm.Dataset{
		{
			ID:           "ds-tag",
			KbID:         "kb-tag",
			DisplayName:  "Product docs",
			Desc:         "API references",
			CoverImage:   "",
			DatasetState: 0,
			ShareType:    0,
			Type:         1,
			Ext:          json.RawMessage(`{"tags":["333333","release"]}`),
			BaseModel: orm.BaseModel{
				CreateUserID:   "u1",
				CreateUserName: "Alice",
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		},
		{
			ID:           "ds-other",
			KbID:         "kb-other",
			DisplayName:  "Engineering notes",
			Desc:         "Runbooks",
			CoverImage:   "",
			DatasetState: 0,
			ShareType:    0,
			Type:         1,
			Ext:          json.RawMessage(`{"tags":["release"]}`),
			BaseModel: orm.BaseModel{
				CreateUserID:   "u1",
				CreateUserName: "Alice",
				CreatedAt:      now.Add(-time.Hour),
				UpdatedAt:      now.Add(-time.Hour),
			},
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create datasets: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/datasets?page_token=&page_size=10&keyword=333333", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	ListDatasets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp ListDatasetsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.TotalSize != 1 || len(resp.Datasets) != 1 {
		t.Fatalf("expected one dataset matched by tag, total=%d len=%d body=%s", resp.TotalSize, len(resp.Datasets), rec.Body.String())
	}
	if got := resp.Datasets[0].DatasetID; got != "ds-tag" {
		t.Fatalf("expected ds-tag, got %q", got)
	}
}

func TestCreateDatasetUsesNamespacedAlgoDisplayName(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.DefaultDataset{}, &orm.ACLModel{}, &orm.KBModel{}, &orm.VisibilityModel{}); err != nil {
		t.Fatalf("auto migrate acl tables: %v", err)
	}
	acl.InitStore(db)

	var got kbCreateRequest
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/kbs":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST /v1/kbs, got %s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode kb create request: %v", err)
				return testJSONResponse(http.StatusBadRequest, `{"message":"bad request"}`), nil
			}
			return testJSONResponse(http.StatusOK, `{"kb_id":"kb-returned"}`), nil
		case "/v1/algo/general_algo/groups":
			return testJSONResponse(http.StatusOK, `{"code":200,"data":[]}`), nil
		default:
			t.Errorf("unexpected algo request path %q", r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")

	body := `{"display_name":"datasource-bendi","desc":"local data","algo":{"algo_id":"general_algo","display_name":"General"},"tags":["local"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/core/datasets?dataset_id=ds_test", strings.NewReader(body))
	req.Header.Set("X-User-Id", "user-123")
	req.Header.Set("X-User-Name", "Alice")
	rec := httptest.NewRecorder()

	CreateDataset(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got.DisplayName != "user@user-123@datasource-bendi" {
		t.Fatalf("expected namespaced algo display name, got %q", got.DisplayName)
	}
	if got.OwnerID != "user-123" {
		t.Fatalf("expected owner id user-123, got %q", got.OwnerID)
	}

	var row orm.Dataset
	if err := db.First(&row, "id = ?", "ds_test").Error; err != nil {
		t.Fatalf("query created dataset: %v", err)
	}
	if row.DisplayName != "datasource-bendi" {
		t.Fatalf("core dataset display name should stay unchanged, got %q", row.DisplayName)
	}
	if row.KbID != "kb-returned" {
		t.Fatalf("expected returned kb id to be stored, got %q", row.KbID)
	}
}

func TestCreateDatasetRejectsReservedDisplayNamePrefixes(t *testing.T) {
	newDocumentTestDB(t)
	called := false
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return testJSONResponse(http.StatusInternalServerError, `{"message":"unexpected call"}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")

	for _, name := range []string{"user@abc", "feishu@abc", "local@abc", " User@abc"} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/core/datasets", strings.NewReader(`{"display_name":`+strconv.Quote(name)+`}`))
			req.Header.Set("X-User-Id", "user-123")
			rec := httptest.NewRecorder()

			CreateDataset(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
	if called {
		t.Fatalf("algo service must not be called for reserved display names")
	}
}

func TestCreateDatasetRejectsInvalidDisplayNames(t *testing.T) {
	newDocumentTestDB(t)
	called := false
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return testJSONResponse(http.StatusInternalServerError, `{"message":"unexpected call"}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")

	for _, name := range []string{"", "Bad Name", "Bad/Name", " Knowledge", "知识库🙂", strings.Repeat("a", 101)} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/core/datasets", strings.NewReader(`{"display_name":`+strconv.Quote(name)+`}`))
			req.Header.Set("X-User-Id", "user-123")
			rec := httptest.NewRecorder()

			CreateDataset(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
	if called {
		t.Fatalf("algo service must not be called for invalid display names")
	}
}

func TestCreateDatasetRejectsDuplicateDisplayNameForSameUser(t *testing.T) {
	db := newDocumentTestDB(t)
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&orm.Dataset{
		ID:           "ds-existing",
		KbID:         "kb-existing",
		DisplayName:  "Duplicate_Name",
		Desc:         "existing",
		DatasetState: 0,
		ShareType:    0,
		Type:         1,
		Ext:          json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-123",
			CreateUserName: "Alice",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create existing dataset: %v", err)
	}

	called := false
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return testJSONResponse(http.StatusInternalServerError, `{"message":"unexpected call"}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")

	req := httptest.NewRequest(http.MethodPost, "/api/core/datasets", strings.NewReader(`{"display_name":"Duplicate_Name"}`))
	req.Header.Set("X-User-Id", "user-123")
	req.Header.Set("X-User-Name", "Alice")
	rec := httptest.NewRecorder()

	CreateDataset(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "dataset name already exists") {
		t.Fatalf("expected duplicate name error, got %s", rec.Body.String())
	}
	if called {
		t.Fatalf("algo service must not be called for duplicate dataset names")
	}
}

func TestCreateDatasetAllowsSameDisplayNameForDifferentUsers(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.DefaultDataset{}, &orm.ACLModel{}, &orm.KBModel{}, &orm.VisibilityModel{}); err != nil {
		t.Fatalf("auto migrate acl tables: %v", err)
	}
	acl.InitStore(db)
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&orm.Dataset{
		ID:           "ds-existing",
		KbID:         "kb-existing",
		DisplayName:  "Shared_Name",
		Desc:         "existing",
		DatasetState: 0,
		ShareType:    0,
		Type:         1,
		Ext:          json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "other-user",
			CreateUserName: "Bob",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create existing dataset: %v", err)
	}

	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/kbs":
			return testJSONResponse(http.StatusOK, `{"kb_id":"kb-returned"}`), nil
		case "/v1/algo/general_algo/groups":
			return testJSONResponse(http.StatusOK, `{"code":200,"data":[]}`), nil
		default:
			t.Errorf("unexpected algo request path %q", r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")

	req := httptest.NewRequest(http.MethodPost, "/api/core/datasets?dataset_id=ds_new", strings.NewReader(`{"display_name":"Shared_Name","algo":{"algo_id":"general_algo"}}`))
	req.Header.Set("X-User-Id", "user-123")
	req.Header.Set("X-User-Name", "Alice")
	rec := httptest.NewRecorder()

	CreateDataset(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var row orm.Dataset
	if err := db.First(&row, "id = ?", "ds_new").Error; err != nil {
		t.Fatalf("query created dataset: %v", err)
	}
	if row.DisplayName != "Shared_Name" || row.CreateUserID != "user-123" {
		t.Fatalf("unexpected created dataset: %#v", row)
	}
}

func TestDeleteDatasetRemovesEvalSetDatasetReferences(t *testing.T) {
	db := newDocumentTestDB(t)
	now := time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&orm.Dataset{
		ID:           "ds-delete",
		KbID:         "kb-delete",
		DisplayName:  "delete me",
		DatasetState: 0,
		ShareType:    0,
		Type:         1,
		Ext:          json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-123",
			CreateUserName: "Alice",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	evalSets := []orm.EvalSet{
		{
			ID:             "eval_set_mixed",
			Name:           "mixed",
			DatasetIDs:     testDatasetIDsJSON(t, "ds-delete", "ds-keep"),
			OwnerID:        "user-123",
			ShardID:        "eval_shard_test",
			Status:         "active",
			CreateUserID:   "user-123",
			CreateUserName: "Alice",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             "eval_set_only_deleted",
			Name:           "only deleted",
			DatasetIDs:     testDatasetIDsJSON(t, "ds-delete"),
			OwnerID:        "user-123",
			ShardID:        "eval_shard_test",
			Status:         "active",
			CreateUserID:   "user-123",
			CreateUserName: "Alice",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             "eval_set_unrelated",
			Name:           "unrelated",
			DatasetIDs:     testDatasetIDsJSON(t, "ds-other"),
			OwnerID:        "user-123",
			ShardID:        "eval_shard_test",
			Status:         "active",
			CreateUserID:   "user-123",
			CreateUserName: "Alice",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := db.Create(&evalSets).Error; err != nil {
		t.Fatalf("create eval sets: %v", err)
	}

	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected method %s for %q", r.Method, r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
		switch r.URL.Path {
		case "/api/scan/internal/sources/by-dataset/ds-delete":
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		case "/v1/kbs/kb-delete":
			return testJSONResponse(http.StatusOK, `{}`), nil
		default:
			t.Errorf("unexpected delete path %q", r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")

	req := httptest.NewRequest(http.MethodDelete, "/api/core/datasets/ds-delete", nil)
	req = mux.SetURLVars(req, map[string]string{"dataset": "ds-delete"})
	req.Header.Set("X-User-Id", "user-123")
	rec := httptest.NewRecorder()

	DeleteDataset(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var mixed orm.EvalSet
	if err := db.First(&mixed, "id = ?", "eval_set_mixed").Error; err != nil {
		t.Fatalf("query mixed eval set: %v", err)
	}
	if got := strings.Join(testParseDatasetIDs(t, mixed.DatasetIDs), ","); got != "ds-keep" {
		t.Fatalf("expected only kept dataset id, got %q", got)
	}
	var onlyDeleted orm.EvalSet
	if err := db.First(&onlyDeleted, "id = ?", "eval_set_only_deleted").Error; err != nil {
		t.Fatalf("query only deleted eval set: %v", err)
	}
	if got := testParseDatasetIDs(t, onlyDeleted.DatasetIDs); len(got) != 0 {
		t.Fatalf("expected empty dataset ids, got %#v", got)
	}
	var unrelated orm.EvalSet
	if err := db.First(&unrelated, "id = ?", "eval_set_unrelated").Error; err != nil {
		t.Fatalf("query unrelated eval set: %v", err)
	}
	if got := strings.Join(testParseDatasetIDs(t, unrelated.DatasetIDs), ","); got != "ds-other" {
		t.Fatalf("expected unrelated eval set unchanged, got %q", got)
	}
}

func TestDeleteDatasetDeletesScanSourceFirst(t *testing.T) {
	db := newDocumentTestDB(t)
	now := time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&orm.Dataset{
		ID:           "ds-delete",
		KbID:         "kb-delete",
		DisplayName:  "delete me",
		DatasetState: 0,
		ShareType:    0,
		Type:         1,
		Ext:          json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-123",
			CreateUserName: "Alice",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create dataset: %v", err)
	}

	var calls []string
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected method %s for %q", r.Method, r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
		calls = append(calls, r.URL.Path)
		switch r.URL.Path {
		case "/api/scan/internal/sources/by-dataset/ds-delete":
			return testJSONResponse(http.StatusOK, `{}`), nil
		case "/v1/kbs/kb-delete":
			return testJSONResponse(http.StatusOK, `{}`), nil
		default:
			t.Errorf("unexpected delete path %q", r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")

	req := httptest.NewRequest(http.MethodDelete, "/api/core/datasets/ds-delete", nil)
	req = mux.SetURLVars(req, map[string]string{"dataset": "ds-delete"})
	req.Header.Set("X-User-Id", "user-123")
	rec := httptest.NewRecorder()

	DeleteDataset(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := strings.Join(calls, ","); got != "/api/scan/internal/sources/by-dataset/ds-delete,/v1/kbs/kb-delete" {
		t.Fatalf("unexpected external delete order: %q", got)
	}
	var row orm.Dataset
	if err := db.First(&row, "id = ?", "ds-delete").Error; err != nil {
		t.Fatalf("query deleted dataset: %v", err)
	}
	if row.DeletedAt == nil {
		t.Fatalf("expected dataset to be soft deleted")
	}
}

func TestDeleteDatasetContinuesWhenScanSourceMissing(t *testing.T) {
	db := newDocumentTestDB(t)
	now := time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&orm.Dataset{
		ID:           "ds-delete",
		KbID:         "kb-delete",
		DisplayName:  "delete me",
		DatasetState: 0,
		ShareType:    0,
		Type:         1,
		Ext:          json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-123",
			CreateUserName: "Alice",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create dataset: %v", err)
	}

	var calls []string
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.Path)
		switch r.URL.Path {
		case "/api/scan/internal/sources/by-dataset/ds-delete":
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		case "/v1/kbs/kb-delete":
			return testJSONResponse(http.StatusOK, `{}`), nil
		default:
			t.Errorf("unexpected delete path %q", r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")

	req := httptest.NewRequest(http.MethodDelete, "/api/core/datasets/ds-delete", nil)
	req = mux.SetURLVars(req, map[string]string{"dataset": "ds-delete"})
	req.Header.Set("X-User-Id", "user-123")
	rec := httptest.NewRecorder()

	DeleteDataset(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := strings.Join(calls, ","); got != "/api/scan/internal/sources/by-dataset/ds-delete,/v1/kbs/kb-delete" {
		t.Fatalf("unexpected external delete order: %q", got)
	}
}

func TestUpdateDatasetUsesNamespacedAlgoDisplayName(t *testing.T) {
	db := newDocumentTestDB(t)
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&orm.Dataset{
		ID:           "ds-update",
		KbID:         "kb-update",
		DisplayName:  "old_name",
		DatasetState: 0,
		ShareType:    0,
		Type:         1,
		Ext:          json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-123",
			CreateUserName: "Alice",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create dataset: %v", err)
	}

	var got kbUpdateRequest
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/kbs/kb-update" {
			t.Errorf("unexpected algo request path %q", r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode kb update request: %v", err)
			return testJSONResponse(http.StatusBadRequest, `{"message":"bad request"}`), nil
		}
		return testJSONResponse(http.StatusOK, `{}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")

	req := httptest.NewRequest(http.MethodPatch, "/api/core/datasets/ds-update", strings.NewReader(`{"display_name":"new_name"}`))
	req = mux.SetURLVars(req, map[string]string{"dataset": "ds-update"})
	req.Header.Set("X-User-Id", "user-123")
	req.Header.Set("X-User-Name", "Alice")
	rec := httptest.NewRecorder()

	UpdateDataset(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got.DisplayName == nil || *got.DisplayName != "user@user-123@new_name" {
		t.Fatalf("expected namespaced algo display name, got %#v", got.DisplayName)
	}
	var row orm.Dataset
	if err := db.First(&row, "id = ?", "ds-update").Error; err != nil {
		t.Fatalf("query updated dataset: %v", err)
	}
	if row.DisplayName != "new_name" {
		t.Fatalf("core dataset display name should stay unchanged, got %q", row.DisplayName)
	}
}

func TestUpdateDatasetRejectsInvalidDisplayName(t *testing.T) {
	db := newDocumentTestDB(t)
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&orm.Dataset{
		ID:           "ds-update-invalid",
		KbID:         "kb-update-invalid",
		DisplayName:  "old_name",
		DatasetState: 0,
		ShareType:    0,
		Type:         1,
		Ext:          json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-123",
			CreateUserName: "Alice",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create dataset: %v", err)
	}

	called := false
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return testJSONResponse(http.StatusInternalServerError, `{"message":"unexpected call"}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")

	req := httptest.NewRequest(http.MethodPatch, "/api/core/datasets/ds-update-invalid", strings.NewReader(`{"display_name":"new name"}`))
	req = mux.SetURLVars(req, map[string]string{"dataset": "ds-update-invalid"})
	req.Header.Set("X-User-Id", "user-123")
	rec := httptest.NewRecorder()

	UpdateDataset(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatalf("algo service must not be called for invalid display names")
	}
}

func TestAllDatasetTagsIncludesACLVisibleDatasets(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.DefaultDataset{}, &orm.ACLModel{}, &orm.UserGroupModel{}); err != nil {
		t.Fatalf("auto migrate acl tables: %v", err)
	}
	acl.InitStore(db)

	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	rows := []orm.Dataset{
		{
			ID:           "ds-owned",
			KbID:         "kb-owned",
			DisplayName:  "owned",
			Desc:         "owned",
			DatasetState: 0,
			ShareType:    0,
			Type:         1,
			Ext:          json.RawMessage(`{"tags":["owned-tag"]}`),
			BaseModel: orm.BaseModel{
				CreateUserID:   "u1",
				CreateUserName: "Alice",
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		},
		{
			ID:           "ds-shared",
			KbID:         "kb-shared",
			DisplayName:  "shared",
			Desc:         "shared",
			DatasetState: 0,
			ShareType:    0,
			Type:         1,
			Ext:          json.RawMessage(`{"tags":["shared-tag"]}`),
			BaseModel: orm.BaseModel{
				CreateUserID:   "owner-2",
				CreateUserName: "Bob",
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		},
		{
			ID:           "ds-hidden",
			KbID:         "kb-hidden",
			DisplayName:  "hidden",
			Desc:         "hidden",
			DatasetState: 0,
			ShareType:    0,
			Type:         1,
			Ext:          json.RawMessage(`{"tags":["hidden-tag"]}`),
			BaseModel: orm.BaseModel{
				CreateUserID:   "owner-3",
				CreateUserName: "Carol",
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create datasets: %v", err)
	}
	if id := acl.GetStore().AddACL(acl.ResourceTypeDB, "ds-shared", acl.GranteeUser, "u1", acl.PermissionDatasetRead, "owner-2", nil); id == 0 {
		t.Fatalf("expected acl row to be created")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/dataset/tags", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	AllDatasetTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp AllDatasetTagsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := []string{"owned-tag", "shared-tag"}
	if got := resp.Tags; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("expected tags %v, got %v", want, got)
	}
}
