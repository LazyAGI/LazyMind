package workflow

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

type artifactUpsertPayload struct {
	ArtifactID   string `json:"artifact_id"`
	AttemptID    string `json:"attempt_id"`
	SlotID       string `json:"slot_id"`
	Slot         string `json:"slot"`
	Revision     int    `json:"revision"`
	ListIndex    *int   `json:"list_index"`
	DraftVersion int64  `json:"draft_version"`
	ChangeSource string `json:"change_source"`
	StateVersion int64  `json:"state_version"`
}

func setArtifactEventOwner(t *testing.T, db *orm.DB, sessionID string) {
	t.Helper()
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", sessionID).
		Update("create_user_id", "owner").Error; err != nil {
		t.Fatal(err)
	}
}

func loadArtifactUpsertEvents(
	t *testing.T, db *orm.DB, sessionID string,
) ([]orm.WorkflowEvent, []artifactUpsertPayload) {
	t.Helper()
	var events []orm.WorkflowEvent
	if err := db.Where("session_id = ? AND event_type = ?", sessionID, "artifact.upsert").
		Order("id ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	payloads := make([]artifactUpsertPayload, len(events))
	for index := range events {
		if err := json.Unmarshal(events[index].PayloadJSON, &payloads[index]); err != nil {
			t.Fatalf("decode event %d: %v", index, err)
		}
	}
	return events, payloads
}

func loadArtifactEventSession(t *testing.T, db *orm.DB, sessionID string) orm.WorkflowSession {
	t.Helper()
	var session orm.WorkflowSession
	if err := db.First(&session, "id = ?", sessionID).Error; err != nil {
		t.Fatal(err)
	}
	return session
}

func TestHumanDraftMutationAdvancesStateAndAppendsDurableEvent(t *testing.T) {
	db := seedSelectedHumanDraft(t)
	setArtifactEventOwner(t, db, "session-draft")
	if err := db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "revision-draft").
		Updates(map[string]any{"slot_id": "draft-slot-id", "slot": "draft-artifact-key"}).Error; err != nil {
		t.Fatal(err)
	}
	baseRevision, baseDraftVersion := 1, int64(1)
	updated, draftVersion, updatedInPlace, err := UpdateSelectedHumanArtifactValue(
		t.Context(), db.DB, "session-draft", "draft-slot-id", nil,
		"text", json.RawMessage(`{"text":"updated"}`), nil,
		&baseRevision, &baseDraftVersion,
	)
	if err != nil || !updatedInPlace || draftVersion != 2 || updated == nil {
		t.Fatalf("draft save: updated=%#v in_place=%v draft_version=%d err=%v", updated, updatedInPlace, draftVersion, err)
	}
	session := loadArtifactEventSession(t, db, "session-draft")
	if session.StateVersion != 1 {
		t.Errorf("state version = %d, want 1", session.StateVersion)
	}
	events, payloads := loadArtifactUpsertEvents(t, db, "session-draft")
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one artifact.upsert", events)
	}
	want := artifactUpsertPayload{
		ArtifactID: "revision-draft", SlotID: "draft-slot-id", Slot: "draft-artifact-key",
		Revision: 1, DraftVersion: 2, ChangeSource: "human", StateVersion: 1,
	}
	if events[0].ContractVersion != "workflow.v1" || events[0].OwnerUserID != "owner" || events[0].EntityID != want.ArtifactID ||
		events[0].StateVersion != 1 || !reflect.DeepEqual(payloads[0], want) {
		t.Fatalf("event=%#v payload=%#v want=%#v", events[0], payloads[0], want)
	}
}

func TestHumanCheckpointAdvancesStateAndAppendsDurableEvent(t *testing.T) {
	db := seedSelectedHumanDraft(t)
	setArtifactEventOwner(t, db, "session-draft")
	if err := db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "revision-draft").
		Updates(map[string]any{"slot_id": "provider-slot-id", "slot": "provider-document"}).Error; err != nil {
		t.Fatal(err)
	}
	baseRevision, baseDraftVersion := 1, int64(1)
	created, err := WriteSlotRevisionWithHumanArtifact(
		t.Context(), db.DB, "session-draft", "provider-slot-id", "provider-document", "write_document", 1,
		"single", nil, "text", json.RawMessage(`{"text":"provider checkpoint"}`), nil,
		"provider_sync", &baseRevision, &baseDraftVersion,
	)
	if err != nil || created == nil {
		t.Fatalf("provider checkpoint: created=%#v err=%v", created, err)
	}
	var selected orm.WorkflowSlotRevision
	if err := db.Where("session_id = ? AND slot_id = ? AND selected = ?", "session-draft", "provider-slot-id", true).
		First(&selected).Error; err != nil {
		t.Fatal(err)
	}
	session := loadArtifactEventSession(t, db, "session-draft")
	if session.StateVersion != 1 {
		t.Errorf("state version = %d, want 1", session.StateVersion)
	}
	events, payloads := loadArtifactUpsertEvents(t, db, "session-draft")
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one artifact.upsert", events)
	}
	want := artifactUpsertPayload{
		ArtifactID: selected.ID, SlotID: selected.SlotID, Slot: selected.Slot,
		Revision: 2, DraftVersion: 1, ChangeSource: "provider_sync", StateVersion: 1,
	}
	if events[0].ContractVersion != "workflow.v1" || events[0].OwnerUserID != "owner" || events[0].EntityID != selected.ID ||
		events[0].StateVersion != 1 || !reflect.DeepEqual(payloads[0], want) {
		t.Fatalf("event=%#v payload=%#v want=%#v", events[0], payloads[0], want)
	}
}

func TestNativeArtifactRevisionAdvancesStateAndAppendsDurableEvent(t *testing.T) {
	db := newTestDB(t)
	ctx := t.Context()
	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "session-native", ConversationID: "conversation-native",
		WorkflowID: "workflow-native", CreateUserID: "owner",
	}); err != nil {
		t.Fatal(err)
	}
	step, err := CreateSessionStep(ctx, db.DB, "session-native", "write", "task-native", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.SubAgentArtifact{
		ID: "artifact-native", TaskID: "task-native", Slot: "native-report", ContentType: "text",
		Value: json.RawMessage(`{"text":"native"}`), Seq: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	revision, err := WriteSlotRevision(
		ctx, db.DB, "session-native", "native-slot-id", "native-report", "write", 1, "single", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if revision.ArtifactSeq == nil || *revision.ArtifactSeq != 1 || revision.ProducerAttemptID != step.ID {
		t.Fatalf("native revision = %#v", revision)
	}
	session := loadArtifactEventSession(t, db, "session-native")
	if session.StateVersion != 1 {
		t.Errorf("state version = %d, want 1", session.StateVersion)
	}
	events, payloads := loadArtifactUpsertEvents(t, db, "session-native")
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one artifact.upsert", events)
	}
	want := artifactUpsertPayload{
		ArtifactID: revision.ID, AttemptID: step.ID, SlotID: "native-slot-id", Slot: "native-report",
		Revision: 1, ChangeSource: "ai", StateVersion: 1,
	}
	if events[0].ContractVersion != "workflow.v1" || events[0].OwnerUserID != "owner" || events[0].EntityID != revision.ID ||
		events[0].StateVersion != 1 || !reflect.DeepEqual(payloads[0], want) {
		t.Fatalf("event=%#v payload=%#v want=%#v", events[0], payloads[0], want)
	}
}

func TestConcurrentArtifactWritersAdvanceStateExactlyThreeTimes(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateSession(t.Context(), db.DB, CreateSessionInput{
		SessionID: "session-concurrent-events", ConversationID: "conversation-concurrent-events",
		WorkflowID: "workflow-events", CreateUserID: "owner",
	}); err != nil {
		t.Fatal(err)
	}
	humanID := "concurrent-draft-human"
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: humanID, SessionID: "session-concurrent-events", Slot: "draft-artifact-key",
		ContentType: "text", Value: json.RawMessage(`{"text":"original"}`), DraftVersion: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "concurrent-draft-revision", SessionID: "session-concurrent-events",
		SlotID: "draft-slot-id", Slot: "draft-artifact-key", StepID: "draft-step",
		Revision: 1, Selected: true, HumanArtifactID: &humanID, ChangeSource: "human",
	}).Error; err != nil {
		t.Fatal(err)
	}
	nativeStep, err := CreateSessionStep(
		t.Context(), db.DB, "session-concurrent-events", "native-step", "concurrent-native-task", 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.SubAgentArtifact{
		ID: "concurrent-native-artifact", TaskID: "concurrent-native-task", Slot: "native-artifact-key",
		ContentType: "text", Value: json.RawMessage(`{"text":"native"}`), Seq: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	type writeResult struct {
		kind           string
		revision       *orm.WorkflowSlotRevision
		draftVersion   int64
		updatedInPlace bool
		err            error
	}
	results := make(chan writeResult, 3)
	var workers sync.WaitGroup
	workers.Add(3)
	go func() {
		defer workers.Done()
		<-start
		baseRevision, baseDraftVersion := 1, int64(1)
		revision, draftVersion, updatedInPlace, err := UpdateSelectedHumanArtifactValue(
			t.Context(), db.DB, "session-concurrent-events", "draft-slot-id", nil,
			"text", json.RawMessage(`{"text":"updated"}`), nil,
			&baseRevision, &baseDraftVersion,
		)
		results <- writeResult{
			kind: "draft", revision: revision, draftVersion: draftVersion,
			updatedInPlace: updatedInPlace, err: err,
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		revision, err := WriteSlotRevision(
			t.Context(), db.DB, "session-concurrent-events", "native-slot-id", "native-artifact-key",
			"native-step", 1, "single", nil,
		)
		results <- writeResult{kind: "native", revision: revision, err: err}
	}()
	go func() {
		defer workers.Done()
		<-start
		revision, err := WriteSlotRevisionWithHumanArtifact(
			t.Context(), db.DB, "session-concurrent-events", "new-slot-id", "new-artifact-key", "write", 1,
			"single", nil, "text", json.RawMessage(`{"text":"value"}`), nil,
			"human", nil, nil,
		)
		results <- writeResult{kind: "human", revision: revision, err: err}
	}()
	close(start)
	workers.Wait()
	close(results)
	writes := map[string]writeResult{}
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent %s write: %v", result.kind, result.err)
		}
		writes[result.kind] = result
	}
	if len(writes) != 3 {
		t.Fatalf("write results = %#v", writes)
	}
	draftWrite := writes["draft"]
	if !draftWrite.updatedInPlace || draftWrite.draftVersion != 2 ||
		draftWrite.revision == nil || draftWrite.revision.ID != "concurrent-draft-revision" {
		t.Fatalf("draft write = %#v", draftWrite)
	}
	nativeWrite := writes["native"]
	if nativeWrite.revision == nil || nativeWrite.revision.ArtifactSeq == nil ||
		*nativeWrite.revision.ArtifactSeq != 1 || nativeWrite.revision.ProducerAttemptID != nativeStep.ID {
		t.Fatalf("native write = %#v", nativeWrite)
	}
	humanWrite := writes["human"]
	if humanWrite.revision == nil || humanWrite.revision.HumanArtifactID == nil ||
		humanWrite.revision.SlotID != "new-slot-id" || humanWrite.revision.Revision != 1 {
		t.Fatalf("human write = %#v", humanWrite)
	}

	var persistedDraft orm.WorkflowHumanArtifact
	if err := db.First(&persistedDraft, "id = ?", humanID).Error; err != nil {
		t.Fatal(err)
	}
	assertArtifactJSON(t, persistedDraft.Value, `{"text":"updated"}`)
	if persistedDraft.DraftVersion != 2 {
		t.Fatalf("persisted draft version = %d, want 2", persistedDraft.DraftVersion)
	}
	var persistedNative orm.WorkflowSlotRevision
	if err := db.First(&persistedNative, "id = ?", nativeWrite.revision.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persistedNative.ArtifactSeq == nil || *persistedNative.ArtifactSeq != 1 ||
		persistedNative.ProducerAttemptID != nativeStep.ID || !persistedNative.Selected {
		t.Fatalf("persisted native revision = %#v", persistedNative)
	}
	var persistedHuman orm.WorkflowSlotRevision
	if err := db.First(&persistedHuman, "id = ?", humanWrite.revision.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persistedHuman.HumanArtifactID == nil || !persistedHuman.Selected {
		t.Fatalf("persisted human revision = %#v", persistedHuman)
	}
	var persistedHumanValue orm.WorkflowHumanArtifact
	if err := db.First(&persistedHumanValue, "id = ?", *persistedHuman.HumanArtifactID).Error; err != nil {
		t.Fatal(err)
	}
	assertArtifactJSON(t, persistedHumanValue.Value, `{"text":"value"}`)
	if persistedHumanValue.DraftVersion != 1 {
		t.Fatalf("persisted human draft version = %d, want 1", persistedHumanValue.DraftVersion)
	}
	session := loadArtifactEventSession(t, db, "session-concurrent-events")
	if session.StateVersion != 3 {
		t.Fatalf("state version = %d, want 3", session.StateVersion)
	}
	events, payloads := loadArtifactUpsertEvents(t, db, "session-concurrent-events")
	if len(events) != 3 {
		t.Fatalf("events = %#v, want three artifact.upsert events", events)
	}
	versions := []int64{events[0].StateVersion, events[1].StateVersion, events[2].StateVersion}
	if !reflect.DeepEqual(versions, []int64{1, 2, 3}) {
		t.Fatalf("event state versions in cursor order = %v, want [1 2 3]", versions)
	}
	wantEntities := map[string]int{
		draftWrite.revision.ID:  1,
		nativeWrite.revision.ID: 1,
		humanWrite.revision.ID:  1,
	}
	gotEntities := map[string]int{}
	for index := range events {
		if events[index].ContractVersion != "workflow.v1" ||
			payloads[index].ArtifactID != events[index].EntityID ||
			payloads[index].StateVersion != events[index].StateVersion {
			t.Fatalf("event=%#v payload=%#v", events[index], payloads[index])
		}
		gotEntities[events[index].EntityID]++
	}
	if !reflect.DeepEqual(gotEntities, wantEntities) {
		t.Fatalf("event entities = %#v, want %#v", gotEntities, wantEntities)
	}
}

var errForcedArtifactEvent = errors.New("forced artifact event failure")

func failArtifactEventCreates(t *testing.T, db *orm.DB) {
	t.Helper()
	if err := db.Callback().Create().Before("gorm:create").Register(
		"test:fail_artifact_event", func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "workflow_events" {
				tx.AddError(errForcedArtifactEvent)
			}
		},
	); err != nil {
		t.Fatal(err)
	}
}

func TestHumanDraftRollsBackWhenDurableEventFails(t *testing.T) {
	db := seedSelectedHumanDraft(t)
	setArtifactEventOwner(t, db, "session-draft")
	failArtifactEventCreates(t, db)
	baseRevision, baseDraftVersion := 1, int64(1)
	_, _, _, err := UpdateSelectedHumanArtifactValue(
		t.Context(), db.DB, "session-draft", "draft_document", nil,
		"text", json.RawMessage(`{"text":"must roll back"}`), nil,
		&baseRevision, &baseDraftVersion,
	)
	if !errors.Is(err, errForcedArtifactEvent) {
		t.Errorf("error = %v, want forced event failure", err)
	}
	artifact, draftVersion := loadSelectedHumanDraft(t, db)
	assertArtifactJSON(t, artifact.Value, `{"text":"original"}`)
	if draftVersion != 1 || loadArtifactEventSession(t, db, "session-draft").StateVersion != 0 {
		t.Fatalf("draft_version=%d session=%#v", draftVersion, loadArtifactEventSession(t, db, "session-draft"))
	}
	events, _ := loadArtifactUpsertEvents(t, db, "session-draft")
	if len(events) != 0 {
		t.Fatalf("events = %#v, want none", events)
	}
}

func TestHumanRevisionRollsBackWhenDurableEventFails(t *testing.T) {
	db := seedSelectedHumanDraft(t)
	setArtifactEventOwner(t, db, "session-draft")
	failArtifactEventCreates(t, db)
	baseRevision, baseDraftVersion := 1, int64(1)
	created, err := WriteSlotRevisionWithHumanArtifact(
		t.Context(), db.DB, "session-draft", "draft_document", "draft_document", "write_document", 1,
		"single", nil, "text", json.RawMessage(`{"text":"must roll back"}`), nil,
		"human", &baseRevision, &baseDraftVersion,
	)
	if !errors.Is(err, errForcedArtifactEvent) || created != nil {
		t.Errorf("created=%#v error=%v, want forced event failure", created, err)
	}
	assertSingleSelectedRevisionOne(t, db)
	var artifacts int64
	if err := db.Model(&orm.WorkflowHumanArtifact{}).Count(&artifacts).Error; err != nil {
		t.Fatal(err)
	}
	if artifacts != 1 || loadArtifactEventSession(t, db, "session-draft").StateVersion != 0 {
		t.Fatalf("artifacts=%d session=%#v", artifacts, loadArtifactEventSession(t, db, "session-draft"))
	}
	events, _ := loadArtifactUpsertEvents(t, db, "session-draft")
	if len(events) != 0 {
		t.Fatalf("events = %#v, want none", events)
	}
}

func TestNativeRevisionRollsBackWhenDurableEventFails(t *testing.T) {
	db := newTestDB(t)
	if _, err := CreateSession(t.Context(), db.DB, CreateSessionInput{
		SessionID: "session-native-failure", ConversationID: "conversation-native-failure",
		WorkflowID: "workflow-native", CreateUserID: "owner",
	}); err != nil {
		t.Fatal(err)
	}
	oldSeq := 1
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "native-old-revision", SessionID: "session-native-failure",
		SlotID: "native-slot-id", Slot: "native-artifact-key", StepID: "write",
		Revision: 1, Selected: true, ArtifactSeq: &oldSeq, ChangeSource: "ai",
	}).Error; err != nil {
		t.Fatal(err)
	}
	failArtifactEventCreates(t, db)
	created, err := WriteSlotRevision(
		t.Context(), db.DB, "session-native-failure", "native-slot-id", "native-artifact-key", "write", 2, "single", nil,
	)
	if !errors.Is(err, errForcedArtifactEvent) || created != nil {
		t.Errorf("created=%#v error=%v, want forced event failure", created, err)
	}
	var revisions []orm.WorkflowSlotRevision
	if err := db.Where("session_id = ?", "session-native-failure").Find(&revisions).Error; err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 1 || revisions[0].ID != "native-old-revision" ||
		revisions[0].Revision != 1 || !revisions[0].Selected ||
		loadArtifactEventSession(t, db, "session-native-failure").StateVersion != 0 {
		t.Fatalf("revisions=%#v session=%#v", revisions, loadArtifactEventSession(t, db, "session-native-failure"))
	}
	events, _ := loadArtifactUpsertEvents(t, db, "session-native-failure")
	if len(events) != 0 {
		t.Fatalf("events = %#v, want none", events)
	}
}
