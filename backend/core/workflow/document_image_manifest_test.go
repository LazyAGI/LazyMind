package workflow

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDocumentImageReferencesUseOnlyRenderedImages(t *testing.T) {
	source := "# Article\n![First](asset://one)\n![Repeated][photo]\n[photo]: asset://one\n\n[ordinary link](https://example.org/not-an-image.png)\n\n```md\n![example](asset://unused)\n```\n`![inline](asset://unused)`\n\n<img src=\"https://example.org/two.png?a=1&amp;b=2\" alt=\"Second\">\n<!-- <img src=\"secret.png\"> -->"
	raw, _ := json.Marshal(source)
	refs, _, err := documentImageReferences(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []imageReference{{Source: "asset://one", Alt: "First"}, {Source: "https://example.org/two.png?a=1&b=2", Alt: "Second"}}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("got %#v, want %#v", refs, want)
	}
}

func TestDocumentImageReferencesTraverseIRAndIgnorePreviewAliases(t *testing.T) {
	raw := json.RawMessage(`{"title":"Article","blocks":[{"type":"section","children":[{"type":"image","content":"First","references":[{"type":"preview_asset","url":"https://preview.invalid/thumbnail"},{"type":"media_asset","id":"one","path":"assets/one.png"}]}]},{"type":"paragraph","content":"![not rendered](unused.png)"},{"type":"image","content":"Missing image"}]}`)
	refs, title, err := documentImageReferences(raw)
	if err != nil || title != "Article" || len(refs) != 2 || refs[0].Source != "asset://one" || refs[0].Fallback != "assets/one.png" || refs[1].Source != "" {
		t.Fatalf("references=%#v title=%q err=%v", refs, title, err)
	}
}

func TestDocumentImageReferencesRejectInvalidAndExcessiveInput(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"path":"/etc/passwd"}`, `{"blocks":"invalid"}`} {
		if _, _, err := documentImageReferences(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	blocks := make([]map[string]any, maxDocumentImages+1)
	for i := range blocks {
		blocks[i] = map[string]any{"type": "image"}
	}
	raw, _ := json.Marshal(map[string]any{"blocks": blocks})
	if _, _, err := documentImageReferences(raw); err == nil {
		t.Fatal("accepted too many images")
	}
}
