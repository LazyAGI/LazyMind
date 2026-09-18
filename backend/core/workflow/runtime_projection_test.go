package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func TestLoadSessionGraphFailsWhenLegacyWorkflowResourceIsMissing(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowResource{}, &orm.WorkflowRevision{}); err != nil {
		t.Fatalf("migrate revision: %v", err)
	}
	_, err := loadSessionGraph(context.Background(), db.DB, &orm.WorkflowSession{
		WorkflowID:         "workflow-a",
		WorkflowRevisionID: "missing-revision",
	})
	if err == nil || !strings.Contains(err.Error(), "missing-revision") {
		t.Fatalf("missing pinned revision must be rejected, got %v", err)
	}
}

func TestLoadSessionGraphPinsLegacySessionToCoreRevision(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowResource{}, &orm.WorkflowRevision{}); err != nil {
		t.Fatalf("migrate catalog: %v", err)
	}
	now := time.Now().UTC()
	graph := &graphengine.CompiledStateGraph{SchemaVersion: graphengine.SchemaVersion,
		GraphHash: "legacy-upgrade-hash", Nodes: map[string]graphengine.CompiledNode{}}
	if err := db.Create(&orm.WorkflowResource{ID: "resource-a", WorkflowRef: "builtin:workflow-a",
		WorkflowID: "workflow-a", Status: "active", HeadRevisionID: "revision-a", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create resource: %v", err)
	}
	if err := db.Create(&orm.WorkflowRevision{ID: "revision-a", WorkflowResourceID: "resource-a",
		CompiledGraph: graph.JSON(), GraphHash: graph.GraphHash, GraphSchemaVersion: graph.SchemaVersion, CreatedAt: now}).Error; err != nil {
		t.Fatalf("create revision: %v", err)
	}
	session := &orm.WorkflowSession{ID: "legacy-session", WorkflowID: "workflow-a"}
	if err := db.Create(session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	loaded, err := loadSessionGraph(context.Background(), db.DB, session)
	if err != nil || loaded.GraphHash != graph.GraphHash {
		t.Fatalf("load legacy graph: graph=%#v err=%v", loaded, err)
	}
	var stored orm.WorkflowSession
	if err := db.First(&stored, "id = ?", session.ID).Error; err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if stored.WorkflowRevisionID != "revision-a" || stored.GraphHash != graph.GraphHash {
		t.Fatalf("legacy session was not pinned: %#v", stored)
	}
}

func TestLoadSessionGraphRejectsSessionHashMismatch(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowRevision{}); err != nil {
		t.Fatalf("migrate revision: %v", err)
	}
	graph := &graphengine.CompiledStateGraph{
		SchemaVersion: graphengine.SchemaVersion,
		GraphHash:     "revision-hash",
		Nodes:         map[string]graphengine.CompiledNode{},
	}
	if err := db.Create(&orm.WorkflowRevision{
		ID:                 "revision-a",
		CompiledGraph:      graph.JSON(),
		GraphHash:          graph.GraphHash,
		GraphSchemaVersion: graph.SchemaVersion,
		CreatedAt:          time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("create revision: %v", err)
	}
	_, err := loadSessionGraph(context.Background(), db.DB, &orm.WorkflowSession{
		WorkflowRevisionID: "revision-a",
		GraphHash:          "different-session-hash",
		GraphSchemaVersion: graphengine.SchemaVersion,
	})
	if err == nil || !strings.Contains(err.Error(), "session graph hash mismatch") {
		t.Fatalf("session hash mismatch must be rejected, got %v", err)
	}
}

func TestLegacySessionRejectsChangedWorkflowDefinition(t *testing.T) {
	session := &orm.WorkflowSession{GraphHash: "hash-at-task-start"}
	graph := &graphengine.CompiledStateGraph{GraphHash: "hash-after-code-change"}
	err := ensureLegacySessionGraphUnchanged(session, graph)
	var changed *workflowDefinitionChangedError
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

func TestRemoveStepIDHidesExhaustedRetryTarget(t *testing.T) {
	got := removeStepID([]string{"prompt", "review"}, "prompt")
	if len(got) != 1 || got[0] != "review" {
		t.Fatalf("retryable=%v, want [review]", got)
	}
}
