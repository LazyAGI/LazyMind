package plugin

import (
	"context"
	"errors"
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

func TestLegacySessionRejectsChangedPluginDefinition(t *testing.T) {
	session := &orm.PluginSession{GraphHash: "hash-at-task-start"}
	graph := &graphengine.CompiledStateGraph{GraphHash: "hash-after-code-change"}
	err := ensureLegacySessionGraphUnchanged(session, graph)
	var changed *pluginDefinitionChangedError
	if !errors.As(err, &changed) {
		t.Fatalf("changed builtin graph must return typed error, got %v", err)
	}
	if changed.expected != session.GraphHash || changed.actual != graph.GraphHash {
		t.Fatalf("unexpected hash details: %#v", changed)
	}
	if !strings.Contains(changed.Error(), "请新建一个对话任务") {
		t.Fatalf("user guidance missing from error: %v", changed)
	}
}
