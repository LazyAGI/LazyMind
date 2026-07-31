package modelprovider

import "testing"

// TestIsMultimodalEmbeddingModelType returns true only for "embed_image".
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
		{"EMBED_IMAGE", false}, // case-sensitive
	}
	for _, tt := range tests {
		got := isMultimodalEmbeddingModelType(tt.modelType)
		if got != tt.want {
			t.Fatalf("isMultimodalEmbeddingModelType(%q) = %v, want %v", tt.modelType, got, tt.want)
		}
	}
}
