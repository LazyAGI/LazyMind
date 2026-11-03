package modelprovider

import "testing"

// All persisted multimodal aliases must trigger the same lazy-mode transition.
func TestIsMultimodalEmbeddingModelType(t *testing.T) {
	tests := []struct {
		modelType string
		want      bool
	}{
		{"embed_image", true},
		{"embed_text", false},
		{"llm", false},
		{"image", false},
		{"", false},
		{"EMBED_IMAGE", true},
		{"multimodal_embedding", true},
		{"cross_modal_embed", true},
		{" MULTIMODAL_EMBEDDING ", true},
	}
	for _, tt := range tests {
		got := isMultimodalEmbeddingModelType(tt.modelType)
		if got != tt.want {
			t.Fatalf("isMultimodalEmbeddingModelType(%q) = %v, want %v", tt.modelType, got, tt.want)
		}
	}
}
