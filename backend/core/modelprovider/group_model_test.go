package modelprovider

import (
	"reflect"
	"testing"
)

func TestCompatibleDBModelTypes(t *testing.T) {
	tests := []struct {
		name      string
		modelType string
		want      []string
	}{
		{
			name:      "cross-modal embedding includes legacy aliases",
			modelType: "cross_modal_embed",
			want:      []string{"cross_modal_embed", "multimodal_embedding", "embed_image"},
		},
		{
			name:      "evo includes text and vision chat models",
			modelType: "evo_llm",
			want:      []string{"llm", "vlm"},
		},
		{
			name:      "other model types remain exact",
			modelType: "llm",
			want:      []string{"llm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compatibleDBModelTypes(tt.modelType); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("compatibleDBModelTypes(%q) = %v, want %v", tt.modelType, got, tt.want)
			}
		})
	}
}
