package artifactfile

import (
	"encoding/json"
	"testing"
)

func TestImageReferencesPreserveRemoteSignaturesAndMetadata(t *testing.T) {
	t.Setenv("LAZYMIND_PUBLIC_BASE_URL", "https://core.example/team/api/core")
	for _, source := range []string{
		"https://images.example/image.png?token=provider-signature",
		"https://images.example/static-files/image.png?token=a%2Fb+c&part=1&part=2#preview",
		"https://images.example/api/core/static-files/image.png?token=provider-signature",
		"https://core.example/other/static-files/image.png?token=provider-signature",
		"data:image/png;base64,eA==",
		"data:image/svg+xml,<svg>/static-files/figure.png</svg>",
	} {
		input, _ := json.Marshal(map[string]any{"url": source, "caption": "image caption", "width": 1280})
		raw, err := NormalizeImageValue("image/png", input)
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil || value["path"] != source || value["caption"] != "image caption" || value["width"] != float64(1280) {
			t.Fatalf("image data changed: %s %v", raw, err)
		}
	}
}

func TestImageReferencesNormalizeCoreURLs(t *testing.T) {
	t.Setenv("LAZYMIND_PUBLIC_BASE_URL", "https://core.example/team/api/core/")
	for _, source := range []string{
		"/static-files/figure%20one.png?expires=1&sig=old",
		"/api/core/static-files/figure%20one.png?expires=1&sig=old",
		"https://core.example/team/api/core/static-files/figure%20one.png?expires=1&sig=old",
	} {
		for _, input := range []any{source, map[string]any{"url": source}, map[string]any{"path": source}} {
			raw, _ := json.Marshal(input)
			normalized, err := NormalizeImageValue("image", raw)
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(normalized, &value); err != nil || value["path"] != "/static-files/figure%20one.png" {
				t.Fatalf("Core URL was not normalized: %s (%v)", normalized, err)
			}
		}
	}
}
