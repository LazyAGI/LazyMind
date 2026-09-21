package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/artifactgraph"
	"lazymind/core/workflow/document"
	workflowstore "lazymind/core/workflow/store"
)

type DocumentPublicationInput struct {
	ArtifactID       string
	CandidateValue   json.RawMessage
	OwnerUserID      string
	SessionID        string
	SlotID           string
	ListIndex        *int
	BaseRevision     int
	BaseDraftVersion *int64
	IdempotencyKey   string
	Provider         string
	Title            string
	ParentURI        string
	Template         string
	// Internal policy, never decoded from public input. Legacy adapters reserve
	// the shared target as well as the exact item, preserving historical aliases.
	AllowBound   bool
	SharedTarget bool
}
type DocumentPublicationOperation = orm.DocumentPublicationOperation
type DocumentPublicationReceipt struct {
	NoLocalChange  bool
	Provider       string
	TargetDocument json.RawMessage
	ContentType    string
	Value          json.RawMessage
}

type publicationDomainError string

func (err publicationDomainError) Error() string { return string(err) }
func publicationError(code string) error         { return publicationDomainError(code) }
func publicationIndex(index *int) int {
	if index == nil {
		return -1
	}
	return *index
}
func publicationPointer(index int) *int {
	if index < 0 {
		return nil
	}
	return &index
}
func publicationHash(schema string, value json.RawMessage) string {
	sum := sha256.Sum256(append([]byte(schema+"\n"), value...))
	return hex.EncodeToString(sum[:])
}

func publicationSession(ctx context.Context, tx *gorm.DB, owner, id string) (*orm.WorkflowSession, error) {
	session, err := artifactgraph.LockSession(tx, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(owner) == "" || session.CreateUserID != owner || session.Dismissed {
		return nil, publicationError("PUBLICATION_NOT_FOUND")
	}
	if scope := workflowstore.ConversationScope(ctx); scope != "" && scope != session.ConversationID {
		return nil, publicationError("PUBLICATION_NOT_FOUND")
	}
	if !workflowstore.DocumentSessionEditable(session) {
		return nil, publicationError("SESSION_NOT_EDITABLE")
	}
	return session, nil
}
func publicationSource(ctx context.Context, tx *gorm.DB, in DocumentPublicationInput, expectedID string) (orm.WorkflowSlotRevision, *document.Content, string, int64, error) {
	var revision orm.WorkflowSlotRevision
	q := tx.Where("session_id = ? AND slot_id = ? AND selected = ?", in.SessionID, in.SlotID, true)
	if in.ListIndex == nil {
		q = q.Where("list_index IS NULL")
	} else {
		q = q.Where("list_index = ?", *in.ListIndex)
	}
	if err := q.First(&revision).Error; err != nil {
		return revision, nil, "", 0, err
	}
	if revision.Validity != "effective" || revision.Revision != in.BaseRevision || expectedID != "" && revision.ID != expectedID {
		return revision, nil, "", 0, ErrConflict
	}
	contentType := "json"
	draft := int64(0)
	if revision.HumanArtifactID != nil {
		var human orm.WorkflowHumanArtifact
		if err := tx.First(&human, "id = ?", *revision.HumanArtifactID).Error; err != nil {
			return revision, nil, "", 0, err
		}
		if in.BaseDraftVersion == nil {
			return revision, nil, "", 0, ErrDraftVersionRequired
		}
		if human.DraftVersion != *in.BaseDraftVersion {
			return revision, nil, "", 0, ErrDraftVersionConflict
		}
		contentType = human.ContentType
		draft = human.DraftVersion
	}
	if err := artifactgraph.CheckConsumers(ctx, tx, in.SessionID, revision.ID); err != nil {
		return revision, nil, "", 0, err
	}
	raw, err := LoadSlotRevisionValue(ctx, tx, revision)
	if err != nil {
		return revision, nil, "", 0, err
	}
	var session orm.WorkflowSession
	if err := tx.First(&session, "id = ?", in.SessionID).Error; err != nil {
		return revision, nil, "", 0, err
	}
	content, failure := document.ReadContent(raw, contentType, func() (bool, error) { return workflowstore.New(tx).PinnedMarkdownHint(ctx, &session, in.SlotID) })
	if failure != nil || content == nil {
		return revision, nil, "", 0, publicationError("DOCUMENT_INVALID")
	}
	return revision, content, contentType, draft, nil
}
func publicationInput(op *DocumentPublicationOperation) DocumentPublicationInput {
	var draft *int64
	if op.SourceDraftVersion > 0 {
		v := op.SourceDraftVersion
		draft = &v
	}
	return DocumentPublicationInput{OwnerUserID: op.OwnerUserID, SessionID: op.SessionID, SlotID: op.SlotID, ListIndex: publicationPointer(op.ItemIndex), BaseRevision: op.SourceRevision, BaseDraftVersion: draft, Provider: op.Provider, AllowBound: op.AllowBound, SharedTarget: op.SharedTarget}
}
func publicationBindingQuery(tx *gorm.DB, op *DocumentPublicationOperation) *gorm.DB {
	return tx.Where("session_id = ? AND slot_id = ? AND item_index = ?", op.SessionID, op.SlotID, op.ItemIndex)
}
func publicationHasIRBinding(raw json.RawMessage) bool {
	var v struct {
		ProviderBinding map[string]any `json:"provider_binding"`
	}
	return json.Unmarshal(raw, &v) == nil && len(v.ProviderBinding) > 0
}

func PrepareDocumentPublication(ctx context.Context, db *gorm.DB, in DocumentPublicationInput) (*DocumentPublicationOperation, error) {
	op, _, err := prepareDocumentPublication(ctx, db, in)
	return op, err
}
func prepareDocumentPublication(ctx context.Context, db *gorm.DB, in DocumentPublicationInput) (*DocumentPublicationOperation, bool, error) {
	if in.SessionID == "" || in.SlotID == "" || in.BaseRevision < 1 || publicationIndex(in.ListIndex) < -1 || strings.TrimSpace(in.Provider) == "" || strings.TrimSpace(in.IdempotencyKey) == "" || len(in.IdempotencyKey) > 128 {
		return nil, false, publicationError("DOCUMENT_ACTION_INVALID")
	}
	raw, _ := json.Marshal(in)
	requestHash := publicationHash("publication-request-v1", raw)
	var result DocumentPublicationOperation
	created := false
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		result = DocumentPublicationOperation{}
		created = false
		if _, err := publicationSession(ctx, tx, in.OwnerUserID, in.SessionID); err != nil {
			return err
		}
		err := tx.Where("owner_user_id = ? AND idempotency_key = ?", in.OwnerUserID, in.IdempotencyKey).First(&result).Error
		if err == nil {
			if result.RequestHash != requestHash {
				return publicationError("PUBLICATION_IDEMPOTENCY_CONFLICT")
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		revision, content, ct, draft, err := publicationSource(ctx, tx, in, in.ArtifactID)
		if err != nil {
			return err
		}
		if !in.AllowBound && publicationHasIRBinding(content.Value) {
			return publicationError("PUBLICATION_ALREADY_BOUND")
		}
		op := DocumentPublicationOperation{ID: "pub_" + common.GenerateID(), OwnerUserID: in.OwnerUserID, SessionID: in.SessionID, SlotID: in.SlotID, ItemIndex: publicationIndex(in.ListIndex), IdempotencyKey: in.IdempotencyKey, Status: "preparing", CandidateValue: in.CandidateValue, SourceRevisionID: revision.ID, SourceRevision: revision.Revision, SourceDraftVersion: draft, SourceSchema: content.Schema, SourceContentType: ct, SourceValue: content.Value, SourceHash: publicationHash(content.Schema, content.Value), RequestHash: requestHash, Provider: in.Provider, Title: in.Title, ParentURI: in.ParentURI, Template: in.Template, AllowBound: in.AllowBound, SharedTarget: in.SharedTarget, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		var binding orm.DocumentPublicationBinding
		err = publicationBindingQuery(tx, &op).First(&binding).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			if binding.OwnerUserID != in.OwnerUserID {
				return publicationError("PUBLICATION_BINDING_CONFLICT")
			}
			if binding.PendingOperationID != "" {
				return publicationError("PUBLICATION_IN_PROGRESS")
			}
			if len(binding.TargetDocument) > 0 && !in.AllowBound {
				return publicationError("PUBLICATION_ALREADY_BOUND")
			}
		}
		if err := tx.Create(&op).Error; err != nil {
			return err
		}
		if binding.ID == "" && op.AllowBound {
			target, remote, err := initialPublicationTarget(ctx, tx, &op, revision)
			if err != nil {
				return err
			}
			if len(target) > 0 {
				binding = orm.DocumentPublicationBinding{ID: "pbind_" + common.GenerateID(), SessionID: op.SessionID, SlotID: op.SlotID, ItemIndex: op.ItemIndex, OwnerUserID: op.OwnerUserID, Provider: targetProvider(target), TargetDocument: target, RemoteValue: remote}
				if err := tx.Create(&binding).Error; err != nil {
					return err
				}
			}
		}
		op.TargetDocument = binding.TargetDocument
		op.RemoteValue = binding.RemoteValue
		op.CandidateValue = in.CandidateValue
		if op.SharedTarget {
			mediaSlot := "resolved_media_assets"
			if op.SlotID == "flat_draft_document" {
				mediaSlot = "flat_resolved_media_assets"
			}
			var media orm.WorkflowSlotRevision
			err := tx.Where("session_id = ? AND slot_id = ? AND list_index IS NULL AND selected = ? AND validity = ?", op.SessionID, mediaSlot, true, "effective").First(&media).Error
			if err == nil {
				value, err := LoadSlotRevisionValue(ctx, tx, media)
				if err != nil {
					return err
				}
				value, err = document.ReadArtifactValue(value)
				if err != nil {
					return err
				}
				op.MediaAssets = document.ArtifactData(value)
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if err := tx.Model(&op).Updates(map[string]any{"target_document": op.TargetDocument, "remote_value": op.RemoteValue, "candidate_value": op.CandidateValue, "media_assets": op.MediaAssets}).Error; err != nil {
			return err
		}
		if binding.ID == "" {
			binding = orm.DocumentPublicationBinding{ID: "pbind_" + common.GenerateID(), SessionID: op.SessionID, SlotID: op.SlotID, ItemIndex: op.ItemIndex, OwnerUserID: op.OwnerUserID, PendingOperationID: op.ID}
			if err := tx.Create(&binding).Error; err != nil {
				return err
			}
		} else if err := tx.Model(&binding).Update("pending_operation_id", op.ID).Error; err != nil {
			return err
		}
		if op.SharedTarget {
			if err := reserveLegacyPublicationTarget(tx, &op); err != nil {
				return err
			}
			var shared orm.DocumentPublicationBinding
			if err := tx.Where("session_id = ? AND slot_id = ? AND item_index = ?", op.SessionID, "target_document", -1).First(&shared).Error; err != nil {
				return err
			}
			if len(shared.TargetDocument) > 0 {
				op.TargetDocument = shared.TargetDocument
				op.RemoteValue = shared.RemoteValue
				if err := tx.Model(&op).Updates(map[string]any{"target_document": op.TargetDocument, "remote_value": op.RemoteValue}).Error; err != nil {
					return err
				}
			}

		}
		created = true
		return tx.First(&result, "id = ?", op.ID).Error
	})
	if err != nil {
		return nil, false, err
	}
	return &result, created, nil
}
func reserveLegacyPublicationTarget(tx *gorm.DB, op *DocumentPublicationOperation) error {
	var binding orm.DocumentPublicationBinding
	err := tx.Where("session_id = ? AND slot_id = ? AND item_index = ?", op.SessionID, "target_document", -1).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&orm.DocumentPublicationBinding{ID: "pbind_" + common.GenerateID(), SessionID: op.SessionID, SlotID: "target_document", ItemIndex: -1, OwnerUserID: op.OwnerUserID, PendingOperationID: op.ID}).Error
	}
	if err != nil {
		return err
	}
	if binding.OwnerUserID != op.OwnerUserID {
		return publicationError("PUBLICATION_BINDING_CONFLICT")
	}
	if binding.PendingOperationID != "" {
		return publicationError("PUBLICATION_IN_PROGRESS")
	}
	return tx.Model(&binding).Update("pending_operation_id", op.ID).Error
}
func publicationLockedOperation(ctx context.Context, tx *gorm.DB, owner, id string) (*DocumentPublicationOperation, error) {
	var op DocumentPublicationOperation
	if err := tx.First(&op, "id = ? AND owner_user_id = ?", id, owner).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, publicationError("PUBLICATION_NOT_FOUND")
		}
		return nil, err
	}
	if _, err := publicationSession(ctx, tx, owner, op.SessionID); err != nil {
		return nil, err
	}
	if err := tx.First(&op, "id = ? AND owner_user_id = ?", id, owner).Error; err != nil {
		return nil, err
	}
	return &op, nil
}
func publicationReserved(tx *gorm.DB, op *DocumentPublicationOperation) error {
	var binding orm.DocumentPublicationBinding
	if err := publicationBindingQuery(tx, op).First(&binding).Error; err != nil {
		return publicationError("PUBLICATION_STATE_CONFLICT")
	}
	if binding.OwnerUserID != op.OwnerUserID || binding.PendingOperationID != op.ID {
		return publicationError("PUBLICATION_STATE_CONFLICT")
	}
	if op.SharedTarget {
		var shared orm.DocumentPublicationBinding
		if err := tx.Where("session_id = ? AND slot_id = ? AND item_index = ?", op.SessionID, "target_document", -1).First(&shared).Error; err != nil || shared.OwnerUserID != op.OwnerUserID || shared.PendingOperationID != op.ID {
			return publicationError("PUBLICATION_STATE_CONFLICT")
		}
	}
	return nil
}
func GetDocumentPublication(ctx context.Context, db *gorm.DB, owner, id string) (*DocumentPublicationOperation, error) {
	var result *DocumentPublicationOperation
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		var err error
		result, err = publicationLockedOperation(ctx, tx, owner, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func ClaimDocumentPublicationWrite(ctx context.Context, db *gorm.DB, owner, id string) error {
	return common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		op, err := publicationLockedOperation(ctx, tx, owner, id)
		if err != nil {
			return err
		}
		if op.Status != "preparing" {
			return publicationError("PUBLICATION_STATE_CONFLICT")
		}
		if err := publicationReserved(tx, op); err != nil {
			return err
		}
		if op.SharedTarget {
			var target orm.WorkflowSlotRevision
			err := tx.Where("session_id = ? AND slot_id = ? AND selected = ?", op.SessionID, "target_document", true).First(&target).Error
			if err == nil {
				if err := artifactgraph.CheckConsumers(ctx, tx, op.SessionID, target.ID); err != nil {
					return err
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		_, content, _, _, err := publicationSource(ctx, tx, publicationInput(op), op.SourceRevisionID)
		if err != nil {
			return err
		}
		if publicationHash(content.Schema, content.Value) != op.SourceHash {
			return ErrDraftVersionConflict
		}
		return tx.Model(op).Updates(map[string]any{"status": "write_started", "updated_at": time.Now().UTC()}).Error
	})
}
func finishPublicationBeforeWrite(ctx context.Context, db *gorm.DB, owner, id, status, errorCode string) error {
	return common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		op, err := publicationLockedOperation(ctx, tx, owner, id)
		if err != nil {
			return err
		}
		if op.Status == status {
			return nil
		}
		if op.Status != "preparing" {
			return publicationError("PUBLICATION_STATE_CONFLICT")
		}
		if err := publicationReserved(tx, op); err != nil {
			return err
		}
		var bindings []orm.DocumentPublicationBinding
		if err := tx.Where("pending_operation_id = ? AND owner_user_id = ?", op.ID, owner).Find(&bindings).Error; err != nil {
			return err
		}
		for _, binding := range bindings {
			if len(binding.TargetDocument) == 0 {
				if err := tx.Delete(&binding).Error; err != nil {
					return err
				}
			} else if err := tx.Model(&binding).Update("pending_operation_id", "").Error; err != nil {
				return err
			}
		}
		return tx.Model(op).Updates(map[string]any{"status": status, "error_code": errorCode, "updated_at": time.Now().UTC()}).Error
	})
}
func CancelDocumentPublication(ctx context.Context, db *gorm.DB, owner, id string) error {
	return finishPublicationBeforeWrite(ctx, db, owner, id, "canceled", "")
}
func FailDocumentPublicationBeforeWrite(ctx context.Context, db *gorm.DB, owner, id string) error {
	return finishPublicationBeforeWrite(ctx, db, owner, id, "failed_no_write", "")
}
func MarkDocumentPublicationUnknown(ctx context.Context, db *gorm.DB, owner, id string) error {
	return common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		op, err := publicationLockedOperation(ctx, tx, owner, id)
		if err != nil {
			return err
		}
		if op.Status == "outcome_unknown" {
			return nil
		}
		if op.Status != "write_started" {
			return publicationError("PUBLICATION_STATE_CONFLICT")
		}
		if err := publicationReserved(tx, op); err != nil {
			return err
		}
		return tx.Model(op).Updates(map[string]any{"status": "outcome_unknown", "updated_at": time.Now().UTC()}).Error
	})
}
func ConfirmDocumentPublication(ctx context.Context, db *gorm.DB, owner, id string, receipt DocumentPublicationReceipt) error {
	return common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		op, err := publicationLockedOperation(ctx, tx, owner, id)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		if len(op.ReceiptJSON) > 0 {
			if string(op.ReceiptJSON) != string(raw) {
				return publicationError("PUBLICATION_RECEIPT_CONFLICT")
			}
			return nil
		}
		if op.Status != "write_started" && op.Status != "outcome_unknown" {
			return publicationError("PUBLICATION_STATE_CONFLICT")
		}
		if err := publicationReserved(tx, op); err != nil {
			return err
		}
		if receipt.Provider != op.Provider || receipt.ContentType == "" || !json.Valid(receipt.Value) || !json.Valid(receipt.TargetDocument) || string(receipt.TargetDocument) == "null" {
			return publicationError("DOCUMENT_ACTION_RESULT_INVALID")
		}
		return tx.Model(op).Updates(map[string]any{"receipt_json": json.RawMessage(raw), "status": "provider_confirmed", "updated_at": time.Now().UTC()}).Error
	})
}
func FinalizeDocumentPublication(ctx context.Context, db *gorm.DB, owner, id string) (*orm.WorkflowSlotRevision, error) {
	var result *orm.WorkflowSlotRevision
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		result = nil
		op, err := publicationLockedOperation(ctx, tx, owner, id)
		if err != nil {
			return err
		}
		if op.Status == "succeeded" {
			var revision orm.WorkflowSlotRevision
			if err := tx.First(&revision, "id = ?", op.ResultRevisionID).Error; err != nil {
				return err
			}
			result = &revision
			return nil
		}
		if op.Status != "provider_confirmed" && op.Status != "local_conflict" && op.Status != "local_persist_failed" {
			return publicationError("PUBLICATION_STATE_CONFLICT")
		}
		if err := publicationReserved(tx, op); err != nil {
			return err
		}
		current, content, _, _, err := publicationSource(ctx, tx, publicationInput(op), op.SourceRevisionID)
		if err != nil {
			return err
		}
		if publicationHash(content.Schema, content.Value) != op.SourceHash {
			return ErrDraftVersionConflict
		}
		var receipt DocumentPublicationReceipt
		if json.Unmarshal(op.ReceiptJSON, &receipt) != nil || len(receipt.Value) == 0 {
			return publicationError("DOCUMENT_ACTION_RESULT_INVALID")
		}
		cardinality := "single"
		if op.ItemIndex >= 0 {
			cardinality = "list"
		}
		revision := &current
		if !receipt.NoLocalChange {
			revision, err = WriteSlotRevisionWithHumanArtifact(ctx, tx, op.SessionID, op.SlotID, current.Slot, current.StepID, current.Attempt, cardinality, publicationPointer(op.ItemIndex), receipt.ContentType, receipt.Value, nil, "provider_sync", &op.SourceRevision, publicationInput(op).BaseDraftVersion)
			if err != nil {
				return err
			}
		}

		var binding orm.DocumentPublicationBinding
		if err := publicationBindingQuery(tx, op).First(&binding).Error; err != nil {
			return err
		}
		if err := tx.Model(&binding).Updates(map[string]any{"pending_operation_id": "", "provider": receipt.Provider, "target_document": receipt.TargetDocument, "remote_value": receipt.Value, "source_revision_id": op.SourceRevisionID, "result_revision_id": revision.ID}).Error; err != nil {
			return err
		}
		if op.SharedTarget {
			if !receipt.NoLocalChange {
				envelope, _ := json.Marshal(map[string]any{"schema": "lazyllm.tools.writer.data_models.task.TargetDocument", "data": receipt.TargetDocument})
				// GitHub target sync preserves all completed consumers; other providers
				// preserve the published draft producer while invalidating other consumers.
				preserveConsumers := op.Provider == "github"
				if _, err := writeSlotRevisionWithHumanArtifact(ctx, tx, op.SessionID, "target_document", "target_document", current.StepID, current.Attempt, "single", nil, "json", envelope, nil, "provider_sync", nil, nil, preserveConsumers, &current); err != nil {
					return err
				}
			}
			if err := tx.Model(&orm.DocumentPublicationBinding{}).Where("session_id = ? AND slot_id = ? AND pending_operation_id = ?", op.SessionID, "target_document", op.ID).Updates(map[string]any{"pending_operation_id": "", "provider": receipt.Provider, "target_document": receipt.TargetDocument, "remote_value": receipt.Value}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(op).Updates(map[string]any{"status": "succeeded", "result_revision_id": revision.ID, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		result = revision
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
