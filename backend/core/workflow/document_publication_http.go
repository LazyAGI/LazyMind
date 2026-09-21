package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	corestore "lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/workflow/document"
	workflowstore "lazymind/core/workflow/store"
)

type DocumentPublishInput struct {
	Provider       string `json:"provider" required:"true"`
	Mode           string `json:"mode,omitempty" enum:"replace"`
	IdempotencyKey string `json:"idempotency_key" required:"true"`
	Title          string `json:"title,omitempty"`
	ParentURI      string `json:"parent_uri,omitempty"`
	Template       string `json:"template,omitempty"`
}
type DocumentPublishRequest struct {
	Action           string                `json:"action" enum:"publish_document" required:"true"`
	BaseRevision     *int                  `json:"base_revision" required:"true"`
	BaseDraftVersion *int64                `json:"base_draft_version,omitempty"`
	Input            *DocumentPublishInput `json:"input" required:"true"`
}
type DocumentPublishResult struct {
	OperationID    string          `json:"operation_id" required:"true"`
	ArtifactID     string          `json:"artifact_id"`
	Revision       int             `json:"revision"`
	DraftVersion   int64           `json:"draft_version"`
	Provider       string          `json:"provider" required:"true"`
	ProviderSynced bool            `json:"provider_synced" required:"true"`
	ArtifactSaved  bool            `json:"artifact_saved" required:"true"`
	Status         string          `json:"status"`
	Representation string          `json:"representation"`
	Document       json.RawMessage `json:"document"`
	PatchResult    map[string]any  `json:"patch_result"`
	TargetDocument json.RawMessage `json:"target_document,omitempty"`
}
type DocumentPublicationStatus struct {
	OperationID    string     `json:"operation_id" required:"true"`
	Status         string     `json:"status" required:"true"`
	Provider       string     `json:"provider" required:"true"`
	ArtifactID     string     `json:"artifact_id,omitempty"`
	ErrorCode      string     `json:"error_code,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at" required:"true"`
	RecoveryAfter  *time.Time `json:"recovery_after,omitempty"`
	ProviderSynced bool       `json:"provider_synced" required:"true"`
	TargetURL      string     `json:"target_url,omitempty"`
	Actions        []string   `json:"actions" required:"true"`
	SourceSlotID   string     `json:"source_slot_id" required:"true"`
	ItemIndex      int        `json:"item_index" required:"true"`
}

func publicationBindingTarget(raw json.RawMessage) json.RawMessage {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	if data := value["data"]; len(data) > 0 {
		return publicationBindingTarget(data)
	}
	var binding map[string]any
	if json.Unmarshal(value["provider_binding"], &binding) != nil || len(binding) == 0 {
		return nil
	}
	target := map[string]any{"adapter": binding["provider"], "doc_id": binding["document_id"], "uri": binding["uri"]}
	if title := value["title"]; len(title) > 0 {
		target["title"] = title
	}
	meta := map[string]any{}
	for _, key := range []string{"article_index", "thumb_media_id", "browser_url"} {
		if binding[key] != nil {
			meta[key] = binding[key]
		}
	}
	if len(meta) > 0 {
		target["meta"] = meta
	}
	if binding["document_id"] == nil && binding["uri"] == nil {
		var metadata map[string]json.RawMessage
		if json.Unmarshal(value["metadata"], &metadata) == nil {
			return metadata["source"]
		}
		return nil
	}
	encoded, _ := json.Marshal(target)
	return encoded
}
func targetProvider(raw json.RawMessage) string {
	var target struct {
		Adapter string `json:"adapter"`
	}
	_ = json.Unmarshal(raw, &target)
	return target.Adapter
}
func initialPublicationTarget(ctx context.Context, tx *gorm.DB, op *DocumentPublicationOperation, current orm.WorkflowSlotRevision) (json.RawMessage, json.RawMessage, error) {
	raw, err := LoadSlotRevisionValue(ctx, tx, current)
	if err != nil {
		return nil, nil, err
	}
	raw, err = document.ReadArtifactValue(raw)
	if err != nil {
		return nil, nil, err
	}
	var envelope struct {
		Meta struct {
			Sync struct {
				TargetDocument json.RawMessage `json:"target_document"`
			} `json:"lazymind_provider_sync"`
		} `json:"meta"`
	}
	_ = json.Unmarshal(raw, &envelope)
	target := envelope.Meta.Sync.TargetDocument
	if len(target) == 0 && op.SharedTarget {
		var legacy orm.WorkflowSlotRevision
		err := tx.Where("session_id = ? AND slot_id = ? AND selected = ? AND validity = ?", op.SessionID, "target_document", true, "effective").First(&legacy).Error
		if err == nil {
			value, err := LoadSlotRevisionValue(ctx, tx, legacy)
			if err != nil {
				return nil, nil, err
			}
			value, err = document.ReadArtifactValue(value)
			if err != nil {
				return nil, nil, err
			}
			target = document.ArtifactData(value)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, err
		}
	}
	if len(target) == 0 {
		target = publicationBindingTarget(raw)
	}
	if len(target) == 0 {
		return nil, nil, nil
	}
	var baseline json.RawMessage
	if current.ChangeSource == "provider_sync" || current.ChangeSource == "ai" || current.ChangeSource == "host" || len(op.CandidateValue) > 0 {
		baseline = raw
	}
	var prior orm.WorkflowSlotRevision
	q := tx.Where("session_id = ? AND slot_id = ? AND change_source = ? AND revision <= ?", op.SessionID, op.SlotID, "provider_sync", op.SourceRevision)
	if op.ItemIndex < 0 {
		q = q.Where("list_index IS NULL")
	} else {
		q = q.Where("list_index = ?", op.ItemIndex)
	}
	if err := q.Order("revision DESC").First(&prior).Error; err == nil {
		baseline, err = LoadSlotRevisionValue(ctx, tx, prior)
		if err == nil {
			baseline, err = document.ReadArtifactValue(baseline)
		}
		if err != nil {
			return nil, nil, err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}
	if prior.ID == "" {
		var session orm.WorkflowSession
		if err := tx.First(&session, "id = ?", op.SessionID).Error; err != nil {
			return nil, nil, err
		}
		if session.WorkflowID == "writer-workflow" {
			var source orm.WorkflowSlotRevision
			err := tx.Where("session_id = ? AND slot_id = ? AND selected = ? AND validity = ?", op.SessionID, "source_document", true, "effective").First(&source).Error
			if err == nil {
				sourceValue, err := LoadSlotRevisionValue(ctx, tx, source)
				if err != nil {
					return nil, nil, err
				}
				sourceValue, err = document.ReadArtifactValue(sourceValue)
				if err != nil {
					return nil, nil, err
				}
				if targetProvider(publicationBindingTarget(sourceValue)) == targetProvider(target) {
					baseline = sourceValue
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil, err
			}
		}
	}
	return target, baseline, nil
}
func publicationFailure(err error) documentActionFailure {
	var failure documentActionFailure
	if errors.As(err, &failure) {
		return failure
	}
	switch {
	case errors.Is(err, ErrDraftVersionRequired):
		return documentActionFailure{"DRAFT_VERSION_REQUIRED", 400}
	case errors.Is(err, ErrDraftVersionConflict):
		return documentActionFailure{"DRAFT_VERSION_CONFLICT", 409}
	case errors.Is(err, ErrConflict), errors.Is(err, gorm.ErrRecordNotFound):
		return documentActionFailure{"REVISION_CONFLICT", 409}
	case errors.Is(err, ErrArtifactInUse):
		return documentActionFailure{"ARTIFACT_IN_USE", 409}
	}
	switch err.Error() {
	case "PUBLICATION_NOT_FOUND":
		return documentActionFailure{err.Error(), 404}
	case "DOCUMENT_ACTION_INVALID":
		return documentActionFailure{err.Error(), 400}
	case "DOCUMENT_INVALID":
		return documentActionFailure{"DOCUMENT_ACTION_INVALID", 400}
	case "PUBLICATION_STATE_CONFLICT", "PUBLICATION_IN_PROGRESS", "PUBLICATION_RECOVERY_CLOSED", "PUBLICATION_ALREADY_BOUND", "PUBLICATION_IDEMPOTENCY_CONFLICT", "PUBLICATION_RECEIPT_CONFLICT", "PUBLICATION_BINDING_CONFLICT", "SESSION_NOT_EDITABLE":
		return documentActionFailure{err.Error(), 409}
	default:
		return documentActionFailure{"DOCUMENT_ACTION_FAILED", 500}
	}
}
func runDocumentPublication(w http.ResponseWriter, r *http.Request, owner string, raw []byte) {
	var body DocumentPublishRequest
	if decodeDocumentJSON(bytes.NewReader(raw), &body) != nil || body.Input == nil || body.BaseRevision == nil || *body.BaseRevision < 1 || strings.TrimSpace(body.Input.Provider) == "" || strings.TrimSpace(body.Input.IdempotencyKey) == "" || len(body.Input.IdempotencyKey) > 128 || body.Input.Mode != "" && body.Input.Mode != "replace" {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
		return
	}
	result, op, err := PublishDocumentArtifact(r.Context(), corestore.DB(), owner, common.PathVar(r, "artifact_id"), body, nil)
	replyPublicationResult(w, result, op, err)
}

// PublishDocumentArtifact is shared by the Artifact endpoint and legacy Writer
// adapters. candidate is only the legacy editor's validated unsaved document.
type DocumentPublicationOptions struct {
	Candidate                   json.RawMessage
	SkipUnchangedDraft          bool
	AllowLegacyProviderDocument bool
}

func PublishDocumentArtifact(ctx context.Context, db *gorm.DB, owner, id string, body DocumentPublishRequest, options *DocumentPublicationOptions) (*DocumentPublishResult, *DocumentPublicationOperation, error) {
	if options == nil {
		options = &DocumentPublicationOptions{}
	}
	var revision orm.WorkflowSlotRevision
	if err := db.WithContext(ctx).First(&revision, "id = ?", id).Error; err != nil {
		return nil, nil, documentFailure("ARTIFACT_NOT_FOUND", 404)
	}
	session, err := GetSession(ctx, db, revision.SessionID)
	if err != nil || session.CreateUserID != owner || strings.TrimSpace(owner) == "" || session.Dismissed {
		return nil, nil, documentFailure("ARTIFACT_NOT_FOUND", 404)
	}
	legacyProviderDocument := options.AllowLegacyProviderDocument && session.WorkflowID == "writer-workflow" &&
		revision.SlotID == "provider_document" && revision.ListIndex == nil
	if !workflowstore.ArtifactPublicationAllowed(session.WorkflowID, revision.SlotID, revision.ListIndex) && !legacyProviderDocument {
		return nil, nil, documentFailure("DOCUMENT_ACTION_UNSUPPORTED", 422)
	}
	shared := session.WorkflowID == "writer-workflow"
	in := DocumentPublicationInput{ArtifactID: id, OwnerUserID: owner, SessionID: session.ID, SlotID: revision.SlotID, ListIndex: revision.ListIndex, BaseRevision: *body.BaseRevision, BaseDraftVersion: body.BaseDraftVersion, Provider: body.Input.Provider, IdempotencyKey: body.Input.IdempotencyKey, Title: body.Input.Title, ParentURI: body.Input.ParentURI, Template: body.Input.Template, AllowBound: true, SharedTarget: shared, CandidateValue: options.Candidate}
	op, created, err := prepareDocumentPublication(ctx, db, in)
	if err != nil {
		if errors.Is(err, publicationError("PUBLICATION_IN_PROGRESS")) {
			if blocked, findErr := FindDocumentPublication(ctx, db, owner, id); findErr == nil {
				return nil, blocked, publicationFailure(err)
			}
		}
		return nil, nil, publicationFailure(err)
	}
	if !created || op.Status != "preparing" {
		if op.Status == "succeeded" {
			result, err := publicationSavedResult(ctx, db, op)
			return result, op, err
		}
		return nil, op, publicationReplayFailure(op)
	}
	// Only the transaction that created this operation may perform conversion.
	// Replays report durable state and never acquire another execution path.
	failBefore := func(failure error) (*DocumentPublishResult, *DocumentPublicationOperation, error) {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		code := publicationFailure(failure).code
		if finishPublicationBeforeWrite(clean, db, owner, op.ID, "failed_no_write", code) == nil {
			op.Status, op.ErrorCode = "failed_no_write", code
			return nil, op, failure
		}
		return publicationFailureState(clean, db, owner, op, failure)
	}
	catalog, err := algo.ListDocumentProviders(ctx)
	if err != nil || !validDocumentProviderCatalog(catalog) {
		return failBefore(documentFailure("DOCUMENT_PROVIDERS_UNAVAILABLE", 502))
	}
	caps := map[string]bool{}
	for _, provider := range catalog.Providers {
		if provider.ID == op.Provider {
			for _, cap := range provider.Capabilities {
				caps[cap] = true
			}
		}
	}
	target := op.TargetDocument
	sameProvider := len(target) > 0 && targetProvider(target) == op.Provider
	source := op.SourceValue
	if len(op.CandidateValue) > 0 {
		source = op.CandidateValue
	}
	var baseline *document.Content
	syncIR := false
	if sameProvider && op.SourceSchema == document.IRSchema {
		var problem *document.ProjectionError
		baseline, problem = document.ReadContent(op.RemoteValue, document.IRSchema, func() (bool, error) { return false, nil })
		if problem != nil || baseline == nil {
			return failBefore(documentFailure("PUBLICATION_BASELINE_REQUIRED", 409))
		}
		syncIR = publicationSyncCompatible(baseline.Value, source)
	}
	if syncIR {
		if !caps["patch"] {
			return failBefore(documentFailure("DOCUMENT_ACTION_UNSUPPORTED", 422))
		}
	} else if !caps["replace"] || !sameProvider && !caps["create"] {
		return failBefore(documentFailure("DOCUMENT_ACTION_UNSUPPORTED", 422))
	}
	if !sameProvider {
		target = nil
	}
	config, err := modelconfig.LoadWriterProviderToolConfig(ctx, op.Provider, owner)
	if err != nil {
		return failBefore(documentFailure("PROVIDER_CREDENTIALS_UNAVAILABLE", 502))
	}
	request := algo.DocumentActionInvokeRequest{Phase: "execute", ToolConfig: config, ArtifactStore: filepath.Join(subagent.WorkspaceRoot(), "document-publications", op.ID)}
	if syncIR {
		request.Reference = "builtin:document.sync_document.v1"
		args := map[string]any{"source_document": baseline.Value, "revised_document": source}
		if len(op.MediaAssets) > 0 {
			args["media_assets"] = op.MediaAssets
		}
		request.Arguments = args
	} else {
		artifact, _ := json.Marshal(map[string]any{"data": source})
		args := map[string]any{"provider": op.Provider}
		if len(op.MediaAssets) > 0 {
			args["media_assets"] = op.MediaAssets
		}
		if len(target) > 0 {
			args["target_document"] = target
		}
		if op.Template != "" {
			args["template"] = op.Template
		}
		converted, status, err := algo.InvokeDocumentAction(ctx, algo.DocumentActionInvokeRequest{Reference: documentConvertReference, Phase: "preview", Artifact: artifact, Arguments: args, ToolConfig: config})
		if err != nil {
			return failBefore(documentFailure("DOCUMENT_CONVERSION_FAILED", publicationFailure(documentUpstreamFailure(status, err)).status))
		}
		var value struct {
			Provider        string            `json:"provider"`
			Format          string            `json:"format"`
			Content         json.RawMessage   `json:"content"`
			SourceDocument  map[string]any    `json:"source_document"`
			MediaReferences map[string]string `json:"media_references"`
		}
		if json.Unmarshal(converted.Result, &value) != nil || value.Provider != op.Provider || value.Format == "" || len(value.Content) == 0 || string(value.Content) == "null" || value.SourceDocument == nil || value.MediaReferences == nil {
			return failBefore(documentFailure("DOCUMENT_CONVERSION_FAILED", 502))
		}
		args = map[string]any{"converted_document": converted.Result, "mode": "replace"}
		if len(op.MediaAssets) > 0 {
			args["media_assets"] = op.MediaAssets
		}
		if len(target) > 0 {
			args["target_document"] = target
		}
		if op.Title != "" {
			args["title"] = op.Title
		}
		if op.ParentURI != "" {
			args["parent_uri"] = op.ParentURI
		}
		request.Reference = "builtin:document.write_document.v1"
		request.Arguments = args
	}
	writeCtx, writeCancel := context.WithTimeout(ctx, algo.DocumentActionTimeout)
	defer writeCancel()
	if err := ClaimDocumentPublicationWrite(writeCtx, db, owner, op.ID); err != nil {
		return failBefore(publicationFailure(err))
	}
	response, _, err := algo.InvokeDocumentAction(writeCtx, request)
	clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err != nil {
		_ = MarkDocumentPublicationUnknown(clean, db, owner, op.ID)
		return publicationFailureState(clean, db, owner, op, documentFailure("PUBLICATION_OUTCOME_UNKNOWN", 502))
	}
	receipt, err := publicationReceiptFromResult(op, response.Result, target, syncIR)
	if err != nil {
		_ = MarkDocumentPublicationUnknown(clean, db, owner, op.ID)
		return publicationFailureState(clean, db, owner, op, err)
	}
	if syncIR && options.SkipUnchangedDraft {
		var flags struct {
			Changed bool `json:"changed"`
		}
		if json.Unmarshal(response.Result, &flags) == nil && !flags.Changed {
			receipt.NoLocalChange = true
		}
	}
	if err := ConfirmDocumentPublication(clean, db, owner, op.ID, receipt); err != nil {
		return publicationFailureState(clean, db, owner, op, documentFailure("PROVIDER_SYNC_LOCAL_PERSIST_FAILED", 500))
	}
	op.Status = "provider_confirmed"
	op.ReceiptJSON, _ = json.Marshal(receipt)
	saved, err := FinalizeDocumentPublication(clean, db, owner, op.ID)
	if err != nil {
		return publicationFailureState(clean, db, owner, op, publicationLocalFailure(err))
	}
	op.Status = "succeeded"
	op.ResultRevisionID = saved.ID
	result, err := publicationSavedResult(clean, db, op)
	return result, op, err
}
func publicationReceiptFromResult(op *DocumentPublicationOperation, raw, target json.RawMessage, syncIR bool) (DocumentPublicationReceipt, error) {
	var value struct {
		Success        bool            `json:"success"`
		ProviderSynced bool            `json:"provider_synced"`
		Provider       *string         `json:"provider"`
		Representation *string         `json:"representation"`
		Persisted      json.RawMessage `json:"persisted_document"`
		Target         json.RawMessage `json:"target_document"`
		Patch          struct {
			Success bool `json:"success"`
		} `json:"patch_result"`
	}
	invalid := func() (DocumentPublicationReceipt, error) {
		return DocumentPublicationReceipt{}, documentFailure("DOCUMENT_ACTION_RESULT_INVALID", 502)
	}
	if json.Unmarshal(raw, &value) != nil || !value.Success || !value.ProviderSynced || !value.Patch.Success || len(value.Persisted) == 0 || string(value.Persisted) == "null" {
		return invalid()
	}
	representation := "ir"
	if !syncIR {
		if value.Provider == nil || *value.Provider != op.Provider || value.Representation == nil {
			return invalid()
		}
		representation = *value.Representation
		target = value.Target
	} else if value.Provider != nil && *value.Provider != "" && *value.Provider != op.Provider {
		return invalid()
	}
	var binding map[string]any
	if json.Unmarshal(target, &binding) != nil || binding["adapter"] != op.Provider || (binding["doc_id"] == nil && binding["uri"] == nil) {
		return invalid()
	}
	schema := "text/markdown"
	if representation == "ir" {
		schema = document.IRSchema
	} else if representation != "markdown" {
		return invalid()
	}
	source, problem := document.ReadContent(value.Persisted, schema, func() (bool, error) { return false, nil })
	if problem != nil || source == nil {
		return invalid()
	}
	artifact, _ := json.Marshal(map[string]any{"schema": schema, "data": source.Value, "meta": map[string]any{"lazymind_provider_sync": map[string]any{"provider": op.Provider, "confirmed": true, "target_document": binding}}})
	return DocumentPublicationReceipt{Provider: op.Provider, TargetDocument: target, ContentType: "json", Value: artifact}, nil
}
func publicationLocalFailure(err error) error {
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrDraftVersionConflict) || errors.Is(err, ErrDraftVersionRequired) || errors.Is(err, ErrArtifactInUse) {
		return documentFailure("PROVIDER_SYNC_LOCAL_CONFLICT", 409)
	}
	return documentFailure("PROVIDER_SYNC_LOCAL_PERSIST_FAILED", 500)
}
func publicationReplayFailure(op *DocumentPublicationOperation) error {
	if op.Status == "failed_no_write" && op.ErrorCode != "" {
		return documentFailure(op.ErrorCode, 409)
	}
	switch op.Status {
	case "outcome_unknown_released", "confirmed_detached":
		return documentFailure("PUBLICATION_RECOVERY_CLOSED", 409)
	case "outcome_unknown", "write_started":
		return documentFailure("PUBLICATION_OUTCOME_UNKNOWN", 409)
	case "provider_confirmed", "local_conflict", "local_persist_failed":
		return documentFailure("PROVIDER_SYNC_LOCAL_CONFLICT", 409)
	default:
		return documentFailure("PUBLICATION_STATE_CONFLICT", 409)
	}
}
func publicationSavedResult(ctx context.Context, db *gorm.DB, op *DocumentPublicationOperation) (*DocumentPublishResult, error) {
	var revision orm.WorkflowSlotRevision
	if err := db.WithContext(ctx).First(&revision, "id = ?", op.ResultRevisionID).Error; err != nil {
		return nil, err
	}
	var receipt DocumentPublicationReceipt
	if json.Unmarshal(op.ReceiptJSON, &receipt) != nil {
		return nil, publicationError("DOCUMENT_ACTION_RESULT_INVALID")
	}
	var value struct {
		Data   json.RawMessage `json:"data"`
		Schema string          `json:"schema"`
	}
	if json.Unmarshal(receipt.Value, &value) != nil {
		return nil, publicationError("DOCUMENT_ACTION_RESULT_INVALID")
	}
	representation := "markdown"
	if value.Schema == document.IRSchema {
		representation = "ir"
	}
	draft := int64(0)
	if revision.HumanArtifactID != nil {
		var human orm.WorkflowHumanArtifact
		if err := db.WithContext(ctx).First(&human, "id = ?", *revision.HumanArtifactID).Error; err != nil {
			return nil, err
		}
		draft = human.DraftVersion
	}
	status := "synced"
	if receipt.NoLocalChange {
		status = "no_change"
	}
	return &DocumentPublishResult{OperationID: op.ID, ArtifactID: revision.ID, Revision: revision.Revision, DraftVersion: draft, Provider: op.Provider, ProviderSynced: true, ArtifactSaved: !receipt.NoLocalChange, Status: status, Representation: representation, Document: value.Data, PatchResult: map[string]any{"success": true}, TargetDocument: receipt.TargetDocument}, nil
}
func replyPublicationResult(w http.ResponseWriter, result *DocumentPublishResult, op *DocumentPublicationOperation, err error) {
	if err == nil {
		common.ReplyOK(w, result)
		return
	}
	failure := publicationFailure(err)
	data := map[string]any{"code": failure.code, "retryable": false}
	if op != nil {
		data["operation_id"] = op.ID
		data["provider"] = op.Provider
		data["provider_synced"] = len(op.ReceiptJSON) > 0
		data["artifact_saved"] = false
	}
	common.ReplyErrWithData(w, "workflow artifact action failed", data, failure.status)
}
func publicationHTTPIdentity(w http.ResponseWriter, r *http.Request) (string, bool) {
	owner := strings.TrimSpace(corestore.UserID(r))
	if owner == "" {
		replyDocumentFailure(w, documentFailure("IDENTITY_REQUIRED", 400))
		return "", false
	}
	if r.Header.Get("X-LazyMind-External-Ref") != "" {
		replyDocumentFailure(w, documentFailure("PERMISSION_DENIED", 403))
		return "", false
	}
	return owner, true
}
func ReadDocumentPublication(w http.ResponseWriter, r *http.Request) {
	owner, ok := publicationHTTPIdentity(w, r)
	if !ok {
		return
	}
	op, err := GetDocumentPublication(r.Context(), corestore.DB(), owner, common.PathVar(r, "operation_id"))
	if err != nil {
		replyPublicationResult(w, nil, nil, publicationFailure(err))
		return
	}
	common.ReplyOK(w, publicationStatusForRead(r.Context(), op))
}
func CancelDocumentPublicationHTTP(w http.ResponseWriter, r *http.Request) {
	owner, ok := publicationHTTPIdentity(w, r)
	if !ok {
		return
	}
	id := common.PathVar(r, "operation_id")
	if err := CancelDocumentPublication(r.Context(), corestore.DB(), owner, id); err != nil {
		replyPublicationResult(w, nil, nil, publicationFailure(err))
		return
	}
	ReadDocumentPublication(w, r)
}
func RetryDocumentPublicationLocal(w http.ResponseWriter, r *http.Request) {
	owner, ok := publicationHTTPIdentity(w, r)
	if !ok {
		return
	}
	db := corestore.DB()
	id := common.PathVar(r, "operation_id")
	op, err := GetDocumentPublication(r.Context(), db, owner, id)
	if err != nil {
		replyPublicationResult(w, nil, nil, publicationFailure(err))
		return
	}
	revision, err := FinalizeDocumentPublication(r.Context(), db, owner, id)
	if err != nil {
		replyPublicationResult(w, nil, op, publicationLocalFailure(err))
		return
	}
	op.ResultRevisionID = revision.ID
	result, err := publicationSavedResult(r.Context(), db, op)
	replyPublicationResult(w, result, op, err)
}

// ReplyDocumentPublication keeps legacy response adapters on the same safe
// success and partial-success contract as the Artifact endpoint.
func ReplyDocumentPublication(w http.ResponseWriter, result *DocumentPublishResult, op *DocumentPublicationOperation, err error) {
	replyPublicationResult(w, result, op, err)
}

// Sync requires these exact WriterDocument identity fields; historical revisions
// with another identity must replace the known target through Convert/Write.
func publicationSyncCompatible(base, revised json.RawMessage) bool {
	var a, b map[string]any
	if json.Unmarshal(base, &a) != nil || json.Unmarshal(revised, &b) != nil {
		return false
	}
	binding, ok := a["provider_binding"].(map[string]any)
	if !ok || len(binding) == 0 {
		return false
	}
	for _, doc := range []map[string]any{a, b} {
		if doc["stage"] == nil {
			doc["stage"] = "draft"
		}
	}
	for _, key := range []string{"document_id", "stage", "revision", "provider_binding"} {
		if !reflect.DeepEqual(a[key], b[key]) {
			return false
		}
	}
	return true
}
