package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"lazymind/core/algo"
	"lazymind/core/common"
)

const documentConvertReference = "builtin:document.convert_document.v1"

// DocumentConvertSnapshot is inline editor content. It is not a carrier and
// must never be passed through path or URL resolution.
type DocumentConvertSnapshot json.RawMessage

func (snapshot *DocumentConvertSnapshot) UnmarshalJSON(raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	switch value.(type) {
	case string, map[string]any:
	default:
		return errors.New("snapshot must be a string or IR object")
	}
	*snapshot = append((*snapshot)[:0], raw...)
	return nil
}
func (snapshot DocumentConvertSnapshot) MarshalJSON() ([]byte, error) {
	return json.RawMessage(snapshot).MarshalJSON()
}

type DocumentConvertPreviewInput struct {
	OutputFormat string                  `json:"output_format" enum:"markdown,latex,text"`
	Document     DocumentConvertSnapshot `json:"document,omitempty"`
}
type DocumentConvertPreviewRequest struct {
	Action           string                       `json:"action" enum:"convert_document"`
	BaseRevision     *int                         `json:"base_revision" required:"true"`
	BaseDraftVersion *int64                       `json:"base_draft_version,omitempty"`
	Input            *DocumentConvertPreviewInput `json:"input" required:"true"`
}
type DocumentConvertResult struct {
	Provider string `json:"provider"`
	Format   string `json:"format"`
	Content  string `json:"content"`
}

type documentConvertAlgorithmResult struct {
	Provider        *string         `json:"provider"`
	Format          *string         `json:"format"`
	Content         *string         `json:"content"`
	SourceDocument  json.RawMessage `json:"source_document"`
	MediaReferences json.RawMessage `json:"media_references"`
}

func runDocumentConvert(w http.ResponseWriter, r *http.Request, owner string, raw []byte) {
	var body DocumentConvertPreviewRequest
	if decodeDocumentJSON(bytes.NewReader(raw), &body) != nil || body.Input == nil || strings.TrimSpace(body.Input.OutputFormat) == "" {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
		return
	}
	switch body.Input.OutputFormat {
	case "markdown", "latex", "text":
	default:
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_UNSUPPORTED", 422))
		return
	}
	if body.BaseRevision == nil {
		replyDocumentFailure(w, documentFailure("REVISION_REQUIRED", 400))
		return
	}
	if *body.BaseRevision < 1 || (body.BaseDraftVersion != nil && *body.BaseDraftVersion < 1) {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
		return
	}
	request := documentActionRequest{baseRevision: body.BaseRevision, baseDraftVersion: body.BaseDraftVersion}
	target, err := prepareDocumentAction(r.Context(), owner, common.PathVar(r, "artifact_id"), request, false)
	if err != nil {
		replyDocumentFailure(w, err)
		return
	}
	if len(body.Input.Document) > 0 && !validPortableSnapshot(body.Input.Document, target.content.Representation) {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_INVALID", 400))
		return
	}
	artifact, _ := json.Marshal(map[string]json.RawMessage{"data": target.content.Value})
	response, status, err := algo.InvokeDocumentAction(r.Context(), algo.DocumentActionInvokeRequest{
		Reference: documentConvertReference, Phase: "preview", Artifact: artifact, Arguments: body.Input,
	})
	if err != nil {
		replyDocumentFailure(w, documentUpstreamFailure(status, err))
		return
	}
	var result documentConvertAlgorithmResult
	if decodeDocumentJSON(bytes.NewReader(response.Result), &result) != nil || result.Provider == nil || *result.Provider != "" || result.Format == nil || *result.Format != body.Input.OutputFormat || result.Content == nil {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_RESULT_INVALID", 502))
		return
	}
	if r.Context().Err() != nil {
		replyDocumentFailure(w, documentFailure("DOCUMENT_ACTION_FAILED", 502))
		return
	}
	// Do not expose the converter's internal IR/media projection or imply a save.
	common.ReplyOK(w, DocumentConvertResult{Provider: "", Format: *result.Format, Content: *result.Content})
}

func validPortableSnapshot(raw DocumentConvertSnapshot, representation string) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	if representation == "markdown" {
		_, ok := value.(string)
		return ok
	}
	if representation != "ir" {
		return false
	}
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for _, key := range []string{"path", "url", "data", "text"} {
		if _, exists := object[key]; exists {
			return false
		}
	}
	// Full Writer IR semantics belong to the pure converter, not a Go clone.
	return true
}
