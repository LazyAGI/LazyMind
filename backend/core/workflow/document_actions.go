package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	corestore "lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/workflow/artifactgraph"
	"lazymind/core/workflow/document"
	workflowstore "lazymind/core/workflow/store"
)

const documentRewriteReference = "builtin:document.rewrite_selection.v1"
const documentActionMaxBytes = 20 << 20

var documentCommitToken = regexp.MustCompile(`^[0-9a-f]{32}$`)

type DocumentRewriteSelection struct {
	Type         string `json:"type"`
	SelectedText string `json:"selected_text,omitempty"`
	NodeID       string `json:"node_id,omitempty"`
}

func (selection *DocumentRewriteSelection) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 {
		return errors.New("invalid selection")
	}
	if json.Unmarshal(fields["type"], &selection.Type) != nil {
		return errors.New("invalid selection type")
	}
	switch selection.Type {
	case "markdown":
		if json.Unmarshal(fields["selected_text"], &selection.SelectedText) != nil || selection.SelectedText == "" {
			return errors.New("invalid markdown selection")
		}
	case "ir":
		if json.Unmarshal(fields["node_id"], &selection.NodeID) != nil || strings.TrimSpace(selection.NodeID) == "" {
			return errors.New("invalid IR selection")
		}
	default:
		return errors.New("invalid selection type")
	}
	return nil
}

type DocumentRewritePreviewInput struct {
	Instruction     string                       `json:"instruction"`
	Selection       *DocumentRewriteSelection    `json:"selection,omitempty"`
	Type            string                       `json:"type,omitempty"`
	SelectionRanges []map[string]json.RawMessage `json:"selection_ranges,omitempty"`
}
type DocumentRewriteExecuteInput struct {
	CommitToken string `json:"commit_token"`
}
type DocumentRewritePreviewRequest struct {
	Action           string                       `json:"action" enum:"rewrite_selection"`
	BaseRevision     *int                         `json:"base_revision" required:"true"`
	BaseDraftVersion *int64                       `json:"base_draft_version,omitempty"`
	Input            *DocumentRewritePreviewInput `json:"input" required:"true"`
}
type DocumentRewriteExecuteRequest struct {
	Action           string                       `json:"action" enum:"rewrite_selection"`
	BaseRevision     *int                         `json:"base_revision" required:"true"`
	BaseDraftVersion *int64                       `json:"base_draft_version,omitempty"`
	Input            *DocumentRewriteExecuteInput `json:"input" required:"true"`
}
type DocumentActionArtifact struct {
	ContentType string          `json:"content_type"`
	Value       json.RawMessage `json:"value" required:"true"`
	Caption     *string         `json:"caption,omitempty"`
}
type DocumentRewriteTarget struct {
	Type      string  `json:"type"`
	BlockType string  `json:"block_type"`
	NodeID    *string `json:"node_id,omitempty"`
}
type DocumentRewritePreview struct {
	OldText *string `json:"old_text" required:"true"`
	NewText *string `json:"new_text" required:"true"`
}
type DocumentRewritePatch struct {
	Type    string                     `json:"type"`
	Payload map[string]json.RawMessage `json:"payload" required:"true"`
}
type DocumentRewriteCommit struct {
	Token string `json:"token"`
}
type DocumentRewritePreviewResult struct {
	Representation string                  `json:"representation"`
	Target         *DocumentRewriteTarget  `json:"target" required:"true"`
	Preview        *DocumentRewritePreview `json:"preview" required:"true"`
	Patch          *DocumentRewritePatch   `json:"patch" required:"true"`
	Artifact       *DocumentActionArtifact `json:"artifact" required:"true"`
	Commit         *DocumentRewriteCommit  `json:"commit" required:"true"`
}

// Public array branch preserves the Algorithm target coordinates.
type DocumentRewriteRangeTarget struct {
	Type        string  `json:"type" enum:"block"`
	BlockType   string  `json:"block_type"`
	NodeID      *string `json:"node_id,omitempty"`
	TargetStart *int    `json:"target_start,omitempty"`
	TargetEnd   *int    `json:"target_end,omitempty"`
}
type DocumentRewriteRangeResult struct {
	Target  *DocumentRewriteRangeTarget `json:"target" required:"true"`
	Preview *DocumentRewritePreview     `json:"preview" required:"true"`
	Patch   *DocumentRewritePatch       `json:"patch" required:"true"`
}
type DocumentRewriteRangesResult struct {
	Representation string                       `json:"representation"`
	Results        []DocumentRewriteRangeResult `json:"results" required:"true"`
	Artifact       *DocumentActionArtifact      `json:"artifact" required:"true"`
	Commit         *DocumentRewriteCommit       `json:"commit" required:"true"`
}
type documentRewritePreviewAlgorithmResult = DocumentRewriteRangesResult

type documentRewriteAlgorithmResult struct {
	Representation string                  `json:"representation"`
	Artifact       *DocumentActionArtifact `json:"artifact"`
}
type DocumentRewriteExecuteResult struct {
	ArtifactID   string `json:"artifact_id"`
	Revision     int    `json:"revision"`
	DraftVersion int64  `json:"draft_version"`
}

type documentActionFailure struct {
	code   string
	status int
}

func (failure documentActionFailure) Error() string { return failure.code }
func documentFailure(code string, status int) error { return documentActionFailure{code, status} }
func replyDocumentFailure(w http.ResponseWriter, err error) {
	failure := documentActionFailure{"DOCUMENT_ACTION_FAILED", http.StatusInternalServerError}
	_ = errors.As(err, &failure)
	message := "workflow artifact action failed"
	switch failure.status {
	case 400, 422:
		message = "invalid artifact action preview request"
	case 403:
		message = "forbidden"
	case 404:
		message = "selected artifact not found"
	case 409:
		message = "revision conflict"
	}
	common.ReplyErrWithData(w, message, map[string]any{"code": failure.code}, failure.status)
}
func decodeDocumentJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("multiple or invalid JSON values")
	}
	return nil
}

type documentActionRequest struct {
	baseRevision     *int
	baseDraftVersion *int64
	arguments        any
	selectionType    string
	arrayInput       bool
}
type documentActionContext struct {
	db       *gorm.DB
	owner    string
	session  *orm.WorkflowSession
	revision orm.WorkflowSlotRevision
	artifact workflowstore.Artifact
	content  *document.Content
}

func PreviewDocumentAction(w http.ResponseWriter, r *http.Request) {
	runDocumentAction(w, r, "preview")
}
func ExecuteDocumentAction(w http.ResponseWriter, r *http.Request) {
	runDocumentAction(w, r, "execute")
}

func runDocumentAction(w http.ResponseWriter, r *http.Request, phase string) {
	owner := strings.TrimSpace(corestore.UserID(r))
	if owner == "" {
		replyDocumentFailure(w, documentFailure("IDENTITY_REQUIRED", 400))
		return
	}
	if strings.TrimSpace(r.Header.Get("X-LazyMind-External-Ref")) != "" {
		replyDocumentFailure(w, documentFailure("PERMISSION_DENIED", 403))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, documentActionMaxBytes)
	raw, err := io.ReadAll(r.Body)
	var dispatch struct {
		Action string `json:"action"`
	}
	if err != nil || json.Unmarshal(raw, &dispatch) != nil {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
		return
	}
	switch dispatch.Action {
	case "publish_document":
		if phase != "execute" {
			replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_UNSUPPORTED", 422))
			return
		}
		runDocumentPublication(w, r, owner, raw)
	case "rewrite_selection":
		runDocumentRewrite(w, r, phase, owner, raw)
	case "cross_reference":
		runDocumentCrossReference(w, r, phase, owner, raw)
	case "numbering":
		runDocumentNumbering(w, r, phase, owner, raw)
	case "convert_document":
		if phase != "preview" {
			replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_UNSUPPORTED", 422))
			return
		}
		runDocumentConvert(w, r, owner, raw)
	default:
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_UNSUPPORTED", 422))
	}
}

func runDocumentRewrite(w http.ResponseWriter, r *http.Request, phase, owner string, raw []byte) {
	var request documentActionRequest
	action := ""
	invalid := false
	if phase == "preview" {
		var body DocumentRewritePreviewRequest
		invalid = decodeDocumentJSON(bytes.NewReader(raw), &body) != nil || body.Input == nil
		action = body.Action
		request.baseRevision = body.BaseRevision
		request.baseDraftVersion = body.BaseDraftVersion
		if !invalid {
			invalid = strings.TrimSpace(body.Input.Instruction) == ""
			if !invalid && body.Input.Selection == nil {
				ranges, rangeErr := documentRewriteRanges(body.Input)
				invalid = rangeErr != nil
				request.arrayInput = true
				request.selectionType = body.Input.Type
				request.arguments = map[string]any{"instruction": body.Input.Instruction, "type": body.Input.Type, "selection_ranges": ranges}
			} else if !invalid {
				invalid = body.Input.Type != "" || body.Input.SelectionRanges != nil

				request.selectionType = body.Input.Selection.Type
				selection := map[string]string{"node_id": body.Input.Selection.NodeID}
				if request.selectionType == "markdown" {
					selection = map[string]string{"selected_text": body.Input.Selection.SelectedText}
				}
				request.arguments = map[string]any{
					"type": request.selectionType, "instruction": body.Input.Instruction,
					"selection_ranges": []map[string]string{selection},
				}
			}
		}
	} else {
		var body DocumentRewriteExecuteRequest
		invalid = decodeDocumentJSON(bytes.NewReader(raw), &body) != nil || body.Input == nil
		action = body.Action
		request.baseRevision = body.BaseRevision
		request.baseDraftVersion = body.BaseDraftVersion
		if !invalid {
			invalid = !documentCommitToken.MatchString(body.Input.CommitToken)
			request.arguments = body.Input
		}
	}
	if invalid {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
		return
	}
	if action != "rewrite_selection" {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_UNSUPPORTED", 422))
		return
	}
	if request.baseRevision == nil {
		replyDocumentFailure(w, documentFailure("REVISION_REQUIRED", 400))
		return
	}
	if *request.baseRevision < 1 || (request.baseDraftVersion != nil && *request.baseDraftVersion < 1) {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
		return
	}
	target, err := prepareDocumentAction(r.Context(), owner, common.PathVar(r, "artifact_id"), request, true)
	if err != nil {
		replyDocumentFailure(w, err)
		return
	}
	if request.selectionType != "" && request.selectionType != target.content.Representation {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
		return
	}
	if request.arrayInput && !validDocumentRewriteRangeSource(request.arguments, target.content) {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
		return
	}
	var config map[string]any
	if phase == "preview" {
		config, err = modelconfig.LoadLLMConfig(r.Context(), target.db, owner)
		if err != nil {
			replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_FAILED", 500))
			return
		}
		if !workflowstore.RewriteModelAvailable(config) {
			replyDocumentFailure(w, documentFailure("MODEL_CONFIG_REQUIRED", 400))
			return
		}
	}
	// Only server-authoritative identity and baselines select a manifest namespace.
	namespace, _ := json.Marshal([]any{owner, target.session.ID, target.revision.ID, *request.baseRevision, target.artifact.DraftVersion, documentRewriteReference})
	digest := sha256.Sum256(namespace)
	artifactStore := filepath.Join(subagent.WorkspaceRoot(), "document-actions", hex.EncodeToString(digest[:]))
	artifact, _ := json.Marshal(map[string]json.RawMessage{"data": target.content.Value})
	response, status, err := algo.InvokeDocumentAction(r.Context(), algo.DocumentActionInvokeRequest{
		Reference: documentRewriteReference, Phase: phase, Artifact: artifact, Arguments: request.arguments, ArtifactStore: artifactStore, LLMConfig: config,
	})
	if err != nil {
		replyDocumentFailure(w, documentUpstreamFailure(status, err))
		return
	}
	if phase == "preview" {
		var result documentRewritePreviewAlgorithmResult
		if decodeDocumentJSON(bytes.NewReader(response.Result), &result) != nil || len(result.Results) == 0 || (!request.arrayInput && len(result.Results) != 1) {
			replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_RESULT_INVALID", 502))
			return
		}
		for _, item := range result.Results {
			if item.Target == nil {
				replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_RESULT_INVALID", 502))
				return
			}
			checked := DocumentRewritePreviewResult{Representation: result.Representation, Target: &DocumentRewriteTarget{Type: item.Target.Type, BlockType: item.Target.BlockType, NodeID: item.Target.NodeID}, Preview: item.Preview, Patch: item.Patch, Artifact: result.Artifact, Commit: result.Commit}
			if !validDocumentPreview(checked, target.content.Representation) {
				replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_RESULT_INVALID", 502))
				return
			}
		}
		item := result.Results[0]
		preview := DocumentRewritePreviewResult{
			Representation: result.Representation, Target: &DocumentRewriteTarget{Type: item.Target.Type, BlockType: item.Target.BlockType, NodeID: item.Target.NodeID},
			Preview: item.Preview, Patch: item.Patch, Artifact: result.Artifact, Commit: result.Commit,
		}
		if !validDocumentPreview(preview, target.content.Representation) || !validDocumentResult(r.Context(), preview.Artifact, target.content.Representation) {
			replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_RESULT_INVALID", 502))
			return
		}
		if r.Context().Err() != nil {
			replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_FAILED", 502))
			return
		}
		if request.arrayInput {
			if !validDocumentRewriteRangeResult(result, request.arguments, target.content) {
				replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_RESULT_INVALID", 502))
				return
			}
			common.ReplyOK(w, result)
		} else {
			common.ReplyOK(w, preview)
		}
		return
	}
	var result documentRewriteAlgorithmResult
	if decodeDocumentJSON(bytes.NewReader(response.Result), &result) != nil || result.Representation != target.content.Representation || !validDocumentResult(r.Context(), result.Artifact, target.content.Representation) {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_RESULT_INVALID", 502))
		return
	}
	saved, err := commitDocumentRewrite(r.Context(), target, request, result.Artifact)
	if err != nil {
		replyDocumentFailure(w, err)
		return
	}
	NotifyWorkflowArtifactUpdated(r.Context(), target.db, saved.SessionID, saved.StepID, saved.SlotID, saved.Slot, saved.Revision, saved.ListIndex, "human")
	common.ReplyOK(w, DocumentRewriteExecuteResult{ArtifactID: saved.ID, Revision: saved.Revision, DraftVersion: 1})
}

func prepareDocumentAction(ctx context.Context, owner, id string, request documentActionRequest, checkLive bool) (*documentActionContext, error) {
	db := corestore.DB()
	if db == nil {
		return nil, documentFailure("DOCUMENT_ACTION_FAILED", 500)
	}
	var revision orm.WorkflowSlotRevision
	if err := db.WithContext(ctx).Where("id = ?", id).First(&revision).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, documentFailure("ARTIFACT_NOT_FOUND", 404)
		}
		return nil, documentFailure("DOCUMENT_ACTION_FAILED", 500)
	}
	repo := workflowstore.New(db)
	if err := repo.AuthorizeSession(ctx, revision.SessionID, owner); err != nil {
		if errors.Is(err, workflowstore.ErrPermissionDenied) {
			return nil, documentFailure("PERMISSION_DENIED", 403)
		}
		return nil, documentFailure("DOCUMENT_ACTION_FAILED", 500)
	}
	session, err := GetSession(ctx, db, revision.SessionID)
	if err != nil {
		return nil, documentFailure("DOCUMENT_ACTION_FAILED", 500)
	}
	if err := checkDocumentTarget(session, revision, owner, request); err != nil {
		return nil, err
	}
	artifact, err := repo.ReadArtifact(ctx, owner, revision.ID)
	if err != nil {
		return nil, documentFailure("DOCUMENT_ACTION_FAILED", 500)
	}
	if !artifact.Selected || artifact.Validity != "effective" || artifact.Revision != *request.baseRevision {
		return nil, documentFailure("REVISION_CONFLICT", 409)
	}
	if revision.HumanArtifactID != nil {
		if request.baseDraftVersion == nil {
			return nil, documentFailure("DRAFT_VERSION_REQUIRED", 400)
		}
		if artifact.DraftVersion != *request.baseDraftVersion {
			return nil, documentFailure("DRAFT_VERSION_CONFLICT", 409)
		}
	}
	if checkLive {
		if err := artifactgraph.CheckConsumers(ctx, db, session.ID, revision.ID); err != nil {
			if errors.Is(err, ErrArtifactInUse) {
				return nil, documentFailure("ARTIFACT_IN_USE", 409)
			}
			return nil, documentFailure("DOCUMENT_ACTION_FAILED", 500)
		}
	}
	content, inspectionError := document.InspectContent(ctx, artifact.Value, artifact.ContentType, func() (bool, error) { return repo.PinnedMarkdownHint(ctx, session, revision.SlotID) })
	if inspectionError != nil {
		return nil, documentFailure("DOCUMENT_ACTION_FAILED", 502)
	}
	if content == nil {
		return nil, documentFailure("DOCUMENT_ACTION_UNSUPPORTED", 422)
	}
	return &documentActionContext{db: db, owner: owner, session: session, revision: revision, artifact: artifact, content: content}, nil
}

func checkDocumentTarget(session *orm.WorkflowSession, revision orm.WorkflowSlotRevision, owner string, request documentActionRequest) error {
	if session.CreateUserID != owner {
		return documentFailure("PERMISSION_DENIED", 403)
	}
	if session.Dismissed {
		return documentFailure("ARTIFACT_NOT_FOUND", 404)
	}
	if !workflowstore.DocumentSessionEditable(session) {
		return documentFailure("SESSION_NOT_EDITABLE", 409)
	}
	if !revision.Selected || revision.Validity != "effective" || revision.Revision != *request.baseRevision {
		return documentFailure("REVISION_CONFLICT", 409)
	}
	return nil
}

func validDocumentPreview(result DocumentRewritePreviewResult, representation string) bool {
	patchType := "string_replace_set"
	if representation == "ir" {
		patchType = "writer_ir_patch"
	}
	return result.Representation == representation && result.Target != nil && result.Target.Type == "block" && result.Target.BlockType != "" && result.Preview != nil && result.Preview.OldText != nil && result.Preview.NewText != nil && result.Patch != nil && result.Patch.Type == patchType && result.Patch.Payload != nil && result.Commit != nil && documentCommitToken.MatchString(result.Commit.Token)
}

func validDocumentResult(ctx context.Context, artifact *DocumentActionArtifact, representation string) bool {
	if artifact == nil || len(artifact.Value) == 0 || len(artifact.Value) > documentActionMaxBytes {
		return false
	}
	if representation == "markdown" {
		var text *string
		return (artifact.ContentType == "text" || artifact.ContentType == "text/markdown") && json.Unmarshal(artifact.Value, &text) == nil && text != nil
	}
	if artifact.ContentType != "json" && artifact.ContentType != document.IRSchema {
		return false
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(artifact.Value, &value) != nil || value == nil {
		return false
	}
	var id string
	if json.Unmarshal(value["document_id"], &id) != nil || strings.TrimSpace(id) == "" {
		return false
	}
	for _, key := range []string{"path", "url", "data", "text"} {
		if _, exists := value[key]; exists {
			return false
		}
	}
	envelope, _ := json.Marshal(map[string]json.RawMessage{"data": artifact.Value})
	result, _, err := algo.InspectDocument(ctx, algo.DocumentInspectRequest{Artifact: envelope, Schema: document.IRSchema})
	return err == nil && *result.IsDocument && *result.Representation == "ir"
}

func documentUpstreamFailure(status int, err error) error {
	var upstream *common.HTTPError
	if errors.As(err, &upstream) {
		var body struct {
			Detail struct {
				Code string `json:"code"`
			} `json:"detail"`
		}
		if json.Unmarshal(upstream.Body, &body) == nil {
			switch {
			case status == 409 && (body.Detail.Code == "SELECTION_STALE" || body.Detail.Code == "SELECTION_AMBIGUOUS"):
				return documentFailure(body.Detail.Code, 409)
			case status == 422 && body.Detail.Code == "WORKFLOW_ACTION_INVALID":
				return documentFailure("DOCUMENT_ACTION_INVALID", 400)
			case status == 502 && body.Detail.Code == "WORKFLOW_ACTION_RESULT_INVALID":
				return documentFailure("DOCUMENT_ACTION_RESULT_INVALID", 502)
			}
		}
	}
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &syntaxError) || errors.As(err, &typeError) {
		return documentFailure("DOCUMENT_ACTION_RESULT_INVALID", 502)
	}
	return documentFailure("DOCUMENT_ACTION_FAILED", 502)
}

func commitDocumentRewrite(ctx context.Context, target *documentActionContext, request documentActionRequest, artifact *DocumentActionArtifact) (*orm.WorkflowSlotRevision, error) {
	contentType := "text/markdown"
	value := artifact.Value
	if target.content.Representation == "ir" {
		contentType = "json"
		value, _ = json.Marshal(map[string]any{"schema": document.IRSchema, "data": artifact.Value})
	}
	cardinality := "single"
	if target.revision.ListIndex != nil {
		cardinality = "list"
	}
	var saved *orm.WorkflowSlotRevision
	err := common.TransactionWithSQLiteBusyRetry(ctx, target.db, func(tx *gorm.DB) error {
		saved = nil
		session, err := artifactgraph.LockSession(tx, target.session.ID)
		if err != nil {
			return err
		}
		if scope := workflowstore.ConversationScope(ctx); scope != "" && scope != session.ConversationID {
			return documentFailure("PERMISSION_DENIED", 403)
		}
		var current orm.WorkflowSlotRevision
		if err := tx.Where("id = ? AND session_id = ?", target.revision.ID, session.ID).First(&current).Error; err != nil {
			return err
		}
		if err := checkDocumentTarget(session, current, target.owner, request); err != nil {
			return err
		}
		saved, err = WriteSlotRevisionWithHumanArtifact(ctx, tx, current.SessionID, current.SlotID, current.Slot, current.StepID, current.Attempt, cardinality, current.ListIndex, contentType, value, artifact.Caption, "human", request.baseRevision, request.baseDraftVersion)
		return err
	})
	if err != nil {
		var failure documentActionFailure
		if errors.As(err, &failure) {
			return nil, failure
		}
		switch {
		case errors.Is(err, ErrConflict):
			return nil, documentFailure("REVISION_CONFLICT", 409)
		case errors.Is(err, ErrDraftVersionConflict):
			return nil, documentFailure("DRAFT_VERSION_CONFLICT", 409)
		case errors.Is(err, ErrDraftVersionRequired):
			return nil, documentFailure("DRAFT_VERSION_REQUIRED", 400)
		case errors.Is(err, ErrArtifactInUse):
			return nil, documentFailure("ARTIFACT_IN_USE", 409)
		default:
			return nil, documentFailure("DOCUMENT_ACTION_SAVE_FAILED", 500)
		}
	}
	return saved, nil
}
