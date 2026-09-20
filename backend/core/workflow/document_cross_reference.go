package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/subagent"
)

const documentCrossReferenceList = "builtin:document.list_cross_reference_targets.v1"
const documentCrossReferenceUpdate = "builtin:document.update_cross_reference.v1"

type DocumentCrossReferenceSelection struct {
	Type         string `json:"type"`
	SelectedText string `json:"selected_text"`
	NodeID       string `json:"node_id,omitempty"`
}

func (selection *DocumentCrossReferenceSelection) UnmarshalJSON(raw []byte) error {
	type plain DocumentCrossReferenceSelection
	var value plain
	if err := decodeDocumentJSON(bytes.NewReader(raw), &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	count := 2
	if value.Type == "ir" {
		count = 3
		if strings.TrimSpace(value.NodeID) == "" {
			return documentFailure("DOCUMENT_ACTION_INVALID", 400)
		}
	} else if value.Type != "markdown" {
		return documentFailure("DOCUMENT_ACTION_INVALID", 400)
	}
	if len(fields) != count || strings.TrimSpace(value.SelectedText) == "" {
		return documentFailure("DOCUMENT_ACTION_INVALID", 400)
	}
	*selection = DocumentCrossReferenceSelection(value)
	return nil
}

type DocumentCrossReferencePreviewInput struct {
	Operation string                           `json:"operation" enum:"list_targets,add,remove,retarget"`
	Selection *DocumentCrossReferenceSelection `json:"selection,omitempty" desc:"Required for add, remove and retarget; absent for list_targets."`
	TargetID  *string                          `json:"target_id,omitempty" desc:"Required for add and retarget; absent for list_targets and remove."`
}

func (input *DocumentCrossReferencePreviewInput) UnmarshalJSON(raw []byte) error {
	type plain DocumentCrossReferencePreviewInput
	var value plain
	if err := decodeDocumentJSON(bytes.NewReader(raw), &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	valid := false
	switch value.Operation {
	case "list_targets":
		valid = len(fields) == 1
	case "remove":
		valid = len(fields) == 2 && value.Selection != nil && value.TargetID == nil
	case "add", "retarget":
		valid = len(fields) == 3 && value.Selection != nil && value.TargetID != nil && strings.TrimSpace(*value.TargetID) != ""
	}
	if !valid {
		return documentFailure("DOCUMENT_ACTION_INVALID", 400)
	}
	*input = DocumentCrossReferencePreviewInput(value)
	return nil
}

type DocumentCrossReferencePreviewRequest struct {
	Action           string                              `json:"action" enum:"cross_reference"`
	BaseRevision     *int                                `json:"base_revision" required:"true"`
	BaseDraftVersion *int64                              `json:"base_draft_version,omitempty"`
	Input            *DocumentCrossReferencePreviewInput `json:"input" required:"true"`
}
type DocumentCrossReferenceExecuteRequest struct {
	Action           string                       `json:"action" enum:"cross_reference"`
	BaseRevision     *int                         `json:"base_revision" required:"true"`
	BaseDraftVersion *int64                       `json:"base_draft_version,omitempty"`
	Input            *DocumentRewriteExecuteInput `json:"input" required:"true"`
}
type DocumentCrossReferenceTarget struct {
	TargetID *string `json:"target_id" required:"true"`
	Type     string  `json:"type" enum:"heading,image"`
	Title    *string `json:"title" required:"true"`
}
type DocumentInvalidCrossReference struct {
	TargetID *string `json:"target_id" required:"true"`
}
type DocumentCrossReferenceTargetsResult struct {
	Representation    string                           `json:"representation" enum:"markdown,ir"`
	Targets           []*DocumentCrossReferenceTarget  `json:"targets" required:"true"`
	InvalidReferences []*DocumentInvalidCrossReference `json:"invalid_references" required:"true"`
}
type DocumentCrossReferencePreviewResult struct {
	Representation string                  `json:"representation" enum:"markdown,ir"`
	Operation      string                  `json:"operation" enum:"add,remove,retarget"`
	Patch          *DocumentRewritePatch   `json:"patch" required:"true"`
	Artifact       *DocumentActionArtifact `json:"artifact" required:"true"`
	Commit         *DocumentRewriteCommit  `json:"commit" required:"true"`
}

func runDocumentCrossReference(w http.ResponseWriter, r *http.Request, phase, owner string, raw []byte) {
	request := documentActionRequest{}
	operation := ""
	if phase == "preview" {
		var body DocumentCrossReferencePreviewRequest
		if decodeDocumentJSON(bytes.NewReader(raw), &body) != nil || body.Input == nil {
			replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
			return
		}
		request.baseRevision, request.baseDraftVersion = body.BaseRevision, body.BaseDraftVersion
		request.arguments = body.Input
		operation = body.Input.Operation
		if body.Input.Selection != nil {
			request.selectionType = body.Input.Selection.Type
		}
	} else {
		var body DocumentCrossReferenceExecuteRequest
		if decodeDocumentJSON(bytes.NewReader(raw), &body) != nil || body.Input == nil || !documentCommitToken.MatchString(body.Input.CommitToken) {
			replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
			return
		}
		request.baseRevision, request.baseDraftVersion = body.BaseRevision, body.BaseDraftVersion
		request.arguments = body.Input
	}
	if request.baseRevision == nil {
		replyDocumentFailure(w, documentFailure("REVISION_REQUIRED", 400))
		return
	}
	if *request.baseRevision < 1 || (request.baseDraftVersion != nil && *request.baseDraftVersion < 1) {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
		return
	}
	lookup := phase == "preview" && operation == "list_targets"
	target, err := prepareDocumentAction(r.Context(), owner, common.PathVar(r, "artifact_id"), request, !lookup)
	if err != nil {
		replyDocumentFailure(w, err)
		return
	}
	if request.selectionType != "" && request.selectionType != target.content.Representation {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
		return
	}
	reference, artifactStore := documentCrossReferenceUpdate, ""
	if lookup {
		reference = documentCrossReferenceList
		request.arguments = map[string]any{}
	} else {
		namespace, _ := json.Marshal([]any{owner, target.session.ID, target.revision.ID, *request.baseRevision, target.artifact.DraftVersion, reference})
		digest := sha256.Sum256(namespace)
		artifactStore = filepath.Join(subagent.WorkspaceRoot(), "document-actions", hex.EncodeToString(digest[:]))
	}
	artifact, _ := json.Marshal(map[string]json.RawMessage{"data": target.content.Value})
	response, status, err := algo.InvokeDocumentAction(r.Context(), algo.DocumentActionInvokeRequest{Reference: reference, Phase: phase, Artifact: artifact, Arguments: request.arguments, ArtifactStore: artifactStore})
	if err != nil {
		replyDocumentFailure(w, crossReferenceUpstreamFailure(status, err))
		return
	}
	if lookup {
		var result DocumentCrossReferenceTargetsResult
		if decodeDocumentJSON(bytes.NewReader(response.Result), &result) != nil || !validCrossReferenceTargets(result, target.content.Representation) {
			replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_RESULT_INVALID", 502))
			return
		}
		if r.Context().Err() != nil {
			replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_FAILED", 502))
			return
		}
		common.ReplyOK(w, result)
		return
	}
	if phase == "preview" {
		var result DocumentCrossReferencePreviewResult
		patchType := "string_replace_set"
		if target.content.Representation == "ir" {
			patchType = "writer_ir_patch"
		}
		if decodeDocumentJSON(bytes.NewReader(response.Result), &result) != nil || result.Representation != target.content.Representation || result.Operation != operation || result.Patch == nil || result.Patch.Type != patchType || result.Patch.Payload == nil || result.Commit == nil || !documentCommitToken.MatchString(result.Commit.Token) || !validDocumentResult(r.Context(), result.Artifact, target.content.Representation) {
			replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_RESULT_INVALID", 502))
			return
		}
		if r.Context().Err() != nil {
			replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_FAILED", 502))
			return
		}
		common.ReplyOK(w, result)
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
func validCrossReferenceTargets(result DocumentCrossReferenceTargetsResult, representation string) bool {
	if result.Representation != representation || result.Targets == nil || result.InvalidReferences == nil {
		return false
	}
	for _, target := range result.Targets {
		if target == nil || target.TargetID == nil || strings.TrimSpace(*target.TargetID) == "" || target.Title == nil || (target.Type != "heading" && target.Type != "image") {
			return false
		}
	}
	for _, reference := range result.InvalidReferences {
		if reference == nil || reference.TargetID == nil || strings.TrimSpace(*reference.TargetID) == "" {
			return false
		}
	}
	return true
}
func crossReferenceUpstreamFailure(status int, err error) error {
	var upstream *common.HTTPError
	if status == 422 && errors.As(err, &upstream) {
		var body struct {
			Detail struct {
				Code string `json:"code"`
			} `json:"detail"`
		}
		if json.Unmarshal(upstream.Body, &body) == nil {
			switch body.Detail.Code {
			case "CROSS_REFERENCE_SELECTION_INVALID", "CROSS_REFERENCE_TARGET_NOT_FOUND":
				return documentFailure(body.Detail.Code, 400)
			}
		}
	}
	return documentUpstreamFailure(status, err)
}
