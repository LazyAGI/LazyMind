package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow"
	workflowstore "lazymind/core/workflow/store"
)

func recoveryOperation(t *testing.T, f descriptorFixture, state string) *workflow.DocumentPublicationOperation {
	t.Helper()
	draft := int64(7)
	op, err := workflow.PrepareDocumentPublication(t.Context(), f.db.DB, workflow.DocumentPublicationInput{ArtifactID: "descriptor-artifact", OwnerUserID: "descriptor-owner", SessionID: "descriptor-session", SlotID: "arbitrary-slot", BaseRevision: 3, BaseDraftVersion: &draft, IdempotencyKey: "recovery-original", Provider: deliveryProvider, AllowBound: true})
	if err != nil {
		t.Fatal(err)
	}
	if state != "preparing" {
		if err = workflow.ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "descriptor-owner", op.ID); err != nil {
			t.Fatal(err)
		}
	}
	if state == "outcome_unknown" {
		if err = workflow.MarkDocumentPublicationUnknown(t.Context(), f.db.DB, "descriptor-owner", op.ID); err != nil {
			t.Fatal(err)
		}
	}
	return op
}

func TestDocumentPublicationRecoveryFindsSharedOwnerAndReportsBusyOperation(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Updates(&orm.WorkflowSession{WorkflowID: "writer-workflow"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Updates(&orm.WorkflowSlotRevision{SlotID: "draft_document", Slot: "draft_document"}).Error; err != nil {
		t.Fatal(err)
	}
	draft := int64(7)
	op, err := workflow.PrepareDocumentPublication(t.Context(), f.db.DB, workflow.DocumentPublicationInput{ArtifactID: "descriptor-artifact", OwnerUserID: "descriptor-owner", SessionID: "descriptor-session", SlotID: "draft_document", BaseRevision: 3, BaseDraftVersion: &draft, IdempotencyKey: "shared-original", Provider: deliveryProvider, AllowBound: true, SharedTarget: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = workflow.ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "descriptor-owner", op.ID); err != nil {
		t.Fatal(err)
	}
	if err = workflow.MarkDocumentPublicationUnknown(t.Context(), f.db.DB, "descriptor-owner", op.ID); err != nil {
		t.Fatal(err)
	}
	f.seed(t, "other-draft", "flat_draft_document", "json", `{"schema":"text/markdown","data":"# Other draft"}`)
	lookup := rewriteData(t, deliveryRequest(t.Context(), f, http.MethodGet, "/workflow-artifacts/other-draft/publication", "descriptor-owner", nil))
	if lookup["operation"].(map[string]any)["operation_id"] != op.ID {
		t.Fatal("shared owner not discovered")
	}
	w := deliveryRequest(t.Context(), f, http.MethodPost, "/workflow-artifacts/other-draft/document-actions:execute", "descriptor-owner", deliveryBody("other-request"))
	rewriteError(t, w, 409, "PUBLICATION_IN_PROGRESS")
	var body struct {
		Data struct {
			OperationID string `json:"operation_id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Data.OperationID != op.ID {
		t.Fatal("busy response lost actual operation ID")
	}
	recoverCall(t, f, op.ID, map[string]any{"action": "release_unknown", "confirmed": true, "reason": "accept_unknown"})
	var count int64
	f.db.Model(&orm.DocumentPublicationBinding{}).Where("pending_operation_id = ?", op.ID).Count(&count)
	if count != 0 || len(s.allCalls()) != 0 {
		t.Fatal("shared release retained reservation or wrote externally")
	}
}

func TestDocumentPublicationRecoveryCancellationFencesLateConversion(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	s.afterConvert = func() {
		var op orm.DocumentPublicationOperation
		f.db.Where("idempotency_key = ?", "cancel-before-write").First(&op)
		w := deliveryRequest(t.Context(), f, http.MethodPost, "/document-publications/"+op.ID+":cancel", "descriptor-owner", nil)
		if w.Code != 200 {
			t.Errorf("cancel failed: %s", w.Body.String())
		}
	}
	w := deliveryPublish(t, f, deliveryBody("cancel-before-write"))
	rewriteError(t, w, 409, "PUBLICATION_STATE_CONFLICT")
	if len(s.writes()) != 0 {
		t.Fatal("canceled conversion started a provider write")
	}
}

func TestDocumentPublicationRecoveryLateSuccessReportsReleasedState(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	s.afterWrite = func() {
		var op orm.DocumentPublicationOperation
		f.db.Where("idempotency_key = ?", "release-during-response").First(&op)
		if err := workflow.MarkDocumentPublicationUnknown(t.Context(), f.db.DB, "descriptor-owner", op.ID); err != nil {
			t.Error(err)
			return
		}
		recoverCall(t, f, op.ID, map[string]any{"action": "release_unknown", "confirmed": true, "reason": "accept_unknown"})
	}
	before := descriptorSnapshot(t, f)
	w := deliveryPublish(t, f, deliveryBody("release-during-response"))
	rewriteError(t, w, 409, "PUBLICATION_RECOVERY_CLOSED")
	if descriptorSnapshot(t, f) != before || len(s.writes()) != 1 {
		t.Fatal("late success overwrote or retried")
	}
}

func TestDocumentPublicationRecoveryRejectsScopedAndExternalRequests(t *testing.T) {
	f, _ := newDeliveryFixture(t, "markdown")
	op := recoveryOperation(t, f, "outcome_unknown")
	scoped := workflowstore.WithConversationScope(t.Context(), "other-conversation")
	w := deliveryRequest(scoped, f, http.MethodGet, "/workflow-artifacts/descriptor-artifact/publication", "descriptor-owner", nil)
	if w.Code != 404 {
		t.Fatalf("scope bypass: %d", w.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "/document-publications/"+op.ID, nil)
	request.Header.Set("X-User-Id", "descriptor-owner")
	request.Header.Set("Workflow-Contract-Version", "workflow.v1")
	request.Header.Set("X-LazyMind-External-Ref", "external-fixture")
	w = httptest.NewRecorder()
	f.router.ServeHTTP(w, request)
	if w.Code < 400 || w.Code >= 500 {
		t.Fatalf("external-ref bypass: %d", w.Code)
	}
	w = httptest.NewRecorder()
	workflow.ReadDocumentPublication(w, request)
	if w.Code != 403 {
		t.Fatalf("publication identity did not reject external ref: %d", w.Code)
	}
}

func TestDocumentPublicationRecoveryDeadlineExistsBeforeClaim(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observed := false
	hook := "test:publication-deadline"
	if err := f.db.Callback().Update().After("gorm:update").Register(hook, func(tx *gorm.DB) {
		values, ok := tx.Statement.Dest.(map[string]any)
		if tx.Statement.Table != "document_publication_operations" || !ok || values["status"] != "write_started" {
			return
		}
		deadline, present := tx.Statement.Context.Deadline()
		observed = present
		if !present || time.Until(deadline) > 2*time.Minute {
			t.Error("write deadline starts too late")
		}
		cancel()
	}); err != nil {
		t.Fatal(err)
	}
	defer f.db.Callback().Update().Remove(hook)
	_ = deliveryRequest(ctx, f, http.MethodPost, "/workflow-artifacts/descriptor-artifact/document-actions:execute", "descriptor-owner", deliveryBody("bounded-claim"))
	if !observed || len(s.writes()) != 0 {
		t.Fatal("deadline missing at claim or canceled request sent to provider")
	}
}
func recoveryReceipt() workflow.DocumentPublicationReceipt {
	return workflow.DocumentPublicationReceipt{Provider: deliveryProvider, ContentType: "json", Value: json.RawMessage(`{"schema":"text/markdown","data":"# Remote result"}`), TargetDocument: json.RawMessage(`{"adapter":"fixture-provider-outside-old-whitelist","doc_id":"remote-kept","uri":"https://example.org/remote-kept"}`)}
}
func recoverCall(t *testing.T, f descriptorFixture, id string, body any) map[string]any {
	t.Helper()
	return rewriteData(t, deliveryRequest(t.Context(), f, http.MethodPost, "/document-publications/"+id+":recover", "descriptor-owner", body))
}

func TestDocumentPublicationRecoveryUnknownReleasesWithoutReplay(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	op := recoveryOperation(t, f, "outcome_unknown")
	data := rewriteData(t, deliveryRequest(t.Context(), f, http.MethodGet, "/workflow-artifacts/descriptor-artifact/publication", "descriptor-owner", nil))
	current, ok := data["operation"].(map[string]any)
	if !ok || current["operation_id"] != op.ID {
		t.Fatalf("missing durable operation %#v", data)
	}
	before := descriptorSnapshot(t, f)
	denied := deliveryRequest(t.Context(), f, http.MethodPost, "/document-publications/"+op.ID+":recover", "descriptor-owner", map[string]any{"action": "release_unknown", "reason": "user_verified_no_write"})
	rewriteError(t, denied, 400, "DOCUMENT_ACTION_INVALID")
	body := map[string]any{"action": "release_unknown", "reason": "user_verified_no_write", "confirmed": true}
	for i := 0; i < 2; i++ {
		if recoverCall(t, f, op.ID, body)["status"] != "outcome_unknown_released" {
			t.Fatal("release did not retain honest terminal state")
		}
	}
	if len(s.allCalls()) != 0 || descriptorSnapshot(t, f) != before {
		t.Fatal("recovery performed external IO or modified draft")
	}
	var count int64
	f.db.Model(&orm.DocumentPublicationBinding{}).Where("pending_operation_id = ?", op.ID).Count(&count)
	if count != 0 {
		t.Fatal("reservation retained")
	}
	f.db.Model(&orm.DocumentPublicationOperation{}).Where("id = ?", op.ID).Count(&count)
	if count != 1 {
		t.Fatal("audit operation deleted")
	}
	rewriteError(t, deliveryPublish(t, f, deliveryBody("recovery-original")), 409, "PUBLICATION_RECOVERY_CLOSED")
	draft := int64(7)
	next, err := workflow.PrepareDocumentPublication(t.Context(), f.db.DB, workflow.DocumentPublicationInput{ArtifactID: "descriptor-artifact", OwnerUserID: "descriptor-owner", SessionID: "descriptor-session", SlotID: "arbitrary-slot", BaseRevision: 3, BaseDraftVersion: &draft, IdempotencyKey: "recovery-next", Provider: deliveryProvider, AllowBound: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = workflow.ConfirmDocumentPublication(t.Context(), f.db.DB, "descriptor-owner", op.ID, recoveryReceipt()); err == nil {
		t.Fatal("late receipt accepted after release")
	}
	if _, err = workflow.FinalizeDocumentPublication(t.Context(), f.db.DB, "descriptor-owner", op.ID); err == nil {
		t.Fatal("late finalize accepted")
	}
	var binding orm.DocumentPublicationBinding
	f.db.Where("session_id = ? AND slot_id = ?", op.SessionID, op.SlotID).First(&binding)
	if binding.PendingOperationID != next.ID || descriptorSnapshot(t, f) != before {
		t.Fatal("late old request changed new reservation or draft")
	}
}

func TestDocumentPublicationRecoveryExpiredWriteIsUnknownNotAutomaticRetry(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	op := recoveryOperation(t, f, "write_started")
	if recoverCall(t, f, op.ID, map[string]any{"action": "check"})["status"] != "write_started" {
		t.Fatal("live write expired early")
	}
	f.db.Model(&orm.DocumentPublicationOperation{}).Where("id = ?", op.ID).Update("updated_at", time.Now().Add(-time.Hour))
	read := rewriteData(t, deliveryRequest(t.Context(), f, http.MethodGet, "/document-publications/"+op.ID, "descriptor-owner", nil))
	if read["status"] != "write_started" {
		t.Fatal("GET changed durable state")
	}
	if recoverCall(t, f, op.ID, map[string]any{"action": "check"})["status"] != "outcome_unknown" {
		t.Fatal("stale write not recoverable")
	}
	var count int64
	f.db.Model(&orm.DocumentPublicationBinding{}).Where("pending_operation_id = ?", op.ID).Count(&count)
	if count != 1 || len(s.allCalls()) != 0 {
		t.Fatal("timeout released or replayed without consent")
	}
}

func TestDocumentPublicationRecoveryKeepsConfirmedRemoteWithoutOverwritingDraft(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	op := recoveryOperation(t, f, "write_started")
	if err := workflow.ConfirmDocumentPublication(t.Context(), f.db.DB, "descriptor-owner", op.ID, recoveryReceipt()); err != nil {
		t.Fatal(err)
	}
	f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Updates(map[string]any{"draft_version": 8, "value": json.RawMessage(`{"text":"# New local edit"}`)})
	before := descriptorSnapshot(t, f)
	w := deliveryRequest(t.Context(), f, http.MethodPost, "/document-publications/"+op.ID+":retry-local", "descriptor-owner", nil)
	rewriteError(t, w, 409, "PROVIDER_SYNC_LOCAL_CONFLICT")
	result := recoverCall(t, f, op.ID, map[string]any{"action": "keep_remote", "confirmed": true})
	if result["status"] != "confirmed_detached" || result["provider_synced"] != true {
		t.Fatalf("invalid result %#v", result)
	}
	if descriptorSnapshot(t, f) != before || len(s.allCalls()) != 0 {
		t.Fatal("keep remote overwrote draft or called provider")
	}
	var b orm.DocumentPublicationBinding
	f.db.Where("session_id = ? AND slot_id = ?", op.SessionID, op.SlotID).First(&b)
	var target map[string]any
	json.Unmarshal(b.TargetDocument, &target)
	if b.PendingOperationID != "" || target["doc_id"] != "remote-kept" || len(b.RemoteValue) == 0 {
		t.Fatal("confirmed target lost")
	}
}

func TestDocumentPublicationRecoveryRejectsForeignOwnerAndClientReceipt(t *testing.T) {
	f, _ := newDeliveryFixture(t, "markdown")
	op := recoveryOperation(t, f, "outcome_unknown")
	for _, path := range []string{"/document-publications/" + op.ID, "/workflow-artifacts/descriptor-artifact/publication"} {
		if w := deliveryRequest(t.Context(), f, http.MethodGet, path, "other-owner", nil); w.Code != 404 {
			t.Fatalf("foreign read status %d", w.Code)
		}
	}
	w := deliveryRequest(t.Context(), f, http.MethodPost, "/document-publications/"+op.ID+":recover", "other-owner", map[string]any{"action": "release_unknown", "confirmed": true, "reason": "accept_unknown"})
	rewriteError(t, w, 404, "PUBLICATION_NOT_FOUND")
	w = deliveryRequest(t.Context(), f, http.MethodPost, "/document-publications/"+op.ID+":recover", "descriptor-owner", map[string]any{"action": "keep_remote", "confirmed": true, "receipt": recoveryReceipt()})
	rewriteError(t, w, 400, "DOCUMENT_ACTION_INVALID")
	w = deliveryRequest(t.Context(), f, http.MethodPost, "/document-publications/"+op.ID+":recover", "descriptor-owner", map[string]any{"action": "keep_remote", "confirmed": true})
	rewriteError(t, w, 409, "PUBLICATION_STATE_CONFLICT")
}

func TestOpenAPIPublicationLookupRequiresOnlyArtifactID(t *testing.T) {
	router := mux.NewRouter()
	registerCoreRoutes(router)
	raw, err := buildOpenAPISpecFromRouter(router)
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name     string `json:"name"`
				In       string `json:"in"`
				Required bool   `json:"required"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, parameter := range spec.Paths["/api/core/workflow-artifacts/{artifact_id}/publication"]["get"].Parameters {
		if parameter.In == "path" {
			paths = append(paths, parameter.Name)
			if !parameter.Required {
				t.Fatal("artifact ID must be required")
			}
		}
	}
	if len(paths) != 1 || paths[0] != "artifact_id" {
		t.Fatalf("lookup path parameters = %v, want only artifact_id", paths)
	}
}
