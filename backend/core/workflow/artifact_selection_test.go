package workflow

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
	"lazymind/core/workflow/artifactgraph"
)

type rollbackFixture struct {
	db                *orm.DB
	targetRevisionID  string
	currentRevisionID string
	consumerAttemptID string
	consumerOutputID  string
}

func seedRollbackFixture(t *testing.T, consumerStatus string) rollbackFixture {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	fixture := rollbackFixture{
		db: db, targetRevisionID: "rollback-target", currentRevisionID: "rollback-current",
		consumerAttemptID: "rollback-consumer", consumerOutputID: "rollback-consumer-output",
	}
	if err := db.Create(&orm.WorkflowSession{
		ID: "rollback-session", ConversationID: "rollback-conversation", WorkflowID: "rollback-workflow",
		Status: SessionStatusActive, CreateUserID: "owner", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, value := range []orm.WorkflowHumanArtifact{
		{ID: "rollback-target-human", SessionID: "rollback-session", Slot: "document-key", ContentType: "text", Value: json.RawMessage(`{"text":"target"}`), DraftVersion: 1, CreatedAt: now},
		{ID: "rollback-current-human", SessionID: "rollback-session", Slot: "document-key", ContentType: "text", Value: json.RawMessage(`{"text":"current"}`), DraftVersion: 1, CreatedAt: now},
	} {
		if err := db.Create(&value).Error; err != nil {
			t.Fatal(err)
		}
	}
	targetHumanID := "rollback-target-human"
	currentHumanID := "rollback-current-human"
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: fixture.targetRevisionID, SessionID: "rollback-session", SlotID: "document-slot",
		Revision: 1, Selected: false, HumanArtifactID: &targetHumanID, ChangeSource: "human",
		Slot: "document-key", StepID: "source", Attempt: 1, Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", fixture.targetRevisionID).
		Update("selected", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: fixture.currentRevisionID, SessionID: "rollback-session", SlotID: "document-slot",
		Revision: 3, Selected: true, HumanArtifactID: &currentHumanID, ChangeSource: "human",
		Slot: "document-key", StepID: "source", Attempt: 3, Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if consumerStatus != "" {
		if err := db.Create(&orm.WorkflowSessionStep{
			ID: fixture.consumerAttemptID, SessionID: "rollback-session", StepID: "consumer",
			Attempt: 1, TaskID: "rollback-consumer-task", Status: consumerStatus,
			Validity: "effective", CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowAttemptInputBinding{
			ID: "rollback-binding", SessionID: "rollback-session", AttemptID: fixture.consumerAttemptID,
			MaterialID: "document-slot", MaterialRevisionID: fixture.currentRevisionID,
			SourceType: "artifact", CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowSlotRevision{
			ID: fixture.consumerOutputID, SessionID: "rollback-session", SlotID: "consumer-output-slot",
			Revision: 1, Selected: true, ChangeSource: "ai", Slot: "consumer-output-key",
			StepID: "consumer", Attempt: 1, ProducerAttemptID: fixture.consumerAttemptID,
			Validity: "effective", CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func extendRollbackTerminalGraph(t *testing.T, fixture rollbackFixture) {
	t.Helper()
	now := time.Now().UTC()
	if err := fixture.db.Create(&orm.WorkflowSessionStep{
		ID: "rollback-downstream", SessionID: "rollback-session", StepID: "downstream",
		Attempt: 1, TaskID: "rollback-downstream-task", Status: StepStatusSucceeded,
		Validity: "effective", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&orm.WorkflowAttemptInputBinding{
		ID: "rollback-downstream-binding", SessionID: "rollback-session",
		AttemptID: "rollback-downstream", MaterialID: "consumer-output-slot",
		MaterialRevisionID: fixture.consumerOutputID, SourceType: "artifact", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&orm.WorkflowSlotRevision{
		ID: "rollback-downstream-output", SessionID: "rollback-session", SlotID: "downstream-output-slot",
		Revision: 1, Selected: true, ChangeSource: "ai", Slot: "downstream-output-key",
		StepID: "downstream", Attempt: 1, ProducerAttemptID: "rollback-downstream",
		Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&orm.WorkflowSessionStep{
		ID: "rollback-route-attempt", SessionID: "rollback-session", StepID: "route-step",
		Attempt: 1, TaskID: "rollback-route-task", Status: StepStatusSucceeded,
		Validity: "effective", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&orm.WorkflowSlotRevision{
		ID: "rollback-route-output", SessionID: "rollback-session", SlotID: "route-output-slot",
		Revision: 1, Selected: true, ChangeSource: "ai", Slot: "route-output-key",
		StepID: "route-step", Attempt: 1, ProducerAttemptID: "rollback-route-attempt",
		Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	witnesses := json.RawMessage(`[{"material_id":"document-slot","revision_id":"rollback-current"}]`)
	if err := fixture.db.Create(&orm.WorkflowRouteDecision{
		ID: "rollback-witness-decision", SessionID: "rollback-session", FromStepID: "source",
		ActivatedJSON: json.RawMessage(`["route-step"]`), PrunedJSON: json.RawMessage(`[]`),
		BypassedJSON: json.RawMessage(`[]`), WitnessJSON: witnesses,
		Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func requireRollbackSelection(t *testing.T, fixture rollbackFixture, targetSelected, currentSelected bool) {
	t.Helper()
	var target, current orm.WorkflowSlotRevision
	if err := fixture.db.First(&target, "id = ?", fixture.targetRevisionID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.First(&current, "id = ?", fixture.currentRevisionID).Error; err != nil {
		t.Fatal(err)
	}
	if target.Selected != targetSelected || current.Selected != currentSelected {
		t.Fatalf("selection target=%v current=%v, want %v/%v", target.Selected, current.Selected, targetSelected, currentSelected)
	}
	if target.Validity != "effective" || current.Validity != "effective" ||
		target.Revision != 1 || current.Revision != 3 ||
		target.ChangeSource != "human" || current.ChangeSource != "human" ||
		target.HumanArtifactID == nil || *target.HumanArtifactID != "rollback-target-human" ||
		current.HumanArtifactID == nil || *current.HumanArtifactID != "rollback-current-human" {
		t.Fatalf("rollback rewrote lineage: target=%#v current=%#v", target, current)
	}
}

func TestRollbackSlotRevisionInvalidatesConsumersAndPersistsEvent(t *testing.T) {
	fixture := seedRollbackFixture(t, StepStatusSucceeded)
	extendRollbackTerminalGraph(t, fixture)
	target, err := RollbackSlotRevision(
		t.Context(), fixture.db.DB, "rollback-session", "document-slot", nil, 1, "rollback",
	)
	if err != nil || target == nil || target.ID != fixture.targetRevisionID {
		t.Fatalf("rollback: target=%#v err=%v", target, err)
	}
	requireRollbackSelection(t, fixture, true, false)
	requireAttemptValidity(t, artifactDependencyFixture{db: fixture.db}, fixture.consumerAttemptID, "stale")
	requireRevisionState(t, artifactDependencyFixture{db: fixture.db}, fixture.consumerOutputID, "stale", false)
	requireAttemptValidity(t, artifactDependencyFixture{db: fixture.db}, "rollback-downstream", "stale")
	requireRevisionState(t, artifactDependencyFixture{db: fixture.db}, "rollback-downstream-output", "stale", false)
	requireAttemptValidity(t, artifactDependencyFixture{db: fixture.db}, "rollback-route-attempt", "stale")
	requireRevisionState(t, artifactDependencyFixture{db: fixture.db}, "rollback-route-output", "stale", false)
	requireDecisionValidity(t, artifactDependencyFixture{db: fixture.db}, "rollback-witness-decision", "stale")
	session := loadArtifactEventSession(t, fixture.db, "rollback-session")
	events, payloads := loadArtifactUpsertEvents(t, fixture.db, "rollback-session")
	if session.StateVersion != 1 || len(events) != 1 {
		t.Fatalf("state=%d events=%#v", session.StateVersion, events)
	}
	want := artifactUpsertPayload{
		ArtifactID: fixture.targetRevisionID, SlotID: "document-slot", Slot: "document-key",
		Revision: 1, DraftVersion: 1, ChangeSource: "rollback", StateVersion: 1,
	}
	if events[0].ContractVersion != "workflow.v1" || events[0].OwnerUserID != "owner" ||
		events[0].EntityID != fixture.targetRevisionID || events[0].StateVersion != 1 ||
		!reflect.DeepEqual(payloads[0], want) {
		t.Fatalf("event=%#v payload=%#v want=%#v", events[0], payloads[0], want)
	}
}

func TestRollbackSlotRevisionNoOpDoesNotInvalidateOrAdvanceState(t *testing.T) {
	fixture := seedRollbackFixture(t, StepStatusSucceeded)
	target, err := RollbackSlotRevision(
		t.Context(), fixture.db.DB, "rollback-session", "document-slot", nil, 3, "rollback",
	)
	if err != nil || target == nil || target.ID != fixture.currentRevisionID {
		t.Fatalf("no-op rollback: target=%#v err=%v", target, err)
	}
	requireRollbackSelection(t, fixture, false, true)
	requireAttemptValidity(t, artifactDependencyFixture{db: fixture.db}, fixture.consumerAttemptID, "effective")
	if loadArtifactEventSession(t, fixture.db, "rollback-session").StateVersion != 0 {
		t.Fatal("no-op rollback advanced state")
	}
	events, _ := loadArtifactUpsertEvents(t, fixture.db, "rollback-session")
	if len(events) != 0 {
		t.Fatalf("no-op events=%#v", events)
	}
}

func TestRollbackSlotRevisionRejectsStaleTarget(t *testing.T) {
	fixture := seedRollbackFixture(t, "")
	if err := fixture.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", fixture.targetRevisionID).
		Update("validity", "stale").Error; err != nil {
		t.Fatal(err)
	}
	var targetBefore, currentBefore orm.WorkflowSlotRevision
	if err := fixture.db.First(&targetBefore, "id = ?", fixture.targetRevisionID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.First(&currentBefore, "id = ?", fixture.currentRevisionID).Error; err != nil {
		t.Fatal(err)
	}
	target, err := RollbackSlotRevision(
		t.Context(), fixture.db.DB, "rollback-session", "document-slot", nil, 1, "rollback",
	)
	if target != nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("stale rollback: target=%#v err=%v", target, err)
	}
	requireRevisionState(t, artifactDependencyFixture{db: fixture.db}, fixture.targetRevisionID, "stale", false)
	requireRevisionState(t, artifactDependencyFixture{db: fixture.db}, fixture.currentRevisionID, "effective", true)
	var targetAfter, currentAfter orm.WorkflowSlotRevision
	if err := fixture.db.First(&targetAfter, "id = ?", fixture.targetRevisionID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.First(&currentAfter, "id = ?", fixture.currentRevisionID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(targetAfter, targetBefore) || !reflect.DeepEqual(currentAfter, currentBefore) {
		t.Fatalf("stale rollback rewrote revisions: target=%#v current=%#v", targetAfter, currentAfter)
	}
	if loadArtifactEventSession(t, fixture.db, "rollback-session").StateVersion != 0 {
		t.Fatal("stale rollback advanced state")
	}
	events, _ := loadArtifactUpsertEvents(t, fixture.db, "rollback-session")
	if len(events) != 0 {
		t.Fatalf("stale rollback events=%#v", events)
	}
}

func TestRollbackSlotRevisionRejectsLiveConsumer(t *testing.T) {
	fixture := seedRollbackFixture(t, StepStatusRunning)
	if err := fixture.db.Delete(&orm.WorkflowSlotRevision{}, "id = ?", fixture.consumerOutputID).Error; err != nil {
		t.Fatal(err)
	}
	target, err := RollbackSlotRevision(
		t.Context(), fixture.db.DB, "rollback-session", "document-slot", nil, 1, "rollback",
	)
	if target != nil || err == nil || err.Error() != "ARTIFACT_IN_USE" {
		t.Fatalf("live rollback: target=%#v err=%v", target, err)
	}
	requireRollbackSelection(t, fixture, false, true)
	requireAttemptValidity(t, artifactDependencyFixture{db: fixture.db}, fixture.consumerAttemptID, "effective")
	if loadArtifactEventSession(t, fixture.db, "rollback-session").StateVersion != 0 {
		t.Fatal("live rollback advanced state")
	}
	events, _ := loadArtifactUpsertEvents(t, fixture.db, "rollback-session")
	if len(events) != 0 {
		t.Fatalf("live rollback events=%#v", events)
	}
}

func TestRollbackSlotRevisionScopesListIndex(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "list-rollback-session", ConversationID: "conversation", WorkflowID: "workflow",
		CreateUserID: "owner", Status: SessionStatusActive, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	index0, index1 := 0, 1
	rows := []orm.WorkflowSlotRevision{
		{ID: "list-0-target", SessionID: "list-rollback-session", SlotID: "items", ListIndex: &index0, Revision: 1, Selected: false, Slot: "item-key", StepID: "write", Validity: "effective", CreatedAt: now},
		{ID: "list-0-current", SessionID: "list-rollback-session", SlotID: "items", ListIndex: &index0, Revision: 2, Selected: true, Slot: "item-key", StepID: "write", Validity: "effective", CreatedAt: now},
		{ID: "list-1-current", SessionID: "list-rollback-session", SlotID: "items", ListIndex: &index1, Revision: 1, Selected: true, Slot: "item-key", StepID: "write", Validity: "effective", CreatedAt: now},
	}
	for index := range rows {
		if err := db.Create(&rows[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "list-0-target").Update("selected", false).Error; err != nil {
		t.Fatal(err)
	}
	target, err := RollbackSlotRevision(
		t.Context(), db.DB, "list-rollback-session", "items", &index0, 1, "rollback",
	)
	if err != nil || target == nil || target.ID != "list-0-target" {
		t.Fatalf("list rollback: target=%#v err=%v", target, err)
	}
	var selected []orm.WorkflowSlotRevision
	if err := db.Where("session_id = ? AND slot_id = ? AND selected = ?", "list-rollback-session", "items", true).
		Order("list_index ASC").Find(&selected).Error; err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[0].ID != "list-0-target" || selected[1].ID != "list-1-current" {
		t.Fatalf("selected list rows=%#v", selected)
	}
	if loadArtifactEventSession(t, db, "list-rollback-session").StateVersion != 1 {
		t.Fatal("list rollback did not advance state")
	}
	events, payloads := loadArtifactUpsertEvents(t, db, "list-rollback-session")
	if len(events) != 1 || events[0].ContractVersion != "workflow.v1" ||
		events[0].OwnerUserID != "owner" || events[0].StateVersion != 1 ||
		events[0].EntityID != "list-0-target" ||
		payloads[0].ListIndex == nil || *payloads[0].ListIndex != 0 ||
		payloads[0].ArtifactID != "list-0-target" || payloads[0].ChangeSource != "rollback" {
		t.Fatalf("list rollback event=%#v payload=%#v", events, payloads)
	}
}

func TestRollbackSlotRevisionListNoOpAndStaleTarget(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		targetRevision int
		targetValidity string
		wantNotFound   bool
	}{
		{name: "selected no-op", targetRevision: 2, targetValidity: "effective"},
		{name: "stale target", targetRevision: 1, targetValidity: "stale", wantNotFound: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db := newTestDB(t)
			now := time.Now().UTC()
			if err := db.Create(&orm.WorkflowSession{
				ID: "list-guard-session", ConversationID: "conversation", WorkflowID: "workflow",
				CreateUserID: "owner", Status: SessionStatusActive, CreatedAt: now, UpdatedAt: now,
			}).Error; err != nil {
				t.Fatal(err)
			}
			index0, index1 := 0, 1
			rows := []orm.WorkflowSlotRevision{
				{ID: "list-guard-target", SessionID: "list-guard-session", SlotID: "items", ListIndex: &index0, Revision: 1, Selected: false, Slot: "item-key", StepID: "write", Validity: testCase.targetValidity, CreatedAt: now},
				{ID: "list-guard-current", SessionID: "list-guard-session", SlotID: "items", ListIndex: &index0, Revision: 2, Selected: true, Slot: "item-key", StepID: "write", Validity: "effective", CreatedAt: now},
				{ID: "list-guard-other", SessionID: "list-guard-session", SlotID: "items", ListIndex: &index1, Revision: 1, Selected: true, Slot: "item-key", StepID: "write", Validity: "effective", CreatedAt: now},
			}
			for index := range rows {
				if err := db.Create(&rows[index]).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "list-guard-target").
				Update("selected", false).Error; err != nil {
				t.Fatal(err)
			}
			target, err := RollbackSlotRevision(
				t.Context(), db.DB, "list-guard-session", "items", &index0,
				testCase.targetRevision, "rollback",
			)
			if testCase.wantNotFound {
				if target != nil || !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("stale list target=%#v err=%v", target, err)
				}
			} else if err != nil || target == nil || target.ID != "list-guard-current" {
				t.Fatalf("list no-op target=%#v err=%v", target, err)
			}
			var selected []orm.WorkflowSlotRevision
			if err := db.Where("session_id = ? AND slot_id = ? AND selected = ?", "list-guard-session", "items", true).
				Order("list_index ASC").Find(&selected).Error; err != nil {
				t.Fatal(err)
			}
			if len(selected) != 2 || selected[0].ID != "list-guard-current" || selected[1].ID != "list-guard-other" {
				t.Fatalf("list guard selected=%#v", selected)
			}
			events, _ := loadArtifactUpsertEvents(t, db, "list-guard-session")
			if len(events) != 0 || loadArtifactEventSession(t, db, "list-guard-session").StateVersion != 0 {
				t.Fatalf("list guard changed state: events=%#v", events)
			}
		})
	}
}

func TestRollbackSlotRevisionRollsBackWhenEventFails(t *testing.T) {
	fixture := seedRollbackFixture(t, StepStatusSucceeded)
	extendRollbackTerminalGraph(t, fixture)
	failArtifactEventCreates(t, fixture.db)
	target, err := RollbackSlotRevision(
		t.Context(), fixture.db.DB, "rollback-session", "document-slot", nil, 1, "rollback",
	)
	if target != nil || !errors.Is(err, errForcedArtifactEvent) {
		t.Fatalf("event failure rollback: target=%#v err=%v", target, err)
	}
	requireRollbackSelection(t, fixture, false, true)
	requireAttemptValidity(t, artifactDependencyFixture{db: fixture.db}, fixture.consumerAttemptID, "effective")
	requireRevisionState(t, artifactDependencyFixture{db: fixture.db}, fixture.consumerOutputID, "effective", true)
	requireAttemptValidity(t, artifactDependencyFixture{db: fixture.db}, "rollback-downstream", "effective")
	requireRevisionState(t, artifactDependencyFixture{db: fixture.db}, "rollback-downstream-output", "effective", true)
	requireAttemptValidity(t, artifactDependencyFixture{db: fixture.db}, "rollback-route-attempt", "effective")
	requireRevisionState(t, artifactDependencyFixture{db: fixture.db}, "rollback-route-output", "effective", true)
	requireDecisionValidity(t, artifactDependencyFixture{db: fixture.db}, "rollback-witness-decision", "effective")
	if loadArtifactEventSession(t, fixture.db, "rollback-session").StateVersion != 0 {
		t.Fatal("failed rollback advanced state")
	}
	events, _ := loadArtifactUpsertEvents(t, fixture.db, "rollback-session")
	if len(events) != 0 {
		t.Fatalf("failed rollback events=%#v", events)
	}
}

func TestSelectionHandlersReturnArtifactInUse(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		listIndex string
		call      func(http.ResponseWriter, *http.Request)
		body      string
	}{
		{name: "single selection", listIndex: "", call: PatchSessionSlot, body: `{"selected_revision":1}`},
		{name: "list rollback", listIndex: "0", call: RollbackSlotItemByIndex, body: `{"revision":1}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := seedRollbackFixture(t, StepStatusRunning)
			if err := fixture.db.Delete(&orm.WorkflowSlotRevision{}, "id = ?", fixture.consumerOutputID).Error; err != nil {
				t.Fatal(err)
			}
			if testCase.listIndex != "" {
				index := 0
				if err := fixture.db.Model(&orm.WorkflowSlotRevision{}).
					Where("id IN ?", []string{fixture.targetRevisionID, fixture.currentRevisionID}).
					Update("list_index", index).Error; err != nil {
					t.Fatal(err)
				}
			}
			corestore.Init(fixture.db.DB, nil, nil)
			t.Cleanup(func() { corestore.Init(nil, nil, nil) })
			path := "/workflow-sessions/rollback-session/slots/document-slot"
			method := http.MethodPatch
			vars := map[string]string{"session_id": "rollback-session", "slot_id": "document-slot"}
			if testCase.listIndex != "" {
				path += "/items/idx/0/rollback"
				method = http.MethodPost
				vars["list_index"] = testCase.listIndex
			}
			req := mux.SetURLVars(
				httptest.NewRequest(method, path, strings.NewReader(testCase.body)), vars,
			)
			recorder := httptest.NewRecorder()
			testCase.call(recorder, req)
			if recorder.Code != http.StatusConflict || responseData(t, recorder)["code"] != "ARTIFACT_IN_USE" {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			requireRollbackSelection(t, fixture, false, true)
			events, _ := loadArtifactUpsertEvents(t, fixture.db, "rollback-session")
			if len(events) != 0 {
				t.Fatalf("rejected handler events=%#v", events)
			}
		})
	}
}

func TestPatchSessionSlotUsesSelectionMutationContract(t *testing.T) {
	fixture := seedRollbackFixture(t, StepStatusSucceeded)
	extendRollbackTerminalGraph(t, fixture)
	corestore.Init(fixture.db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })
	req := mux.SetURLVars(
		httptest.NewRequest(
			http.MethodPatch, "/workflow-sessions/rollback-session/slots/document-slot",
			strings.NewReader(`{"selected_revision":1}`),
		),
		map[string]string{"session_id": "rollback-session", "slot_id": "document-slot"},
	)
	recorder := httptest.NewRecorder()
	PatchSessionSlot(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("selection status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	requireRollbackSelection(t, fixture, true, false)
	requireAttemptValidity(t, artifactDependencyFixture{db: fixture.db}, fixture.consumerAttemptID, "stale")
	requireRevisionState(t, artifactDependencyFixture{db: fixture.db}, fixture.consumerOutputID, "stale", false)
	requireAttemptValidity(t, artifactDependencyFixture{db: fixture.db}, "rollback-downstream", "stale")
	requireRevisionState(t, artifactDependencyFixture{db: fixture.db}, "rollback-downstream-output", "stale", false)
	requireAttemptValidity(t, artifactDependencyFixture{db: fixture.db}, "rollback-route-attempt", "stale")
	requireRevisionState(t, artifactDependencyFixture{db: fixture.db}, "rollback-route-output", "stale", false)
	requireDecisionValidity(t, artifactDependencyFixture{db: fixture.db}, "rollback-witness-decision", "stale")
	events, payloads := loadArtifactUpsertEvents(t, fixture.db, "rollback-session")
	want := artifactUpsertPayload{
		ArtifactID: fixture.targetRevisionID, SlotID: "document-slot", Slot: "document-key",
		Revision: 1, DraftVersion: 1, ChangeSource: "selection", StateVersion: 1,
	}
	if len(events) != 1 || events[0].ContractVersion != "workflow.v1" ||
		events[0].OwnerUserID != "owner" || events[0].StateVersion != 1 ||
		events[0].EntityID != fixture.targetRevisionID ||
		!reflect.DeepEqual(payloads[0], want) {
		t.Fatalf("selection event=%#v payload=%#v want=%#v", events, payloads, want)
	}
}

func TestRollbackSerializesWithConcurrentRevisionWriter(t *testing.T) {
	fixture := seedRollbackFixture(t, "")
	start := make(chan struct{})
	type result struct {
		operation string
		revision  *orm.WorkflowSlotRevision
		err       error
	}
	results := make(chan result, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		revision, err := RollbackSlotRevision(
			t.Context(), fixture.db.DB, "rollback-session", "document-slot", nil, 1, "rollback",
		)
		results <- result{operation: "rollback", revision: revision, err: err}
	}()
	go func() {
		defer workers.Done()
		<-start
		baseRevision, baseDraftVersion := 3, int64(1)
		revision, err := WriteSlotRevisionWithHumanArtifact(
			t.Context(), fixture.db.DB, "rollback-session", "document-slot", "document-key",
			"source", 3, "single", nil, "text", json.RawMessage(`{"text":"concurrent"}`), nil,
			"human", &baseRevision, &baseDraftVersion,
		)
		results <- result{operation: "writer", revision: revision, err: err}
	}()
	close(start)
	workers.Wait()
	close(results)
	var rollbackResult, writerResult result
	for result := range results {
		if result.operation == "rollback" {
			rollbackResult = result
		} else {
			writerResult = result
		}
	}
	if rollbackResult.err != nil || rollbackResult.revision == nil ||
		rollbackResult.revision.ID != fixture.targetRevisionID {
		t.Fatalf("rollback result = %#v", rollbackResult)
	}
	if writerResult.err != nil && !errors.Is(writerResult.err, ErrConflict) {
		t.Fatalf("writer result = %#v", writerResult)
	}
	wantMutations := int64(1)
	if writerResult.err == nil {
		wantMutations = 2
		if writerResult.revision == nil || writerResult.revision.Revision != 4 ||
			writerResult.revision.HumanArtifactID == nil {
			t.Fatalf("successful writer result = %#v", writerResult)
		}
	}
	var selectedRows []orm.WorkflowSlotRevision
	if err := fixture.db.Where(
		"session_id = ? AND slot_id = ? AND selected = ?", "rollback-session", "document-slot", true,
	).Find(&selectedRows).Error; err != nil {
		t.Fatal(err)
	}
	events, payloads := loadArtifactUpsertEvents(t, fixture.db, "rollback-session")
	session := loadArtifactEventSession(t, fixture.db, "rollback-session")
	if len(selectedRows) != 1 || selectedRows[0].ID != fixture.targetRevisionID ||
		int64(len(events)) != wantMutations || session.StateVersion != wantMutations {
		t.Fatalf("serialized result: selected=%#v events=%#v state=%d writer=%#v", selectedRows, events, session.StateVersion, writerResult)
	}
	if writerResult.err == nil {
		if events[0].EntityID != writerResult.revision.ID || events[0].StateVersion != 1 ||
			events[1].EntityID != fixture.targetRevisionID || events[1].StateVersion != 2 ||
			payloads[0].ArtifactID != writerResult.revision.ID || payloads[0].ChangeSource != "human" ||
			payloads[0].StateVersion != 1 || payloads[1].ArtifactID != fixture.targetRevisionID ||
			payloads[1].ChangeSource != "rollback" || payloads[1].StateVersion != 2 {
			t.Fatalf("two-mutation event sequence=%#v", payloads)
		}
	} else if events[0].EntityID != fixture.targetRevisionID || events[0].StateVersion != 1 ||
		payloads[0].ArtifactID != fixture.targetRevisionID ||
		payloads[0].ChangeSource != "rollback" || payloads[0].StateVersion != 1 {
		t.Fatalf("rollback-only event sequence=%#v", payloads)
	}
}

func TestRollbackWaitsForPostgreSQLSessionLock(t *testing.T) {
	fixture := seedRollbackFixture(t, "")
	if fixture.db.Dialector.Name() != "postgres" {
		t.Skip("requires PostgreSQL row locking")
	}
	guardTx := fixture.db.Begin()
	if guardTx.Error != nil {
		t.Fatal(guardTx.Error)
	}
	defer guardTx.Rollback()
	if _, err := artifactgraph.LockSession(guardTx, "rollback-session"); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := RollbackSlotRevision(
			t.Context(), fixture.db.DB, "rollback-session", "document-slot", nil, 1, "rollback",
		)
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("rollback escaped Session lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := guardTx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	requireRollbackSelection(t, fixture, true, false)
}
