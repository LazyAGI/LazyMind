package source

import (
	"context"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type datasetUsageClientStub struct {
	usage map[string]coreclient.DatasetUsage
}

func (s datasetUsageClientStub) BatchGetDatasetUsage(_ context.Context, _ string, _ []string) (map[string]coreclient.DatasetUsage, error) {
	return s.usage, nil
}

func TestSortRecordsByDatasetUsageMostUsed(t *testing.T) {
	base := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	records := []store.SourceListRecord{
		{Source: store.Source{SourceID: "s-1", DatasetID: "ds-1", UpdatedAt: base}},
		{Source: store.Source{SourceID: "s-2", DatasetID: "ds-2", UpdatedAt: base.Add(-time.Hour)}},
		{Source: store.Source{SourceID: "s-3", DatasetID: "ds-3", UpdatedAt: base.Add(-2 * time.Hour)}},
	}
	engine := &DefaultEngine{datasetUsage: datasetUsageClientStub{usage: map[string]coreclient.DatasetUsage{
		"ds-1": {UsageCount: 1},
		"ds-2": {UsageCount: 5},
		"ds-3": {UsageCount: 10},
	}}}
	sorted, err := engine.sortRecordsByDatasetUsage(context.Background(), ListSourcesRequest{CallerID: "user-1", OrderBy: "most_used"}, records)
	if err != nil {
		t.Fatalf("sortRecordsByDatasetUsage returned error: %v", err)
	}
	if sorted[0].Source.SourceID != "s-3" || sorted[1].Source.SourceID != "s-2" || sorted[2].Source.SourceID != "s-1" {
		t.Fatalf("most_used order = %v", []string{sorted[0].Source.SourceID, sorted[1].Source.SourceID, sorted[2].Source.SourceID})
	}
}

func TestSortRecordsByDatasetUsageRecentUsed(t *testing.T) {
	base := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	lastOld := base.Add(-30 * time.Minute)
	lastNew := base.Add(-5 * time.Minute)
	records := []store.SourceListRecord{
		{Source: store.Source{SourceID: "s-1", DatasetID: "ds-1", UpdatedAt: base}},
		{Source: store.Source{SourceID: "s-2", DatasetID: "ds-2", UpdatedAt: base.Add(-time.Hour)}, LastSuccessAt: &lastOld},
		{Source: store.Source{SourceID: "s-3", DatasetID: "ds-3", UpdatedAt: base.Add(-2 * time.Hour)}, LastSuccessAt: &lastNew},
	}
	engine := &DefaultEngine{datasetUsage: datasetUsageClientStub{usage: map[string]coreclient.DatasetUsage{
		"ds-1": {UsageCount: 1, LastUsedAt: &lastOld},
		"ds-2": {UsageCount: 5, LastUsedAt: &lastOld},
		"ds-3": {UsageCount: 10, LastUsedAt: &lastNew},
	}}}
	sorted, err := engine.sortRecordsByDatasetUsage(context.Background(), ListSourcesRequest{CallerID: "user-1", OrderBy: "recent_used"}, records)
	if err != nil {
		t.Fatalf("sortRecordsByDatasetUsage returned error: %v", err)
	}
	if sorted[0].Source.SourceID != "s-1" || sorted[1].Source.SourceID != "s-3" || sorted[2].Source.SourceID != "s-2" {
		t.Fatalf("recent_used order = %v", []string{sorted[0].Source.SourceID, sorted[1].Source.SourceID, sorted[2].Source.SourceID})
	}
}

func TestNormalizeSourceOrderBy(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string
	}{
		{in: "latest_updated", want: "latest_updated"},
		{in: "  most_used ", want: "most_used"},
		{in: "recent_used", want: "recent_used"},
		{in: "updated_at desc", want: ""},
		{in: "", want: ""},
		{in: "garbage", want: ""},
	} {
		if got := normalizeSourceOrderBy(tt.in); got != tt.want {
			t.Fatalf("normalizeSourceOrderBy(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPaginateSourceRecords(t *testing.T) {
	records := make([]store.SourceListRecord, 5)
	for i := range records {
		records[i] = store.SourceListRecord{Source: store.Source{SourceID: string(rune('a' + i))}}
	}
	if got := paginateSourceRecords(records, 2, 2); len(got) != 2 || got[0].Source.SourceID != "c" || got[1].Source.SourceID != "d" {
		t.Fatalf("page 2 = %#v", got)
	}
	if got := paginateSourceRecords(records, 10, 2); got != nil {
		t.Fatalf("out-of-range page = %#v, want nil", got)
	}
}
