package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/tasktiming"
	"lazymind/core/workflow/artifactgraph"
	"lazymind/core/workflow/graphengine"
)

func TestSuccessfulStepHandoffPreservesExecutionClock(t *testing.T) {
	for _, tc := range []struct {
		name, initial, mode, next, sibling, wantDisplay string
		wantRunning                                     bool
	}{
		{"ready successor", "active", "", "ready", "", "running", true},
		{"approval", "active", "human", "ready", "", "waiting", false},
		{"blocked successor", "active", "", "blocked", "", "failed", false},
		{"completed", "active", "", "end", "", "completed", false},
		{"failed sibling", "active", "", "ready", "failed", "failed", false},
		{"interrupted sibling", "active", "", "ready", "interrupted", "failed", false},
		{"running sibling", "active", "", "ready", "running", "running", true},
		{"stopping", "pausing", "", "ready", "", "running", true},
		{"paused", "stopped", "", "ready", "", "paused", false},
		{"not admitted", "waiting", "", "ready", "", "failed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, hash := setupBatchTransitionSession(t)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(db.AutoMigrate(&orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{}))
			graph := graphengine.CompiledStateGraph{
				SchemaVersion: graphengine.SchemaVersion, GraphHash: hash, StartRoute: "all",
				Nodes: map[string]graphengine.CompiledNode{
					"prepare": {ID: "prepare", Mode: tc.mode, Route: "all"},
					"outline": {ID: "outline", Route: "all"},
				},
				ControlEdges: []graphengine.CompiledEdge{
					{ID: "start", From: "__start__", To: "prepare"},
					{ID: "next", From: "prepare", To: "outline"},
					{ID: "end", From: "outline", To: "__end__"},
				},
			}
			if tc.next == "blocked" {
				node := graph.Nodes["outline"]
				node.Input = &graphengine.Expression{Material: "missing"}
				graph.Nodes["outline"] = node
			} else if tc.next == "end" {
				delete(graph.Nodes, "outline")
				graph.ControlEdges = graph.ControlEdges[:2]
				graph.ControlEdges[1].To = "__end__"
			}
			if tc.sibling != "" {
				graph.Nodes["sibling"] = graphengine.CompiledNode{ID: "sibling"}
				graph.ControlEdges = append(graph.ControlEdges, graphengine.CompiledEdge{ID: "sibling", From: "__start__", To: "sibling"})
				must(db.Create(&orm.WorkflowSessionStep{ID: "sibling", SessionID: "batch-session", StepID: "sibling", Status: tc.sibling, Validity: "effective"}).Error)
			}
			must(db.Model(&orm.WorkflowRevision{}).Where("id = ?", "batch-revision").Update("compiled_graph", graph.JSON()).Error)
			start := time.Now().UTC().Add(-time.Minute)
			must(db.Create(&orm.WorkflowSessionStep{ID: "prepare-attempt", TaskID: "prepare-task", SessionID: "batch-session", StepID: "prepare", Status: "succeeded", Validity: "effective", Attempt: 1, CreatedAt: start, UpdatedAt: start}).Error)
			session, err := GetSession(t.Context(), db.DB, "batch-session")
			must(err)
			session.Status = "active"
			must(tasktiming.BindWorkflow(t.Context(), db.DB, *session, start))
			updates := map[string]any{"status": tc.initial}
			if tc.initial == "stopped" {
				updates["last_stopped_at"] = time.Now().UTC()
			}
			must(db.Model(session).Updates(updates).Error)

			// Persist the real completion snapshot before the driver starts the next step.
			must(freezeRouteDecision(t.Context(), db.DB, session.ID, "prepare", "prepare-task"))
			var event orm.WorkflowEvent
			must(db.Where("session_id = ? AND event_type = ?", session.ID, "workflow.snapshot").Order("created_at DESC").First(&event).Error)
			var payload projectionResponse
			must(json.Unmarshal(event.PayloadJSON, &payload))
			if payload.Display.Status != tc.wantDisplay {
				t.Fatalf("display = %+v, want %s", payload.Display, tc.wantDisplay)
			}
			var intervals []orm.TaskExecutionTiming
			must(db.Where("workflow_session_id = ?", session.ID).Find(&intervals).Error)
			if len(intervals) != 1 || (intervals[0].FinishedAt == nil) != tc.wantRunning {
				t.Fatalf("handoff split or incorrectly stopped the clock: %+v", intervals)
			}
			if tc.name == "ready successor" {
				admitted, err := prepareContinuation(t.Context(), db.DB, session.ID)
				must(err)
				if !admitted {
					t.Fatal("successor continuation rejected")
				}
				must(db.Where("workflow_session_id = ?", session.ID).Find(&intervals).Error)
				if len(intervals) != 1 || intervals[0].FinishedAt != nil {
					t.Fatalf("continuation reopened the clock: %+v", intervals)
				}
				run, err := tasktiming.Snapshot(t.Context(), db.DB, session.CreateUserID, session.ConversationID, session.ID, start.Add(65*time.Second))
				must(err)
				if run == nil || run.Timing.ElapsedMS == nil || *run.Timing.ElapsedMS != 65000 {
					t.Fatalf("step handoff lost elapsed time: %+v", run)
				}
			}
		})
	}
}

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

func TestRuntimeSnapshotUsesDeclaredProducerForStartupNameCollision(t *testing.T) {
	for _, state := range []string{"absent", "effective", "stale"} {
		t.Run(state, func(t *testing.T) {
			db, session, now := ordinaryFixture(t)
			graph, err := loadSessionGraph(t.Context(), db.DB, &session)
			if err != nil {
				t.Fatal(err)
			}
			graph.MaterialProducers = map[string]graphengine.ProducerRef{
				"normalized_mode": {Kind: "step", StepID: "write"},
				"references":      {Kind: "external"},
			}
			graph.MaterialCardinalities = map[string]string{"normalized_mode": "single", "references": "list"}
			for _, input := range []struct{ id, material string }{
				{"startup-answer", "normalized_mode"}, {"reference-a", "references"}, {"reference-b", "references"},
			} {
				if err := db.Create(&orm.WorkflowInputBinding{ID: input.id, WorkflowSessionID: session.ID,
					MaterialID: input.material, ResourceID: input.id, ResourceRevision: 1, Validity: "effective", CreatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if state != "absent" {
				if err := db.Create(&orm.WorkflowSlotRevision{ID: "normalized-output", SessionID: session.ID,
					SlotID: "normalized_mode", Slot: "normalized_mode", StepID: "write", Revision: 1,
					Selected: true, Validity: state, CreatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := loadRuntimeSnapshot(t.Context(), db.DB, session.ID, graph)
			if err != nil {
				t.Fatal(err)
			}
			mode := graphengine.Evaluate(&graphengine.Expression{Material: "normalized_mode"}, snapshot.Materials)
			if state == "effective" {
				if !mode.Satisfied || len(mode.Witnesses) != 1 || mode.Witnesses[0].RevisionID != "normalized-output" {
					t.Fatalf("normalized output must be the sole witness: %+v", mode)
				}
				binding := attemptInputBindingFromWitness(db.DB, session.ID, "next-attempt", mode.Witnesses[0], now)
				if binding.SourceType != "artifact" || binding.MaterialRevisionID != "normalized-output" {
					t.Fatalf("wrong frozen input: %+v", binding)
				}
			} else if mode.Satisfied || len(mode.Witnesses) != 0 {
				t.Fatalf("startup answer must not substitute for %s output: %+v", state, mode)
			}
			refs := graphengine.Evaluate(&graphengine.Expression{Material: "references"}, snapshot.Materials)
			if !refs.Satisfied || len(refs.Witnesses) != 2 {
				t.Fatalf("external list lost bindings: %+v", refs)
			}
			var count int64
			if err := db.Model(&orm.WorkflowInputBinding{}).Where("id = ?", "startup-answer").Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("startup answer was modified: count=%d error=%v", count, err)
			}
		})
	}
}
