package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/artifactgraph"
	"lazymind/core/workflow/graphengine"
)

func TestFreezeRouteDecisionSerializesWithArtifactInvalidation(t *testing.T) {
	db := newTestDB(t)
	if db.Dialector.Name() != "postgres" {
		t.Skip("requires PostgreSQL row locking")
	}
	if err := db.AutoMigrate(
		&orm.WorkflowRevision{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowInputBinding{},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	graph := graphengine.CompiledStateGraph{
		SchemaVersion: graphengine.SchemaVersion, GraphHash: "route-lock-graph", StartRoute: "source-step",
		Nodes: map[string]graphengine.CompiledNode{
			"source-step": {ID: "source-step"},
			"next-step":   {ID: "next-step"},
		},
		ControlEdges: []graphengine.CompiledEdge{{From: "source-step", To: "next-step"}},
	}
	if err := db.Create(&orm.WorkflowRevision{
		ID: "route-lock-revision", WorkflowResourceID: "resource", RevisionNo: 1,
		CompiledGraph: graph.JSON(), GraphHash: graph.GraphHash,
		GraphSchemaVersion: graph.SchemaVersion, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSession{
		ID: "route-lock-session", ConversationID: "conversation", WorkflowID: "workflow",
		WorkflowRevisionID: "route-lock-revision", GraphHash: graph.GraphHash,
		GraphSchemaVersion: graph.SchemaVersion, Status: SessionStatusActive,
		CreateUserID: "owner", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: "route-lock-human", SessionID: "route-lock-session", Slot: "source-material",
		ContentType: "text", Value: []byte(`{"text":"source"}`), CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	humanID := "route-lock-human"
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "route-lock-input", SessionID: "route-lock-session", SlotID: "source-material",
		Revision: 1, Selected: true, HumanArtifactID: &humanID, Slot: "source-material",
		StepID: "input", Validity: "effective", ChangeSource: "human", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{
		ID: "route-lock-attempt", SessionID: "route-lock-session", StepID: "source-step",
		Attempt: 1, TaskID: "route-lock-task", Status: StepStatusSucceeded, Validity: "effective",
		ProgressJSON: `{}`, ResultJSON: `{}`, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowAttemptInputBinding{
		ID: "route-lock-binding", SessionID: "route-lock-session", AttemptID: "route-lock-attempt",
		MaterialID: "source-material", MaterialRevisionID: "route-lock-input",
		SourceType: "artifact", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	mutationTx := db.Begin()
	if mutationTx.Error != nil {
		t.Fatal(mutationTx.Error)
	}
	defer mutationTx.Rollback()
	if _, err := artifactgraph.LockSession(mutationTx, "route-lock-session"); err != nil {
		t.Fatal(err)
	}
	if err := artifactgraph.InvalidateConsumers(
		t.Context(), mutationTx, "route-lock-session", "route-lock-input",
	); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- freezeRouteDecision(
			t.Context(), db.DB, "route-lock-session", "source-step", "route-lock-task",
		)
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("route freeze escaped mutation Session lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := mutationTx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("route freeze after invalidation error = %v", err)
	}
	var attempt orm.WorkflowSessionStep
	db.First(&attempt, "id = ?", "route-lock-attempt")
	var decisions int64
	db.Model(&orm.WorkflowRouteDecision{}).Where("session_id = ? AND validity = ?", "route-lock-session", "effective").Count(&decisions)
	if attempt.Validity != "stale" || decisions != 0 {
		t.Fatalf("stale route was resurrected: attempt=%#v decisions=%d", attempt, decisions)
	}
}

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
