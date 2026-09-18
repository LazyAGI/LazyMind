package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
	"lazymind/core/workflow"

	"gorm.io/gorm"
)

type writerDocumentSyncBody struct {
	BaseRevision     int             `json:"base_revision"`
	BaseDraftVersion *int64          `json:"base_draft_version"`
	SourceDocument   json.RawMessage `json:"source_document"`
	RevisedDocument  json.RawMessage `json:"revised_document"`
	// Mode controls versioning: "draft" updates the selected human artifact in
	// place when possible; "checkpoint" (default) always creates a new revision.
	Mode string `json:"mode"`
}

type writerDocumentWriteBackBody struct {
	BaseRevision     int    `json:"base_revision"`
	BaseDraftVersion *int64 `json:"base_draft_version"`
	Slot             string `json:"slot"`
	Provider         string `json:"provider"`
	Template         string `json:"template"`
	// Legacy client fields remain accepted, but the selected server-side
	// revision and synchronized baseline are authoritative.
	SourceDocument  json.RawMessage `json:"source_document"`
	RevisedDocument json.RawMessage `json:"revised_document"`
}

type writerDocumentSaveBody struct {
	BaseRevision     int             `json:"base_revision"`
	BaseDraftVersion *int64          `json:"base_draft_version"`
	Document         json.RawMessage `json:"document"`
	Slot             string          `json:"slot"`
	NumberingUpdate  json.RawMessage `json:"numbering_update"`
	// Mode controls versioning: "draft" updates the selected human artifact in
	// place when possible; "checkpoint" (default) creates a new revision.
	Mode string `json:"mode"`
}

type writerDocumentRenderBody struct {
	Slot string `json:"slot"`
}

type selectedWriterArtifact struct {
	Revision orm.WorkflowSlotRevision
	Value    json.RawMessage
}

type writerWriteBackArtifact struct {
	Format   string
	Document json.RawMessage
	Markdown string
	Title    string
}

func writerDocumentProvider(values ...json.RawMessage) string {
	for _, value := range values {
		if len(value) == 0 {
			continue
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		document := value
		if json.Unmarshal(value, &envelope) == nil && len(envelope.Data) > 0 {
			document = envelope.Data
		}
		var identity struct {
			Adapter         string `json:"adapter"`
			ProviderBinding struct {
				Provider string `json:"provider"`
			} `json:"provider_binding"`
		}
		if json.Unmarshal(document, &identity) != nil {
			continue
		}
		provider := canonicalWriterProvider(identity.ProviderBinding.Provider)
		if provider == "" {
			provider = canonicalWriterProvider(identity.Adapter)
		}
		if writerDocumentProviderSupported(provider) {
			return provider
		}
	}
	return ""
}

func writerProviderToolConfig(toolConfig map[string]any, provider string) (map[string]any, bool) {
	credential := toolConfig[provider]
	if credential == nil {
		return nil, false
	}
	return map[string]any{provider: credential}, true
}

func writerProviderRequiresToolConfig(provider string) bool {
	return modelconfig.IsCloudToolProvider(canonicalWriterProvider(provider))
}

func writerDocumentProviderSupported(provider string) bool { return strings.TrimSpace(provider) != "" }

func writerDocumentSlot(slot string) (string, bool) {
	if slot == "" {
		return "draft_document", true
	}
	return slot, slot == "outline_document" || slot == "flat_draft_document" || slot == "draft_document"
}

func writerDocumentRenderSlot(slot string) (string, bool) {
	if slot == "source_document" {
		return slot, true
	}
	return writerDocumentSlot(slot)
}

// SyncWriterDocument writes an edited WriterDocument to its bound provider, then commits
// the provider-confirmed document as a human artifact revision.
func SyncWriterDocument(w http.ResponseWriter, r *http.Request) {
	sessionID, slotID := common.PathVar(r, "session_id"), common.PathVar(r, "slot_id")
	index, err := strconv.Atoi(common.PathVar(r, "list_index"))
	if err != nil || index < -1 {
		common.ReplyErr(w, "invalid request", 400)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	var body writerDocumentSyncBody
	if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.SourceDocument) == 0 || len(body.RevisedDocument) == 0 {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if body.BaseRevision < 1 {
		replyWriterRevisionRequired(w)
		return
	}
	db := store.DB()
	owner := strings.TrimSpace(store.UserID(r))
	if owner == "" {
		common.ReplyErr(w, "missing X-User-Id", 400)
		return
	}
	if db == nil {
		common.ReplyErr(w, "store not initialized", 500)
		return
	}
	session, err := workflow.GetSession(r.Context(), db, sessionID)
	if err != nil || session.WorkflowID != "writer-workflow" || session.CreateUserID != owner || session.Dismissed {
		common.ReplyErr(w, "writer session not found", 404)
		return
	}
	var current orm.WorkflowSlotRevision
	q := db.WithContext(r.Context()).Where("session_id = ? AND slot_id = ?", sessionID, slotID)
	if index < 0 {
		q = q.Where("list_index IS NULL")
	} else {
		q = q.Where("list_index = ?", index)
	}
	if q.Session(&gorm.Session{}).Where("selected = ?", true).First(&current).Error != nil {
		common.ReplyErr(w, "slot revision not found", 404)
		return
	}
	if current.Revision != body.BaseRevision {
		var original orm.WorkflowSlotRevision
		err := q.Session(&gorm.Session{}).Where("revision = ?", body.BaseRevision).First(&original).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			replyWriterRevisionConflict(w, current.Revision)
			return
		}
		if err != nil {
			common.ReplyErr(w, "slot revision not found", 404)
			return
		}
		current = original
	}
	raw, err := workflow.LoadSlotRevisionValue(r.Context(), db, current)
	if err != nil {
		common.ReplyErr(w, "slot revision not found", 404)
		return
	}
	server, err := writerArtifactData(raw, true)
	if err != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if !sameWriterPublicationIdentity(server, body.SourceDocument) || !sameWriterPublicationIdentity(server, body.RevisedDocument) {
		common.ReplyErrWithData(w, "writer document sync failed", map[string]any{"code": "PROVIDER_BINDING_CONFLICT", "retryable": false}, 409)
		return
	}
	provider := writerDocumentProvider(server)
	if provider == "" {
		common.ReplyErrWithData(w, "bound provider required", map[string]any{"code": "PROVIDER_BINDING_REQUIRED", "retryable": false}, 409)
		return
	}
	key := legacyWriterPublicationKey(owner, sessionID, slotID, body)
	request := workflow.DocumentPublishRequest{Action: "publish_document", BaseRevision: &body.BaseRevision, BaseDraftVersion: body.BaseDraftVersion, Input: &workflow.DocumentPublishInput{Provider: provider, Mode: "replace", IdempotencyKey: key}}
	result, operation, err := workflow.PublishDocumentArtifact(r.Context(), db, owner, current.ID, request, &workflow.DocumentPublicationOptions{
		Candidate: body.RevisedDocument, SkipUnchangedDraft: body.Mode == "draft", AllowLegacyProviderDocument: true,
	})
	workflow.ReplyDocumentPublication(w, result, operation, err)
}

// RenderWriterDocument renders a source, outline, or draft with automatic numbering.
// Editor documents remain canonical; generated numbering is returned as sidecar data.
func RenderWriterDocument(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	if sessionID == "" {
		common.ReplyErr(w, "session_id required", http.StatusBadRequest)
		return
	}
	var body writerDocumentRenderBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	slot, ok := writerDocumentRenderSlot(body.Slot)
	if !ok {
		common.ReplyErr(w, "slot must be source_document, outline_document, flat_draft_document, or draft_document", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	session, err := workflow.GetSession(ctx, db, sessionID)
	if err != nil || session == nil || session.WorkflowID != "writer-workflow" || session.Dismissed ||
		(session.CreateUserID == "" || session.CreateUserID != userID) {
		common.ReplyErr(w, "writer session not found", http.StatusNotFound)
		return
	}
	draft, err := loadSelectedWriterArtifact(ctx, db, sessionID, slot)
	if err != nil {
		common.ReplyErr(w, "active "+slot+" not found", http.StatusNotFound)
		return
	}
	artifact := writerRenderArtifact(draft.Value)
	response, status, err := algo.InvokeDocumentAction(ctx, algo.DocumentActionInvokeRequest{
		Reference: "builtin:document.render_document.v1", Phase: "preview",
		Artifact:  artifact,
		Arguments: map[string]any{},
	})
	if err != nil {
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		common.ReplyErrWithData(w, "render writer document failed", map[string]any{
			"detail": err.Error(),
		}, status)
		return
	}
	var result map[string]any
	if json.Unmarshal(response.Result, &result) != nil {
		common.ReplyErr(w, "invalid render response", http.StatusBadGateway)
		return
	}
	// Sessions are pinned to the workflow revision that created them. Older Writer
	// revisions returned a number-materialized IR document, so enforce the editor
	// boundary here as well as in the latest workflow implementation.
	if representation, _ := result["representation"].(string); representation == "ir" {
		canonical, canonicalErr := writerArtifactData(draft.Value, false)
		var document map[string]any
		if canonicalErr != nil || json.Unmarshal(canonical, &document) != nil {
			common.ReplyErr(w, "invalid writer IR artifact", http.StatusBadGateway)
			return
		}
		result["document"] = document
	}
	attachWriterMediaURLs(ctx, db, sessionID, slot, result)
	common.ReplyOK(w, result)
}

// SaveWriterDocument persists an IR or Markdown edit as a mutable draft or a
// versioned checkpoint. Editor content remains clean and numbering stays in sidecar data.
func SaveWriterDocument(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	if sessionID == "" {
		common.ReplyErr(w, "session_id required", http.StatusBadRequest)
		return
	}
	var body writerDocumentSaveBody
	if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Document) == 0 {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.BaseRevision <= 0 {
		replyWriterRevisionRequired(w)
		return
	}
	mode := body.Mode
	if mode == "" {
		mode = "checkpoint"
	}
	if mode != "draft" && mode != "checkpoint" {
		common.ReplyErr(w, "invalid mode: must be draft or checkpoint", http.StatusBadRequest)
		return
	}
	slot, ok := writerDocumentSlot(body.Slot)
	if !ok {
		common.ReplyErr(w, "slot must be outline_document, flat_draft_document, or draft_document", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	session, err := workflow.GetSession(ctx, db, sessionID)
	if err != nil || session == nil || session.WorkflowID != "writer-workflow" || session.Dismissed ||
		(session.CreateUserID == "" || session.CreateUserID != userID) {
		common.ReplyErr(w, "writer session not found", http.StatusNotFound)
		return
	}
	draft, err := loadSelectedWriterArtifact(ctx, db, sessionID, slot)
	if err != nil {
		common.ReplyErr(w, "active "+slot+" not found", http.StatusNotFound)
		return
	}
	if draft.Revision.Revision != body.BaseRevision {
		replyWriterRevisionConflict(w, draft.Revision.Revision)
		return
	}
	if err := validateWriterDraftVersion(ctx, db, draft.Revision, body.BaseDraftVersion); err != nil {
		if !replyWriterDraftVersionError(w, err) {
			common.ReplyErr(w, "load draft version failed", http.StatusInternalServerError)
		}
		return
	}
	document := body.Document
	var structuredDocument map[string]any
	if json.Unmarshal(document, &structuredDocument) == nil {
		document, err = normalizeWriterDocumentForSync(document)
		if err != nil {
			common.ReplyErr(w, "invalid document", http.StatusBadRequest)
			return
		}
	}
	editedArtifact, err := json.Marshal(map[string]json.RawMessage{"data": document})
	if err != nil {
		common.ReplyErr(w, "invalid document", http.StatusBadRequest)
		return
	}
	arguments := map[string]any{"base_artifact": draft.Value}
	if len(body.NumberingUpdate) > 0 {
		arguments["numbering_update"] = body.NumberingUpdate
	}
	response, status, err := algo.InvokeDocumentAction(ctx, algo.DocumentActionInvokeRequest{Reference: "builtin:document.save_document.v1", Phase: "execute",
		Artifact:  editedArtifact,
		Arguments: arguments,
	})
	if err != nil {
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		common.ReplyErrWithData(w, "writer document sync failed", map[string]any{
			"detail": err.Error(),
		}, status)
		return
	}
	var result map[string]any
	if json.Unmarshal(response.Result, &result) != nil {
		common.ReplyErr(w, "invalid workflow action response", http.StatusBadGateway)
		return
	}
	sourceValue, ok := result["source_document"]
	representation, representationOK := result["representation"].(string)
	renderedDocument, documentOK := result["document"]
	numbering, numberingOK := result["numbering"].(map[string]any)
	_, markdownSource := sourceValue.(string)
	_, irSource := sourceValue.(map[string]any)
	_, markdownDocument := renderedDocument.(string)
	_, irDocument := renderedDocument.(map[string]any)
	if !ok || !representationOK || !documentOK || !numberingOK ||
		(markdownSource && (representation != "markdown" || !markdownDocument)) ||
		(!markdownSource && (!irSource || representation != "ir" || !irDocument)) {
		common.ReplyErr(w, "invalid workflow action response", http.StatusBadGateway)
		return
	}
	if representation == "ir" {
		// The persisted source is the canonical editor document. Do not echo a
		// materialized compatibility response from a pinned older workflow revision.
		renderedDocument = sourceValue
	}
	schema := "lazyllm.tools.writer.data_models.writer_ir.WriterDocument"
	if markdownSource {
		schema = "text/markdown"
	}
	artifact, err := json.Marshal(map[string]any{
		"schema":         schema,
		"schema_version": "0.1",
		"data":           sourceValue,
		"meta": map[string]any{
			"created_by": "writer-document-save-api",
			"created_at": time.Now().UTC().Format(time.RFC3339Nano),
			"title":      result["title"],
		},
	})
	if err != nil {
		common.ReplyErr(w, "marshal writerdocument artifact failed", http.StatusInternalServerError)
		return
	}
	unchangedGitHubSync := mode == "draft" && len(body.NumberingUpdate) == 0 &&
		writerGitHubSyncedMarkdownUnchanged(draft, sourceValue)
	var revision *orm.WorkflowSlotRevision
	var draftVersion int64
	if unchangedGitHubSync {
		revision = &draft.Revision
		draftVersion = draftVersionValue(body.BaseDraftVersion)
	} else {
		revision, draftVersion, _, err = workflow.SaveHumanArtifactValue(ctx, db, sessionID, draft.Revision.SlotID, draft.Revision.Slot, draft.Revision.StepID, draft.Revision.Attempt, "single", nil, "json", artifact, nil, &body.BaseRevision, body.BaseDraftVersion, mode == "draft")
	}

	if err != nil {
		if replyWriterDraftVersionError(w, err) {
			return
		}
		if errors.Is(err, workflow.ErrConflict) || errors.Is(err, gorm.ErrRecordNotFound) {
			currentRevision := 0
			if current, currentErr := loadSelectedWriterArtifact(ctx, db, sessionID, slot); currentErr == nil {
				currentRevision = current.Revision.Revision
			}
			common.ReplyErrWithData(w, "revision conflict", map[string]any{
				"code":             "REVISION_CONFLICT",
				"current_revision": currentRevision,
			}, http.StatusConflict)
			return
		}
		common.ReplyErrWithData(w, "artifact save failed", map[string]any{
			"detail": err.Error(),
		}, http.StatusInternalServerError)
		return
	}
	if !unchangedGitHubSync {
		workflow.NotifyWorkflowArtifactUpdated(
			ctx, db, sessionID, revision.StepID, revision.SlotID, revision.Slot,
			revision.Revision, revision.ListIndex, "human",
		)
	}
	reply := map[string]any{
		"revision":       revision.Revision,
		"draft_version":  draftVersion,
		"title":          result["title"],
		"representation": representation,
		"document":       renderedDocument,
		"numbering":      numbering,
	}
	if exportDocument, exists := result["export_document"]; exists {
		reply["export_document"] = exportDocument
	}
	attachWriterMediaURLs(ctx, db, sessionID, slot, reply)
	common.ReplyOK(w, reply)
}

// WriteBackWriterDocument writes the active IR or Markdown draft to the selected
// provider and saves the provider-confirmed IR as a new revision.
func WriteBackWriterDocument(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	var body writerDocumentWriteBackBody
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if body.BaseRevision < 1 {
		replyWriterRevisionRequired(w)
		return
	}
	slot, ok := writerDocumentSlot(body.Slot)
	if !ok || slot == "outline_document" {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	db := store.DB()
	owner := strings.TrimSpace(store.UserID(r))
	if owner == "" {
		common.ReplyErr(w, "missing X-User-Id", 400)
		return
	}
	if db == nil {
		common.ReplyErr(w, "store not initialized", 500)
		return
	}
	session, err := workflow.GetSession(r.Context(), db, sessionID)
	if err != nil || session.WorkflowID != "writer-workflow" || session.CreateUserID != owner || session.Dismissed {
		common.ReplyErr(w, "writer session not found", 404)
		return
	}
	draft, err := loadSelectedWriterArtifact(r.Context(), db, sessionID, slot)
	if err != nil {
		common.ReplyErr(w, "writer session not found", 404)
		return
	}
	if draft.Revision.Revision != body.BaseRevision {
		var source orm.WorkflowSlotRevision
		err = db.WithContext(r.Context()).Where("session_id = ? AND slot_id = ? AND list_index IS NULL AND revision = ?", sessionID, slot, body.BaseRevision).First(&source).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			replyWriterRevisionConflict(w, draft.Revision.Revision)
			return
		}
		if err == nil {
			draft, err = loadWriterArtifactRevision(r.Context(), db, source)
		}
		if err != nil {
			common.ReplyErr(w, "writer session not found", 404)
			return
		}
	}
	provider := strings.TrimSpace(body.Provider)
	if provider == "" {
		provider = writerDocumentProvider(draft.Value)
		if target, err := loadSelectedWriterArtifact(r.Context(), db, sessionID, "target_document"); provider == "" && err == nil {
			provider = writerDocumentProvider(target.Value)
		}
	}
	if provider == "" {
		common.ReplyErrWithData(w, "writer document provider selection required", map[string]any{"code": "PROVIDER_SELECTION_REQUIRED", "status": "provider_selection_required", "retryable": false}, 400)
		return
	}
	key := legacyWriterPublicationKey(owner, sessionID, slot, body)
	request := workflow.DocumentPublishRequest{Action: "publish_document", BaseRevision: &body.BaseRevision, BaseDraftVersion: body.BaseDraftVersion, Input: &workflow.DocumentPublishInput{Provider: provider, Mode: "replace", IdempotencyKey: key, Template: body.Template}}
	result, operation, err := workflow.PublishDocumentArtifact(r.Context(), db, owner, draft.Revision.ID, request, nil)
	workflow.ReplyDocumentPublication(w, result, operation, err)
}

func legacyWriterPublicationKey(owner, session, slot string, body any) string {
	raw, _ := json.Marshal([]any{owner, session, slot, body})
	sum := sha256.Sum256(raw)
	return "legacy-" + hex.EncodeToString(sum[:])
}
func sameWriterPublicationIdentity(a, b json.RawMessage) bool {
	var left, right struct {
		ID      string         `json:"document_id"`
		Binding map[string]any `json:"provider_binding"`
	}
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && left.ID != "" && left.ID == right.ID && len(left.Binding) > 0 && reflect.DeepEqual(left.Binding, right.Binding)
}

func loadSelectedWriterArtifact(
	ctx context.Context,
	db *gorm.DB,
	sessionID string,
	slotID string,
) (*selectedWriterArtifact, error) {
	var revision orm.WorkflowSlotRevision
	if err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND selected = ? AND list_index IS NULL",
			sessionID, slotID, true).
		First(&revision).Error; err != nil {
		return nil, err
	}
	return loadWriterArtifactRevision(ctx, db, revision)
}

type writerMediaAsset struct {
	MediaAssetID string `json:"media_asset_id"`
	URI          string `json:"uri"`
	LocalPath    string `json:"local_path"`
	Meta         struct {
		SourceReference string `json:"source_reference"`
		SHA256          string `json:"sha256"`
	} `json:"meta"`
}

type writerTargetMediaAliases struct {
	Adapter string `json:"adapter"`
	Meta    struct {
		GitHub map[string]string `json:"github_writer_media_aliases"`
	} `json:"meta"`
}

func attachWriterMediaURLs(
	ctx context.Context,
	db *gorm.DB,
	sessionID string,
	documentSlot string,
	result map[string]any,
) {
	representation, _ := result["representation"].(string)
	if representation != "markdown" && representation != "ir" {
		return
	}
	urls := writerDocumentMediaURLs(ctx, db, sessionID, documentSlot)
	if len(urls) == 0 {
		return
	}
	if representation == "markdown" {
		result["media_urls"] = urls
		return
	}
	switch document := result["document"].(type) {
	case map[string]any:
		attachWriterIRMediaPreviewURLs(document, urls)
	case json.RawMessage:
		var decoded map[string]any
		if json.Unmarshal(document, &decoded) == nil {
			attachWriterIRMediaPreviewURLs(decoded, urls)
			result["document"] = decoded
		}
	}
}

func attachWriterIRMediaPreviewURLs(document map[string]any, urls map[string]string) {
	blocks, _ := document["blocks"].([]any)
	var visit func([]any)
	visit = func(items []any) {
		for _, item := range items {
			block, _ := item.(map[string]any)
			if block == nil {
				continue
			}
			if block["type"] == "image" {
				references, _ := block["references"].([]any)
				for _, value := range references {
					reference, _ := value.(map[string]any)
					if reference == nil || reference["type"] != "media_asset" {
						continue
					}
					assetID, _ := reference["id"].(string)
					path, _ := reference["path"].(string)
					previewURL := urls["asset://"+strings.TrimSpace(assetID)]
					if previewURL == "" {
						previewURL = urls[strings.TrimSpace(path)]
					}
					if previewURL != "" {
						block["references"] = append(references, map[string]any{
							"type": "preview_asset",
							"id":   assetID,
							"url":  previewURL,
						})
					}
					break
				}
			}
			if children, ok := block["children"].([]any); ok {
				visit(children)
			}
		}
	}
	visit(blocks)
}

func writerDocumentMediaURLs(
	ctx context.Context,
	db *gorm.DB,
	sessionID string,
	documentSlot string,
) map[string]string {
	mediaSlots := []string{"resolved_media_assets", "media_assets"}
	switch documentSlot {
	case "source_document", "outline_document":
		mediaSlots = []string{"media_assets"}
	case "flat_draft_document":
		mediaSlots = []string{"flat_resolved_media_assets", "media_assets"}
	}

	urls := map[string]string{}
	assetURLs := map[string]string{}
	materializedURLs := map[string]string{}
	for _, mediaSlot := range mediaSlots {
		artifact, err := loadSelectedWriterArtifact(ctx, db, sessionID, mediaSlot)
		if err != nil {
			continue
		}
		data, err := writerArtifactData(artifact.Value, false)
		if err != nil {
			continue
		}
		for assetID, asset := range writerMediaAssets(data) {
			previewURL := doc.StaticFileURLFromAnyStoragePath(asset.LocalPath)
			if previewURL == "" {
				previewURL = doc.StaticFileURLFromAnyStoragePath(asset.URI)
			}
			if previewURL == "" {
				continue
			}
			if assetID = strings.TrimSpace(assetID); assetID != "" {
				assetURLs[assetID] = previewURL
			}
			if mediaAssetID := strings.TrimSpace(asset.MediaAssetID); mediaAssetID != "" {
				assetURLs[mediaAssetID] = previewURL
			}
			digest := strings.ToLower(strings.TrimSpace(asset.Meta.SHA256))
			suffix := strings.ToLower(filepath.Ext(asset.LocalPath))
			if suffix == "" {
				suffix = strings.ToLower(filepath.Ext(asset.URI))
			}
			if len(digest) == 64 && suffix != "" {
				path := "assets/" + digest[:2] + "/" + digest + suffix
				materializedURLs[path] = previewURL
				materializedURLs["_"+path] = previewURL
			}
			references := []string{
				strings.TrimSpace(asset.Meta.SourceReference),
				strings.TrimSpace(asset.LocalPath),
				strings.TrimSpace(asset.URI),
			}
			if assetID != "" {
				references = append(references, "asset://"+assetID)
			}
			if mediaAssetID := strings.TrimSpace(asset.MediaAssetID); mediaAssetID != "" {
				references = append(references, "asset://"+mediaAssetID)
			}
			for _, reference := range references {
				if reference == "" {
					continue
				}
				if _, exists := urls[reference]; !exists {
					urls[reference] = previewURL
				}
			}
		}
	}
	if documentSlot == "draft_document" || documentSlot == "flat_draft_document" {
		if target, err := loadSelectedWriterArtifact(ctx, db, sessionID, "target_document"); err == nil {
			if data, dataErr := writerArtifactData(target.Value, false); dataErr == nil {
				addWriterGitHubMediaAliases(data, urls, assetURLs, materializedURLs)
			}
		}
	}
	return urls
}

func addWriterGitHubMediaAliases(
	target json.RawMessage,
	urls, assetURLs, materializedURLs map[string]string,
) {
	var aliases writerTargetMediaAliases
	if json.Unmarshal(target, &aliases) != nil || canonicalWriterProvider(aliases.Adapter) != "github" {
		return
	}
	for reference, previewURL := range materializedURLs {
		if _, exists := urls[reference]; !exists {
			urls[reference] = previewURL
		}
	}
	for reference, assetID := range aliases.Meta.GitHub {
		reference = strings.TrimSpace(reference)
		previewURL := assetURLs[strings.TrimSpace(assetID)]
		if reference == "" || previewURL == "" {
			continue
		}
		if _, exists := urls[reference]; !exists {
			urls[reference] = previewURL
		}
	}
}

func writerMediaAssets(data json.RawMessage) map[string]writerMediaAsset {
	var library struct {
		Assets json.RawMessage `json:"assets"`
	}
	if json.Unmarshal(data, &library) != nil || len(library.Assets) == 0 {
		return nil
	}
	keyed := map[string]writerMediaAsset{}
	if json.Unmarshal(library.Assets, &keyed) == nil {
		return keyed
	}
	var listed []writerMediaAsset
	if json.Unmarshal(library.Assets, &listed) != nil {
		return nil
	}
	for index, asset := range listed {
		key := strings.TrimSpace(asset.MediaAssetID)
		if key == "" {
			key = strconv.Itoa(index)
		}
		keyed[key] = asset
	}
	return keyed
}

func loadWriterArtifactRevision(
	ctx context.Context,
	db *gorm.DB,
	revision orm.WorkflowSlotRevision,
) (*selectedWriterArtifact, error) {
	value, err := workflow.LoadSlotRevisionValue(ctx, db, revision)
	if err != nil {
		return nil, err
	}
	return &selectedWriterArtifact{Revision: revision, Value: value}, nil
}

func loadLatestSyncedWriterArtifact(
	ctx context.Context,
	db *gorm.DB,
	sessionID string,
	slot string,
	beforeRevision int,
) (*selectedWriterArtifact, error) {
	var revisions []orm.WorkflowSlotRevision
	if err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND list_index IS NULL AND revision < ?",
			sessionID, slot, beforeRevision).
		Order("revision DESC").
		Find(&revisions).Error; err != nil {
		return nil, err
	}
	for _, revision := range revisions {
		artifact, err := loadWriterArtifactRevision(ctx, db, revision)
		if err != nil {
			continue
		}
		if writerArtifactRevisionSynced(artifact) {
			return artifact, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

// loadWriterWriteBackBaseline prefers the latest provider-confirmed draft.  The
// first manual write-back has no such draft yet, so its source_document is the
// authoritative Feishu baseline instead.
func loadWriterWriteBackBaseline(
	ctx context.Context,
	db *gorm.DB,
	sessionID string,
	slot string,
	beforeRevision int,
) (*selectedWriterArtifact, error) {
	baseline, err := loadLatestSyncedWriterArtifact(ctx, db, sessionID, slot, beforeRevision)
	if err == nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		return baseline, err
	}
	return loadSelectedWriterArtifact(ctx, db, sessionID, "source_document")
}

type writerDocumentIdentity struct {
	DocumentID      string         `json:"document_id"`
	ProviderBinding map[string]any `json:"provider_binding"`
}

func writerDocumentIsUnbound(document json.RawMessage) bool {
	var identity writerDocumentIdentity
	return json.Unmarshal(document, &identity) == nil && len(identity.ProviderBinding) == 0
}

// unbindWriterDocument removes provider-owned identity and round-trip payloads
// before publishing an existing draft to a different provider.
func unbindWriterDocument(document json.RawMessage) (json.RawMessage, error) {
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		return nil, err
	}
	delete(value, "revision")
	value["provider_binding"] = map[string]any{}
	if metadata, ok := value["metadata"].(map[string]any); ok {
		for _, key := range []string{"source", "provider_metadata", "block_count", "source_block_count"} {
			delete(metadata, key)
		}
	}
	var cleanBlocks func(any)
	cleanBlocks = func(raw any) {
		blocks, ok := raw.([]any)
		if !ok {
			return
		}
		for _, item := range blocks {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			block["provider_binding"] = map[string]any{}
			block["provider_payload"] = map[string]any{}
			cleanBlocks(block["children"])
		}
	}
	cleanBlocks(value["blocks"])
	return json.Marshal(value)
}

func validateWriterWriteBackPair(source, revised json.RawMessage) error {
	var sourceDoc, revisedDoc writerDocumentIdentity
	if json.Unmarshal(source, &sourceDoc) != nil || json.Unmarshal(revised, &revisedDoc) != nil {
		return fmt.Errorf("invalid WriterDocument state")
	}
	if sourceDoc.DocumentID == "" || sourceDoc.DocumentID != revisedDoc.DocumentID {
		return fmt.Errorf("WriterDocument identity does not match synchronized baseline")
	}
	provider, _ := sourceDoc.ProviderBinding["provider"].(string)
	externalID, _ := sourceDoc.ProviderBinding["document_id"].(string)
	if !writerDocumentProviderSupported(provider) || externalID == "" {
		return fmt.Errorf("synchronized baseline is not bound to a supported cloud document")
	}
	revisedProvider, _ := revisedDoc.ProviderBinding["provider"].(string)
	revisedExternalID, _ := revisedDoc.ProviderBinding["document_id"].(string)
	if revisedProvider != provider || revisedExternalID != externalID {
		return fmt.Errorf("current WriterDocument provider binding does not match baseline")
	}
	return nil
}

// normalizeWriterDocumentForSync converts editor-compatible rich-text span
// styles to the Writer IR wire contract. The editor accepts legacy string
// arrays (and stype), while the algorithm model requires style to be an
// object. Manual write-back reads revisions on the server, so it cannot rely
// on the equivalent frontend normalization performed for client payloads.
func normalizeWriterDocumentForSync(document json.RawMessage) (json.RawMessage, error) {
	var record map[string]any
	if err := json.Unmarshal(document, &record); err != nil {
		return nil, err
	}
	blocks, ok := record["blocks"].([]any)
	if !ok {
		return nil, fmt.Errorf("blocks must be an array")
	}
	for _, block := range blocks {
		normalizeWriterBlockForSync(block)
	}
	return json.Marshal(record)
}

// preserveExistingWriterImageBlocks keeps provider-owned image blocks byte-for-byte
// equivalent to their synchronized baseline. The Writer revision tool accepts new
// images, but deliberately rejects any update to an existing image block. Human
// editor saves can otherwise add placeholder newlines or spans to an untouched
// image caption while the user is editing surrounding text.
func preserveExistingWriterImageBlocks(source, revised json.RawMessage) (json.RawMessage, error) {
	var sourceDocument, revisedDocument map[string]any
	if err := json.Unmarshal(source, &sourceDocument); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(revised, &revisedDocument); err != nil {
		return nil, err
	}
	sourceBlocks, ok := sourceDocument["blocks"].([]any)
	if !ok {
		return nil, fmt.Errorf("source blocks must be an array")
	}
	revisedBlocks, ok := revisedDocument["blocks"].([]any)
	if !ok {
		return nil, fmt.Errorf("blocks must be an array")
	}
	baselineImages := make(map[string]map[string]any)
	collectWriterImageBlocks(sourceBlocks, baselineImages)
	preserveWriterImageBlocks(revisedBlocks, baselineImages)
	return json.Marshal(revisedDocument)
}

func collectWriterImageBlocks(blocks []any, images map[string]map[string]any) {
	for _, value := range blocks {
		block, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if block["type"] == "image" {
			if nodeID, _ := block["node_id"].(string); nodeID != "" {
				images[nodeID] = block
			}
		}
		if children, ok := block["children"].([]any); ok {
			collectWriterImageBlocks(children, images)
		}
	}
}

func preserveWriterImageBlocks(blocks []any, baselineImages map[string]map[string]any) {
	for index, value := range blocks {
		block, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if block["type"] == "image" {
			if nodeID, _ := block["node_id"].(string); nodeID != "" {
				if baseline, ok := baselineImages[nodeID]; ok {
					blocks[index] = baseline
					continue
				}
			}
		}
		if children, ok := block["children"].([]any); ok {
			preserveWriterImageBlocks(children, baselineImages)
		}
	}
}

func normalizeWriterBlockForSync(value any) {
	block, ok := value.(map[string]any)
	if !ok {
		return
	}
	if block["type"] == "image" {
		if content, ok := block["content"].(string); ok {
			block["content"] = strings.TrimLeft(content, "\r\n")
		}
	}
	if spans, ok := block["spans"].([]any); ok {
		for _, value := range spans {
			span, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] == "image" {
				if text, ok := span["text"].(string); ok {
					span["text"] = strings.TrimLeft(text, "\r\n")
				}
			}
			style := span["style"]
			if style == nil {
				style = span["stype"]
			}
			if values, ok := style.([]any); ok {
				normalized := make(map[string]any, len(values))
				for _, value := range values {
					name, ok := value.(string)
					if !ok || name == "" {
						continue
					}
					switch name {
					case "strong":
						name = "bold"
					case "code":
						name = "inline_code"
					}
					normalized[name] = true
				}
				span["style"] = normalized
			} else if _, ok := style.(map[string]any); ok {
				span["style"] = style
			} else if style == nil {
				span["style"] = map[string]any{}
			}
			delete(span, "stype")
		}
	}
	if children, ok := block["children"].([]any); ok {
		for _, child := range children {
			normalizeWriterBlockForSync(child)
		}
	}
}

func writerArtifactRevisionSynced(artifact *selectedWriterArtifact) bool {
	if artifact == nil {
		return false
	}
	if artifact.Revision.ChangeSource == "provider_sync" {
		return true
	}
	return (artifact.Revision.ChangeSource == "ai" || artifact.Revision.ChangeSource == "host") &&
		writerArtifactEnvelopeProviderSynced(artifact.Value)
}

func writerGitHubSyncedMarkdownUnchanged(artifact *selectedWriterArtifact, value any) bool {
	markdown, ok := value.(string)
	if !ok || artifact == nil || artifact.Revision.ChangeSource != "provider_sync" ||
		writerArtifactEnvelopeSyncProvider(artifact.Value) != "github" {
		return false
	}
	current, err := loadWriterWriteBackArtifact(artifact.Value)
	return err == nil && current.Format == "markdown" && current.Markdown == markdown
}

func writerArtifactEnvelopeSyncProvider(value json.RawMessage) string {
	var record struct {
		Meta struct {
			Sync struct {
				Confirmed bool   `json:"confirmed"`
				Provider  string `json:"provider"`
			} `json:"lazymind_provider_sync"`
		} `json:"meta"`
	}
	if json.Unmarshal(value, &record) != nil || !record.Meta.Sync.Confirmed {
		return ""
	}
	return canonicalWriterProvider(record.Meta.Sync.Provider)
}

func writerArtifactEnvelopeProviderSynced(value json.RawMessage) bool {
	var record map[string]json.RawMessage
	if json.Unmarshal(value, &record) != nil {
		return false
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(record["meta"], &metadata) == nil {
		var marker struct {
			Confirmed bool `json:"confirmed"`
		}
		if json.Unmarshal(metadata["lazymind_provider_sync"], &marker) == nil && marker.Confirmed {
			return true
		}
	}
	var path string
	_ = json.Unmarshal(record["path"], &path)
	if path == "" || strings.ToLower(filepath.Ext(path)) != ".lmd" {
		return false
	}
	cleanPath := filepath.Clean(path)
	if !writerArtifactPathAllowed(cleanPath) {
		return false
	}
	content, err := os.ReadFile(cleanPath)
	return err == nil && writerArtifactEnvelopeProviderSynced(content)
}

func writerArtifactData(value json.RawMessage, requireLMD bool) (json.RawMessage, error) {
	var record map[string]json.RawMessage
	if err := json.Unmarshal(value, &record); err != nil {
		return nil, fmt.Errorf("invalid writer artifact")
	}
	if data := record["data"]; len(data) > 0 {
		return data, nil
	}
	if text := record["text"]; len(text) > 0 {
		return text, nil
	}
	if len(record["document_id"]) > 0 || len(record["uri"]) > 0 || len(record["doc_id"]) > 0 {
		return value, nil
	}
	var path string
	_ = json.Unmarshal(record["path"], &path)
	if path == "" {
		return nil, fmt.Errorf("writer artifact has no local path")
	}
	if requireLMD && strings.ToLower(filepath.Ext(path)) != ".lmd" {
		// TODO(writing-2.0): Convert Markdown to IR on its first provider write-back,
		// resolve/create the destination, then use the provider-confirmed IR as the
		// baseline for all later revisions.
		return nil, fmt.Errorf("active draft_document must be an .lmd artifact")
	}
	cleanPath := filepath.Clean(path)
	if !writerArtifactPathAllowed(cleanPath) {
		return nil, fmt.Errorf("writer artifact path is outside allowed storage")
	}
	content, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("read writer artifact: %w", err)
	}
	return writerArtifactData(content, false)
}

func writerRenderArtifact(value json.RawMessage) json.RawMessage {
	var record map[string]json.RawMessage
	if json.Unmarshal(value, &record) != nil || len(record["text"]) == 0 {
		return value
	}
	artifact, err := json.Marshal(map[string]json.RawMessage{
		"schema": json.RawMessage(`"text/markdown"`),
		"data":   record["text"],
	})
	if err != nil {
		return value
	}
	return artifact
}

func loadWriterWriteBackArtifact(value json.RawMessage) (*writerWriteBackArtifact, error) {
	var record struct {
		Schema   string          `json:"schema"`
		Data     json.RawMessage `json:"data"`
		Path     string          `json:"path"`
		Filename string          `json:"filename"`
		Meta     struct {
			Title string `json:"title"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(value, &record); err != nil {
		return nil, fmt.Errorf("invalid writer artifact")
	}
	path := record.Path
	if path == "" {
		if record.Schema == "text/markdown" {
			var markdown string
			if json.Unmarshal(record.Data, &markdown) != nil || strings.TrimSpace(markdown) == "" {
				return nil, fmt.Errorf("active draft_document Markdown is empty")
			}
			title := record.Meta.Title
			if title == "" {
				if heading, ok := strings.CutPrefix(strings.TrimSpace(strings.SplitN(markdown, "\n", 2)[0]), "# "); ok {
					title = strings.TrimSpace(heading)
				}
			}
			return &writerWriteBackArtifact{
				Format: "markdown", Markdown: markdown, Title: title,
			}, nil
		}
		document, err := writerArtifactData(value, false)
		if err != nil {
			return nil, err
		}
		return &writerWriteBackArtifact{Format: "lmd", Document: document}, nil
	}

	cleanPath := filepath.Clean(path)
	if !writerArtifactPathAllowed(cleanPath) {
		return nil, fmt.Errorf("writer artifact path is outside allowed storage")
	}
	content, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("read writer artifact: %w", err)
	}
	extension := strings.ToLower(filepath.Ext(cleanPath))
	switch extension {
	case ".md", ".markdown":
		if strings.TrimSpace(string(content)) == "" {
			return nil, fmt.Errorf("active draft_document Markdown is empty")
		}
		filename := strings.TrimSpace(record.Filename)
		if filename == "" {
			filename = filepath.Base(cleanPath)
		}
		title := strings.TrimSpace(record.Meta.Title)
		normalizedFilename := strings.ToLower(filepath.Base(filename))
		if title == "" && normalizedFilename != "draft_document.md" &&
			normalizedFilename != "flat_draft_document.md" {
			title = strings.TrimSuffix(filename, filepath.Ext(filename))
		}
		return &writerWriteBackArtifact{
			Format: "markdown", Markdown: string(content),
			Title: title,
		}, nil
	case ".lmd":
		document, dataErr := writerArtifactData(content, false)
		if dataErr != nil {
			return nil, dataErr
		}
		return &writerWriteBackArtifact{Format: "lmd", Document: document}, nil
	default:
		return nil, fmt.Errorf("active draft_document must be an .lmd or .md artifact")
	}
}

func writerArtifactPathAllowed(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	roots := []string{os.Getenv("LAZYMIND_SUBAGENT_WORKSPACE"), "/var/lib/lazymind/uploads"}
	for _, root := range roots {
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(filepath.Clean(root), path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func canonicalWriterProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "lark", "feishu":
		return "feishu"
	case "github", "githubrepo", "githubwiki":
		return "github"
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func draftVersionValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func validateWriterDraftVersion(
	ctx context.Context,
	db *gorm.DB,
	revision orm.WorkflowSlotRevision,
	expected *int64,
) error {
	if revision.HumanArtifactID == nil || *revision.HumanArtifactID == "" {
		return nil
	}
	if expected == nil {
		return workflow.ErrDraftVersionRequired
	}
	var artifact orm.WorkflowHumanArtifact
	if err := db.WithContext(ctx).
		Select("draft_version").
		Where("id = ?", *revision.HumanArtifactID).
		First(&artifact).Error; err != nil {
		return err
	}
	if artifact.DraftVersion != *expected {
		return workflow.ErrDraftVersionConflict
	}
	return nil
}

func replyWriterDraftVersionError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, workflow.ErrDraftVersionRequired):
		common.ReplyErrWithData(w, "base_draft_version required", map[string]any{
			"code": "DRAFT_VERSION_REQUIRED",
		}, http.StatusBadRequest)
		return true
	case errors.Is(err, workflow.ErrDraftVersionConflict):
		common.ReplyErrWithData(w, "draft version conflict; refresh and retry", map[string]any{
			"code": "DRAFT_VERSION_CONFLICT",
		}, http.StatusConflict)
		return true
	case errors.Is(err, workflow.ErrArtifactInUse):
		common.ReplyErrWithData(w, "artifact is in use by a running workflow attempt", map[string]any{
			"code": "ARTIFACT_IN_USE",
		}, http.StatusConflict)
		return true
	default:
		return false
	}
}

func replyWriterRevisionRequired(w http.ResponseWriter) {
	common.ReplyErrWithData(w, "base_revision required", map[string]any{
		"code": "REVISION_REQUIRED",
	}, http.StatusBadRequest)
}

func replyWriterRevisionConflict(w http.ResponseWriter, currentRevision int) {
	common.ReplyErrWithData(w, "revision conflict", map[string]any{
		"code":             "REVISION_CONFLICT",
		"current_revision": currentRevision,
	}, http.StatusConflict)
}

func replyWriterProviderLocalConflict(
	w http.ResponseWriter,
	currentRevision int,
	result *algo.WriterDocumentSyncResponse,
) {
	common.ReplyErrWithData(w, "provider sync succeeded but local artifact changed", map[string]any{
		"code":             "PROVIDER_SYNC_LOCAL_CONFLICT",
		"provider":         result.Provider,
		"provider_synced":  true,
		"artifact_saved":   false,
		"retryable":        false,
		"current_revision": currentRevision,
		"patch_result":     result.PatchResult,
		"document":         result.PersistedDocument,
	}, http.StatusConflict)
}

func replyWriterProviderTargetLocalConflict(w http.ResponseWriter, result *algo.WriterDocumentSyncResponse) {
	common.ReplyErrWithData(w, "provider sync succeeded but local artifact changed", map[string]any{
		"code":                  "PROVIDER_SYNC_LOCAL_CONFLICT",
		"provider":              result.Provider,
		"provider_synced":       true,
		"artifact_saved":        true,
		"target_artifact_saved": false,
		"retryable":             false,
		"patch_result":          result.PatchResult,
		"document":              result.PersistedDocument,
	}, http.StatusConflict)
}

func writerSyncReply(
	w http.ResponseWriter,
	status string,
	revision int,
	draftVersion int64,
	artifactSaved bool,
	result *algo.WriterDocumentSyncResponse,
) {
	common.ReplyOK(w, map[string]any{
		"status": status, "revision": revision, "draft_version": draftVersion,
		"provider_synced": true,
		"artifact_saved":  artifactSaved, "patch_result": result.PatchResult,
		"document": result.PersistedDocument,
	})
}

func writerSyncStatus(status int) int {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity,
		http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict:
		return status
	default:
		return http.StatusBadGateway
	}
}

func writerActionErrorData(err error, defaults map[string]any) map[string]any {
	data := make(map[string]any, len(defaults)+4)
	for key, value := range defaults {
		data[key] = value
	}
	data["detail"] = err.Error()
	var httpErr *common.HTTPError
	if !errors.As(err, &httpErr) || len(httpErr.Body) == 0 {
		return data
	}
	var envelope struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(httpErr.Body, &envelope) != nil || len(envelope.Detail) == 0 {
		return data
	}
	var detail map[string]any
	if json.Unmarshal(envelope.Detail, &detail) != nil {
		return data
	}
	for key, value := range detail {
		data[key] = value
	}
	return data
}
