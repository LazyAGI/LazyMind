package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
)

type artifactDependencyFixture struct {
	db                  *orm.DB
	sourceRevisionID    string
	sourceHumanID       string
	directAttemptID     string
	directOutputID      string
	downstreamAttemptID string
	downstreamOutputID  string
	routeAttemptID      string
	routeOutputID       string
	witnessDecisionID   string
	sourceDecisionID    string
	unrelatedAttemptID  string
	unrelatedOutputID   string
	unrelatedDecisionID string
}

func seedArtifactDependencyFixture(
	t *testing.T, directStatus, downstreamStatus string, includeWitnessRoute bool,
) artifactDependencyFixture {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	fixture := artifactDependencyFixture{
		db: db, sourceRevisionID: "source-revision", sourceHumanID: "source-human",
		directAttemptID: "attempt-direct", directOutputID: "output-direct",
		downstreamAttemptID: "attempt-downstream", downstreamOutputID: "output-downstream",
		routeAttemptID: "attempt-route", routeOutputID: "output-route",
		witnessDecisionID: "decision-witness", sourceDecisionID: "decision-source",
		unrelatedAttemptID: "attempt-unrelated", unrelatedOutputID: "output-unrelated",
		unrelatedDecisionID: "decision-unrelated",
	}
	if err := db.Create(&orm.WorkflowSession{
		ID: "session-dependency", ConversationID: "conversation-dependency", WorkflowID: "workflow-dependency",
		Status: SessionStatusActive, CreateUserID: "owner", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: fixture.sourceHumanID, SessionID: "session-dependency", Slot: "source-artifact-key",
		ContentType: "text", Value: json.RawMessage(`{"text":"source"}`), DraftVersion: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	humanID := fixture.sourceHumanID
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: fixture.sourceRevisionID, SessionID: "session-dependency", SlotID: "source-slot-id",
		Revision: 1, Selected: true, HumanArtifactID: &humanID, ChangeSource: "human",
		Slot: "source-artifact-key", StepID: "source-step", Attempt: 1, Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if directStatus != "" {
		if err := db.Create(&orm.WorkflowSessionStep{
			ID: fixture.directAttemptID, SessionID: "session-dependency", StepID: "direct-step",
			Attempt: 1, TaskID: "task-direct", Status: directStatus, Validity: "effective",
			CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowAttemptInputBinding{
			ID: "binding-direct", SessionID: "session-dependency", AttemptID: fixture.directAttemptID,
			MaterialID: "source-slot-id", MaterialRevisionID: fixture.sourceRevisionID,
			SourceType: "artifact", CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowSlotRevision{
			ID: fixture.directOutputID, SessionID: "session-dependency", SlotID: "direct-output-slot",
			Revision: 1, Selected: true, ChangeSource: "ai", Slot: "direct-output-key",
			StepID: "direct-step", Attempt: 1, Validity: "effective",
			ProducerAttemptID: fixture.directAttemptID, CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if downstreamStatus != "" {
		if err := db.Create(&orm.WorkflowSessionStep{
			ID: fixture.downstreamAttemptID, SessionID: "session-dependency", StepID: "downstream-step",
			Attempt: 1, TaskID: "task-downstream", Status: downstreamStatus, Validity: "effective",
			CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowAttemptInputBinding{
			ID: "binding-downstream", SessionID: "session-dependency", AttemptID: fixture.downstreamAttemptID,
			MaterialID: "direct-output-slot", MaterialRevisionID: fixture.directOutputID,
			SourceType: "artifact", CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowSlotRevision{
			ID: fixture.downstreamOutputID, SessionID: "session-dependency", SlotID: "downstream-output-slot",
			Revision: 1, Selected: true, ChangeSource: "ai", Slot: "downstream-output-key",
			StepID: "downstream-step", Attempt: 1, Validity: "effective",
			ProducerAttemptID: fixture.downstreamAttemptID, CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if includeWitnessRoute {
		if err := db.Create(&orm.WorkflowSessionStep{
			ID: fixture.routeAttemptID, SessionID: "session-dependency", StepID: "route-step",
			Attempt: 1, TaskID: "task-route", Status: StepStatusSucceeded, Validity: "effective",
			CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowSlotRevision{
			ID: fixture.routeOutputID, SessionID: "session-dependency", SlotID: "route-output-slot",
			Revision: 1, Selected: true, ChangeSource: "ai", Slot: "route-output-key",
			StepID: "route-step", Attempt: 1, Validity: "effective",
			ProducerAttemptID: fixture.routeAttemptID, CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		witnesses := json.RawMessage(`[{"material_id":"source-slot-id","revision_id":"source-revision"}]`)
		if err := db.Create(&orm.WorkflowRouteDecision{
			ID: fixture.witnessDecisionID, SessionID: "session-dependency", FromStepID: "source-step",
			ActivatedJSON: json.RawMessage(`["route-step"]`), PrunedJSON: json.RawMessage(`[]`),
			BypassedJSON: json.RawMessage(`[]`), WitnessJSON: witnesses, Validity: "effective", CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if directStatus != "" {
		if err := db.Create(&orm.WorkflowRouteDecision{
			ID: fixture.sourceDecisionID, SessionID: "session-dependency", FromStepID: "direct-step",
			SourceAttemptID: fixture.directAttemptID, ActivatedJSON: json.RawMessage(`[]`),
			PrunedJSON: json.RawMessage(`[]`), BypassedJSON: json.RawMessage(`[]`),
			WitnessJSON: json.RawMessage(`[]`), Validity: "effective", CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.WorkflowSessionStep{
		ID: fixture.unrelatedAttemptID, SessionID: "session-dependency", StepID: "unrelated-step",
		Attempt: 1, TaskID: "task-unrelated", Status: StepStatusSucceeded, Validity: "effective",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: fixture.unrelatedOutputID, SessionID: "session-dependency", SlotID: "unrelated-output-slot",
		Revision: 1, Selected: true, ChangeSource: "ai", Slot: "unrelated-output-key",
		StepID: "unrelated-step", Attempt: 1, Validity: "effective",
		ProducerAttemptID: fixture.unrelatedAttemptID, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRouteDecision{
		ID: fixture.unrelatedDecisionID, SessionID: "session-dependency", FromStepID: "unrelated-step",
		SourceAttemptID: fixture.unrelatedAttemptID, ActivatedJSON: json.RawMessage(`[]`),
		PrunedJSON: json.RawMessage(`[]`), BypassedJSON: json.RawMessage(`[]`),
		WitnessJSON: json.RawMessage(`[]`), Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return fixture
}

func requireAttemptValidity(t *testing.T, fixture artifactDependencyFixture, id, want string) {
	t.Helper()
	var attempt orm.WorkflowSessionStep
	if err := fixture.db.First(&attempt, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if attempt.Validity != want {
		t.Fatalf("attempt %s validity = %q, want %q", id, attempt.Validity, want)
	}
}

func requireRevisionState(t *testing.T, fixture artifactDependencyFixture, id, validity string, selected bool) {
	t.Helper()
	var revision orm.WorkflowSlotRevision
	if err := fixture.db.First(&revision, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if revision.Validity != validity || revision.Selected != selected {
		t.Fatalf("revision %s = validity %q selected %v, want %q/%v", id, revision.Validity, revision.Selected, validity, selected)
	}
}

func requireDecisionValidity(t *testing.T, fixture artifactDependencyFixture, id, want string) {
	t.Helper()
	var decision orm.WorkflowRouteDecision
	if err := fixture.db.First(&decision, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if decision.Validity != want {
		t.Fatalf("decision %s validity = %q, want %q", id, decision.Validity, want)
	}
}

func replaceDependencySource(t *testing.T, fixture artifactDependencyFixture, changeSource string) (*orm.WorkflowSlotRevision, error) {
	t.Helper()
	baseRevision, baseDraftVersion := 1, int64(1)
	return WriteSlotRevisionWithHumanArtifact(
		t.Context(), fixture.db.DB, "session-dependency", "source-slot-id", "source-artifact-key",
		"source-step", 1, "single", nil, "text", json.RawMessage(`{"text":"replacement"}`), nil,
		changeSource, &baseRevision, &baseDraftVersion,
	)
}

func TestHumanRevisionInvalidatesTerminalConsumersRecursively(t *testing.T) {
	fixture := seedArtifactDependencyFixture(t, StepStatusSucceeded, StepStatusSucceeded, true)
	revision, err := replaceDependencySource(t, fixture, "human")
	if err != nil || revision == nil {
		t.Fatalf("replace source: revision=%#v err=%v", revision, err)
	}
	requireAttemptValidity(t, fixture, fixture.directAttemptID, "stale")
	requireAttemptValidity(t, fixture, fixture.downstreamAttemptID, "stale")
	requireAttemptValidity(t, fixture, fixture.routeAttemptID, "stale")
	requireRevisionState(t, fixture, fixture.directOutputID, "stale", false)
	requireRevisionState(t, fixture, fixture.downstreamOutputID, "stale", false)
	requireRevisionState(t, fixture, fixture.routeOutputID, "stale", false)
	requireDecisionValidity(t, fixture, fixture.witnessDecisionID, "stale")
	requireDecisionValidity(t, fixture, fixture.sourceDecisionID, "stale")
	requireAttemptValidity(t, fixture, fixture.unrelatedAttemptID, "effective")
	requireRevisionState(t, fixture, fixture.unrelatedOutputID, "effective", true)
	requireDecisionValidity(t, fixture, fixture.unrelatedDecisionID, "effective")
}

func TestHumanRevisionRejectsEveryLiveDirectConsumer(t *testing.T) {
	for _, status := range []string{"pending", "queued", "claimed", "running"} {
		t.Run(status, func(t *testing.T) {
			fixture := seedArtifactDependencyFixture(t, status, "", false)
			if err := fixture.db.Delete(&orm.WorkflowSlotRevision{}, "id = ?", fixture.directOutputID).Error; err != nil {
				t.Fatal(err)
			}
			revision, err := replaceDependencySource(t, fixture, "human")
			if revision != nil || err == nil || err.Error() != "ARTIFACT_IN_USE" {
				t.Fatalf("replace with %s consumer: revision=%#v err=%v", status, revision, err)
			}
			requireAttemptValidity(t, fixture, fixture.directAttemptID, "effective")
			requireRevisionState(t, fixture, fixture.sourceRevisionID, "effective", true)
			var artifacts, revisions, events int64
			fixture.db.Model(&orm.WorkflowHumanArtifact{}).Count(&artifacts)
			fixture.db.Model(&orm.WorkflowSlotRevision{}).Where("slot_id = ?", "source-slot-id").Count(&revisions)
			fixture.db.Model(&orm.WorkflowEvent{}).Where("session_id = ?", "session-dependency").Count(&events)
			if artifacts != 1 || revisions != 1 || events != 0 ||
				loadArtifactEventSession(t, fixture.db, "session-dependency").StateVersion != 0 {
				t.Fatalf("mutation leaked: artifacts=%d revisions=%d events=%d", artifacts, revisions, events)
			}
		})
	}
}

func TestHumanRevisionRejectsTransitiveLiveConsumerAndRollsBackEarlierInvalidation(t *testing.T) {
	fixture := seedArtifactDependencyFixture(t, StepStatusSucceeded, StepStatusRunning, false)
	revision, err := replaceDependencySource(t, fixture, "human")
	if revision != nil || err == nil || err.Error() != "ARTIFACT_IN_USE" {
		t.Fatalf("replace with transitive live consumer: revision=%#v err=%v", revision, err)
	}
	requireAttemptValidity(t, fixture, fixture.directAttemptID, "effective")
	requireAttemptValidity(t, fixture, fixture.downstreamAttemptID, "effective")
	requireRevisionState(t, fixture, fixture.sourceRevisionID, "effective", true)
	requireRevisionState(t, fixture, fixture.directOutputID, "effective", true)
	requireRevisionState(t, fixture, fixture.downstreamOutputID, "effective", true)
}

func TestHumanDraftInvalidatesTerminalRouteWitnessConsumers(t *testing.T) {
	fixture := seedArtifactDependencyFixture(t, "", "", true)
	baseRevision, baseDraftVersion := 1, int64(1)
	updated, draftVersion, updatedInPlace, err := UpdateSelectedHumanArtifactValue(
		t.Context(), fixture.db.DB, "session-dependency", "source-slot-id", nil,
		"text", json.RawMessage(`{"text":"updated in place"}`), nil,
		&baseRevision, &baseDraftVersion,
	)
	if err != nil || !updatedInPlace || draftVersion != 2 || updated == nil {
		t.Fatalf("draft update: updated=%#v in_place=%v version=%d err=%v", updated, updatedInPlace, draftVersion, err)
	}
	requireDecisionValidity(t, fixture, fixture.witnessDecisionID, "stale")
	requireAttemptValidity(t, fixture, fixture.routeAttemptID, "stale")
	requireRevisionState(t, fixture, fixture.routeOutputID, "stale", false)
	requireAttemptValidity(t, fixture, fixture.unrelatedAttemptID, "effective")
}

func TestNativeRevisionUsesDependencyInvalidation(t *testing.T) {
	fixture := seedArtifactDependencyFixture(t, StepStatusSucceeded, "", false)
	step, err := CreateSessionStep(
		t.Context(), fixture.db.DB, "session-dependency", "source-step", "task-native-replacement", 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&orm.SubAgentArtifact{
		ID: "native-replacement", TaskID: step.TaskID, Slot: "source-artifact-key", ContentType: "text",
		Value: json.RawMessage(`{"text":"native replacement"}`), Seq: 1, CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	revision, err := WriteSlotRevision(
		t.Context(), fixture.db.DB, "session-dependency", "source-slot-id", "source-artifact-key",
		"source-step", 2, "single", nil,
	)
	if err != nil || revision == nil {
		t.Fatalf("native replacement: revision=%#v err=%v", revision, err)
	}
	requireAttemptValidity(t, fixture, fixture.directAttemptID, "stale")
	requireRevisionState(t, fixture, fixture.directOutputID, "stale", false)
}

func TestHumanRevisionInvalidatesEveryTerminalConsumerStatus(t *testing.T) {
	for _, status := range []string{"failed", "interrupted", "cancelled", "canceled"} {
		t.Run(status, func(t *testing.T) {
			fixture := seedArtifactDependencyFixture(t, status, "", false)
			revision, err := replaceDependencySource(t, fixture, "human")
			if err != nil || revision == nil {
				t.Fatalf("replace source: revision=%#v err=%v", revision, err)
			}
			requireAttemptValidity(t, fixture, fixture.directAttemptID, "stale")
			requireRevisionState(t, fixture, fixture.directOutputID, "stale", false)
		})
	}
}

func TestHumanRevisionIgnoresStaleConsumersAndRouteDecisions(t *testing.T) {
	fixture := seedArtifactDependencyFixture(t, StepStatusSucceeded, "", true)
	if err := fixture.db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", fixture.directAttemptID).
		Updates(map[string]any{"status": StepStatusRunning, "validity": "stale"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", fixture.directOutputID).
		Updates(map[string]any{"validity": "stale", "selected": false}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&orm.WorkflowRouteDecision{}).Where("id = ?", fixture.witnessDecisionID).
		Update("validity", "stale").Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", fixture.routeAttemptID).
		Update("status", StepStatusRunning).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := fixture.db.Create(&orm.WorkflowSessionStep{
		ID: "stale-history-descendant", SessionID: "session-dependency", StepID: "stale-descendant-step",
		Attempt: 1, TaskID: "stale-descendant-task", Status: StepStatusRunning, Validity: "effective",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&orm.WorkflowAttemptInputBinding{
		ID: "stale-history-descendant-binding", SessionID: "session-dependency",
		AttemptID: "stale-history-descendant", MaterialID: "direct-output-slot",
		MaterialRevisionID: fixture.directOutputID, SourceType: "artifact", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&orm.WorkflowSlotRevision{
		ID: "stale-history-descendant-output", SessionID: "session-dependency",
		SlotID: "stale-descendant-output-slot", Revision: 1, Selected: true,
		ChangeSource: "ai", Slot: "stale-descendant-output-key", StepID: "stale-descendant-step",
		Attempt: 1, ProducerAttemptID: "stale-history-descendant", Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	revision, err := replaceDependencySource(t, fixture, "human")
	if err != nil || revision == nil {
		t.Fatalf("stale history blocked replacement: revision=%#v err=%v", revision, err)
	}
	requireAttemptValidity(t, fixture, fixture.directAttemptID, "stale")
	requireDecisionValidity(t, fixture, fixture.witnessDecisionID, "stale")
	requireAttemptValidity(t, fixture, fixture.routeAttemptID, "effective")
	requireRevisionState(t, fixture, fixture.routeOutputID, "effective", true)
	requireAttemptValidity(t, fixture, "stale-history-descendant", "effective")
	requireRevisionState(t, fixture, "stale-history-descendant-output", "effective", true)
}

func TestHumanRevisionScopesConsumersToExactRevision(t *testing.T) {
	fixture := seedArtifactDependencyFixture(t, "", "", false)
	now := time.Now().UTC()
	if err := fixture.db.Create(&orm.WorkflowSlotRevision{
		ID: "historical-same-slot", SessionID: "session-dependency", SlotID: "source-slot-id",
		Revision: 2, Selected: false, HumanArtifactID: &fixture.sourceHumanID,
		ChangeSource: "human", Slot: "source-artifact-key", StepID: "source-step",
		Attempt: 0, Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "historical-same-slot").
		Update("selected", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&orm.WorkflowSessionStep{
		ID: "historical-live-consumer", SessionID: "session-dependency", StepID: "historical-consumer",
		Attempt: 1, TaskID: "historical-task", Status: StepStatusRunning, Validity: "effective",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&orm.WorkflowAttemptInputBinding{
		ID: "historical-binding", SessionID: "session-dependency", AttemptID: "historical-live-consumer",
		MaterialID: "source-slot-id", MaterialRevisionID: "historical-same-slot",
		SourceType: "artifact", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	revision, err := replaceDependencySource(t, fixture, "human")
	if err != nil || revision == nil {
		t.Fatalf("historical revision consumer blocked current replacement: revision=%#v err=%v", revision, err)
	}
	requireAttemptValidity(t, fixture, "historical-live-consumer", "effective")
}

func TestHumanRevisionInvalidatesLegacyAttemptOutputs(t *testing.T) {
	fixture := seedArtifactDependencyFixture(t, StepStatusSucceeded, StepStatusSucceeded, false)
	if err := fixture.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", fixture.directOutputID).
		Update("producer_attempt_id", "").Error; err != nil {
		t.Fatal(err)
	}
	revision, err := replaceDependencySource(t, fixture, "human")
	if err != nil || revision == nil {
		t.Fatalf("replace source: revision=%#v err=%v", revision, err)
	}
	requireRevisionState(t, fixture, fixture.directOutputID, "stale", false)
	requireAttemptValidity(t, fixture, fixture.downstreamAttemptID, "stale")
	requireRevisionState(t, fixture, fixture.downstreamOutputID, "stale", false)
}

func TestSourceDecisionActivationInvalidatesTerminalDescendant(t *testing.T) {
	fixture := seedArtifactDependencyFixture(t, StepStatusSucceeded, "", false)
	now := time.Now().UTC()
	if err := fixture.db.Create(&orm.WorkflowSessionStep{
		ID: "source-route-child", SessionID: "session-dependency", StepID: "source-route-step",
		Attempt: 1, TaskID: "source-route-task", Status: StepStatusSucceeded, Validity: "effective",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&orm.WorkflowSlotRevision{
		ID: "source-route-output", SessionID: "session-dependency", SlotID: "source-route-output-slot",
		Revision: 1, Selected: true, ChangeSource: "ai", Slot: "source-route-output-key",
		StepID: "source-route-step", Attempt: 1, ProducerAttemptID: "source-route-child",
		Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&orm.WorkflowSessionStep{
		ID: "source-route-grandchild", SessionID: "session-dependency", StepID: "source-route-grandchild-step",
		Attempt: 1, TaskID: "source-route-grandchild-task", Status: StepStatusSucceeded,
		Validity: "effective", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&orm.WorkflowAttemptInputBinding{
		ID: "source-route-grandchild-binding", SessionID: "session-dependency",
		AttemptID: "source-route-grandchild", MaterialID: "source-route-output-slot",
		MaterialRevisionID: "source-route-output", SourceType: "artifact", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&orm.WorkflowSlotRevision{
		ID: "source-route-grandchild-output", SessionID: "session-dependency",
		SlotID: "source-route-grandchild-output-slot", Revision: 1, Selected: true,
		ChangeSource: "ai", Slot: "source-route-grandchild-output-key",
		StepID: "source-route-grandchild-step", Attempt: 1,
		ProducerAttemptID: "source-route-grandchild", Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&orm.WorkflowRouteDecision{}).Where("id = ?", fixture.sourceDecisionID).
		Update("activated_json", json.RawMessage(`["source-route-step"]`)).Error; err != nil {
		t.Fatal(err)
	}
	revision, err := replaceDependencySource(t, fixture, "human")
	if err != nil || revision == nil {
		t.Fatalf("replace source: revision=%#v err=%v", revision, err)
	}
	requireDecisionValidity(t, fixture, fixture.sourceDecisionID, "stale")
	requireAttemptValidity(t, fixture, "source-route-child", "stale")
	requireRevisionState(t, fixture, "source-route-output", "stale", false)
	requireAttemptValidity(t, fixture, "source-route-grandchild", "stale")
	requireRevisionState(t, fixture, "source-route-grandchild-output", "stale", false)
}

func TestSharedRouteActivationPreservesTerminalDescendant(t *testing.T) {
	fixture := seedArtifactDependencyFixture(t, "", "", true)
	now := time.Now().UTC()
	if err := fixture.db.Create(&orm.WorkflowRouteDecision{
		ID: "decision-shared", SessionID: "session-dependency", FromStepID: "unrelated-step",
		SourceAttemptID: fixture.unrelatedAttemptID, ActivatedJSON: json.RawMessage(`["route-step"]`),
		PrunedJSON: json.RawMessage(`[]`), BypassedJSON: json.RawMessage(`[]`),
		WitnessJSON: json.RawMessage(`[]`), Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	revision, err := replaceDependencySource(t, fixture, "human")
	if err != nil || revision == nil {
		t.Fatalf("replace source: revision=%#v err=%v", revision, err)
	}
	requireDecisionValidity(t, fixture, fixture.witnessDecisionID, "stale")
	requireDecisionValidity(t, fixture, "decision-shared", "effective")
	requireAttemptValidity(t, fixture, fixture.routeAttemptID, "effective")
	requireRevisionState(t, fixture, fixture.routeOutputID, "effective", true)
}

func TestHumanDraftRejectsLiveRouteWitnessAndRollsBack(t *testing.T) {
	fixture := seedArtifactDependencyFixture(t, "", "", true)
	if err := fixture.db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", fixture.routeAttemptID).
		Update("status", StepStatusRunning).Error; err != nil {
		t.Fatal(err)
	}
	baseRevision, baseDraftVersion := 1, int64(1)
	updated, _, updatedInPlace, err := UpdateSelectedHumanArtifactValue(
		t.Context(), fixture.db.DB, "session-dependency", "source-slot-id", nil,
		"text", json.RawMessage(`{"text":"must not persist"}`), nil,
		&baseRevision, &baseDraftVersion,
	)
	if updated != nil || updatedInPlace || err == nil || err.Error() != "ARTIFACT_IN_USE" {
		t.Fatalf("draft live route: updated=%#v in_place=%v err=%v", updated, updatedInPlace, err)
	}
	var artifact orm.WorkflowHumanArtifact
	if err := fixture.db.First(&artifact, "id = ?", fixture.sourceHumanID).Error; err != nil {
		t.Fatal(err)
	}
	assertArtifactJSON(t, artifact.Value, `{"text":"source"}`)
	if artifact.DraftVersion != 1 || loadArtifactEventSession(t, fixture.db, "session-dependency").StateVersion != 0 {
		t.Fatalf("draft mutation leaked: artifact=%#v", artifact)
	}
	requireDecisionValidity(t, fixture, fixture.witnessDecisionID, "effective")
	requireAttemptValidity(t, fixture, fixture.routeAttemptID, "effective")
	requireRevisionState(t, fixture, fixture.routeOutputID, "effective", true)
	var events int64
	if err := fixture.db.Model(&orm.WorkflowEvent{}).Where("session_id = ?", "session-dependency").
		Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("events = %d, want 0 after rejected draft", events)
	}
}

func TestNativeRevisionRejectsLiveConsumerAndRollsBack(t *testing.T) {
	fixture := seedArtifactDependencyFixture(t, StepStatusRunning, "", false)
	if err := fixture.db.Delete(&orm.WorkflowSlotRevision{}, "id = ?", fixture.directOutputID).Error; err != nil {
		t.Fatal(err)
	}
	step, err := CreateSessionStep(
		t.Context(), fixture.db.DB, "session-dependency", "source-step", "task-native-live", 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&orm.SubAgentArtifact{
		ID: "native-live-replacement", TaskID: step.TaskID, Slot: "source-artifact-key", ContentType: "text",
		Value: json.RawMessage(`{"text":"must not persist"}`), Seq: 1, CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	revision, err := WriteSlotRevision(
		t.Context(), fixture.db.DB, "session-dependency", "source-slot-id", "source-artifact-key",
		"source-step", 2, "single", nil,
	)
	if revision != nil || err == nil || err.Error() != "ARTIFACT_IN_USE" {
		t.Fatalf("native live consumer: revision=%#v err=%v", revision, err)
	}
	requireAttemptValidity(t, fixture, fixture.directAttemptID, "effective")
	requireRevisionState(t, fixture, fixture.sourceRevisionID, "effective", true)
	var revisions int64
	fixture.db.Model(&orm.WorkflowSlotRevision{}).Where("session_id = ? AND slot_id = ?", "session-dependency", "source-slot-id").Count(&revisions)
	if revisions != 1 || loadArtifactEventSession(t, fixture.db, "session-dependency").StateVersion != 0 {
		t.Fatalf("native mutation leaked: revisions=%d", revisions)
	}
}

func TestPatchSlotItemReturnsArtifactInUseForRunningConsumer(t *testing.T) {
	fixture := seedArtifactDependencyFixture(t, StepStatusRunning, "", false)
	corestore.Init(fixture.db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })
	req := httptest.NewRequest(
		http.MethodPatch,
		"/workflow-sessions/session-dependency/slots/source-slot-id/items/idx/-1",
		strings.NewReader(`{"value":{"text":"replacement"},"content_type":"text","mode":"draft","base_revision":1,"base_draft_version":1}`),
	)
	req = mux.SetURLVars(req, map[string]string{
		"session_id": "session-dependency", "slot_id": "source-slot-id", "list_index": "-1",
	})
	recorder := httptest.NewRecorder()
	PatchSlotItemByIndex(recorder, req)
	if recorder.Code != http.StatusConflict || responseData(t, recorder)["code"] != "ARTIFACT_IN_USE" {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	requireAttemptValidity(t, fixture, fixture.directAttemptID, "effective")
	requireRevisionState(t, fixture, fixture.sourceRevisionID, "effective", true)
}
