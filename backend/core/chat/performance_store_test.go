package chat

import (
	"context"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func metricInt64(value int64) *int64 { return &value }

func TestPersistRunPerformancePreservesKnownZeroAndUnknown(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/performance.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.ChatRunPerformance{}); err != nil {
		t.Fatalf("migrate performance table: %v", err)
	}

	err = persistRunPerformance(context.Background(), db.DB, runPerformanceRecord{
		RunID: "run-1", ConversationID: "conv-1", HistoryID: "history-1", UserID: "user-1",
		Status: "completed", ObservedAt: time.Unix(100, 0).UTC(),
		Metrics: &RunPerformanceMetrics{
			SchemaVersion:    1,
			ModelSteps:       1,
			ToolSteps:        0,
			ToolMS:           metricInt64(0),
			InputTokens:      metricInt64(100),
			CachedTokens:     metricInt64(0),
			CacheInputTokens: metricInt64(100),
		},
	})
	if err != nil {
		t.Fatalf("persist performance: %v", err)
	}

	var stored orm.ChatRunPerformance
	if err := db.Where("run_id = ?", "run-1").Take(&stored).Error; err != nil {
		t.Fatalf("load performance row: %v", err)
	}
	if stored.ToolMS == nil || *stored.ToolMS != 0 {
		t.Fatalf("known zero tool_ms was not preserved: %#v", stored.ToolMS)
	}
	if stored.ModelMS != nil || stored.OutputTokens != nil {
		t.Fatalf("unknown facts were not stored as NULL: model_ms=%#v output_tokens=%#v", stored.ModelMS, stored.OutputTokens)
	}
	if stored.InputTokens == nil || *stored.InputTokens != 100 || stored.CachedTokens == nil || *stored.CachedTokens != 0 ||
		stored.CacheInputTokens == nil || *stored.CacheInputTokens != 100 {
		t.Fatalf("unexpected token facts: %#v", stored)
	}
}

func TestPersistRunPerformanceUpsertsByRunID(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/performance-upsert.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.ChatRunPerformance{}); err != nil {
		t.Fatalf("migrate performance table: %v", err)
	}

	base := runPerformanceRecord{
		RunID: "run-1", ConversationID: "conv-1", HistoryID: "history-1", UserID: "user-1",
		Status: "completed", ObservedAt: time.Unix(100, 0).UTC(),
		Metrics: &RunPerformanceMetrics{SchemaVersion: 1, ModelSteps: 1, ModelMS: metricInt64(900)},
	}
	if err := persistRunPerformance(context.Background(), db.DB, base); err != nil {
		t.Fatalf("persist initial performance: %v", err)
	}
	base.Metrics.ModelMS = metricInt64(800)
	if err := persistRunPerformance(context.Background(), db.DB, base); err != nil {
		t.Fatalf("upsert performance: %v", err)
	}

	var count int64
	if err := db.Model(&orm.ChatRunPerformance{}).Where("run_id = ?", "run-1").Count(&count).Error; err != nil {
		t.Fatalf("count performance rows: %v", err)
	}
	var stored orm.ChatRunPerformance
	if err := db.Where("run_id = ?", "run-1").Take(&stored).Error; err != nil {
		t.Fatalf("load performance row: %v", err)
	}
	if count != 1 || stored.ModelMS == nil || *stored.ModelMS != 800 {
		t.Fatalf("upsert result: count=%d row=%#v", count, stored)
	}
}

func TestPersistRunPerformanceRejectsRunOwnershipChange(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/performance-owner.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.ChatRunPerformance{}); err != nil {
		t.Fatalf("migrate performance table: %v", err)
	}

	initial := runPerformanceRecord{
		RunID: "run-shared", ConversationID: "conv-1", HistoryID: "history-1", UserID: "user-1",
		Status: "completed", Metrics: &RunPerformanceMetrics{SchemaVersion: 1, ModelSteps: 1},
	}
	if err := persistRunPerformance(context.Background(), db.DB, initial); err != nil {
		t.Fatalf("persist initial performance: %v", err)
	}
	conflict := initial
	conflict.ConversationID = "conv-2"
	conflict.HistoryID = "history-2"
	conflict.UserID = "user-2"
	if err := persistRunPerformance(context.Background(), db.DB, conflict); err == nil {
		t.Fatal("cross-owner run_id collision was accepted")
	}

	var stored orm.ChatRunPerformance
	if err := db.Where("run_id = ?", "run-shared").Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ConversationID != "conv-1" || stored.HistoryID != "history-1" || stored.UserID != "user-1" {
		t.Fatalf("run ownership was overwritten: %#v", stored)
	}
}

func TestHydrateHistoryPerformanceScopesRowsToConversationOwner(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/performance-history.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.ChatRunPerformance{}); err != nil {
		t.Fatalf("migrate performance table: %v", err)
	}
	for _, record := range []runPerformanceRecord{
		{
			RunID: "run-owned", ConversationID: "conv-owned", HistoryID: "history-owned", UserID: "user-1",
			Status: "completed", Metrics: &RunPerformanceMetrics{
				SchemaVersion: 1, Steps: 7, ModelSteps: 1, ModelMS: metricInt64(500), OutputTokens: metricInt64(10),
				InputTokens: metricInt64(200), CachedTokens: metricInt64(80), CacheInputTokens: metricInt64(160),
			},
		},
		{
			RunID: "run-other", ConversationID: "conv-other", HistoryID: "history-other", UserID: "user-2",
			Status: "completed", Metrics: &RunPerformanceMetrics{
				SchemaVersion: 1, ModelSteps: 1, ModelMS: metricInt64(100), OutputTokens: metricInt64(50),
			},
		},
	} {
		if err := persistRunPerformance(context.Background(), db.DB, record); err != nil {
			t.Fatalf("persist performance: %v", err)
		}
	}

	items := []map[string]any{
		{"id": "history-owned", "run_id": "run-owned"},
		{"id": "history-other", "run_id": "run-other"},
	}
	if err := hydrateHistoryPerformance(context.Background(), db.DB, "user-1", "conv-owned", items); err != nil {
		t.Fatalf("hydrate history performance: %v", err)
	}

	owned, ok := items[0]["performance_metrics"].(*RunPerformanceMetrics)
	if !ok || owned.TokS == nil || *owned.TokS != 20 || owned.CacheHitRate == nil || *owned.CacheHitRate != 0.5 {
		t.Fatalf("owned metrics were not hydrated with derived throughput: %#v", items[0])
	}
	if owned.Steps != 7 {
		t.Fatalf("expected persisted step count 7, got %d", owned.Steps)
	}
	if _, leaked := items[1]["performance_metrics"]; leaked {
		t.Fatalf("another owner's metrics leaked into history: %#v", items[1])
	}
}
