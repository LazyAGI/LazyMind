package doc

import (
	"context"
	"encoding/json"
	"errors"
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
