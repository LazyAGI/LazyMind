package workflow

import (
	"encoding/json"
	"testing"

	"lazymind/core/common/orm"
)

func TestIndependentPublicationKeepsEditsAndCleansPayload(t *testing.T) {
	f, input := publicationFixture(t, false)
	input.PublicationID = "publication-confirmation"
	op := preparePublication(t, f, input)
	// Editing after confirmation must neither change the sent content nor be
	// overwritten when the provider eventually replies.
	newValue := json.RawMessage(`{"schema":"text/markdown","data":"# New local edits"}`)
	if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Updates(map[string]any{"value": newValue, "draft_version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	before := publicationArtifacts(t, f)
	confirmPublication(t, f, op)
	if _, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID); err != nil {
		t.Fatal(err)
	}
	if publicationArtifacts(t, f) != before {
		t.Fatal("publication changed local document or workflow state")
	}
	saved := requirePublicationStatus(t, f, op.ID, "succeeded")
	if len(saved.SourceValue) != 0 || len(saved.RemoteValue) != 0 || len(saved.CandidateValue) != 0 || len(saved.MediaAssets) != 0 {
		t.Fatal("finished operation retained full payload")
	}
	var receipt DocumentPublicationReceipt
	if err := json.Unmarshal(saved.ReceiptJSON, &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt.Value) != 0 {
		t.Fatal("receipt retained historical document body")
	}
	var binding orm.DocumentPublicationBinding
	if err := publicationBindingQuery(f.db.DB, op).First(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if binding.LastSuccessHash != op.SourceHash || binding.LastOperationID != op.ID || binding.PendingOperationID != "" || len(binding.RemoteValue) != 0 {
		t.Fatalf("invalid successful binding: %#v", binding)
	}
	if _, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID); err != nil {
		t.Fatalf("duplicate finalization: %v", err)
	}
}

func TestIndependentPublicationTargetsDoNotBlockEachOther(t *testing.T) {
	f, input := publicationFixture(t, false)
	input.PublicationID = "publication-confirmation"
	first := preparePublication(t, f, input)
	input.Provider, input.IdempotencyKey = "github", "request-github"
	second := preparePublication(t, f, input)
	if first.SourceHash != second.SourceHash {
		t.Fatal("platforms captured different content")
	}
	confirmPublication(t, f, first)
	if _, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", first.ID); err != nil {
		t.Fatal(err)
	}
	if err := FailDocumentPublicationBeforeWrite(t.Context(), f.db.DB, "owner", second.ID); err != nil {
		t.Fatal(err)
	}
	var binding orm.DocumentPublicationBinding
	if err := publicationBindingQuery(f.db.DB, first).First(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if binding.LastSuccessHash != first.SourceHash {
		t.Fatal("another platform failure changed successful binding")
	}
}

func TestIndependentPublicationUnknownKeepsReservationAndPayload(t *testing.T) {
	f, input := publicationFixture(t, false)
	input.PublicationID = "publication-confirmation"
	op := preparePublication(t, f, input)
	if err := ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID); err != nil {
		t.Fatal(err)
	}
	if err := MarkDocumentPublicationUnknown(t.Context(), f.db.DB, "owner", op.ID); err != nil {
		t.Fatal(err)
	}
	input.IdempotencyKey = "second-confirmation"
	_, err := PrepareDocumentPublication(t.Context(), f.db.DB, input)
	requirePublicationError(t, err, "PUBLICATION_IN_PROGRESS")
	_, err = RecoverDocumentPublication(t.Context(), f.db.DB, "owner", op.ID, DocumentPublicationRecoveryRequest{Action: "release_unknown", Confirmed: true, Reason: "accept_unknown"})
	requirePublicationError(t, err, "PUBLICATION_STATE_CONFLICT")
	saved := requirePublicationStatus(t, f, op.ID, "outcome_unknown")
	if len(saved.SourceValue) == 0 {
		t.Fatal("unresolved operation lost recovery payload")
	}
}

func TestIndependentPublicationFailureDoesNotReplaceLastSuccess(t *testing.T) {
	f, input := publicationFixture(t, false)
	input.PublicationID = "publication-confirmation"
	op := preparePublication(t, f, input)
	confirmPublication(t, f, op)
	if _, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID); err != nil {
		t.Fatal(err)
	}
	input.AllowBound = true
	input.IdempotencyKey, input.PublicationID = "next-confirmation", "next-publication"
	next := preparePublication(t, f, input)
	if err := FailDocumentPublicationBeforeWrite(t.Context(), f.db.DB, "owner", next.ID); err != nil {
		t.Fatal(err)
	}
	var binding orm.DocumentPublicationBinding
	if err := publicationBindingQuery(f.db.DB, op).First(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if binding.LastOperationID != op.ID || binding.LastSuccessHash != op.SourceHash {
		t.Fatal("failure overwrote last success")
	}
}
