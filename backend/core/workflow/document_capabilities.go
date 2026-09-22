package workflow

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/workflow/document"
	workflowstore "lazymind/core/workflow/store"
)

func documentReadOwner(w http.ResponseWriter, r *http.Request) (string, bool) {
	owner := strings.TrimSpace(r.Header.Get("X-User-Id"))
	if owner == "" {
		common.ReplyErr(w, "user_id required", http.StatusBadRequest)
		return "", false
	}
	return owner, true
}

func authorizeDocumentSession(w http.ResponseWriter, r *http.Request, db *gorm.DB, sessionID, owner string) bool {
	err := workflowstore.New(db).AuthorizeSession(r.Context(), sessionID, owner)
	switch {
	case err == nil:
		return true
	case errors.Is(err, workflowstore.ErrNotFound):
		common.ReplyErr(w, "session not found", http.StatusNotFound)
	case errors.Is(err, workflowstore.ErrPermissionDenied):
		common.ReplyErr(w, "forbidden", http.StatusForbidden)
	default:
		common.ReplyErr(w, "query session failed", http.StatusInternalServerError)
	}
	return false
}

func describeDocumentRevision(ctx context.Context, db *gorm.DB, owner, id string) workflowstore.Artifact {
	repo := workflowstore.New(db)
	artifact, err := repo.ReadArtifact(ctx, owner, id)
	if err != nil {
		return workflowstore.Artifact{ID: id, DocumentError: document.Unavailable()}
	}
	repo.DescribeArtifact(ctx, owner, &artifact, true)
	return artifact
}

func enrichDocumentSlots(ctx context.Context, db *gorm.DB, owner string, slots []slotDTO) {
	for i := range slots {
		artifact := describeDocumentRevision(ctx, db, owner, slots[i].ArtifactID)
		slots[i].Document = artifact.Document
		slots[i].DocumentError = artifact.DocumentError
	}
}

// SlotResponse and SessionResponse let the OpenAPI registry describe the actual
// Panel DTOs without maintaining a second copy of their fields.
type SlotResponse = slotDTO
type SessionResponse = sessionDTO
