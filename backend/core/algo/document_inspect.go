package algo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"lazymind/core/common"
)

type DocumentInspectRequest struct {
	Artifact json.RawMessage `json:"artifact"`
	Schema   string          `json:"schema,omitempty"`
}

type DocumentInspection struct {
	IsDocument     *bool                  `json:"is_document"`
	Representation *string                `json:"representation"`
	Schema         *string                `json:"schema"`
	Features       map[string]*bool       `json:"features"`
	RenderContext  *DocumentRenderContext `json:"render_context,omitempty"`
}

// InspectDocument uses the read-only Algorithm contract, without workflow or credential context.
func InspectDocument(ctx context.Context, req DocumentInspectRequest) (*DocumentInspection, int, error) {
	var out DocumentInspection
	err := common.ApiPost(ctx, common.JoinURL(common.ChatServiceEndpoint(), "/api/document:inspect"), req, nil, &out, 5*time.Second)
	if err != nil {
		return nil, workflowActionHTTPStatus(err), err
	}
	if out.IsDocument == nil {
		return nil, 0, errors.New("invalid document inspection")
	}
	for _, feature := range []string{"headings", "numbering", "cross_references", "provider_binding"} {
		if value, ok := out.Features[feature]; !ok || value == nil {
			return nil, 0, errors.New("incomplete document inspection")
		}
	}
	if *out.IsDocument {
		if out.Representation == nil || out.Schema == nil ||
			!((*out.Representation == "markdown" && *out.Schema == "text/markdown") ||
				(*out.Representation == "ir" && *out.Schema == "application/vnd.lazymind.writer+json")) {
			return nil, 0, errors.New("invalid document representation")
		}
	} else {
		if out.Representation != nil || out.Schema != nil {
			return nil, 0, errors.New("invalid non-document inspection")
		}
		for _, enabled := range out.Features {
			if enabled != nil && *enabled {
				return nil, 0, errors.New("invalid non-document features")
			}
		}
	}
	return &out, 0, nil
}
