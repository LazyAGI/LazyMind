package workflow

import "testing"

func TestDocumentPublicationConfirmedNoChangeDoesNotCreateRevision(t *testing.T) {
	fixture, input := publicationFixture(t, false)
	op := preparePublication(t, fixture, input)
	mustPublicationErrorNil(t, ClaimDocumentPublicationWrite(t.Context(), fixture.db.DB, "owner", op.ID))
	receipt := publicationReceipt()
	receipt.NoLocalChange = true
	mustPublicationErrorNil(t, ConfirmDocumentPublication(t.Context(), fixture.db.DB, "owner", op.ID, receipt))
	before := publicationArtifacts(t, fixture)
	revision, err := FinalizeDocumentPublication(t.Context(), fixture.db.DB, "owner", op.ID)
	mustPublicationErrorNil(t, err)
	if revision.ID != fixture.currentRevisionID || publicationArtifacts(t, fixture) != before {
		t.Fatal("confirmed no-change mutated the existing artifact or state")
	}
	requirePublicationStatus(t, fixture, op.ID, "succeeded")
}
