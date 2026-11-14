package paperguide

import (
	"context"
	"encoding/json"
	"sync"
)

// ChatSelectionReference contains only identifiers and rune offsets. The chat
// transport resolves it again using the authenticated conversation owner.
type ChatSelectionReference struct {
	Scope
	RevisionID string `json:"revision_id"`
	SelectionRequest
}

var chatResolver struct {
	sync.RWMutex
	resolve func(context.Context, Scope, string, SelectionRequest) (SelectionContext, error)
}

func SetChatSelectionResolver(fn func(context.Context, Scope, string, SelectionRequest) (SelectionContext, error)) {
	chatResolver.Lock()
	defer chatResolver.Unlock()
	chatResolver.resolve = fn
}
func ResolveChatContext(ctx context.Context, user string, input map[string]any) (map[string]any, error) {
	raw, ok := input["paper_guide"]
	if !ok {
		// This field is exclusively produced by the authenticated resolver.
		clean := make(map[string]any, len(input))
		for key, value := range input {
			if key != "paper_guide_context" {
				clean[key] = value
			}
		}
		return clean, nil
	}
	b, e := json.Marshal(raw)
	if e != nil {
		return nil, ErrEvidence
	}
	var ref ChatSelectionReference
	if e = decodeStrict(b, &ref); e != nil {
		return nil, ErrEvidence
	}
	ref.OwnerID = user
	if ref.Language == "" {
		ref.Language = "zh"
	}
	if user == "" || ref.DatasetID == "" || ref.DocumentID == "" || ref.RevisionID == "" || ref.Language != "zh" && ref.Language != "en" {
		return nil, ErrEvidence
	}
	chatResolver.RLock()
	resolve := chatResolver.resolve
	chatResolver.RUnlock()
	if resolve == nil {
		return nil, ErrEvidence
	}
	result, e := resolve(ctx, ref.Scope, ref.RevisionID, ref.SelectionRequest)
	if e != nil {
		return nil, e
	}
	// Read-only historical selections remain available, but they must not be
	// attached to a new chat against the current source projection.
	if !result.SourceCurrent {
		return nil, ErrSourceChanged
	}
	return map[string]any{"dataset_id": ref.DatasetID, "document_id": ref.DocumentID, "selected_text": result.Text, "paragraph_text": result.Text, "paper_guide": ref, "paper_guide_context": result}, nil
}
