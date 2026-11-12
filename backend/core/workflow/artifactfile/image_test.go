package artifactfile

import (
	"encoding/json"
	"testing"
)

func TestImageReferencesPreserveRemoteSignaturesAndMetadata(t *testing.T) {
	for _, source := range []string{"https://images.example/image.png?token=provider-signature", "data:image/png;base64,eA=="} {
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
