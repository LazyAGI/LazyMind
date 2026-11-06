package doc

import (
	"encoding/json"
	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDatasetDeletionProgress(t *testing.T) {
	for _, tc := range []struct {
		name, cleanup, kbStatus, want   string
		remaining, upstream, httpStatus int
		failed                          bool
	}{
		{name: "pending", cleanup: "submitted", kbStatus: "DELETING", remaining: 2, upstream: 200, httpStatus: 200, want: "cleaning"},
		{name: "finished", cleanup: "submitted", upstream: 404, httpStatus: 200, want: "completed"},
		{name: "submission failed", cleanup: "failed", httpStatus: 200, want: "failed"},
		{name: "document failed", cleanup: "submitted", kbStatus: "DELETING", remaining: 2, upstream: 200, httpStatus: 200, want: "failed", failed: true},
		{name: "upstream unavailable", cleanup: "submitted", upstream: 503, httpStatus: 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newDocumentTestDB(t)
			seedDocumentListDataset(t, db, "deleting", "owner")
			now := time.Now().UTC()
			if err := db.Model(&orm.Dataset{}).Where("id = ?", "deleting").Update("deleted_at", now).Error; err != nil {
				t.Fatal(err)
			}
			result := ResourceOperationResult{Items: []ResourceOperationItem{{ResourceRef: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: "deleting"}, CleanupStatus: tc.cleanup, DocumentCount: 5}}}
			raw, _ := json.Marshal(result)
			if err := db.Create(&orm.ResourceOperationReceipt{TenantID: "default", UserID: "owner", OperationKey: "dataset-delete:deleting", Action: "knowledge.resource.delete", Result: raw, CreatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			if tc.failed {
				if err := db.Create(&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "delete-task", TaskType: "DOC_DELETE", DocID: "doc", KbID: "kb-deleting", Status: "FAILED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&readonlyorm.LazyLLMDocParseStateRow{DocID: "doc", KbID: "kb-deleting", Status: "FAILED", CurrentTaskID: "delete-task"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			transport := http.DefaultTransport
			http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if tc.cleanup != "submitted" {
					t.Error("failed submission must not be reported as complete from a missing KB")
				}
				if r.Method != "GET" || r.URL.Path != "/v1/kbs/kb-deleting" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				body, _ := json.Marshal(map[string]any{"code": 200, "data": map[string]any{"status": tc.kbStatus, "doc_count": tc.remaining}})
				return testJSONResponse(tc.upstream, string(body)), nil
			})
			t.Cleanup(func() { http.DefaultTransport = transport })
			t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
			req := httptest.NewRequest("GET", "/api/core/datasets/deleting/deletion", nil)
			req = mux.SetURLVars(req, map[string]string{"dataset": "deleting"})
			req.Header.Set("X-User-Id", "owner")
			rec := httptest.NewRecorder()
			GetDatasetDeletion(rec, req)
			if rec.Code != tc.httpStatus {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if tc.httpStatus != 200 {
				return
			}
			var progress DatasetDeletionProgress
			if err := json.Unmarshal(rec.Body.Bytes(), &progress); err != nil {
				t.Fatal(err)
			}
			if progress.Status != tc.want {
				t.Fatalf("unexpected progress: %+v", progress)
			}
			if tc.want == "cleaning" && (progress.Total != 5 || progress.Completed != 3) {
				t.Fatalf("wrong counts: %+v", progress)
			}
			if tc.want == "completed" && progress.Completed != 5 {
				t.Fatalf("wrong completion: %+v", progress)
			}
			if tc.failed && progress.Failed != 1 {
				t.Fatalf("missing failure: %+v", progress)
			}
			for _, who := range []struct{ user, tenant string }{{"other", ""}, {"owner", "other-tenant"}, {"", ""}} {
				req.Header.Set("X-User-Id", who.user)
				req.Header.Set("X-Tenant-ID", who.tenant)
				denied := httptest.NewRecorder()
				GetDatasetDeletion(denied, req)
				if denied.Code != 404 {
					t.Fatalf("deletion leaked to %+v: %d %s", who, denied.Code, denied.Body.String())
				}
			}
		})
	}
}

func TestOfficialDeletionProgressUsesRequestTenantForLegacyDataset(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	seedDocumentListDataset(t, db, "ds-official", "user-1")
	if err := db.Model(&orm.Dataset{}).Where("id = ?", "ds-official").Update("tenant_id", "").Error; err != nil {
		t.Fatal(err)
	}
	seedMarketInstall(t, db, "law-cn", "user-1", "ds-official", string(orm.InstallStateDone))
	var calls []string
	original := http.DefaultTransport
	deletion := mockScanAndAlgoDelete(t, &calls)
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" && r.URL.Path == "/v1/kbs/kb-ds-official" {
			return testJSONResponse(200, `{"code":200,"data":{"status":"DELETING","doc_count":3}}`), nil
		}
		return deletion(r)
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
	req := httptest.NewRequest("DELETE", "/api/core/datasets/ds-official", nil)
	req = mux.SetURLVars(req, map[string]string{"dataset": "ds-official"})
	req.Header.Set("X-User-Id", "user-1")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	removed := httptest.NewRecorder()
	DeleteDataset(removed, req)
	if removed.Code != 200 {
		t.Fatalf("uninstall failed: %d %s", removed.Code, removed.Body.String())
	}
	req.Method = "GET"
	own := httptest.NewRecorder()
	GetDatasetDeletion(own, req)
	if own.Code != 200 {
		t.Fatalf("owner cannot inspect legacy official cleanup: %d %s", own.Code, own.Body.String())
	}
	var progress DatasetDeletionProgress
	if err := json.Unmarshal(own.Body.Bytes(), &progress); err != nil {
		t.Fatal(err)
	}
	if progress.Status != "cleaning" {
		t.Fatalf("unexpected progress: %+v", progress)
	}
	req.Header.Set("X-Tenant-ID", "tenant-b")
	other := httptest.NewRecorder()
	GetDatasetDeletion(other, req)
	if other.Code != 404 {
		t.Fatalf("other tenant can inspect cleanup: %d %s", other.Code, other.Body.String())
	}
}
