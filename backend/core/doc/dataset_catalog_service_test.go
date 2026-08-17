package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"lazymind/core/common/orm"

	"gorm.io/gorm"
)

func TestDatasetCatalogServiceListFiltersStatsAndPaginates(t *testing.T) {
	db := newDocumentTestDB(t)
	installDatasetCatalogScanTransport(t)
	service := mustDatasetCatalogService(t, db.DB)

	base := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	seedDatasetCatalogDataset(t, db, "ds-new", "user-1", "Alpha Docs", "Runbook", []string{"api", "team"}, base)
	seedDatasetCatalogDataset(t, db, "ds-mid", "user-1", "Beta", "alpha notes", []string{"api", "team"}, base.Add(-time.Hour))
	seedDatasetCatalogDataset(t, db, "ds-old", "user-1", "Gamma", "alpha notes", []string{"api", "team"}, base.Add(-2*time.Hour))
	seedDatasetCatalogDataset(t, db, "ds-tag-miss", "user-1", "Alpha Missing Tag", "", []string{"api"}, base.Add(time.Hour))
	seedDatasetCatalogDocument(t, db, "doc-a", "ds-new", "report.pdf", 11)
	seedDatasetCatalogDocument(t, db, "doc-folder", "ds-new", "folder", 99)

	first, err := service.ListDatasets(context.Background(), DatasetListRequest{
		UserID:  "user-1",
		Keyword: " alpha ",
		Tags:    []string{"team"},
		Limit:   2,
	})
	if err != nil {
		t.Fatalf("ListDatasets first returned error: %v", err)
	}
	if len(first.Datasets) != 2 || first.Datasets[0].DatasetID != "ds-new" || first.Datasets[1].DatasetID != "ds-mid" {
		t.Fatalf("first datasets = %#v, want ds-new/ds-mid", first.Datasets)
	}
	if first.Datasets[0].DocumentCount != 1 || first.Datasets[0].DocumentSize != 11 {
		t.Fatalf("stats count=%d size=%d, want 1/11", first.Datasets[0].DocumentCount, first.Datasets[0].DocumentSize)
	}
	if !first.HasMore || first.NextOffset != 2 {
		t.Fatalf("page = hasMore %v offset %d, want true/2", first.HasMore, first.NextOffset)
	}

	second, err := service.ListDatasets(context.Background(), DatasetListRequest{
		UserID:  "user-1",
		Keyword: "alpha",
		Tags:    []string{"team"},
		Offset:  2,
		Limit:   2,
	})
	if err != nil {
		t.Fatalf("ListDatasets second returned error: %v", err)
	}
	if len(second.Datasets) != 1 || second.Datasets[0].DatasetID != "ds-old" {
		t.Fatalf("second datasets = %#v, want ds-old", second.Datasets)
	}
	if second.HasMore {
		t.Fatalf("second page HasMore = true, want false")
	}
}

func TestDatasetCatalogServiceListScansPastInvisibleFirstBatch(t *testing.T) {
	db := newDocumentTestDB(t)
	installDatasetCatalogScanTransport(t)
	service := mustDatasetCatalogService(t, db.DB)

	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 510; i++ {
		id := fmt.Sprintf("ds-hidden-%03d", i)
		seedDatasetCatalogDataset(t, db, id, "user-hidden", "Hidden", "", nil, base.Add(time.Duration(510-i)*time.Second))
	}
	seedDatasetCatalogDataset(t, db, "ds-visible-1", "user-1", "Visible 1", "", nil, base.Add(-time.Minute))
	seedDatasetCatalogDataset(t, db, "ds-visible-2", "user-1", "Visible 2", "", nil, base.Add(-2*time.Minute))

	got, err := service.ListDatasets(context.Background(), DatasetListRequest{
		UserID: "user-1",
		Limit:  100,
	})
	if err != nil {
		t.Fatalf("ListDatasets returned error: %v", err)
	}
	if len(got.Datasets) != 2 || got.Datasets[0].DatasetID != "ds-visible-1" || got.Datasets[1].DatasetID != "ds-visible-2" {
		t.Fatalf("datasets = %#v, want visible datasets after hidden first batch", got.Datasets)
	}
	if got.TotalSize != 2 || got.HasMore {
		t.Fatalf("total/hasMore = %d/%v, want 2/false", got.TotalSize, got.HasMore)
	}
}

func TestDatasetCatalogServiceListNextPageNoDuplicatesAndNoFinalToken(t *testing.T) {
	db := newDocumentTestDB(t)
	installDatasetCatalogScanTransport(t)
	service := mustDatasetCatalogService(t, db.DB)

	base := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		seedDatasetCatalogDataset(t, db, fmt.Sprintf("ds-visible-%d", i+1), "user-1", "Visible", "", nil, base.Add(-time.Duration(i)*time.Minute))
	}

	first, err := service.ListDatasets(context.Background(), DatasetListRequest{UserID: "user-1", Limit: 2})
	if err != nil {
		t.Fatalf("first ListDatasets returned error: %v", err)
	}
	if len(first.Datasets) != 2 || !first.HasMore || first.NextOffset != 2 {
		t.Fatalf("first page = len %d hasMore %v offset %d, want 2/true/2", len(first.Datasets), first.HasMore, first.NextOffset)
	}

	second, err := service.ListDatasets(context.Background(), DatasetListRequest{UserID: "user-1", Offset: first.NextOffset, Limit: 2})
	if err != nil {
		t.Fatalf("second ListDatasets returned error: %v", err)
	}
	if len(second.Datasets) != 1 || second.Datasets[0].DatasetID != "ds-visible-3" {
		t.Fatalf("second datasets = %#v, want ds-visible-3", second.Datasets)
	}
	if second.HasMore {
		t.Fatalf("second HasMore = true, want false")
	}
	seen := map[string]bool{}
	for _, ds := range first.Datasets {
		seen[ds.DatasetID] = true
	}
	if seen[second.Datasets[0].DatasetID] {
		t.Fatalf("second page repeated first page dataset %q", second.Datasets[0].DatasetID)
	}
}

func TestDatasetCatalogServiceListPageSizeBoundaries(t *testing.T) {
	db := newDocumentTestDB(t)
	installDatasetCatalogScanTransport(t)
	service := mustDatasetCatalogService(t, db.DB)

	base := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 120; i++ {
		seedDatasetCatalogDataset(t, db, fmt.Sprintf("ds-boundary-%03d", i), "user-1", "Boundary", "", nil, base.Add(-time.Duration(i)*time.Second))
	}

	defaulted, err := service.ListDatasets(context.Background(), DatasetListRequest{UserID: "user-1", Limit: 0})
	if err != nil {
		t.Fatalf("default ListDatasets returned error: %v", err)
	}
	if len(defaulted.Datasets) != 20 || defaulted.TotalSize != 120 || !defaulted.HasMore {
		t.Fatalf("default page len/total/hasMore = %d/%d/%v, want 20/120/true", len(defaulted.Datasets), defaulted.TotalSize, defaulted.HasMore)
	}

	maxed, err := service.ListDatasets(context.Background(), DatasetListRequest{UserID: "user-1", Limit: 500})
	if err != nil {
		t.Fatalf("max ListDatasets returned error: %v", err)
	}
	if len(maxed.Datasets) != 100 || maxed.TotalSize != 120 || !maxed.HasMore || maxed.NextOffset != 100 {
		t.Fatalf("max page len/total/hasMore/offset = %d/%d/%v/%d, want 100/120/true/100", len(maxed.Datasets), maxed.TotalSize, maxed.HasMore, maxed.NextOffset)
	}
}

func TestDatasetCatalogServiceGetUserIsolation(t *testing.T) {
	db := newDocumentTestDB(t)
	installDatasetCatalogScanTransport(t)
	service := mustDatasetCatalogService(t, db.DB)
	seedDatasetCatalogDataset(t, db, "ds-private", "user-1", "Private", "", nil, time.Now().UTC())

	got, err := service.GetDataset(context.Background(), DatasetGetRequest{UserID: "user-1", DatasetID: "ds-private"})
	if err != nil {
		t.Fatalf("GetDataset owner returned error: %v", err)
	}
	if got.DatasetID != "ds-private" {
		t.Fatalf("DatasetID = %q, want ds-private", got.DatasetID)
	}

	_, err = service.GetDataset(context.Background(), DatasetGetRequest{UserID: "user-2", DatasetID: "ds-private"})
	if codeOfDatasetServiceError(err) != DatasetServiceForbidden {
		t.Fatalf("isolated Get error = %v, want forbidden", err)
	}
}

func installDatasetCatalogScanTransport(t *testing.T) {
	t.Helper()
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/scan/internal/source-access/by-dataset:batch":
			return testJSONResponse(http.StatusOK, `{"items":[]}`), nil
		case "/api/scan/internal/sources/by-datasets":
			return testJSONResponse(http.StatusOK, `{"source_map":{}}`), nil
		default:
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
}

func seedDatasetCatalogDataset(t *testing.T, db *orm.DB, id, userID, name, desc string, tags []string, updatedAt time.Time) {
	t.Helper()
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	ext, err := json.Marshal(map[string]any{"tags": tags})
	if err != nil {
		t.Fatalf("marshal ext: %v", err)
	}
	if err := db.Create(&orm.Dataset{
		ID:           id,
		KbID:         "kb-" + id,
		DisplayName:  name,
		Desc:         desc,
		DatasetState: 0,
		ShareType:    0,
		Type:         1,
		Ext:          ext,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID,
			CreatedAt:      updatedAt,
			UpdatedAt:      updatedAt,
		},
	}).Error; err != nil {
		t.Fatalf("create dataset %s: %v", id, err)
	}
}

func seedDatasetCatalogDocument(t *testing.T, db *orm.DB, id, datasetID, displayName string, fileSize int64) {
	t.Helper()
	ext, err := json.Marshal(map[string]any{"file_size": fileSize})
	if err != nil {
		t.Fatalf("marshal document ext: %v", err)
	}
	if err := db.Create(&orm.Document{
		ID:          id,
		DatasetID:   datasetID,
		DisplayName: displayName,
		Tags:        []byte(`[]`),
		Ext:         ext,
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-1",
			CreateUserName: "user-1",
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		},
	}).Error; err != nil {
		t.Fatalf("create document %s: %v", id, err)
	}
}

func mustDatasetCatalogService(t *testing.T, db *gorm.DB) *DatasetCatalogService {
	t.Helper()
	service, err := NewDatasetCatalogService(DatasetCatalogServiceDeps{DB: db})
	if err != nil {
		t.Fatalf("NewDatasetCatalogService: %v", err)
	}
	return service
}

func codeOfDatasetServiceError(err error) DatasetServiceErrorCode {
	var svcErr *DatasetServiceError
	if errors.As(err, &svcErr) {
		return svcErr.Code
	}
	return ""
}
