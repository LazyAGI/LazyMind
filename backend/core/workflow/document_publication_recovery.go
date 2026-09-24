package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"gorm.io/gorm"
	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
)

type DocumentPublicationLookup struct {
	Operation *DocumentPublicationStatus `json:"operation,omitempty"`
}
type DocumentPublicationRecoveryRequest struct {
	Action    string `json:"action" enum:"check,release_unknown,keep_remote" required:"true"`
	Confirmed bool   `json:"confirmed,omitempty"`
	Reason    string `json:"reason,omitempty" enum:"user_verified_no_write,accept_unknown"`
}

func (r DocumentPublicationRecoveryRequest) valid() bool {
	switch r.Action {
	case "check":
		return !r.Confirmed && r.Reason == ""
	case "release_unknown":
		return r.Confirmed && (r.Reason == "user_verified_no_write" || r.Reason == "accept_unknown")
	case "keep_remote":
		return r.Confirmed && r.Reason == ""
	}
	return false
}

func publicationStatus(op *DocumentPublicationOperation, now time.Time) DocumentPublicationStatus {
	result := DocumentPublicationStatus{OperationID: op.ID, Status: op.Status, Provider: op.Provider, ArtifactID: op.ResultRevisionID, ErrorCode: op.ErrorCode, UpdatedAt: op.UpdatedAt, ProviderSynced: len(op.ReceiptJSON) > 0, Actions: []string{}, SourceSlotID: op.SlotID, ItemIndex: op.ItemIndex}
	switch op.Status {
	case "preparing":
		result.Actions = []string{"cancel"}
	case "write_started":
		deadline := op.UpdatedAt.Add(algo.DocumentActionTimeout + 10*time.Second)
		result.RecoveryAfter = &deadline
		if !now.Before(deadline) {
			result.Actions = []string{"check"}
		}
	case "outcome_unknown":
		result.Actions = []string{"release_unknown"}
	case "provider_confirmed", "local_conflict", "local_persist_failed":
		if len(op.ReceiptJSON) > 0 {
			result.Actions = []string{"retry_local", "keep_remote"}
		}
	}
	target := op.TargetDocument
	if len(op.ReceiptJSON) > 0 {
		var receipt DocumentPublicationReceipt
		if json.Unmarshal(op.ReceiptJSON, &receipt) == nil {
			target = receipt.TargetDocument
		}
	}
	result.TargetURL = documentPublicationTargetURL(op.Provider, target)
	return result
}

// Find the durable operation for this logical document, including Writer's
// shared target. Historical artifact IDs still resolve after a lost response.
func FindDocumentPublication(ctx context.Context, db *gorm.DB, owner, artifactID string) (*DocumentPublicationOperation, error) {
	var result *DocumentPublicationOperation
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		result = nil
		var revision orm.WorkflowSlotRevision
		if err := tx.First(&revision, "id = ?", artifactID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return documentFailure("ARTIFACT_NOT_FOUND", 404)
			}
			return err
		}
		session, err := publicationSession(ctx, tx, owner, revision.SessionID)
		if err != nil {
			return err
		}
		slots := []struct {
			slot  string
			index int
		}{{revision.SlotID, publicationIndex(revision.ListIndex)}}
		if session.WorkflowID == "writer-workflow" && revision.ListIndex == nil && (revision.SlotID == "draft_document" || revision.SlotID == "flat_draft_document") {
			slots = append(slots, struct {
				slot  string
				index int
			}{"target_document", -1})
		}
		for _, item := range slots {
			var binding orm.DocumentPublicationBinding
			err := tx.Where("session_id = ? AND slot_id = ? AND item_index = ?", session.ID, item.slot, item.index).First(&binding).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if binding.OwnerUserID != owner {
				return publicationError("PUBLICATION_NOT_FOUND")
			}
			if binding.PendingOperationID == "" {
				continue
			}
			var op DocumentPublicationOperation
			if err = tx.Where("id = ? AND session_id = ? AND owner_user_id = ?", binding.PendingOperationID, session.ID, owner).First(&op).Error; err != nil {
				return publicationError("PUBLICATION_STATE_CONFLICT")
			}
			result = &op
			return nil
		}
		var op DocumentPublicationOperation
		err = tx.Where("session_id = ? AND slot_id = ? AND item_index = ? AND owner_user_id = ?", session.ID, revision.SlotID, publicationIndex(revision.ListIndex), owner).Order("created_at DESC, id DESC").First(&op).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err == nil {
			result = &op
		}
		return err
	})
	return result, err
}

func RecoverDocumentPublication(ctx context.Context, db *gorm.DB, owner, id string, request DocumentPublicationRecoveryRequest) (*DocumentPublicationOperation, error) {
	if !request.valid() {
		return nil, publicationError("DOCUMENT_ACTION_INVALID")
	}
	var result *DocumentPublicationOperation
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		op, err := publicationLockedOperation(ctx, tx, owner, id)
		if err != nil {
			return err
		}
		result = op
		if request.Action == "check" {
			if op.Status == "write_started" && !time.Now().Before(op.UpdatedAt.Add(algo.DocumentActionTimeout+10*time.Second)) {
				if err = publicationReserved(tx, op); err != nil {
					return err
				}
				if err = tx.Model(op).Updates(map[string]any{"status": "outcome_unknown", "updated_at": time.Now().UTC()}).Error; err != nil {
					return err
				}
			}
			return tx.First(result, "id = ?", id).Error
		}
		next, reason := "outcome_unknown_released", "PUBLICATION_RELEASED_UNVERIFIED"
		var receipt DocumentPublicationReceipt
		if request.Action == "release_unknown" {
			if op.Status == next {
				return nil
			}
			if op.Status != "outcome_unknown" || len(op.ReceiptJSON) > 0 {
				return publicationError("PUBLICATION_STATE_CONFLICT")
			}
			if request.Reason == "user_verified_no_write" {
				reason = "PUBLICATION_RELEASED_USER_NO_WRITE"
			}
		} else {
			next, reason = "confirmed_detached", ""
			if op.Status == next {
				return nil
			}
			if (op.Status != "provider_confirmed" && op.Status != "local_conflict" && op.Status != "local_persist_failed") || len(op.ReceiptJSON) == 0 {
				return publicationError("PUBLICATION_STATE_CONFLICT")
			}
			if json.Unmarshal(op.ReceiptJSON, &receipt) != nil || receipt.Provider != op.Provider || !json.Valid(receipt.TargetDocument) || string(receipt.TargetDocument) == "null" || !json.Valid(receipt.Value) {
				return publicationError("DOCUMENT_ACTION_INVALID")
			}
		}
		if err = publicationReserved(tx, op); err != nil {
			return err
		}
		values := map[string]any{"pending_operation_id": ""}
		if request.Action == "keep_remote" {
			values["provider"], values["target_document"], values["remote_value"] = receipt.Provider, receipt.TargetDocument, receipt.Value
			values["source_revision_id"], values["result_revision_id"] = op.SourceRevisionID, ""
		}
		if err = tx.Model(&orm.DocumentPublicationBinding{}).Where("pending_operation_id = ? AND owner_user_id = ? AND session_id = ?", op.ID, owner, op.SessionID).Updates(values).Error; err != nil {
			return err
		}
		if err = tx.Model(op).Updates(map[string]any{"status": next, "error_code": reason, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		return tx.First(result, "id = ?", id).Error
	})
	return result, err
}

func ReadArtifactDocumentPublication(w http.ResponseWriter, r *http.Request) {
	owner, ok := publicationHTTPIdentity(w, r)
	if !ok {
		return
	}
	op, err := FindDocumentPublication(r.Context(), corestore.DB(), owner, common.PathVar(r, "artifact_id"))
	if err != nil {
		replyPublicationResult(w, nil, nil, publicationFailure(err))
		return
	}
	result := DocumentPublicationLookup{}
	if op != nil {
		value := publicationStatusForRead(r.Context(), op)
		result.Operation = &value
	}
	common.ReplyOK(w, result)
}
func RecoverDocumentPublicationHTTP(w http.ResponseWriter, r *http.Request) {
	owner, ok := publicationHTTPIdentity(w, r)
	if !ok {
		return
	}
	var request DocumentPublicationRecoveryRequest
	if decodeDocumentJSON(r.Body, &request) != nil || !request.valid() {
		replyPublicationResult(w, nil, nil, documentFailure("DOCUMENT_ACTION_INVALID", 400))
		return
	}
	op, err := RecoverDocumentPublication(r.Context(), corestore.DB(), owner, common.PathVar(r, "operation_id"), request)
	if err != nil {
		replyPublicationResult(w, nil, nil, publicationFailure(err))
		return
	}
	common.ReplyOK(w, publicationStatus(op, time.Now()))
}

// A late HTTP result must report the durable recovery decision, not resurrect
// a stale in-memory status after the owner released or finalized the operation.
func publicationFailureState(ctx context.Context, db *gorm.DB, owner string, op *DocumentPublicationOperation, failure error) (*DocumentPublishResult, *DocumentPublicationOperation, error) {
	current, err := GetDocumentPublication(ctx, db, owner, op.ID)
	if err != nil {
		return nil, nil, failure
	}
	if current.Status == "succeeded" {
		saved, err := publicationSavedResult(ctx, db, current)
		return saved, current, err
	}
	if current.Status == "outcome_unknown_released" || current.Status == "confirmed_detached" || current.Status == "canceled" || current.Status == "failed_no_write" {
		return nil, current, publicationReplayFailure(current)
	}
	if len(current.ReceiptJSON) > 0 && publicationFailure(failure).code == "PUBLICATION_OUTCOME_UNKNOWN" {
		return nil, current, publicationReplayFailure(current)
	}
	return nil, current, failure
}
