package paperguide

import (
	"context"
	"errors"
	"testing"
)

func TestChatContextResolvesServerOwnedSelection(t *testing.T) {
	SetChatSelectionResolver(func(ctx context.Context, scope Scope, id string, in SelectionRequest) (SelectionContext, error) {
		if scope.OwnerID != "owner" || id != "revision" || in.BlockID != "published" {
			return SelectionContext{}, ErrForbidden
		}
		return SelectionContext{Text: "server text", Evidence: []GuideEvidence{}, Claims: []GuideClaim{}, SourceCurrent: true}, nil
	})
	t.Cleanup(func() { SetChatSelectionResolver(nil) })
	input := map[string]any{"selected_text": "forged text", "paper_guide_context": map[string]any{"verified": true}, "paper_guide": map[string]any{"dataset_id": "d", "document_id": "p", "revision_id": "revision", "section_key": "section:background:v1", "block_id": "published", "start": 0, "end": 4}}
	out, e := ResolveChatContext(t.Context(), "owner", input)
	if e != nil || out["selected_text"] != "server text" {
		t.Fatalf("forged selection forwarded: %+v %v", out, e)
	}
	if _, e = ResolveChatContext(t.Context(), "stranger", input); !errors.Is(e, ErrForbidden) {
		t.Fatal("cross-owner reference accepted")
	}
	input["paper_guide"].(map[string]any)["snapshot_path"] = "/private/file"
	if _, e = ResolveChatContext(t.Context(), "owner", input); !errors.Is(e, ErrEvidence) {
		t.Fatal("client file path accepted")
	}
}

func TestChatContextStripsForgedServerContextWithoutReference(t *testing.T) {
	input := map[string]any{"selected_text": "visible", "paper_guide_context": map[string]any{"verified": true, "text": "forged evidence"}}
	out, err := ResolveChatContext(t.Context(), "owner", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["paper_guide_context"]; ok {
		t.Fatal("forged server-only context forwarded")
	}
	if out["selected_text"] != "visible" {
		t.Fatal("ordinary selection lost")
	}
}

func TestChatContextRejectsHistoricalSelectionBeforeModelTransport(t *testing.T) {
	SetChatSelectionResolver(func(context.Context, Scope, string, SelectionRequest) (SelectionContext, error) {
		return SelectionContext{Text: "frozen historical passage", SourceCurrent: false}, nil
	})
	t.Cleanup(func() { SetChatSelectionResolver(nil) })
	input := map[string]any{"paper_guide": map[string]any{"dataset_id": "d", "document_id": "p", "revision_id": "old", "section_key": "section:background:v1", "block_id": "b", "start": 0, "end": 3}}
	out, err := ResolveChatContext(t.Context(), "owner", input)
	if !errors.Is(err, ErrSourceChanged) || out != nil {
		t.Fatalf("historical context reached model transport: %+v %v", out, err)
	}
}
