package doc

import (
	"context"
	"encoding/json"
	"lazymind/core/common/orm"
	"testing"
	"time"
)

func TestCatalogManagementFiltersBeforePagination(t *testing.T) {
	db := newDocumentTestDB(t)
	installDatasetCatalogScanTransport(t)
	for _, item := range []struct{ id, name, tenant string }{
		{"a", "Papers extra", "t1"}, {"b", "Papers", "t2"}, {"c", "Papers", "t1"}, {"d", "Papers", "t1"},
	} {
		seedDatasetCatalogDataset(t, db, item.id, "u", item.name, "", nil, time.Now())
		if err := db.Model(&orm.Dataset{}).Where("id = ?", item.id).Update("tenant_id", item.tenant).Error; err != nil {
			t.Fatal(err)
		}
	}
	s := mustDatasetCatalogService(t, db.DB)
	got, err := s.ListDatasets(context.Background(), DatasetListRequest{UserID: "u", Keyword: "Papers", Match: "exact", DatasetIDs: []string{"a", "b", "c"}, RequiredAction: "upload", Limit: 1, Caller: DatasetCatalogCaller{UserID: "u", TenantID: "t1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalSize != 1 || got.HasMore || len(got.Datasets) != 1 || got.Datasets[0].DatasetID != "c" {
		t.Fatalf("unexpected page: %+v", got)
	}
	if _, err = s.GetDataset(context.Background(), DatasetGetRequest{UserID: "u", DatasetID: "b", Caller: DatasetCatalogCaller{UserID: "u", TenantID: "t1"}}); err == nil {
		t.Fatal("cross-tenant dataset disclosed")
	}
}

func TestCatalogRejectsConflictingCallerIdentity(t *testing.T) {
	db := newDocumentTestDB(t)
	installDatasetCatalogScanTransport(t)
	seedDatasetCatalogDataset(t, db, "a", "owner", "Private", "", nil, time.Now())
	s := mustDatasetCatalogService(t, db.DB)
	_, err := s.ListDatasets(context.Background(), DatasetListRequest{UserID: "owner", Caller: DatasetCatalogCaller{UserID: "other"}})
	if err == nil {
		t.Fatal("caller identity did not constrain catalog")
	}
}

func TestDatasetStatisticsUseExplicitResourceType(t *testing.T) {
	db := newDocumentTestDB(t)
	rows := []orm.Document{
		{ID: "folder", DatasetID: "kb", DisplayName: "release.v1", DocumentType: "FOLDER", Ext: json.RawMessage(`{"file_size":1000}`)},
		{ID: "document", DatasetID: "kb", DisplayName: "README", DocumentType: "FILE", Ext: json.RawMessage(`{"file_size":17}`)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, stats := range []datasetStats{calcDatasetStatsWithDB(context.Background(), db.DB, "kb"), calcDatasetStatsBatchWithDB(context.Background(), db.DB, []string{"kb"})["kb"]} {
		if stats.DocumentCount != 1 || stats.DocumentSize != 17 {
			t.Fatalf("types lost in stats projection: %+v", stats)
		}
	}
}
