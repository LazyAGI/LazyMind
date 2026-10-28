package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
	workflowstore "lazymind/core/workflow/store"
)

type DocumentPublicationTargetInput struct {
	Provider         string `json:"provider" required:"true"`
	ConnectionID     string `json:"connection_id,omitempty"`
	Title            string `json:"title,omitempty"`
	ParentURI        string `json:"parent_uri,omitempty"`
	RetryOperationID string `json:"retry_operation_id,omitempty"`
}
type DocumentPublicationGroupRequest struct {
	BaseRevision     int                              `json:"base_revision" required:"true"`
	BaseDraftVersion *int64                           `json:"base_draft_version,omitempty"`
	IdempotencyKey   string                           `json:"idempotency_key" required:"true"`
	Targets          []DocumentPublicationTargetInput `json:"targets" required:"true"`
}
type DocumentPublicationTargetState struct {
	Provider        string                     `json:"provider" required:"true"`
	ConnectionID    string                     `json:"connection_id,omitempty"`
	Bound           bool                       `json:"bound" required:"true"`
	TargetURL       string                     `json:"target_url,omitempty"`
	LastSuccessHash string                     `json:"last_success_hash,omitempty"`
	LastSuccessAt   *time.Time                 `json:"last_success_at,omitempty"`
	Operation       *DocumentPublicationStatus `json:"operation,omitempty"`
	OperationHash   string                     `json:"operation_hash,omitempty"`
	Title           string                     `json:"title,omitempty"`
	ParentURI       string                     `json:"parent_uri,omitempty"`
}
type DocumentPublicationGroupState struct {
	PublicationID      string                           `json:"publication_id,omitempty"`
	CurrentHash        string                           `json:"current_hash" required:"true"`
	PreferredProviders []string                         `json:"preferred_providers" required:"true"`
	Targets            []DocumentPublicationTargetState `json:"targets" required:"true"`
}

func validPublicationTarget(input DocumentPublicationTargetInput) bool {
	if len(input.ConnectionID) > 255 || len(input.Title) > 512 || len(input.ParentURI) > 2048 || len(input.RetryOperationID) > 64 {
		return false
	}
	switch input.Provider {
	case "github", "feishu", "notion", "googledrive", "wechat":
		if strings.TrimSpace(input.ConnectionID) == "" {
			return false
		}
	case "obsidian":
		if input.ConnectionID != "" {
			return false
		}
	default:
		return false
	}
	if input.ParentURI != "" {
		u, err := url.Parse(input.ParentURI)
		if err != nil || u.User != nil || u.Scheme != "https" || u.Hostname() == "" || u.Port() != "" {
			return false
		}
		h := strings.ToLower(u.Hostname())
		switch input.Provider {
		case "github":
			return h == "github.com"
		case "feishu":
			return strings.HasSuffix(h, ".feishu.cn") || strings.HasSuffix(h, ".larksuite.com")
		case "notion":
			return h == "notion.so" || strings.HasSuffix(h, ".notion.so") || strings.HasSuffix(h, ".notion.site")
		case "googledrive":
			return h == "drive.google.com" || h == "docs.google.com"
		default:
			return false
		}
	}
	return true
}

// One transaction freezes the same saved source for every selected target and
// queues durable work. There is no publication history/snapshot table.
func CreateDocumentPublicationGroup(ctx context.Context, db *gorm.DB, owner, artifactID string, request DocumentPublicationGroupRequest) (string, error) {
	if request.BaseRevision < 1 || strings.TrimSpace(request.IdempotencyKey) == "" || len(request.IdempotencyKey) > 128 || len(request.Targets) == 0 || len(request.Targets) > 6 {
		return "", publicationError("DOCUMENT_ACTION_INVALID")
	}
	seen := map[string]bool{}
	for _, target := range request.Targets {
		if !validPublicationTarget(target) || seen[target.Provider] {
			return "", publicationError("DOCUMENT_ACTION_INVALID")
		}
		seen[target.Provider] = true
	}
	raw, _ := json.Marshal(struct {
		ArtifactID string
		Request    DocumentPublicationGroupRequest
	}{artifactID, request})
	fingerprint := publicationHash("publication-confirmation-v1", raw)
	groupID := publicationHash(owner, json.RawMessage(request.IdempotencyKey))
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		var revision orm.WorkflowSlotRevision
		if err := tx.First(&revision, "id = ?", artifactID).Error; err != nil {
			return publicationError("PUBLICATION_NOT_FOUND")
		}
		session, err := publicationSession(ctx, tx, owner, revision.SessionID)
		if err != nil {
			return err
		}
		if !workflowstore.ArtifactPublicationAllowed(session.WorkflowID, revision.SlotID, revision.ListIndex) {
			return documentFailure("DOCUMENT_ACTION_UNSUPPORTED", 422)
		}
		var previous []DocumentPublicationOperation
		if err := tx.Where("owner_user_id = ? AND publication_id = ?", owner, groupID).Find(&previous).Error; err != nil {
			return err
		}
		if len(previous) > 0 {
			if len(previous) != len(request.Targets) {
				return publicationError("PUBLICATION_IDEMPOTENCY_CONFLICT")
			}
			for _, op := range previous {
				if op.RequestHash != fingerprint {
					return publicationError("PUBLICATION_IDEMPOTENCY_CONFLICT")
				}
			}
			return nil
		}
		for _, target := range request.Targets {
			input := DocumentPublicationInput{PublicationID: groupID, ConnectionID: target.ConnectionID, ArtifactID: artifactID, OwnerUserID: owner, SessionID: session.ID, SlotID: revision.SlotID, ListIndex: revision.ListIndex, BaseRevision: request.BaseRevision, BaseDraftVersion: request.BaseDraftVersion, IdempotencyKey: groupID + ":" + target.Provider, Provider: target.Provider, Title: target.Title, ParentURI: target.ParentURI, AllowBound: true}
			op, _, err := prepareDocumentPublication(ctx, tx, input)
			if err != nil {
				return err
			}
			if target.RetryOperationID != "" {
				var failed DocumentPublicationOperation
				if err := tx.First(&failed, "id = ? AND owner_user_id = ?", target.RetryOperationID, owner).Error; err != nil {
					return publicationError("PUBLICATION_NOT_FOUND")
				}
				if failed.PublicationID == "" || failed.SessionID != op.SessionID || failed.SlotID != op.SlotID || failed.ItemIndex != op.ItemIndex || failed.Provider != op.Provider || failed.Status != "failed_no_write" {
					return publicationError("PUBLICATION_STATE_CONFLICT")
				}
				if failed.SourceHash != op.SourceHash {
					return ErrDraftVersionConflict
				}
			}
			// The writer already derives the title from the fixed source and supports
			// default destinations for cloud documents. GitHub still requires a
			// repository; Obsidian must not create in an arbitrary discovered vault.
			if len(op.TargetDocument) == 0 && (target.Provider == "obsidian" || target.Provider == "github" && strings.TrimSpace(target.ParentURI) == "") {
				return documentFailure("DOCUMENT_ACTION_UNSUPPORTED", 422)
			}
			if err := tx.Model(op).Update("request_hash", fingerprint).Error; err != nil {
				return err
			}
			var binding orm.DocumentPublicationBinding
			if err := publicationBindingQuery(tx, op).First(&binding).Error; err != nil {
				return err
			}
			if binding.LastSuccessHash == op.SourceHash && len(binding.TargetDocument) > 0 {
				receipt, _ := json.Marshal(DocumentPublicationReceipt{Provider: op.Provider, TargetDocument: binding.TargetDocument, NoLocalChange: true})
				values := map[string]any{"status": "succeeded", "receipt_json": json.RawMessage(receipt)}
				clearPublicationPayload(values)
				if err := tx.Model(op).Updates(values).Error; err != nil {
					return err
				}
				if err := tx.Model(&binding).Update("pending_operation_id", "").Error; err != nil {
					return err
				}
				continue
			}
			if _, err := asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{JobType: documentPublicationJobType, ResourceType: "document_publication", ResourceID: op.ID, IdempotencyKey: op.ID, CreateUserID: owner, MaxAttempts: 3}); err != nil {
				return err
			}
		}
		return nil
	})
	return groupID, err
}

func ReadDocumentPublicationGroup(ctx context.Context, db *gorm.DB, owner, artifactID string) (*DocumentPublicationGroupState, error) {
	result := &DocumentPublicationGroupState{PreferredProviders: []string{}, Targets: []DocumentPublicationTargetState{}}
	var wechatOperation *DocumentPublicationOperation
	var wechatLastSuccess *DocumentPublicationOperation
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		wechatOperation, wechatLastSuccess = nil, nil
		var revision orm.WorkflowSlotRevision
		if err := tx.First(&revision, "id = ?", artifactID).Error; err != nil {
			return publicationError("PUBLICATION_NOT_FOUND")
		}
		if _, err := publicationSession(ctx, tx, owner, revision.SessionID); err != nil {
			return err
		}
		var selected orm.WorkflowSlotRevision
		q := tx.Where("session_id = ? AND slot_id = ? AND selected = ?", revision.SessionID, revision.SlotID, true)
		if revision.ListIndex == nil {
			q = q.Where("list_index IS NULL")
		} else {
			q = q.Where("list_index = ?", *revision.ListIndex)
		}
		if err := q.First(&selected).Error; err != nil {
			return err
		}
		var draft *int64
		if selected.HumanArtifactID != nil {
			var human orm.WorkflowHumanArtifact
			if err := tx.First(&human, "id = ?", *selected.HumanArtifactID).Error; err != nil {
				return err
			}
			draft = &human.DraftVersion
		}
		_, content, _, _, err := publicationSource(ctx, tx, DocumentPublicationInput{SessionID: selected.SessionID, SlotID: selected.SlotID, ListIndex: selected.ListIndex, BaseRevision: selected.Revision, BaseDraftVersion: draft}, selected.ID)
		if err != nil {
			return err
		}
		result.CurrentHash = publicationHash(content.Schema, content.Value)
		var latest DocumentPublicationOperation
		err = tx.Where("owner_user_id = ? AND publication_id <> ''", owner).Order("created_at DESC, id DESC").First(&latest).Error
		if err == nil {
			if err := tx.Model(&DocumentPublicationOperation{}).Where("owner_user_id = ? AND publication_id = ?", owner, latest.PublicationID).Order("provider").Pluck("provider", &result.PreferredProviders).Error; err != nil {
				return err
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var bindings []orm.DocumentPublicationBinding
		if err := tx.Where("session_id = ? AND slot_id = ? AND item_index = ? AND owner_user_id = ?", selected.SessionID, selected.SlotID, publicationIndex(selected.ListIndex), owner).Find(&bindings).Error; err != nil {
			return err
		}
		byProvider := map[string]orm.DocumentPublicationBinding{}
		for _, b := range bindings {
			if b.BindingScope != "" {
				byProvider[b.BindingScope] = b
			}
		}
		for _, b := range bindings {
			if _, exists := byProvider[b.Provider]; b.BindingScope == "" && b.Provider != "" && !exists {
				byProvider[b.Provider] = b
			}
		}
		// Expose a pre-existing Writer target before the first independent publication.
		inherited, _, err := initialPublicationTarget(ctx, tx, &DocumentPublicationOperation{PublicationID: "lookup", SessionID: selected.SessionID, SlotID: selected.SlotID, ItemIndex: publicationIndex(selected.ListIndex), SourceRevision: selected.Revision}, selected)
		if err != nil {
			return err
		}
		if provider := targetProvider(inherited); provider != "" {
			if _, exists := byProvider[provider]; !exists {
				byProvider[provider] = orm.DocumentPublicationBinding{Provider: provider, TargetDocument: inherited}
			}
		}
		latestByProvider := map[string]DocumentPublicationOperation{}
		for _, provider := range []string{"github", "feishu", "notion", "googledrive", "wechat", "obsidian"} {
			var op DocumentPublicationOperation
			err := tx.Where("session_id = ? AND slot_id = ? AND item_index = ? AND owner_user_id = ? AND publication_id <> '' AND provider = ?", selected.SessionID, selected.SlotID, publicationIndex(selected.ListIndex), owner, provider).Order("created_at DESC, id DESC").First(&op).Error
			if err == nil {
				latestByProvider[provider] = op
				if provider == "wechat" {
					copy := op
					wechatOperation = &copy
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		var documentLatest DocumentPublicationOperation
		for _, op := range latestByProvider {
			if documentLatest.ID == "" || op.CreatedAt.After(documentLatest.CreatedAt) {
				documentLatest = op
			}
		}
		result.PublicationID = documentLatest.PublicationID
		if binding := byProvider["wechat"]; binding.LastOperationID != "" && (wechatOperation == nil || binding.LastOperationID != wechatOperation.ID) {
			var success DocumentPublicationOperation
			err := tx.Where("id = ? AND owner_user_id = ? AND session_id = ? AND slot_id = ? AND item_index = ? AND provider = ? AND status = ?",
				binding.LastOperationID, owner, selected.SessionID, selected.SlotID, publicationIndex(selected.ListIndex), "wechat", "succeeded").First(&success).Error
			if err == nil {
				wechatLastSuccess = &success
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		for _, provider := range []string{"github", "feishu", "notion", "googledrive", "wechat", "obsidian"} {
			binding := byProvider[provider]
			state := DocumentPublicationTargetState{Provider: provider, ConnectionID: binding.ConnectionID, Bound: len(binding.TargetDocument) > 0, TargetURL: documentPublicationTargetURL(provider, binding.TargetDocument), LastSuccessHash: binding.LastSuccessHash, LastSuccessAt: binding.LastSuccessAt}
			if op, ok := latestByProvider[provider]; ok {
				status := publicationStatus(&op, time.Now())
				state.Operation = &status
				state.OperationHash = op.SourceHash
				state.Title = op.Title
				state.ParentURI = op.ParentURI
				if state.ConnectionID == "" {
					state.ConnectionID = op.ConnectionID
				}
			}
			result.Targets = append(result.Targets, state)
		}
		return nil
	})
	if err == nil && (wechatOperation != nil || wechatLastSuccess != nil) {
		var status *DocumentPublicationStatus
		targetURL := ""
		if wechatOperation != nil {
			latest := publicationStatusForRead(ctx, wechatOperation)
			status, targetURL = &latest, latest.TargetURL
		}
		// The latest attempt owns the displayed status; the last successful
		// binding still owns an article when that attempt has no write receipt.
		if targetURL == "" && wechatLastSuccess != nil {
			targetURL = publicationStatusForRead(ctx, wechatLastSuccess).TargetURL
		}
		for i := range result.Targets {
			if result.Targets[i].Provider == "wechat" {
				result.Targets[i].Operation = status
				result.Targets[i].TargetURL = targetURL
			}
		}
	}
	return result, err
}

func ReadDocumentPublicationGroupHTTP(w http.ResponseWriter, r *http.Request) {
	owner, ok := publicationHTTPIdentity(w, r)
	if !ok {
		return
	}
	result, err := ReadDocumentPublicationGroup(r.Context(), corestore.DB(), owner, common.PathVar(r, "artifact_id"))
	if err != nil {
		replyPublicationResult(w, nil, nil, publicationFailure(err))
		return
	}
	common.ReplyOK(w, result)
}
func CreateDocumentPublicationGroupHTTP(w http.ResponseWriter, r *http.Request) {
	owner, ok := publicationHTTPIdentity(w, r)
	if !ok {
		return
	}
	var request DocumentPublicationGroupRequest
	if decodeDocumentJSON(r.Body, &request) != nil {
		replyPublicationResult(w, nil, nil, documentFailure("DOCUMENT_ACTION_INVALID", 400))
		return
	}
	id := common.PathVar(r, "artifact_id")
	_, err := CreateDocumentPublicationGroup(r.Context(), corestore.DB(), owner, id, request)
	if err != nil {
		replyPublicationResult(w, nil, nil, publicationFailure(err))
		return
	}
	ReadDocumentPublicationGroupHTTP(w, r)
}

const documentPublicationJobType = "document.publication"

func RegisterDocumentPublicationJob() {
	asyncjob.Register(documentPublicationJobType, func(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
		return runDocumentPublicationJob(ctx, corestore.DB(), job.CreateUserID, job.ResourceID)
	})
}
func runDocumentPublicationJob(ctx context.Context, db *gorm.DB, owner, id string) (asyncjob.Result, error) {
	op, err := GetDocumentPublication(ctx, db, owner, id)
	if err != nil {
		return asyncjob.Result{}, err
	}
	switch op.Status {
	case "preparing":
		session, err := GetSession(ctx, db, op.SessionID)
		if err != nil {
			return asyncjob.Result{}, err
		}
		_, _, err = executeDocumentPublication(ctx, db, owner, op, session, &DocumentPublicationOptions{})
		if err != nil {
			current, readErr := GetDocumentPublication(ctx, db, owner, id)
			if readErr != nil {
				return asyncjob.Result{}, readErr
			}
			if current.Status == "preparing" || current.Status == "provider_confirmed" {
				return asyncjob.Result{}, err
			}
		}
	case "provider_confirmed", "local_persist_failed", "local_conflict":
		if _, err := FinalizeDocumentPublication(ctx, db, owner, id); err != nil {
			return asyncjob.Result{}, err
		}
	case "write_started":
		// A restarted worker cannot know whether the previous write completed.
		if err := MarkDocumentPublicationUnknown(ctx, db, owner, id); err != nil {
			return asyncjob.Result{}, err
		}
	}
	return asyncjob.Result{}, nil
}
