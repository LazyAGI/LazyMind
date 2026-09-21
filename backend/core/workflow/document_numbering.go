package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"lazymind/core/algo"
	"lazymind/core/common"
)

type DocumentNumberingUpdate struct {
	Type         string  `json:"type" enum:"ordered_style,heading"`
	OrderedStyle *string `json:"ordered_style,omitempty" enum:"hierarchical,chinese,parenthesized"`
	TargetID     *string `json:"target_id,omitempty"`
	Mode         *string `json:"mode,omitempty" enum:"ordered,unordered"`
	Restart      *bool   `json:"restart,omitempty"`
}

func (u *DocumentNumberingUpdate) valid() bool {
	if u == nil {
		return false
	}
	switch u.Type {
	case "ordered_style":
		return u.OrderedStyle != nil && (*u.OrderedStyle == "hierarchical" || *u.OrderedStyle == "chinese" || *u.OrderedStyle == "parenthesized") && u.TargetID == nil && u.Mode == nil && u.Restart == nil
	case "heading":
		return u.OrderedStyle == nil && u.TargetID != nil && strings.TrimSpace(*u.TargetID) != "" && (u.Mode != nil || u.Restart != nil) && (u.Mode == nil || *u.Mode == "ordered" || *u.Mode == "unordered") && !(u.Mode != nil && *u.Mode == "unordered" && u.Restart != nil && *u.Restart)
	}
	return false
}

type DocumentNumberingPreviewInput struct{}
type DocumentNumberingExecuteInput struct {
	NumberingUpdate *DocumentNumberingUpdate `json:"numbering_update" required:"true"`
}
type DocumentNumberingPreviewRequest struct {
	Action           string                         `json:"action" enum:"numbering"`
	BaseRevision     *int                           `json:"base_revision" required:"true"`
	BaseDraftVersion *int64                         `json:"base_draft_version,omitempty"`
	Input            *DocumentNumberingPreviewInput `json:"input" required:"true"`
}
type DocumentNumberingExecuteRequest struct {
	Action           string                         `json:"action" enum:"numbering"`
	BaseRevision     *int                           `json:"base_revision" required:"true"`
	BaseDraftVersion *int64                         `json:"base_draft_version,omitempty"`
	Input            *DocumentNumberingExecuteInput `json:"input" required:"true"`
}
type DocumentNumberingEntry struct {
	Label   *string `json:"label" required:"true"`
	Mode    *string `json:"mode,omitempty" enum:"ordered,unordered"`
	Restart *bool   `json:"restart,omitempty"`
}
type DocumentNumberingView struct {
	OrderedStyle string                            `json:"ordered_style" enum:"hierarchical,chinese,parenthesized"`
	Entries      map[string]DocumentNumberingEntry `json:"entries" required:"true"`
}
type DocumentNumberingResult struct {
	Title          *string                `json:"title" required:"true"`
	Representation string                 `json:"representation" enum:"markdown,ir"`
	Document       json.RawMessage        `json:"document" required:"true"`
	Numbering      *DocumentNumberingView `json:"numbering" required:"true"`
	ExportDocument *string                `json:"export_document,omitempty"`
}
type DocumentNumberingExecuteResult struct {
	Title          *string                `json:"title" required:"true"`
	Representation string                 `json:"representation" enum:"markdown,ir"`
	Document       json.RawMessage        `json:"document" required:"true"`
	Numbering      *DocumentNumberingView `json:"numbering" required:"true"`
	ExportDocument *string                `json:"export_document,omitempty"`
	ArtifactID     string                 `json:"artifact_id"`
	Revision       int                    `json:"revision"`
	DraftVersion   int64                  `json:"draft_version"`
}
type documentNumberingAlgorithmResult struct {
	DocumentNumberingResult
	SourceDocument json.RawMessage `json:"source_document,omitempty"`
}

func runDocumentNumbering(w http.ResponseWriter, r *http.Request, phase, owner string, raw []byte) {
	request := documentActionRequest{}
	var update *DocumentNumberingUpdate
	invalid := false
	if phase == "preview" {
		var body DocumentNumberingPreviewRequest
		invalid = decodeDocumentJSON(bytes.NewReader(raw), &body) != nil || body.Input == nil
		request.baseRevision, request.baseDraftVersion = body.BaseRevision, body.BaseDraftVersion
	} else {
		var body DocumentNumberingExecuteRequest
		invalid = decodeDocumentJSON(bytes.NewReader(raw), &body) != nil || body.Input == nil
		request.baseRevision, request.baseDraftVersion = body.BaseRevision, body.BaseDraftVersion
		if !invalid {
			update = body.Input.NumberingUpdate
			invalid = !update.valid()
		}
	}
	if invalid {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
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
	target, err := prepareDocumentAction(r.Context(), owner, common.PathVar(r, "artifact_id"), request, phase == "execute")
	if err != nil {
		replyDocumentFailure(w, err)
		return
	}
	artifact, _ := json.Marshal(map[string]json.RawMessage{"data": target.content.Value})
	reference := "builtin:document.render_document.v1"
	arguments := map[string]any{}
	if phase == "execute" {
		reference = "builtin:document.save_document.v1"
		arguments = map[string]any{"base_artifact": json.RawMessage(artifact), "numbering_update": update}
	}
	response, status, err := algo.InvokeDocumentAction(r.Context(), algo.DocumentActionInvokeRequest{Reference: reference, Phase: phase, Artifact: artifact, Arguments: arguments})
	if err != nil {
		replyDocumentFailure(w, documentUpstreamFailure(status, err))
		return
	}
	var result documentNumberingAlgorithmResult
	if decodeDocumentJSON(bytes.NewReader(response.Result), &result) != nil || !validNumberingView(result.DocumentNumberingResult, target.content.Representation) || !validNumberingDocument(r.Context(), result.Document, target.content.Representation) || (phase == "execute" && !validNumberingDocument(r.Context(), result.SourceDocument, target.content.Representation)) {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_RESULT_INVALID", 502))
		return
	}
	if r.Context().Err() != nil {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_FAILED", 502))
		return
	}
	if phase == "preview" {
		common.ReplyOK(w, result.DocumentNumberingResult)
		return
	}
	contentType := "text/markdown"
	if target.content.Representation == "ir" {
		contentType = "json"
	}
	saved, err := commitDocumentRewrite(r.Context(), target, request, &DocumentActionArtifact{ContentType: contentType, Value: result.SourceDocument})
	if err != nil {
		replyDocumentFailure(w, err)
		return
	}
	NotifyWorkflowArtifactUpdated(r.Context(), target.db, saved.SessionID, saved.StepID, saved.SlotID, saved.Slot, saved.Revision, saved.ListIndex, "human")
	common.ReplyOK(w, DocumentNumberingExecuteResult{Title: result.Title, Representation: result.Representation, Document: result.Document, Numbering: result.Numbering, ExportDocument: result.ExportDocument, ArtifactID: saved.ID, Revision: saved.Revision, DraftVersion: 1})
}
func validNumberingView(result DocumentNumberingResult, representation string) bool {
	if result.Title == nil || result.Representation != representation || result.Numbering == nil || result.Numbering.Entries == nil {
		return false
	}
	switch result.Numbering.OrderedStyle {
	case "hierarchical", "chinese", "parenthesized":
	default:
		return false
	}
	for id, entry := range result.Numbering.Entries {
		if strings.TrimSpace(id) == "" || entry.Label == nil || (entry.Mode != nil && *entry.Mode != "ordered" && *entry.Mode != "unordered") {
			return false
		}
	}
	return true
}
func validNumberingDocument(ctx context.Context, raw json.RawMessage, representation string) bool {
	contentType := "text/markdown"
	if representation == "ir" {
		contentType = "json"
		// Reject malformed transport shapes before the authoritative IR inspection.
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || object == nil {
			return false
		}
		var blocks []json.RawMessage
		if rawBlocks, exists := object["blocks"]; exists && (json.Unmarshal(rawBlocks, &blocks) != nil || blocks == nil) {
			return false
		}
	}
	return validDocumentResult(ctx, &DocumentActionArtifact{ContentType: contentType, Value: raw}, representation)
}
