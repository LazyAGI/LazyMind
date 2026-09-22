package workflow

import (
	"encoding/json"
	"errors"
	"lazymind/core/workflow/artifactgraph"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func publicationFixture(t *testing.T, graph bool) (rollbackFixture, DocumentPublicationInput) {
	t.Helper()
	status := ""
	if graph {
		status = StepStatusSucceeded
	}
	f := seedRollbackFixture(t, status)
	if graph {
		extendRollbackTerminalGraph(t, f)
	}
	if err := f.db.AutoMigrate(&orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}); err != nil {
		t.Fatal(err)
	}
	value := json.RawMessage(`{"schema":"text/markdown","data":"# Draft"}`)
	if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Updates(map[string]any{"content_type": "json", "value": value}).Error; err != nil {
		t.Fatal(err)
	}
	draft := int64(1)
	return f, DocumentPublicationInput{OwnerUserID: "owner", SessionID: "rollback-session", SlotID: "document-slot", BaseRevision: 3, BaseDraftVersion: &draft, IdempotencyKey: "request-a", Provider: "obsidian", Title: "Draft", ParentURI: "fixture://vault"}
}

func publicationReceipt() DocumentPublicationReceipt {
	return DocumentPublicationReceipt{Provider: "obsidian", TargetDocument: json.RawMessage(`{"adapter":"obsidian","document_id":"fixture-document","uri":"fixture://vault/draft.md"}`), ContentType: "json", Value: json.RawMessage(`{"schema":"text/markdown","data":"# Published"}`)}
}

func preparePublication(t *testing.T, f rollbackFixture, input DocumentPublicationInput) *DocumentPublicationOperation {
	t.Helper()
	op, err := PrepareDocumentPublication(t.Context(), f.db.DB, input)
	if err != nil || op == nil {
		t.Fatalf("prepare publication: op=%#v err=%v", op, err)
	}
	if op.ID == "" || op.Status != "preparing" || op.SourceRevisionID != f.currentRevisionID || op.SourceRevision != input.BaseRevision || op.SourceDraftVersion != *input.BaseDraftVersion || op.SourceHash == "" || len(op.SourceValue) == 0 || op.RequestHash == "" {
		t.Fatalf("incomplete prepared operation: %#v", op)
	}
	var saved orm.DocumentPublicationOperation
	if err := f.db.First(&saved, "id = ?", op.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, *op) {
		t.Fatalf("returned operation differs from durable row: %#v / %#v", op, saved)
	}
	return op
}

func confirmPublication(t *testing.T, f rollbackFixture, op *DocumentPublicationOperation) {
	t.Helper()
	if err := ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID); err != nil {
		t.Fatal(err)
	}
	if err := ConfirmDocumentPublication(t.Context(), f.db.DB, "owner", op.ID, publicationReceipt()); err != nil {
		t.Fatal(err)
	}
	requirePublicationStatus(t, f, op.ID, "provider_confirmed")
}

func requirePublicationStatus(t *testing.T, f rollbackFixture, id, status string) orm.DocumentPublicationOperation {
	t.Helper()
	var op orm.DocumentPublicationOperation
	if err := f.db.First(&op, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if op.Status != status {
		t.Fatalf("operation status=%q want %q", op.Status, status)
	}
	return op
}

func requirePublicationError(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || err.Error() != code {
		t.Fatalf("error=%v want %s", err, code)
	}
}

// Capture all local artifact state, including lineage, outputs, route decisions,
// humans and durable events. Publication rows are asserted separately because
// local failure is allowed to update the operation's diagnostic state.
func publicationArtifacts(t *testing.T, f rollbackFixture) string {
	t.Helper()
	var rows []json.RawMessage
	for _, model := range []any{&[]orm.WorkflowSession{}, &[]orm.WorkflowSlotRevision{}, &[]orm.WorkflowHumanArtifact{}, &[]orm.WorkflowSessionStep{}, &[]orm.WorkflowRouteDecision{}, &[]orm.WorkflowEvent{}} {
		if err := f.db.Order("id").Find(model).Error; err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(model)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, raw)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func publicationRows(t *testing.T, f rollbackFixture) string {
	t.Helper()
	var ops []orm.DocumentPublicationOperation
	var bindings []orm.DocumentPublicationBinding
	if err := f.db.Order("id").Find(&ops).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Order("id").Find(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal([]any{ops, bindings})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func requirePublicationUnchanged(t *testing.T, f rollbackFixture, artifacts, rows string) {
	t.Helper()
	if got := publicationArtifacts(t, f); got != artifacts {
		t.Fatal("rejected operation changed artifact state")
	}
	if got := publicationRows(t, f); got != rows {
		t.Fatal("rejected operation changed publication state")
	}
}

func TestDocumentPublicationFixtureControl(t *testing.T) {
	f, _ := publicationFixture(t, true)
	before := publicationArtifacts(t, f)
	failArtifactEventCreates(t, f.db)
	revision, err := RollbackSlotRevision(t.Context(), f.db.DB, "rollback-session", "document-slot", nil, 1, "rollback")
	if revision != nil || !errors.Is(err, errForcedArtifactEvent) {
		t.Fatalf("event failure control: %v %v", revision, err)
	}
	if publicationArtifacts(t, f) != before {
		t.Fatal("snapshot or event failure control did not preserve full graph")
	}
}

func TestDocumentPublicationPrepareGuards(t *testing.T) {
	for _, name := range []string{"wrong owner", "blank owner", "unknown owner", "dismissed session", "stale", "unselected", "revision", "draft", "missing draft", "live"} {
		t.Run(name, func(t *testing.T) {
			f, in := publicationFixture(t, false)
			switch name {
			case "wrong owner":
				in.OwnerUserID = "other"
			case "blank owner":
				in.OwnerUserID = " "
			case "unknown owner":
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowSession{}).Where("id = ?", in.SessionID).Update("create_user_id", ""))
			case "dismissed session":
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowSession{}).Where("id = ?", in.SessionID).Update("dismissed", true))
			case "stale":
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", f.currentRevisionID).Update("validity", "stale"))
			case "unselected":
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", f.currentRevisionID).Update("selected", false))
			case "revision":
				in.BaseRevision = 2
			case "draft":
				v := int64(2)
				in.BaseDraftVersion = &v
			case "missing draft":
				in.BaseDraftVersion = nil
			case "live":
				mustPublicationUpdate(t, f.db.Create(&orm.WorkflowSessionStep{ID: "publication-live", SessionID: in.SessionID, StepID: "consumer", Attempt: 1, Status: StepStatusRunning, Validity: "effective"}))
				mustPublicationUpdate(t, f.db.Create(&orm.WorkflowAttemptInputBinding{ID: "publication-live-binding", SessionID: in.SessionID, AttemptID: "publication-live", MaterialID: in.SlotID, MaterialRevisionID: f.currentRevisionID, SourceType: "artifact"}))
			}
			before, rows := publicationArtifacts(t, f), publicationRows(t, f)
			op, err := PrepareDocumentPublication(t.Context(), f.db.DB, in)
			if err == nil || op != nil || errors.Is(err, errDocumentPublicationNotImplemented) {
				t.Fatalf("expected actual guard rejection, op=%#v err=%v", op, err)
			}
			requirePublicationUnchanged(t, f, before, rows)
		})
	}
}

func mustPublicationUpdate(t *testing.T, result *gorm.DB) {
	t.Helper()
	if result.Error != nil {
		t.Fatal(result.Error)
	}
}

func TestDocumentPublicationIdempotencyAndOccupiedItem(t *testing.T) {
	f, in := publicationFixture(t, false)
	before := publicationArtifacts(t, f)
	op := preparePublication(t, f, in)
	rows := publicationRows(t, f)
	replay, err := PrepareDocumentPublication(t.Context(), f.db.DB, in)
	if err != nil || replay == nil || !reflect.DeepEqual(*replay, *op) {
		t.Fatalf("idempotent prepare: %#v %v", replay, err)
	}
	requirePublicationUnchanged(t, f, before, rows)
	for _, name := range []string{"title", "provider", "parent", "baseline", "key"} {
		changed := in
		switch name {
		case "title":
			changed.Title = "Different"
		case "provider":
			changed.Provider = "notion"
		case "parent":
			changed.ParentURI = "fixture://other"
		case "baseline":
			changed.BaseRevision = 1
		case "key":
			changed.IdempotencyKey = "request-b"
		}
		result, err := PrepareDocumentPublication(t.Context(), f.db.DB, changed)
		code := "PUBLICATION_IDEMPOTENCY_CONFLICT"
		if name == "key" {
			code = "PUBLICATION_IN_PROGRESS"
		}
		if result != nil {
			t.Fatalf("%s returned operation %#v", name, result)
		}
		requirePublicationError(t, err, code)
		requirePublicationUnchanged(t, f, before, rows)
	}
}

func TestDocumentPublicationCancelAndFailBeforeWrite(t *testing.T) {
	for _, cancel := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancel", false: "failure"}[cancel], func(t *testing.T) {
			f, in := publicationFixture(t, false)
			op := preparePublication(t, f, in)
			if cancel {
				mustPublicationErrorNil(t, CancelDocumentPublication(t.Context(), f.db.DB, "owner", op.ID))
			} else {
				mustPublicationErrorNil(t, FailDocumentPublicationBeforeWrite(t.Context(), f.db.DB, "owner", op.ID))
			}
			status := "failed_no_write"
			if cancel {
				status = "canceled"
			}
			requirePublicationStatus(t, f, op.ID, status)
			requirePublicationError(t, ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID), "PUBLICATION_STATE_CONFLICT")
			replay, err := PrepareDocumentPublication(t.Context(), f.db.DB, in)
			if err != nil || replay == nil || replay.ID != op.ID || replay.Status != status {
				t.Fatalf("old key restarted: %#v %v", replay, err)
			}
			in.IdempotencyKey = "new-request"
			next := preparePublication(t, f, in)
			if next.ID == op.ID {
				t.Fatal("new request reused old operation")
			}
			rows := publicationRows(t, f)
			_ = CancelDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
			if publicationRows(t, f) != rows {
				t.Fatal("old cancellation altered new reservation")
			}
		})
	}
}
func mustPublicationErrorNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestDocumentPublicationClaimRechecksBaseline(t *testing.T) {
	for _, name := range []string{"draft", "content without version", "revision", "owner", "live", "stale", "unselected"} {
		t.Run(name, func(t *testing.T) {
			f, in := publicationFixture(t, false)
			op := preparePublication(t, f, in)
			switch name {
			case "stale":
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", f.currentRevisionID).Update("validity", "stale"))
			case "unselected":
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", f.currentRevisionID).Update("selected", false))
			case "draft":
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Update("draft_version", 2))
			case "content without version":
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Update("value", json.RawMessage(`{"schema":"text/markdown","data":"new draft"}`)))
			case "revision":
				_, err := RollbackSlotRevision(t.Context(), f.db.DB, in.SessionID, in.SlotID, nil, 1, "rollback")
				mustPublicationErrorNil(t, err)
			case "owner":
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowSession{}).Where("id = ?", in.SessionID).Update("create_user_id", "other"))
			case "live":
				mustPublicationUpdate(t, f.db.Create(&orm.WorkflowSessionStep{ID: "live", SessionID: in.SessionID, StepID: "consumer", Attempt: 1, Status: StepStatusRunning, Validity: "effective"}))
				mustPublicationUpdate(t, f.db.Create(&orm.WorkflowAttemptInputBinding{ID: "binding", SessionID: in.SessionID, AttemptID: "live", MaterialID: in.SlotID, MaterialRevisionID: f.currentRevisionID, SourceType: "artifact"}))
			}
			artifacts := publicationArtifacts(t, f)
			if err := ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID); err == nil {
				t.Fatal("claimed after baseline/owner/live changed")
			}
			if publicationArtifacts(t, f) != artifacts {
				t.Fatal("failed claim changed artifacts")
			}
			var persisted orm.DocumentPublicationOperation
			mustPublicationUpdate(t, f.db.First(&persisted, "id = ?", op.ID))
			if persisted.Status == "write_started" {
				t.Fatal("failed claim acquired write permission")
			}
		})
	}
}

func TestDocumentPublicationUnknownNeverReclaimsWrite(t *testing.T) {
	f, in := publicationFixture(t, false)
	op := preparePublication(t, f, in)
	mustPublicationErrorNil(t, ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID))
	mustPublicationErrorNil(t, MarkDocumentPublicationUnknown(t.Context(), f.db.DB, "owner", op.ID))
	requirePublicationStatus(t, f, op.ID, "outcome_unknown")
	for _, action := range []func() error{
		func() error { return ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID) },
		func() error { return CancelDocumentPublication(t.Context(), f.db.DB, "owner", op.ID) },
		func() error { return FailDocumentPublicationBeforeWrite(t.Context(), f.db.DB, "owner", op.ID) },
	} {
		rows := publicationRows(t, f)
		requirePublicationError(t, action(), "PUBLICATION_STATE_CONFLICT")
		if publicationRows(t, f) != rows {
			t.Fatal("unknown operation changed")
		}
	}
	replay, err := PrepareDocumentPublication(t.Context(), f.db.DB, in)
	if err != nil || replay == nil || replay.Status != "outcome_unknown" {
		t.Fatalf("unknown replay %#v %v", replay, err)
	}
	in.IdempotencyKey = "another"
	_, err = PrepareDocumentPublication(t.Context(), f.db.DB, in)
	requirePublicationError(t, err, "PUBLICATION_IN_PROGRESS")
	mustPublicationErrorNil(t, ConfirmDocumentPublication(t.Context(), f.db.DB, "owner", op.ID, publicationReceipt()))
	saved := requirePublicationStatus(t, f, op.ID, "provider_confirmed")
	if len(saved.ReceiptJSON) == 0 {
		t.Fatal("late receipt not persisted")
	}
	requirePublicationError(t, ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID), "PUBLICATION_STATE_CONFLICT")
}

func TestDocumentPublicationFinalizeAtomicityAndRetry(t *testing.T) {
	f, in := publicationFixture(t, true)
	op := preparePublication(t, f, in)
	confirmPublication(t, f, op)
	receipt := requirePublicationStatus(t, f, op.ID, "provider_confirmed").ReceiptJSON
	before := publicationArtifacts(t, f)
	failArtifactEventCreates(t, f.db)
	result, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
	if result != nil || !errors.Is(err, errForcedArtifactEvent) {
		t.Fatalf("event failure: %#v %v", result, err)
	}
	if publicationArtifacts(t, f) != before {
		t.Fatal("event failure left partial artifact/graph/state changes")
	}
	var failed orm.DocumentPublicationOperation
	mustPublicationUpdate(t, f.db.First(&failed, "id = ?", op.ID))
	if !reflect.DeepEqual(receipt, failed.ReceiptJSON) || failed.ResultRevisionID != "" || failed.Status == "succeeded" {
		t.Fatalf("lost receipt or reported success: %#v", failed)
	}
	var binding orm.DocumentPublicationBinding
	mustPublicationUpdate(t, f.db.Where("session_id = ? AND slot_id = ? AND item_index = ?", in.SessionID, in.SlotID, -1).First(&binding))
	if binding.PendingOperationID != op.ID || len(binding.TargetDocument) > 0 || binding.ResultRevisionID != "" {
		t.Fatalf("partial binding: %#v", binding)
	}
	mustPublicationErrorNil(t, f.db.Callback().Create().Remove("test:fail_artifact_event"))
	revision, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
	mustPublicationErrorNil(t, err)
	requirePublicationFinalized(t, f, in, op.ID, revision)
	artifacts, rows := publicationArtifacts(t, f), publicationRows(t, f)
	again, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
	if err != nil || again == nil || again.ID != revision.ID {
		t.Fatalf("finalize replay %#v %v", again, err)
	}
	requirePublicationUnchanged(t, f, artifacts, rows)
}

func requirePublicationFinalized(t *testing.T, f rollbackFixture, in DocumentPublicationInput, id string, rev *orm.WorkflowSlotRevision) {
	t.Helper()
	if rev == nil || rev.Revision != 4 || !rev.Selected || rev.Validity != "effective" || rev.ChangeSource != "provider_sync" || rev.HumanArtifactID == nil {
		t.Fatalf("invalid saved revision %#v", rev)
	}
	var human orm.WorkflowHumanArtifact
	mustPublicationUpdate(t, f.db.First(&human, "id = ?", *rev.HumanArtifactID))
	if human.DraftVersion != 1 || human.ContentType != "json" {
		t.Fatalf("wrong published human: %#v", human)
	}
	requirePublicationJSON(t, human.Value, publicationReceipt().Value)
	op := requirePublicationStatus(t, f, id, "succeeded")
	if op.ResultRevisionID != rev.ID || len(op.ReceiptJSON) == 0 {
		t.Fatalf("wrong success receipt %#v", op)
	}
	var b orm.DocumentPublicationBinding
	mustPublicationUpdate(t, f.db.Where("session_id = ? AND slot_id = ? AND item_index = ?", in.SessionID, in.SlotID, -1).First(&b))
	if b.PendingOperationID != "" || b.ResultRevisionID != rev.ID || b.SourceRevisionID != f.currentRevisionID || b.Provider != in.Provider || !reflect.DeepEqual(b.TargetDocument, publicationReceipt().TargetDocument) || !reflect.DeepEqual(b.RemoteValue, publicationReceipt().Value) {
		t.Fatalf("wrong binding %#v", b)
	}
	for _, id := range []string{f.consumerAttemptID, "rollback-downstream", "rollback-route-attempt"} {
		requireAttemptValidity(t, artifactDependencyFixture{db: f.db}, id, "stale")
	}
	for _, id := range []string{f.consumerOutputID, "rollback-downstream-output", "rollback-route-output"} {
		requireRevisionState(t, artifactDependencyFixture{db: f.db}, id, "stale", false)
	}
	requireDecisionValidity(t, artifactDependencyFixture{db: f.db}, "rollback-witness-decision", "stale")
	requireRevisionState(t, artifactDependencyFixture{db: f.db}, f.currentRevisionID, "effective", false)
	events, payloads := loadArtifactUpsertEvents(t, f.db, in.SessionID)
	if len(events) != 1 {
		t.Fatalf("events=%#v", events)
	}
	if events[0].EntityID != rev.ID || events[0].StateVersion != 1 || events[0].OwnerUserID != "owner" || payloads[0].ArtifactID != rev.ID || payloads[0].ChangeSource != "provider_sync" || payloads[0].StateVersion != 1 || payloads[0].Revision != 4 || payloads[0].DraftVersion != 1 || payloads[0].SlotID != in.SlotID || payloads[0].ListIndex != nil {
		t.Fatalf("wrong event %#v %#v", events[0], payloads[0])
	}
	if loadArtifactEventSession(t, f.db, in.SessionID).StateVersion != 1 {
		t.Fatal("wrong session state")
	}
}

func TestDocumentPublicationOperationOwner(t *testing.T) {
	for _, action := range []string{"get", "claim", "cancel", "fail", "unknown", "confirm", "finalize"} {
		for _, owner := range []string{"other", "", " "} {
			t.Run(action+"/"+owner, func(t *testing.T) {
				f, in := publicationFixture(t, false)
				op := preparePublication(t, f, in)
				if action == "unknown" || action == "confirm" {
					mustPublicationErrorNil(t, ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID))
				}
				if action == "finalize" {
					confirmPublication(t, f, op)
				}
				artifacts, rows := publicationArtifacts(t, f), publicationRows(t, f)
				var err error
				switch action {
				case "get":
					var value *DocumentPublicationOperation
					value, err = GetDocumentPublication(t.Context(), f.db.DB, owner, op.ID)
					if value != nil {
						t.Fatal("foreign query returned contents")
					}
				case "claim":
					err = ClaimDocumentPublicationWrite(t.Context(), f.db.DB, owner, op.ID)
				case "cancel":
					err = CancelDocumentPublication(t.Context(), f.db.DB, owner, op.ID)
				case "fail":
					err = FailDocumentPublicationBeforeWrite(t.Context(), f.db.DB, owner, op.ID)
				case "unknown":
					err = MarkDocumentPublicationUnknown(t.Context(), f.db.DB, owner, op.ID)
				case "confirm":
					err = ConfirmDocumentPublication(t.Context(), f.db.DB, owner, op.ID, publicationReceipt())
				case "finalize":
					var value *orm.WorkflowSlotRevision
					value, err = FinalizeDocumentPublication(t.Context(), f.db.DB, owner, op.ID)
					if value != nil {
						t.Fatal("foreign finalize returned result")
					}
				}
				if err == nil {
					t.Fatal("foreign owner was accepted in otherwise valid phase")
				}
				requirePublicationUnchanged(t, f, artifacts, rows)
			})
		}
	}
	t.Run("transferred session", func(t *testing.T) {
		f, in := publicationFixture(t, false)
		op := preparePublication(t, f, in)
		confirmPublication(t, f, op)
		mustPublicationUpdate(t, f.db.Model(&orm.WorkflowSession{}).Where("id = ?", in.SessionID).Update("create_user_id", "other"))
		artifacts, rows := publicationArtifacts(t, f), publicationRows(t, f)
		if _, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID); err == nil {
			t.Fatal("former owner finalized")
		}
		if value, err := GetDocumentPublication(t.Context(), f.db.DB, "owner", op.ID); err == nil || value != nil {
			t.Fatal("former owner read transferred resource")
		}
		if _, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "other", op.ID); err == nil {
			t.Fatal("new owner inherited old operation")
		}
		requirePublicationUnchanged(t, f, artifacts, rows)
	})
}

func TestDocumentPublicationConcurrentClaimAndCancel(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "two claims", true: "claim versus cancel"}[cancel], func(t *testing.T) {
			f, in := publicationFixture(t, false)
			op := preparePublication(t, f, in)
			start := make(chan struct{})
			results := make(chan error, 2)
			var wg sync.WaitGroup
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					if cancel && i == 1 {
						results <- CancelDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
					} else {
						results <- ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID)
					}
				}(i)
			}
			close(start)
			wg.Wait()
			close(results)
			wins := 0
			for err := range results {
				if err == nil {
					wins++
				} else {
					requirePublicationError(t, err, "PUBLICATION_STATE_CONFLICT")
				}
			}
			if wins != 1 {
				t.Fatalf("winners=%d want 1", wins)
			}
			var saved orm.DocumentPublicationOperation
			mustPublicationUpdate(t, f.db.First(&saved, "id = ?", op.ID))
			if saved.Status != "write_started" && (!cancel || saved.Status != "canceled") {
				t.Fatalf("invalid terminal race state %#v", saved)
			}
		})
	}
}

func TestDocumentPublicationConcurrentPrepare(t *testing.T) {
	for _, sameKey := range []bool{true, false} {
		t.Run(map[bool]string{true: "same key", false: "different keys"}[sameKey], func(t *testing.T) {
			f, in := publicationFixture(t, false)
			type result struct {
				op  *DocumentPublicationOperation
				err error
			}
			start := make(chan struct{})
			results := make(chan result, 2)
			for i := 0; i < 2; i++ {
				go func(i int) {
					input := in
					if !sameKey && i == 1 {
						input.IdempotencyKey = "second"
					}
					<-start
					op, err := PrepareDocumentPublication(t.Context(), f.db.DB, input)
					results <- result{op, err}
				}(i)
			}
			close(start)
			a, b := <-results, <-results
			if sameKey {
				if a.err != nil || b.err != nil || a.op == nil || b.op == nil || a.op.ID != b.op.ID {
					t.Fatalf("same key race %#v %#v", a, b)
				}
			} else {
				wins := 0
				for _, r := range []result{a, b} {
					if r.err == nil && r.op != nil {
						wins++
					} else {
						requirePublicationError(t, r.err, "PUBLICATION_IN_PROGRESS")
					}
				}
				if wins != 1 {
					t.Fatalf("different key winners=%d", wins)
				}
			}
			var ops int64
			mustPublicationUpdate(t, f.db.Model(&orm.DocumentPublicationOperation{}).Count(&ops))
			var bindings int64
			mustPublicationUpdate(t, f.db.Model(&orm.DocumentPublicationBinding{}).Count(&bindings))
			if ops != 1 || bindings != 1 {
				t.Fatalf("duplicate rows: operations=%d bindings=%d", ops, bindings)
			}
		})
	}
}

func TestDocumentPublicationFinalizeConflictPreservesReceipt(t *testing.T) {
	for _, name := range []string{"draft", "content without version", "revision", "live"} {
		t.Run(name, func(t *testing.T) {
			f, in := publicationFixture(t, false)
			op := preparePublication(t, f, in)
			confirmPublication(t, f, op)
			receipt := requirePublicationStatus(t, f, op.ID, "provider_confirmed").ReceiptJSON
			switch name {
			case "draft":
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Updates(map[string]any{"draft_version": 2, "value": json.RawMessage(`{"schema":"text/markdown","data":"new draft"}`)}))
			case "content without version":
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Update("value", json.RawMessage(`{"schema":"text/markdown","data":"new draft"}`)))
			case "revision":
				_, err := RollbackSlotRevision(t.Context(), f.db.DB, in.SessionID, in.SlotID, nil, 1, "rollback")
				mustPublicationErrorNil(t, err)
			case "live":
				mustPublicationUpdate(t, f.db.Create(&orm.WorkflowSessionStep{ID: "live", SessionID: in.SessionID, StepID: "consumer", Attempt: 1, Status: StepStatusRunning, Validity: "effective"}))
				mustPublicationUpdate(t, f.db.Create(&orm.WorkflowAttemptInputBinding{ID: "binding", SessionID: in.SessionID, AttemptID: "live", MaterialID: in.SlotID, MaterialRevisionID: f.currentRevisionID, SourceType: "artifact"}))
			}
			before := publicationArtifacts(t, f)
			for i := 0; i < 2; i++ {
				rev, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
				if rev != nil || err == nil {
					t.Fatalf("finalize overwrote conflict: %#v %v", rev, err)
				}
				if publicationArtifacts(t, f) != before {
					t.Fatal("local retry changed newer draft/live state")
				}
			}
			var saved orm.DocumentPublicationOperation
			mustPublicationUpdate(t, f.db.First(&saved, "id = ?", op.ID))
			if !reflect.DeepEqual(receipt, saved.ReceiptJSON) || saved.Status == "succeeded" || saved.ResultRevisionID != "" {
				t.Fatalf("receipt lost or falsely finalized: %#v", saved)
			}
			requirePublicationError(t, ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID), "PUBLICATION_STATE_CONFLICT")
		})
	}
}

func TestDocumentPublicationFinalizeConcurrent(t *testing.T) {
	f, in := publicationFixture(t, true)
	op := preparePublication(t, f, in)
	confirmPublication(t, f, op)
	type result struct {
		rev *orm.WorkflowSlotRevision
		err error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			rev, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
			results <- result{rev, err}
		}()
	}
	close(start)
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || a.rev == nil || b.rev == nil || a.rev.ID != b.rev.ID {
		t.Fatalf("finalize race %#v %#v", a, b)
	}
	requirePublicationFinalized(t, f, in, op.ID, a.rev)
	var revisions, humans int64
	mustPublicationUpdate(t, f.db.Model(&orm.WorkflowSlotRevision{}).Where("slot_id = ?", in.SlotID).Count(&revisions))
	mustPublicationUpdate(t, f.db.Model(&orm.WorkflowHumanArtifact{}).Count(&humans))
	if revisions != 3 || humans != 3 {
		t.Fatalf("duplicate local records: revisions=%d humans=%d", revisions, humans)
	}
}

func TestDocumentPublicationExactListItemAndHistoricalRevision(t *testing.T) {
	f, in := publicationFixture(t, false)
	index := 2
	in.ListIndex = &index
	mustPublicationUpdate(t, f.db.Model(&orm.WorkflowSlotRevision{}).Where("slot_id = ?", in.SlotID).Update("list_index", index))
	other := orm.WorkflowSlotRevision{ID: "other-item", SessionID: in.SessionID, SlotID: in.SlotID, Slot: "document-key", ListIndex: new(int), Revision: 1, Selected: true, Validity: "effective", HumanArtifactID: ptrPublicationString("other-human")}
	mustPublicationUpdate(t, f.db.Create(&orm.WorkflowHumanArtifact{ID: "other-human", SessionID: in.SessionID, Slot: "document-key", ContentType: "json", Value: json.RawMessage(`{"schema":"text/markdown","data":"other"}`), DraftVersion: 1}))
	mustPublicationUpdate(t, f.db.Create(&other))
	// A newer but unselected checkpoint establishes MAX(revision), independently
	// of the selected baseline. Publishing after rollback must append revision 10.
	history := orm.WorkflowSlotRevision{ID: "historical-nine", SessionID: in.SessionID, SlotID: in.SlotID, Slot: "document-key", ListIndex: &index, Revision: 9, Validity: "effective", Selected: false}
	mustPublicationUpdate(t, f.db.Create(&history))
	mustPublicationUpdate(t, f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", history.ID).Update("selected", false))
	op := preparePublication(t, f, in)
	inOther := in
	inOther.ListIndex = other.ListIndex
	inOther.BaseRevision = 1
	inOther.IdempotencyKey = "other-item-request"
	otherOp, err := PrepareDocumentPublication(t.Context(), f.db.DB, inOther)
	if err != nil || otherOp == nil || otherOp.ID == op.ID {
		t.Fatalf("independent item blocked: %#v %v", otherOp, err)
	}
	otherRows := publicationRowsForItem(t, f, 0)
	mustPublicationUpdate(t, f.db.Create(&orm.WorkflowSlotOrder{SessionID: in.SessionID, SlotID: in.SlotID, OrderList: json.RawMessage(`[0,2]`), OrderVersion: 0}))
	mustPublicationErrorNil(t, ReorderSlot(t.Context(), f.db.DB, in.SessionID, in.SlotID, []int{2, 0}, 0))
	confirmPublication(t, f, op)
	rev, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
	mustPublicationErrorNil(t, err)
	if rev == nil || rev.Revision != 10 || rev.ListIndex == nil || *rev.ListIndex != 2 {
		t.Fatalf("wrong item/revision %#v", rev)
	}
	var actualOther orm.WorkflowSlotRevision
	mustPublicationUpdate(t, f.db.First(&actualOther, "id = ?", other.ID))
	// CreatedAt is assigned by GORM; compare the database baseline, not the input.
	if actualOther.ID != other.ID || !actualOther.Selected || actualOther.Validity != "effective" || actualOther.Revision != 1 || actualOther.HumanArtifactID == nil || *actualOther.HumanArtifactID != "other-human" {
		t.Fatalf("neighbor altered: %#v", actualOther)
	}
	if publicationRowsForItem(t, f, 0) != otherRows {
		t.Fatal("neighbor operation/binding altered")
	}
	events, payload := loadArtifactUpsertEvents(t, f.db, in.SessionID)
	if len(events) != 1 || events[0].EntityID != rev.ID || payload[0].ListIndex == nil || *payload[0].ListIndex != 2 || payload[0].Revision != 10 {
		t.Fatalf("wrong item event %#v %#v", events, payload)
	}
	var binding orm.DocumentPublicationBinding
	mustPublicationUpdate(t, f.db.Where("session_id = ? AND slot_id = ? AND item_index = ?", in.SessionID, in.SlotID, 2).First(&binding))
	if binding.ResultRevisionID != rev.ID || binding.PendingOperationID != "" {
		t.Fatalf("wrong item binding %#v", binding)
	}
	// Historical selection must not discard the already published target.
	bindingBefore := binding
	_, err = RollbackSlotRevision(t.Context(), f.db.DB, in.SessionID, in.SlotID, &index, 3, "rollback")
	mustPublicationErrorNil(t, err)
	mustPublicationUpdate(t, f.db.First(&binding, "id = ?", binding.ID))
	if !reflect.DeepEqual(bindingBefore, binding) {
		t.Fatal("rollback discarded target")
	}
	in.IdempotencyKey = "publish-after-rollback"
	result, err := PrepareDocumentPublication(t.Context(), f.db.DB, in)
	if result != nil {
		t.Fatal("bound historical item accepted as first publication")
	}
	requirePublicationError(t, err, "PUBLICATION_ALREADY_BOUND")
}
func ptrPublicationString(s string) *string { return &s }
func publicationRowsForItem(t *testing.T, f rollbackFixture, index int) string {
	t.Helper()
	var ops []orm.DocumentPublicationOperation
	var bindings []orm.DocumentPublicationBinding
	mustPublicationUpdate(t, f.db.Where("item_index = ?", index).Order("id").Find(&ops))
	mustPublicationUpdate(t, f.db.Where("item_index = ?", index).Order("id").Find(&bindings))
	raw, err := json.Marshal([]any{ops, bindings})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestDocumentPublicationRejectsReceiptBeforeWrite(t *testing.T) {
	f, in := publicationFixture(t, false)
	op := preparePublication(t, f, in)
	artifacts, rows := publicationArtifacts(t, f), publicationRows(t, f)
	requirePublicationError(t, ConfirmDocumentPublication(t.Context(), f.db.DB, "owner", op.ID, publicationReceipt()), "PUBLICATION_STATE_CONFLICT")
	rev, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
	if rev != nil {
		t.Fatal("finalized without provider confirmation")
	}
	requirePublicationError(t, err, "PUBLICATION_STATE_CONFLICT")
	requirePublicationUnchanged(t, f, artifacts, rows)
	confirmPublication(t, f, op)
	rows = publicationRows(t, f)
	changed := publicationReceipt()
	changed.TargetDocument = json.RawMessage(`{"adapter":"obsidian","document_id":"different"}`)
	requirePublicationError(t, ConfirmDocumentPublication(t.Context(), f.db.DB, "owner", op.ID, changed), "PUBLICATION_RECEIPT_CONFLICT")
	if publicationRows(t, f) != rows {
		t.Fatal("confirmed receipt overwritten")
	}
}

func TestDocumentPublicationUniqueConstraintControl(t *testing.T) {
	f, _ := publicationFixture(t, false)
	op := orm.DocumentPublicationOperation{ID: "op-a", OwnerUserID: "owner", IdempotencyKey: "key", SessionID: "rollback-session", SlotID: "document-slot", ItemIndex: -1, Status: "preparing", SourceRevisionID: f.currentRevisionID}
	mustPublicationUpdate(t, f.db.Create(&op))
	duplicate := op
	duplicate.ID = "op-b"
	if err := f.db.Create(&duplicate).Error; err == nil {
		t.Fatal("owner/key uniqueness missing")
	}
	duplicate.OwnerUserID = "other"
	mustPublicationUpdate(t, f.db.Create(&duplicate))
	binding := orm.DocumentPublicationBinding{ID: "binding-a", SessionID: op.SessionID, SlotID: op.SlotID, ItemIndex: -1, OwnerUserID: "owner", PendingOperationID: op.ID}
	mustPublicationUpdate(t, f.db.Create(&binding))
	binding.ID = "binding-b"
	if err := f.db.Create(&binding).Error; err == nil {
		t.Fatal("item uniqueness missing")
	}
	binding.ItemIndex = 0
	mustPublicationUpdate(t, f.db.Create(&binding))
}

func TestDocumentPublicationPrepareRollsBackReservationFailure(t *testing.T) {
	f, in := publicationFixture(t, false)
	forced := errors.New("fixture reservation insert failure")
	mustPublicationErrorNil(t, f.db.Callback().Create().Before("gorm:create").Register("test:publication-binding-fail", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "document_publication_bindings" {
			tx.AddError(forced)
		}
	}))
	before, rows := publicationArtifacts(t, f), publicationRows(t, f)
	op, err := PrepareDocumentPublication(t.Context(), f.db.DB, in)
	if op != nil || !errors.Is(err, forced) {
		t.Fatalf("reservation failure: %#v %v", op, err)
	}
	requirePublicationUnchanged(t, f, before, rows)
}

func TestDocumentPublicationPrepareWaitsForPostgreSQLSessionLock(t *testing.T) {
	f, in := publicationFixture(t, false)
	if f.db.Dialector.Name() != "postgres" {
		t.Skip("requires PostgreSQL Session row lock")
	}
	tx := f.db.Begin()
	mustPublicationErrorNil(t, tx.Error)
	defer tx.Rollback()
	_, err := artifactgraph.LockSession(tx, in.SessionID)
	mustPublicationErrorNil(t, err)
	done := make(chan error, 1)
	go func() { _, err := PrepareDocumentPublication(t.Context(), f.db.DB, in); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("prepare escaped Session lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	mustPublicationErrorNil(t, tx.Commit().Error)
	select {
	case err := <-done:
		mustPublicationErrorNil(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("prepare did not complete after Session unlock")
	}
}

func TestDocumentPublicationConfirmedQueryAndReplay(t *testing.T) {
	f, in := publicationFixture(t, false)
	op := preparePublication(t, f, in)
	confirmPublication(t, f, op)
	durable := requirePublicationStatus(t, f, op.ID, "provider_confirmed")
	rows := publicationRows(t, f)
	read, err := GetDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
	if err != nil || read == nil || !reflect.DeepEqual(*read, durable) {
		t.Fatalf("durable query %#v %v", read, err)
	}
	replay, err := PrepareDocumentPublication(t.Context(), f.db.DB, in)
	if err != nil || replay == nil || !reflect.DeepEqual(*replay, durable) {
		t.Fatalf("confirmed replay %#v %v", replay, err)
	}
	mustPublicationErrorNil(t, ConfirmDocumentPublication(t.Context(), f.db.DB, "owner", op.ID, publicationReceipt()))
	if publicationRows(t, f) != rows {
		t.Fatal("confirmation replay changed durable receipt")
	}
}

func TestDocumentPublicationFinalizeRollsBackLateWrites(t *testing.T) {
	for _, table := range []string{"document_publication_bindings", "document_publication_operations"} {
		t.Run(table, func(t *testing.T) {
			f, in := publicationFixture(t, true)
			op := preparePublication(t, f, in)
			confirmPublication(t, f, op)
			before := publicationArtifacts(t, f)
			saved := requirePublicationStatus(t, f, op.ID, "provider_confirmed")
			var bindingBefore orm.DocumentPublicationBinding
			mustPublicationUpdate(t, f.db.Where("pending_operation_id = ?", op.ID).First(&bindingBefore))
			forced := failPublicationLateWrites(t, f, table)
			revision, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
			if revision != nil || !errors.Is(err, forced) {
				t.Fatalf("late persistence fault not reached: %#v %v", revision, err)
			}
			if publicationArtifacts(t, f) != before {
				t.Fatal("late failure left committed human/revision/graph/event/state")
			}
			var binding orm.DocumentPublicationBinding
			mustPublicationUpdate(t, f.db.First(&binding, "id = ?", bindingBefore.ID))
			if !reflect.DeepEqual(binding, bindingBefore) {
				t.Fatal("late failure changed original reservation/binding")
			}
			var operation orm.DocumentPublicationOperation
			mustPublicationUpdate(t, f.db.First(&operation, "id = ?", op.ID))
			if !reflect.DeepEqual(operation.ReceiptJSON, saved.ReceiptJSON) || operation.ResultRevisionID != "" || operation.Status == "succeeded" {
				t.Fatalf("late failure lost confirmation: %#v", operation)
			}
			mustPublicationErrorNil(t, f.db.Callback().Update().Remove("test:publication-late-write"))
			mustPublicationErrorNil(t, f.db.Callback().Create().Remove("test:publication-late-write"))
			revision, err = FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
			mustPublicationErrorNil(t, err)
			requirePublicationFinalized(t, f, in, op.ID, revision)
			artifacts, rows := publicationArtifacts(t, f), publicationRows(t, f)
			again, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
			if err != nil || again == nil || again.ID != revision.ID {
				t.Fatalf("retry duplicated result: %#v %v", again, err)
			}
			requirePublicationUnchanged(t, f, artifacts, rows)
		})
	}
}

func TestDocumentPublicationRechecksReservation(t *testing.T) {
	for _, stage := range []string{"claim", "confirm"} {
		for _, change := range []string{"missing", "other operation", "other owner"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				f, in := publicationFixture(t, false)
				op := preparePublication(t, f, in)
				if stage == "confirm" {
					mustPublicationErrorNil(t, ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID))
				}
				query := f.db.Model(&orm.DocumentPublicationBinding{}).Where("session_id = ? AND slot_id = ? AND item_index = ?", in.SessionID, in.SlotID, -1)
				switch change {
				case "missing":
					mustPublicationUpdate(t, query.Delete(&orm.DocumentPublicationBinding{}))
				case "other operation":
					mustPublicationUpdate(t, query.Update("pending_operation_id", "different-operation"))
				case "other owner":
					mustPublicationUpdate(t, query.Update("owner_user_id", "other"))
				}
				artifacts, rows := publicationArtifacts(t, f), publicationRows(t, f)
				var err error
				if stage == "claim" {
					err = ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID)
				} else {
					err = ConfirmDocumentPublication(t.Context(), f.db.DB, "owner", op.ID, publicationReceipt())
				}
				if err == nil {
					t.Fatal("accepted missing or foreign reservation")
				}
				requirePublicationUnchanged(t, f, artifacts, rows)
			})
		}
	}
}

func TestDocumentPublicationClaimAndFinalizeRecheckUnderPostgreSQLLock(t *testing.T) {
	for _, stage := range []string{"claim", "finalize"} {
		t.Run(stage, func(t *testing.T) {
			f, in := publicationFixture(t, false)
			if f.db.Dialector.Name() != "postgres" {
				t.Skip("requires PostgreSQL Session row lock")
			}
			op := preparePublication(t, f, in)
			if stage == "finalize" {
				confirmPublication(t, f, op)
			}
			tx := f.db.Begin()
			mustPublicationErrorNil(t, tx.Error)
			defer tx.Rollback()
			_, err := artifactgraph.LockSession(tx, in.SessionID)
			mustPublicationErrorNil(t, err)
			done := make(chan error, 1)
			go func() {
				if stage == "claim" {
					done <- ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID)
				} else {
					_, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
					done <- err
				}
			}()
			select {
			case err := <-done:
				t.Fatalf("%s escaped Session lock: %v", stage, err)
			case <-time.After(150 * time.Millisecond):
			}
			// Commit an edit while the publication writer must still be waiting. Merely
			// checking outside the lock would have accepted the old baseline.
			mustPublicationUpdate(t, tx.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Updates(map[string]any{"draft_version": 2, "value": json.RawMessage(`{"schema":"text/markdown","data":"concurrent editor"}`)}))
			mustPublicationErrorNil(t, tx.Commit().Error)
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("accepted baseline changed by lock holder")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("publication did not finish after unlock")
			}
			var human orm.WorkflowHumanArtifact
			mustPublicationUpdate(t, f.db.First(&human, "id = ?", "rollback-current-human"))
			if human.DraftVersion != 2 {
				t.Fatalf("editor overwritten: %#v", human)
			}
			requirePublicationJSON(t, human.Value, json.RawMessage(`{"schema":"text/markdown","data":"concurrent editor"}`))
			var current orm.WorkflowSlotRevision
			mustPublicationUpdate(t, f.db.First(&current, "id = ?", f.currentRevisionID))
			if !current.Selected {
				t.Fatal("selected revision changed")
			}
			var operation orm.DocumentPublicationOperation
			mustPublicationUpdate(t, f.db.First(&operation, "id = ?", op.ID))
			if stage == "claim" && operation.Status == "write_started" {
				t.Fatal("stale claim acquired write permission")
			}
			if stage == "finalize" && (operation.Status == "succeeded" || len(operation.ReceiptJSON) == 0 || operation.ResultRevisionID != "") {
				t.Fatal("stale finalize lost receipt or saved")
			}
			events, _ := loadArtifactUpsertEvents(t, f.db, in.SessionID)
			if len(events) != 0 || loadArtifactEventSession(t, f.db, in.SessionID).StateVersion != 0 {
				t.Fatal("failed publication advanced local state")
			}
		})
	}
}

func TestDocumentPublicationLogicalSource(t *testing.T) {
	for _, kind := range []string{"inline markdown", "file markdown", "unbound IR", "bound IR"} {
		t.Run(kind, func(t *testing.T) {
			f, in := publicationFixture(t, false)
			logical := json.RawMessage(`"# Draft"`)
			if kind == "file markdown" {
				root := t.TempDir()
				t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
				t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
				path := filepath.Join(root, "draft.md")
				mustPublicationErrorNil(t, os.WriteFile(path, []byte("# Draft"), 0600))
				raw, _ := json.Marshal(map[string]string{"path": path})
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Updates(map[string]any{"content_type": "file", "value": json.RawMessage(raw)}))
			}
			if kind == "unbound IR" || kind == "bound IR" {
				logical = json.RawMessage(`{"document_id":"fixture-ir","blocks":[]}`)
				if kind == "bound IR" {
					logical = json.RawMessage(`{"document_id":"fixture-ir","blocks":[],"provider_binding":{"provider":"obsidian","document_id":"already-remote"}}`)
				}
				raw, _ := json.Marshal(map[string]any{"schema": "lazyllm.tools.writer.data_models.writer_ir.WriterDocument", "data": logical})
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Update("value", json.RawMessage(raw)))
			}
			artifacts, rows := publicationArtifacts(t, f), publicationRows(t, f)
			if kind == "bound IR" {
				op, err := PrepareDocumentPublication(t.Context(), f.db.DB, in)
				if op != nil {
					t.Fatal("bound IR accepted as first publication")
				}
				requirePublicationError(t, err, "PUBLICATION_ALREADY_BOUND")
				requirePublicationUnchanged(t, f, artifacts, rows)
				return
			}
			op := preparePublication(t, f, in)
			requirePublicationJSON(t, op.SourceValue, logical)
			mustPublicationErrorNil(t, ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID))
			receipt := publicationReceipt()
			if kind == "unbound IR" {
				receipt.Value = json.RawMessage(`{"schema":"lazyllm.tools.writer.data_models.writer_ir.WriterDocument","data":{"document_id":"fixture-ir","blocks":[],"provider_binding":{"provider":"obsidian","document_id":"fixture-document"}}}`)
			}
			mustPublicationErrorNil(t, ConfirmDocumentPublication(t.Context(), f.db.DB, "owner", op.ID, receipt))
			revision, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
			mustPublicationErrorNil(t, err)
			if revision == nil || revision.HumanArtifactID == nil {
				t.Fatal("logical source did not finalize")
			}
			var human orm.WorkflowHumanArtifact
			mustPublicationUpdate(t, f.db.First(&human, "id = ?", *revision.HumanArtifactID))
			requirePublicationJSON(t, human.Value, receipt.Value)
		})
	}
}

func requirePublicationJSON(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	var a, b any
	if json.Unmarshal(got, &a) != nil || json.Unmarshal(want, &b) != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("JSON=%s want=%s", got, want)
	}
}

func TestDocumentPublicationFileAndTypeChanges(t *testing.T) {
	for _, stage := range []string{"claim", "finalize"} {
		for _, change := range []string{"same path new content", "same data new type"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				f, in := publicationFixture(t, false)
				root := t.TempDir()
				t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
				t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
				path := filepath.Join(root, "draft.md")
				mustPublicationErrorNil(t, os.WriteFile(path, []byte("# Draft"), 0600))
				raw, _ := json.Marshal(map[string]string{"path": path})
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Updates(map[string]any{"content_type": "file", "value": json.RawMessage(raw)}))
				op := preparePublication(t, f, in)
				requirePublicationJSON(t, op.SourceValue, json.RawMessage(`"# Draft"`))
				if stage == "finalize" {
					confirmPublication(t, f, op)
				}
				if change == "same path new content" {
					mustPublicationErrorNil(t, os.WriteFile(path, []byte("# Changed at same path"), 0600))
				} else {
					// Same locator and file bytes, but the declared logical schema changes.
					typed, _ := json.Marshal(map[string]any{"schema": "lazyllm.tools.writer.data_models.writer_ir.WriterDocument", "data": map[string]string{"path": path}})
					mustPublicationUpdate(t, f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Update("value", json.RawMessage(typed)))
				}
				before := publicationArtifacts(t, f)
				var err error
				if stage == "claim" {
					err = ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID)
				} else {
					_, err = FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
				}
				if err == nil {
					t.Fatal("source path/type accepted without re-reading logical content")
				}
				if publicationArtifacts(t, f) != before {
					t.Fatal("source conflict changed artifacts")
				}
				var saved orm.DocumentPublicationOperation
				mustPublicationUpdate(t, f.db.First(&saved, "id = ?", op.ID))
				requirePublicationJSON(t, saved.SourceValue, json.RawMessage(`"# Draft"`))
				if saved.SourceHash != op.SourceHash {
					t.Fatal("conflict rewrote frozen source hash")
				}
			})
		}
	}
}

func TestDocumentPublicationHashIsLogicalNotLocator(t *testing.T) {
	f, in := publicationFixture(t, false)
	inline := preparePublication(t, f, in)
	mustPublicationErrorNil(t, CancelDocumentPublication(t.Context(), f.db.DB, "owner", inline.ID))
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
	for _, name := range []string{"first.md", "second.md"} {
		path := filepath.Join(root, name)
		mustPublicationErrorNil(t, os.WriteFile(path, []byte("# Draft"), 0600))
		raw, _ := json.Marshal(map[string]string{"path": path})
		mustPublicationUpdate(t, f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Updates(map[string]any{"content_type": "file", "value": json.RawMessage(raw)}))
		in.IdempotencyKey = name
		file := preparePublication(t, f, in)
		requirePublicationJSON(t, file.SourceValue, inline.SourceValue)
		if file.SourceHash != inline.SourceHash {
			t.Fatal("same logical Markdown had different hash due to carrier/path")
		}
		mustPublicationErrorNil(t, CancelDocumentPublication(t.Context(), f.db.DB, "owner", file.ID))
		mustPublicationErrorNil(t, os.WriteFile(path, []byte("# Different"), 0600))
		in.IdempotencyKey = name + "-changed"
		changed := preparePublication(t, f, in)
		if changed.SourceHash == file.SourceHash {
			t.Fatal("same locator concealed changed logical content")
		}
		requirePublicationJSON(t, changed.SourceValue, json.RawMessage(`"# Different"`))
		mustPublicationErrorNil(t, CancelDocumentPublication(t.Context(), f.db.DB, "owner", changed.ID))
	}
}

func failPublicationLateWrites(t *testing.T, f rollbackFixture, table string) error {
	t.Helper()
	forced := errors.New("fixture late publication persistence failure")
	hook := func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Table != table {
			return
		}
		if table == "document_publication_operations" {
			raw, err := json.Marshal(tx.Statement.Dest)
			if err != nil {
				return
			}
			var fields map[string]any
			if json.Unmarshal(raw, &fields) != nil {
				return
			}
			if fields["status"] != "succeeded" && fields["Status"] != "succeeded" {
				return
			}
		}
		tx.AddError(forced)
	}
	mustPublicationErrorNil(t, f.db.Callback().Update().Before("gorm:update").Register("test:publication-late-write", hook))
	mustPublicationErrorNil(t, f.db.Callback().Create().Before("gorm:create").Register("test:publication-late-write", hook))
	return forced
}

func TestDocumentPublicationLateWriteFaultControl(t *testing.T) {
	for _, table := range []string{"document_publication_bindings", "document_publication_operations"} {
		t.Run(table, func(t *testing.T) {
			f, _ := publicationFixture(t, true)
			mustPublicationUpdate(t, f.db.Create(&orm.DocumentPublicationOperation{ID: "control-operation", OwnerUserID: "owner", IdempotencyKey: "control", SessionID: "rollback-session", SlotID: "document-slot", ItemIndex: -1, Status: "provider_confirmed", SourceRevisionID: f.currentRevisionID}))
			mustPublicationUpdate(t, f.db.Create(&orm.DocumentPublicationBinding{ID: "control-binding", OwnerUserID: "owner", SessionID: "rollback-session", SlotID: "document-slot", ItemIndex: -1, PendingOperationID: "control-operation"}))
			forced := failPublicationLateWrites(t, f, table)
			before, rows := publicationArtifacts(t, f), publicationRows(t, f)
			err := f.db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Update("draft_version", 2).Error; err != nil {
					return err
				}
				if table == "document_publication_bindings" {
					return tx.Model(&orm.DocumentPublicationBinding{}).Where("id = ?", "control-binding").Update("result_revision_id", "control-result").Error
				}
				return tx.Model(&orm.DocumentPublicationOperation{}).Where("id = ?", "control-operation").Update("status", "succeeded").Error
			})
			if !errors.Is(err, forced) {
				t.Fatalf("late failure control did not trigger: %v", err)
			}
			requirePublicationUnchanged(t, f, before, rows)
		})
	}
}

// These externally calculated vectors fix the source identity encoding:
// SHA-256(canonical schema + LF + canonical logical JSON), lowercase hex.
// A hash over the body alone cannot satisfy either vector even if parsing and
// representation validation are otherwise correct.
func TestDocumentPublicationHashIncludesCanonicalLogicalType(t *testing.T) {
	for _, tc := range []struct{ name, value, want string }{
		{"markdown", `{"schema":"text/markdown","data":"# Draft"}`, "5f12ce887c107bf530b49e337a962fc16541e9a05bae5acd7e968552dcb89a77"},
		{"ir", `{"schema":"lazyllm.tools.writer.data_models.writer_ir.WriterDocument","data":{"document_id":"fixture-ir","blocks":[]}}`, "97b88b68a126055d3fdb38a9fba732b7af25358f49b03b3ea288ea2af75f23e0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, in := publicationFixture(t, false)
			mustPublicationUpdate(t, f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Update("value", json.RawMessage(tc.value)))
			op := preparePublication(t, f, in)
			if op.SourceHash != tc.want {
				t.Fatalf("typed logical hash=%q want independent vector %q", op.SourceHash, tc.want)
			}
		})
	}
}
