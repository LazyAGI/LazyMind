package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

// TestWriterSyncStatus_BadRequest maps 400 and 422 to 400.
func TestWriterSyncStatus_BadRequest(t *testing.T) {
	if got := writerSyncStatus(http.StatusBadRequest); got != http.StatusBadRequest {
		t.Fatalf("status 400: got %d, want %d", got, http.StatusBadRequest)
	}
	if got := writerSyncStatus(http.StatusUnprocessableEntity); got != http.StatusBadRequest {
		t.Fatalf("status 422: got %d, want %d", got, http.StatusBadRequest)
	}
}

// TestWriterSyncStatus_AuthErrors passes through 401, 403, 409.
func TestWriterSyncStatus_AuthErrors(t *testing.T) {
	if got := writerSyncStatus(http.StatusUnauthorized); got != http.StatusUnauthorized {
		t.Fatalf("got %d, want %d", got, http.StatusUnauthorized)
	}
	if got := writerSyncStatus(http.StatusForbidden); got != http.StatusForbidden {
		t.Fatalf("got %d, want %d", got, http.StatusForbidden)
	}
	if got := writerSyncStatus(http.StatusConflict); got != http.StatusConflict {
		t.Fatalf("got %d, want %d", got, http.StatusConflict)
	}
}

// TestWriterSyncStatus_Default maps unrecognized statuses to 502.
func TestWriterSyncStatus_Default(t *testing.T) {
	if got := writerSyncStatus(http.StatusOK); got != http.StatusBadGateway {
		t.Fatalf("status 200: got %d, want %d", got, http.StatusBadGateway)
	}
	if got := writerSyncStatus(http.StatusInternalServerError); got != http.StatusBadGateway {
		t.Fatalf("status 500: got %d, want %d", got, http.StatusBadGateway)
	}
	if got := writerSyncStatus(http.StatusNotFound); got != http.StatusBadGateway {
		t.Fatalf("status 404: got %d, want %d", got, http.StatusBadGateway)
	}
	if got := writerSyncStatus(999); got != http.StatusBadGateway {
		t.Fatalf("unknown: got %d, want %d", got, http.StatusBadGateway)
	}
}

func TestLoadWriterWriteBackBaseline_UsesSourceDocumentForInitialSync(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.PluginSlotRevision{})
	source := json.RawMessage(`{"data":{"document_id":"feishu-doc","provider_binding":{"provider":"feishu","document_id":"feishu-doc"}}}`)
	seedWriterRevision(t, db, "source", "source_document", 1, true, "ai", source)
	seedWriterRevision(t, db, "draft-1", "draft_document", 1, false, "ai", source)
	seedWriterRevision(t, db, "draft-2", "draft_document", 2, true, "human", source)

	baseline, err := loadWriterWriteBackBaseline(context.Background(), db.DB, "session", 2)
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	if baseline.Revision.SlotID != "source_document" {
		t.Fatalf("baseline slot = %q, want source_document", baseline.Revision.SlotID)
	}
	if baseline.Revision.Revision != 1 {
		t.Fatalf("baseline revision = %d, want 1", baseline.Revision.Revision)
	}
}

func TestLoadWriterWriteBackBaseline_PrefersLatestSyncedDraft(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.PluginSlotRevision{})
	source := json.RawMessage(`{"data":{"document_id":"source-doc","provider_binding":{"provider":"feishu","document_id":"source-doc"}}}`)
	syncedDraft := json.RawMessage(`{"data":{"document_id":"synced-doc","provider_binding":{"provider":"feishu","document_id":"synced-doc"}}}`)
	seedWriterRevision(t, db, "source", "source_document", 1, true, "ai", source)
	seedWriterRevision(t, db, "draft-1", "draft_document", 1, false, "provider_sync", syncedDraft)
	seedWriterRevision(t, db, "draft-2", "draft_document", 2, false, "human", syncedDraft)
	seedWriterRevision(t, db, "draft-3", "draft_document", 3, true, "human", syncedDraft)

	baseline, err := loadWriterWriteBackBaseline(context.Background(), db.DB, "session", 3)
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	if baseline.Revision.ID != "draft-1" {
		t.Fatalf("baseline id = %q, want draft-1", baseline.Revision.ID)
	}
}

func seedWriterRevision(
	t *testing.T,
	db *orm.DB,
	id, slotID string,
	revision int,
	selected bool,
	changeSource string,
	content json.RawMessage,
) {
	t.Helper()
	if err := db.Create(&orm.PluginSlotRevision{
		ID:              id,
		SessionID:       "session",
		SlotID:          slotID,
		Revision:        revision,
		Selected:        selected,
		ContentSnapshot: content,
		ChangeSource:    changeSource,
		Slot:            slotID,
		StepID:          "write_document",
		Attempt:         1,
		CreatedAt:       time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed revision %s: %v", id, err)
	}
}
