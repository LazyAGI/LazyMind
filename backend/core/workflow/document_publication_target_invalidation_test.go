package workflow

import (
	"encoding/json"
	"errors"
	"testing"

	"lazymind/core/common/orm"
)

// Writer revisions consume target_document during preparation. Updating that
// binding after publication must not invalidate the draft we just published.
func TestPublicationTargetSyncKeepsPublishedDraftEffective(t *testing.T) {
	for _, producerRef := range []string{"", "writer-producer", "writer-producer-task"} {
		t.Run("producer="+producerRef, func(t *testing.T) {
			f, in := publicationTargetFixture(t, producerRef)
			op := preparePublication(t, f, in)
			confirmPublication(t, f, op)
			revision, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
			mustPublicationErrorNil(t, err)
			var saved orm.WorkflowSlotRevision
			mustPublicationUpdate(t, f.db.First(&saved, "id = ?", revision.ID))
			if !saved.Selected || saved.Validity != "effective" {
				t.Fatalf("successful publication invalidated its own result: selected=%v validity=%s", saved.Selected, saved.Validity)
			}
			requireRevisionState(t, artifactDependencyFixture{db: f.db}, "other-target-output", "stale", false)
			var producer orm.WorkflowSessionStep
			mustPublicationUpdate(t, f.db.First(&producer, "id = ?", "writer-producer"))
			if producer.Validity != "effective" {
				t.Fatalf("publication invalidated its producer: %s", producer.Validity)
			}
			replay, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
			mustPublicationErrorNil(t, err)
			if replay.ID != saved.ID || !replay.Selected || replay.Validity != "effective" {
				t.Fatalf("publication replay returned unusable result: %#v", replay)
			}
		})
	}
}

func publicationTargetFixture(t *testing.T, producerRef string) (rollbackFixture, DocumentPublicationInput) {
	t.Helper()
	f, in := publicationFixture(t, false)
	in.SharedTarget = true
	mustPublicationUpdate(t, f.db.Create(&orm.WorkflowSessionStep{
		ID: "writer-producer", SessionID: in.SessionID, StepID: "source", Attempt: 3,
		TaskID: "writer-producer-task", Status: StepStatusSucceeded, Validity: "effective",
	}))
	mustPublicationUpdate(t, f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", f.currentRevisionID).
		Update("producer_attempt_id", producerRef))
	humanID := "existing-target-human"
	mustPublicationUpdate(t, f.db.Create(&orm.WorkflowHumanArtifact{
		ID: humanID, SessionID: in.SessionID, Slot: "target_document", ContentType: "json", DraftVersion: 1,
		Value: json.RawMessage(`{"schema":"lazyllm.tools.writer.data_models.task.TargetDocument","data":{"adapter":"obsidian","uri":"fixture://vault/draft.md"}}`),
	}))
	mustPublicationUpdate(t, f.db.Create(&orm.WorkflowSlotRevision{
		ID: "existing-target", SessionID: in.SessionID, SlotID: "target_document", Slot: "target_document",
		Revision: 1, Selected: true, Validity: "effective", StepID: "prepare", Attempt: 1, HumanArtifactID: &humanID,
	}))
	mustPublicationUpdate(t, f.db.Create(&orm.WorkflowAttemptInputBinding{
		ID: "producer-target-input", SessionID: in.SessionID, AttemptID: "writer-producer",
		MaterialID: "target_document", MaterialRevisionID: "existing-target", SourceType: "artifact",
	}))
	mustPublicationUpdate(t, f.db.Create(&orm.WorkflowSessionStep{
		ID: "other-target-consumer", SessionID: in.SessionID, StepID: "other", Attempt: 1,
		Status: StepStatusSucceeded, Validity: "effective",
	}))
	mustPublicationUpdate(t, f.db.Create(&orm.WorkflowAttemptInputBinding{
		ID: "other-target-input", SessionID: in.SessionID, AttemptID: "other-target-consumer",
		MaterialID: "target_document", MaterialRevisionID: "existing-target", SourceType: "artifact",
	}))
	mustPublicationUpdate(t, f.db.Create(&orm.WorkflowSlotRevision{
		ID: "other-target-output", SessionID: in.SessionID, SlotID: "other-output", Slot: "other-output",
		Revision: 1, Selected: true, Validity: "effective", StepID: "other", Attempt: 1,
		ProducerAttemptID: "other-target-consumer",
	}))
	return f, in
}

func TestPublicationTargetSyncStillRejectsLiveConsumers(t *testing.T) {
	for _, liveAttempt := range []string{"writer-producer", "other-target-consumer"} {
		t.Run(liveAttempt, func(t *testing.T) {
			f, in := publicationTargetFixture(t, "writer-producer")
			op := preparePublication(t, f, in)
			confirmPublication(t, f, op)
			mustPublicationUpdate(t, f.db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", liveAttempt).Update("status", StepStatusRunning))
			artifacts, rows := publicationArtifacts(t, f), publicationRows(t, f)
			result, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
			if result != nil || !errors.Is(err, ErrArtifactInUse) {
				t.Fatalf("live target consumer accepted: result=%#v err=%v", result, err)
			}
			requirePublicationUnchanged(t, f, artifacts, rows)
		})
	}
}

func TestOrdinaryTargetMutationStillInvalidatesWriterProducer(t *testing.T) {
	for _, source := range []string{"human", "provider_sync"} {
		t.Run(source, func(t *testing.T) {
			f, in := publicationTargetFixture(t, "writer-producer")
			base, draft := 1, int64(1)
			_, err := WriteSlotRevisionWithHumanArtifact(t.Context(), f.db.DB, in.SessionID, "target_document", "target_document", "prepare", 1,
				"single", nil, "json", json.RawMessage(`{"schema":"lazyllm.tools.writer.data_models.task.TargetDocument","data":{"adapter":"obsidian","uri":"fixture://vault/other.md"}}`), nil, source, &base, &draft)
			mustPublicationErrorNil(t, err)
			requireRevisionState(t, artifactDependencyFixture{db: f.db}, f.currentRevisionID, "stale", false)
			requireRevisionState(t, artifactDependencyFixture{db: f.db}, "other-target-output", "stale", false)
		})
	}
}

func TestGitHubPublicationTargetSyncPreservesCompletedConsumers(t *testing.T) {
	for _, liveAttempt := range []string{"", "writer-producer", "other-target-consumer"} {
		t.Run("live="+liveAttempt, func(t *testing.T) {
			f, in := publicationTargetFixture(t, "writer-producer")
			in.Provider = "github"
			op := preparePublication(t, f, in)
			mustPublicationErrorNil(t, ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID))
			receipt := publicationReceipt()
			receipt.Provider = "github"
			receipt.TargetDocument = json.RawMessage(`{"adapter":"github","uri":"github://fixture/repo/note.md","meta":{"pull_request_url":"https://github.com/fixture/repo/pull/1"}}`)
			mustPublicationErrorNil(t, ConfirmDocumentPublication(t.Context(), f.db.DB, "owner", op.ID, receipt))
			if liveAttempt != "" {
				mustPublicationUpdate(t, f.db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", liveAttempt).Update("status", StepStatusRunning))
			}
			artifacts, rows := publicationArtifacts(t, f), publicationRows(t, f)
			result, err := FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", op.ID)
			if liveAttempt != "" {
				if result != nil || !errors.Is(err, ErrArtifactInUse) {
					t.Fatalf("GitHub sync accepted live consumer: result=%#v err=%v", result, err)
				}
				requirePublicationUnchanged(t, f, artifacts, rows)
				return
			}
			mustPublicationErrorNil(t, err)
			requireRevisionState(t, artifactDependencyFixture{db: f.db}, result.ID, "effective", true)
			requireRevisionState(t, artifactDependencyFixture{db: f.db}, "other-target-output", "effective", true)
			requireAttemptValidity(t, artifactDependencyFixture{db: f.db}, "writer-producer", "effective")
			requireAttemptValidity(t, artifactDependencyFixture{db: f.db}, "other-target-consumer", "effective")
		})
	}
}
