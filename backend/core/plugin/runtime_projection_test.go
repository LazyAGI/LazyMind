package plugin

import (
	"context"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/plugin/graphengine"
)

func TestLoadSessionGraphDoesNotFallbackWhenRevisionIsMissing(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.PluginRevision{}); err != nil {
		t.Fatalf("migrate revision: %v", err)
	}
	_, err := loadSessionGraph(context.Background(), db.DB, &orm.PluginSession{
		PluginID:         "plugin-a",
		PluginRevisionID: "missing-revision",
	})
	if err == nil || !strings.Contains(err.Error(), "missing-revision") {
		t.Fatalf("missing pinned revision must be rejected, got %v", err)
	}
}

func TestLoadSessionGraphRejectsSessionHashMismatch(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.PluginRevision{}); err != nil {
		t.Fatalf("migrate revision: %v", err)
	}
	graph := &graphengine.CompiledStateGraph{
		SchemaVersion: graphengine.SchemaVersion,
		GraphHash:     "revision-hash",
		Nodes:         map[string]graphengine.CompiledNode{},
	}
	if err := db.Create(&orm.PluginRevision{
		ID:                 "revision-a",
		CompiledGraph:      graph.JSON(),
		GraphHash:          graph.GraphHash,
		GraphSchemaVersion: graph.SchemaVersion,
		CreatedAt:          time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("create revision: %v", err)
	}
	_, err := loadSessionGraph(context.Background(), db.DB, &orm.PluginSession{
		PluginRevisionID:   "revision-a",
		GraphHash:          "different-session-hash",
		GraphSchemaVersion: graphengine.SchemaVersion,
	})
	if err == nil || !strings.Contains(err.Error(), "session graph hash mismatch") {
		t.Fatalf("session hash mismatch must be rejected, got %v", err)
	}
}
