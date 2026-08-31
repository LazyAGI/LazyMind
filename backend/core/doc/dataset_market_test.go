package doc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

func seedMarketInstall(t *testing.T, db *orm.DB, marketItemID, userID, datasetID, state string) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Create(&orm.KnowledgeMarketInstall{
		MarketItemID:     marketItemID,
		UserID:           userID,
		InstalledVersion: "v1.0.0",
		DatasetID:        datasetID,
		InstallState:     state,
		InstalledAt:      &now,
		CreatedAt:        now,
		UpdatedAt:        now,
		Config:           json.RawMessage(`{}`),
	}).Error; err != nil {
		t.Fatalf("create market install %s/%s: %v", marketItemID, userID, err)
	}
}

func seedMarketInstallJob(t *testing.T, db *orm.DB, jobID, marketItemID, userID, status string) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Create(&orm.AsyncJob{
		ID:             jobID,
		JobType:        MarketInstallJobType,
		Status:         status,
		ResourceType:   "knowledge_market_item",
		ResourceID:     marketItemID,
		IdempotencyKey: "kb_install:" + marketItemID + ":" + userID,
		PayloadJSON:    json.RawMessage(`{"market_item_id":"` + marketItemID + `"}`),
		NextRunAt:      now,
		CreateUserID:   userID,
		CreateUserName: "Alice",
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create market install job %s: %v", jobID, err)
	}
}

func seedMarketUpdateJob(t *testing.T, db *orm.DB, jobID, marketItemID, userID, status string) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Create(&orm.AsyncJob{
		ID:             jobID,
		JobType:        MarketUpdateJobType,
		Status:         status,
		ResourceType:   "knowledge_market_item",
		ResourceID:     marketItemID,
		IdempotencyKey: "kb_update:" + marketItemID + ":" + userID,
		PayloadJSON:    json.RawMessage(`{"market_item_id":"` + marketItemID + `"}`),
		NextRunAt:      now,
		CreateUserID:   userID,
		CreateUserName: "Alice",
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create market update job %s: %v", jobID, err)
	}
}

func countMarketInstalls(t *testing.T, db *orm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&orm.KnowledgeMarketInstall{}).Count(&n).Error; err != nil {
		t.Fatalf("count market installs: %v", err)
	}
	return n
}

func countMarketInstallJobs(t *testing.T, db *orm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&orm.AsyncJob{}).Where("job_type = ?", MarketInstallJobType).Count(&n).Error; err != nil {
		t.Fatalf("count market install jobs: %v", err)
	}
	return n
}

func deleteDatasetRequest(t *testing.T, datasetID, userID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/core/datasets/"+datasetID, nil)
	req = mux.SetURLVars(req, map[string]string{"dataset": datasetID})
	req.Header.Set("X-User-Id", userID)
	rec := httptest.NewRecorder()
	DeleteDataset(rec, req)
	return rec
}

// mockScanAndAlgoDelete stubs the scan-control-plane access check and records
// the external delete calls so tests can assert order and absence.
func mockScanAndAlgoDelete(t *testing.T, calls *[]string) func(*http.Request) (*http.Response, error) {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/scan/internal/source-access/by-dataset:batch" {
			return testJSONResponse(http.StatusOK, `{"items":[{"dataset_id":"ds-official","exists":false,"allowed":true}]}`), nil
		}
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected method %s for %q", r.Method, r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
		*calls = append(*calls, r.URL.Path)
		switch r.URL.Path {
		case "/api/scan/internal/sources/by-dataset/ds-official", "/v1/kbs/kb-ds-official":
			return testJSONResponse(http.StatusOK, `{}`), nil
		default:
			t.Errorf("unexpected delete path %q", r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
	}
}

func TestDeleteOfficialDatasetResetsMarketInstall(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatalf("auto migrate async jobs: %v", err)
	}
	seedDocumentListDataset(t, db, "ds-official", "user-1")
	seedMarketInstall(t, db, "law-cn", "user-1", "ds-official", string(orm.InstallStateDone))
	seedMarketInstallJob(t, db, "job-old", "law-cn", "user-1", "succeeded")

	var calls []string
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(mockScanAndAlgoDelete(t, &calls))
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")

	rec := deleteDatasetRequest(t, "ds-official", "user-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// The official path skips the scan source delete and only removes the KB.
	if got := strings.Join(calls, ","); got != "/v1/kbs/kb-ds-official" {
		t.Fatalf("unexpected external delete calls: %q", got)
	}
	var row orm.Dataset
	if err := db.First(&row, "id = ?", "ds-official").Error; err != nil {
		t.Fatalf("query deleted dataset: %v", err)
	}
	if row.DeletedAt == nil {
		t.Fatalf("expected dataset to be soft deleted")
	}
	if n := countMarketInstalls(t, db); n != 0 {
		t.Fatalf("expected install record cleared, got %d rows", n)
	}
	if n := countMarketInstallJobs(t, db); n != 0 {
		t.Fatalf("expected install jobs cleared, got %d rows", n)
	}
}

func TestDeleteOfficialDatasetConflictsWhileInstalling(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatalf("auto migrate async jobs: %v", err)
	}
	seedDocumentListDataset(t, db, "ds-official", "user-1")
	seedMarketInstall(t, db, "law-cn", "user-1", "ds-official", string(orm.InstallStateImporting))
	seedMarketInstallJob(t, db, "job-running", "law-cn", "user-1", "running")

	var calls []string
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(mockScanAndAlgoDelete(t, &calls))
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")

	rec := deleteDatasetRequest(t, "ds-official", "user-1")
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(calls) != 0 {
		t.Fatalf("expected no external delete calls while installing, got %q", strings.Join(calls, ","))
	}
	var row orm.Dataset
	if err := db.First(&row, "id = ?", "ds-official").Error; err != nil {
		t.Fatalf("query dataset: %v", err)
	}
	if row.DeletedAt != nil {
		t.Fatalf("expected dataset not deleted while installing")
	}
	if n := countMarketInstalls(t, db); n != 1 {
		t.Fatalf("expected install record kept, got %d rows", n)
	}
	if n := countMarketInstallJobs(t, db); n != 1 {
		t.Fatalf("expected install job kept, got %d rows", n)
	}
}

func TestDeleteOfficialDatasetConflictsWhileUpdating(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatalf("auto migrate async jobs: %v", err)
	}
	seedDocumentListDataset(t, db, "ds-official", "user-1")
	seedMarketInstall(t, db, "law-cn", "user-1", "ds-official", string(orm.InstallStateDone))
	seedMarketUpdateJob(t, db, "job-update", "law-cn", "user-1", "running")

	var calls []string
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(mockScanAndAlgoDelete(t, &calls))
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")

	rec := deleteDatasetRequest(t, "ds-official", "user-1")
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(calls) != 0 {
		t.Fatalf("expected no external delete calls while updating, got %q", strings.Join(calls, ","))
	}
}

func TestDeleteOfficialDatasetStuckStateWithoutActiveJobIsAllowed(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatalf("auto migrate async jobs: %v", err)
	}
	// A stale install_state left by an external failure (no active async job)
	// must not block the uninstall: the delete proceeds and resets the install
	// row plus its install/update jobs.
	seedDocumentListDataset(t, db, "ds-official", "user-1")
	seedMarketInstall(t, db, "law-cn", "user-1", "ds-official", string(orm.InstallStateImporting))
	seedMarketInstallJob(t, db, "job-stale", "law-cn", "user-1", "failed")

	var calls []string
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(mockScanAndAlgoDelete(t, &calls))
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")

	rec := deleteDatasetRequest(t, "ds-official", "user-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var row orm.Dataset
	if err := db.First(&row, "id = ?", "ds-official").Error; err != nil {
		t.Fatalf("query deleted dataset: %v", err)
	}
	if row.DeletedAt == nil {
		t.Fatalf("expected dataset to be soft deleted")
	}
	if n := countMarketInstalls(t, db); n != 0 {
		t.Fatalf("expected install record cleared, got %d rows", n)
	}
	if n := countMarketInstallJobs(t, db); n != 0 {
		t.Fatalf("expected stale install jobs cleared, got %d rows", n)
	}
}

func TestDeleteOfficialDatasetReleasesReinstallIdempotencyKey(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatalf("auto migrate async jobs: %v", err)
	}
	seedDocumentListDataset(t, db, "ds-official", "user-1")
	seedMarketInstall(t, db, "law-cn", "user-1", "ds-official", string(orm.InstallStateDone))
	seedMarketInstallJob(t, db, "job-old", "law-cn", "user-1", "succeeded")

	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(mockScanAndAlgoDelete(t, &[]string{}))
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")

	rec := deleteDatasetRequest(t, "ds-official", "user-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// A fresh install with the same idempotency key must create a new job
	// instead of reusing the deleted succeeded one.
	job, err := asyncjob.Enqueue(context.Background(), db.DB, asyncjob.EnqueueRequest{
		JobType:        MarketInstallJobType,
		ResourceType:   "knowledge_market_item",
		ResourceID:     "law-cn",
		IdempotencyKey: "kb_install:law-cn:user-1",
		Payload:        map[string]any{"market_item_id": "law-cn"},
		MaxAttempts:    2,
		CreateUserID:   "user-1",
		CreateUserName: "Alice",
	})
	if err != nil {
		t.Fatalf("enqueue reinstall job: %v", err)
	}
	if job.ID == "job-old" {
		t.Fatalf("expected a fresh job, got the stale succeeded job %q", job.ID)
	}
	if job.Status != "pending" {
		t.Fatalf("expected new job status pending, got %q", job.Status)
	}
	if n := countMarketInstallJobs(t, db); n != 1 {
		t.Fatalf("expected exactly one install job after reinstall, got %d", n)
	}
}

func TestDeleteRegularDatasetKeepsMarketInstallUntouched(t *testing.T) {
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "ds-local", "user-1")
	seedMarketInstall(t, db, "law-cn", "user-1", "ds-other", string(orm.InstallStateDone))

	var calls []string
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/scan/internal/source-access/by-dataset:batch" {
			return testJSONResponse(http.StatusOK, `{"items":[{"dataset_id":"ds-local","exists":false,"allowed":true}]}`), nil
		}
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected method %s for %q", r.Method, r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
		calls = append(calls, r.URL.Path)
		switch r.URL.Path {
		case "/api/scan/internal/sources/by-dataset/ds-local", "/v1/kbs/kb-ds-local":
			return testJSONResponse(http.StatusOK, `{}`), nil
		default:
			t.Errorf("unexpected delete path %q", r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")

	rec := deleteDatasetRequest(t, "ds-local", "user-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := strings.Join(calls, ","); got != "/api/scan/internal/sources/by-dataset/ds-local,/v1/kbs/kb-ds-local" {
		t.Fatalf("unexpected external delete calls: %q", got)
	}
	if n := countMarketInstalls(t, db); n != 1 {
		t.Fatalf("expected unrelated market install kept, got %d rows", n)
	}
}

func TestListDatasetsSourceFilters(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.KnowledgeMarketInstall{}); err != nil {
		t.Fatalf("auto migrate knowledge market installs: %v", err)
	}
	seedDocumentListDataset(t, db, "ds-local", "user-1")
	seedDocumentListDataset(t, db, "ds-cloud", "user-1")
	seedDocumentListDataset(t, db, "ds-official", "user-1")
	seedMarketInstall(t, db, "law-cn", "user-1", "ds-official", string(orm.InstallStateDone))

	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
		switch r.URL.Path {
		case "/api/scan/internal/source-access/by-dataset:batch":
			return testJSONResponse(http.StatusOK, `{"items":[{"dataset_id":"ds-local","exists":false,"allowed":true},{"dataset_id":"ds-cloud","source_id":"source-1","exists":true,"allowed":true},{"dataset_id":"ds-official","exists":false,"allowed":true}]}`), nil
		case "/api/scan/internal/sources/by-datasets":
			return testJSONResponse(http.StatusOK, `{"source_map":{"ds-local":false,"ds-cloud":true,"ds-official":false}}`), nil
		default:
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")

	cases := []struct {
		source string
		want   []string
	}{
		{source: "", want: []string{"ds-local", "ds-cloud", "ds-official"}},
		// local_upload is not a supported filter value; like any unknown
		// value it is ignored and every source is returned.
		{source: "local_upload", want: []string{"ds-local", "ds-cloud", "ds-official"}},
		{source: "manual", want: []string{"ds-local"}},
		{source: "cloud", want: []string{"ds-cloud"}},
		{source: "official_installed", want: []string{"ds-official"}},
		{source: "unknown", want: []string{"ds-local", "ds-cloud", "ds-official"}},
	}
	for _, tc := range cases {
		t.Run("source="+tc.source, func(t *testing.T) {
			url := "/api/core/datasets?page_size=10"
			if tc.source != "" {
				url += "&source=" + tc.source
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			req.Header.Set("X-User-Id", "user-1")
			rec := httptest.NewRecorder()

			ListDatasets(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
			}
			var resp ListDatasetsResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			got := make([]string, 0, len(resp.Datasets))
			for _, d := range resp.Datasets {
				got = append(got, d.DatasetID)
				// The response source_type must match the requested source
				// filter value for every returned dataset.
				switch tc.source {
				case "manual", "cloud", "official_installed":
					if d.SourceType != tc.source {
						t.Fatalf("source=%q dataset %s source_type = %q, want %q", tc.source, d.DatasetID, d.SourceType, tc.source)
					}
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("source=%q got %v, want %v", tc.source, got, tc.want)
			}
		})
	}
}

func TestListDatasetsReturnsSourceType(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.KnowledgeMarketInstall{}); err != nil {
		t.Fatalf("auto migrate knowledge market installs: %v", err)
	}
	seedDocumentListDataset(t, db, "ds-local", "user-1")
	seedDocumentListDataset(t, db, "ds-cloud", "user-1")
	seedDocumentListDataset(t, db, "ds-official", "user-1")
	seedMarketInstall(t, db, "law-cn", "user-1", "ds-official", string(orm.InstallStateDone))

	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/scan/internal/source-access/by-dataset:batch" {
			return testJSONResponse(http.StatusOK, `{"items":[{"dataset_id":"ds-local","exists":false,"allowed":true},{"dataset_id":"ds-cloud","source_id":"source-1","exists":true,"allowed":true},{"dataset_id":"ds-official","exists":false,"allowed":true}]}`), nil
		}
		if r.URL.Path == "/api/scan/internal/sources/by-datasets" {
			return testJSONResponse(http.StatusOK, `{"source_map":{"ds-local":false,"ds-cloud":true,"ds-official":false}}`), nil
		}
		return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")

	req := httptest.NewRequest(http.MethodGet, "/api/core/datasets?page_size=10", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()

	ListDatasets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp ListDatasetsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := map[string]struct {
		sourceType       string
		createdByDataSrc bool
	}{
		"ds-local":    {sourceType: datasetSourceManual},
		"ds-cloud":    {sourceType: datasetSourceCloud, createdByDataSrc: true},
		"ds-official": {sourceType: datasetSourceOfficialInstalled},
	}
	if len(resp.Datasets) != len(want) {
		t.Fatalf("expected %d datasets, got %d", len(want), len(resp.Datasets))
	}
	for _, d := range resp.Datasets {
		w, ok := want[d.DatasetID]
		if !ok {
			t.Fatalf("unexpected dataset %q", d.DatasetID)
		}
		if d.SourceType != w.sourceType {
			t.Fatalf("dataset %s source_type = %q, want %q", d.DatasetID, d.SourceType, w.sourceType)
		}
		if d.CreatedByDataSource == nil || *d.CreatedByDataSource != w.createdByDataSrc {
			t.Fatalf("dataset %s created_by_data_source = %v, want %v", d.DatasetID, d.CreatedByDataSource, w.createdByDataSrc)
		}
	}
}

func TestListDatasetsOfficialInstalledScopedByUser(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.KnowledgeMarketInstall{}); err != nil {
		t.Fatalf("auto migrate knowledge market installs: %v", err)
	}
	seedDocumentListDataset(t, db, "ds-official", "user-2")
	seedMarketInstall(t, db, "law-cn", "user-2", "ds-official", string(orm.InstallStateDone))

	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return testJSONResponse(http.StatusOK, `{"source_map":{}}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")

	req := httptest.NewRequest(http.MethodGet, "/api/core/datasets?page_size=10&source=official_installed", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()

	ListDatasets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp ListDatasetsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.TotalSize != 0 || len(resp.Datasets) != 0 {
		t.Fatalf("expected no official datasets for other user, got total=%d datasets=%+v", resp.TotalSize, resp.Datasets)
	}
}

func TestDeleteMarketDatasetRemovesResidualDataset(t *testing.T) {
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "ds-residual", "user-1")

	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/kbs/kb-ds-residual" {
			t.Errorf("unexpected request %s %q", r.Method, r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
		return testJSONResponse(http.StatusOK, `{}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")

	if err := DeleteMarketDataset(context.Background(), "user-1", "ds-residual"); err != nil {
		t.Fatalf("delete residual dataset: %v", err)
	}
	var ds orm.Dataset
	if err := db.Where("id = ?", "ds-residual").Take(&ds).Error; err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	if ds.DeletedAt == nil {
		t.Fatal("expected dataset to be soft deleted")
	}
}

func TestDeleteMarketDatasetRefusesOtherOwner(t *testing.T) {
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "ds-residual", "user-1")

	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request %s %q", r.Method, r.URL.Path)
		return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")

	if err := DeleteMarketDataset(context.Background(), "user-2", "ds-residual"); err == nil {
		t.Fatal("expected error for non-owner delete")
	}
}

func TestDeleteMarketDatasetNoopWhenMissing(t *testing.T) {
	newDocumentTestDB(t)
	if err := DeleteMarketDataset(context.Background(), "user-1", "ds-not-exist"); err != nil {
		t.Fatalf("expected no-op, got %v", err)
	}
}

func TestCreateMarketDatasetSendsDefaultAlgo(t *testing.T) {
	db := newDocumentTestDB(t)

	var createdKbBody struct {
		AlgoID string `json:"algo_id"`
	}
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/kbs":
			if err := json.NewDecoder(r.Body).Decode(&createdKbBody); err != nil {
				return testJSONResponse(http.StatusBadRequest, `{"message":"bad body"}`), nil
			}
			return testJSONResponse(http.StatusOK, `{"kb_id":"kb-ds-new"}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/v1/algo/general_algo/groups":
			return testJSONResponse(http.StatusOK, `{"code":200,"msg":"success","data":[{"name":"Chunk","type":"Chunk","display_name":"Chunk","active":true}]}`), nil
		default:
			t.Errorf("unexpected request %s %q", r.Method, r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")

	ds, err := CreateMarketDataset(context.Background(), "user-1", "User One", "ds-new", "Official KB", "desc", []string{"tag"})
	if err != nil {
		t.Fatalf("create market dataset: %v", err)
	}
	if ds.ID != "ds-new" {
		t.Fatalf("dataset id = %q, want ds-new", ds.ID)
	}
	if createdKbBody.AlgoID != defaultMarketAlgoID {
		t.Fatalf("kb request algo_id = %q, want %q", createdKbBody.AlgoID, defaultMarketAlgoID)
	}

	var ext struct {
		AlgoID   string         `json:"algo_id"`
		AlgoName string         `json:"algo_name"`
		Parsers  []ParserConfig `json:"parsers"`
	}
	if err := json.Unmarshal(ds.Ext, &ext); err != nil {
		t.Fatalf("unmarshal ext: %v", err)
	}
	if ext.AlgoID != defaultMarketAlgoID || ext.AlgoName != defaultMarketAlgoName {
		t.Fatalf("ext algo = %q/%q, want %q/%q", ext.AlgoID, ext.AlgoName, defaultMarketAlgoID, defaultMarketAlgoName)
	}
	if len(ext.Parsers) != 1 || ext.Parsers[0].Type != "PARSE_TYPE_SPLIT" {
		t.Fatalf("unexpected parsers: %+v", ext.Parsers)
	}

	var persisted orm.Dataset
	if err := db.Where("id = ?", "ds-new").Take(&persisted).Error; err != nil {
		t.Fatalf("load persisted dataset: %v", err)
	}
	if persisted.KbID != "kb-ds-new" {
		t.Fatalf("kb_id = %q, want kb-ds-new", persisted.KbID)
	}
}

// TestCreateMarketDatasetUsesUniqueKBDisplayName verifies that the algo-side KB
// display name of an official install carries a unique dataset suffix, so a
// reinstall never collides with the residual row left by a previous uninstall
// (the algo service keeps a global unique index on display_name), while the
// core-side display_name the user sees stays unchanged.
func TestCreateMarketDatasetUsesUniqueKBDisplayName(t *testing.T) {
	newDocumentTestDB(t)

	var createdKbBody struct {
		DisplayName string `json:"display_name"`
		KbID        string `json:"kb_id"`
	}
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/kbs":
			if err := json.NewDecoder(r.Body).Decode(&createdKbBody); err != nil {
				return testJSONResponse(http.StatusBadRequest, `{"message":"bad body"}`), nil
			}
			return testJSONResponse(http.StatusOK, `{"kb_id":"kb-market-1"}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/v1/algo/general_algo/groups":
			return testJSONResponse(http.StatusOK, `{"code":200,"msg":"success","data":[]}`), nil
		default:
			t.Errorf("unexpected request %s %q", r.Method, r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")

	ds, err := CreateMarketDataset(context.Background(), "user-1", "User One", "ds-market-1", "官方知识库", "desc", []string{"法律"})
	if err != nil {
		t.Fatalf("create market dataset: %v", err)
	}
	want := "user@user-1@官方知识库__ds-market-1"
	if createdKbBody.DisplayName != want {
		t.Fatalf("algo display_name = %q, want %q", createdKbBody.DisplayName, want)
	}
	if createdKbBody.KbID != "ds-market-1" {
		t.Fatalf("kb_id = %q, want ds-market-1", createdKbBody.KbID)
	}
	if ds.DisplayName != "官方知识库" {
		t.Fatalf("core display_name changed to %q", ds.DisplayName)
	}
}

// TestMarketKBDisplayNameSuffixIsStableForSameDataset verifies the unique
// suffix is derived from the dataset id, so a retry that reuses the same
// dataset id produces the same algo-side name (idempotent reinstall).
func TestMarketKBDisplayNameSuffixIsStableForSameDataset(t *testing.T) {
	a := marketKBDisplayName("user-1", "官方知识库", "ds-x")
	b := marketKBDisplayName("user-1", "官方知识库", "ds-x")
	c := marketKBDisplayName("user-1", "官方知识库", "ds-y")
	if a != b {
		t.Fatalf("same dataset must produce the same name: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("different datasets must produce different names")
	}
	if !strings.HasPrefix(a, "user@user-1@官方知识库__") {
		t.Fatalf("unexpected name %q", a)
	}
}
