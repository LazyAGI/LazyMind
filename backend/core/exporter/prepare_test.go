package exporter

import (
	"fmt"
	"testing"
)

func TestWorkflowExportPreparationMatchesDeclaredFormats(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		want         bool
	}{
		{"editable", "type: export\n        provider: html-presentation\n        formats: [editable-pptx]", true},
		{"default", "type: export\n        provider: html-presentation", true},
		{"raster only", "type: export\n        provider: html-presentation\n        formats: [raster-pptx, pdf]", false},
		{"other provider", "type: export\n        provider: other", false},
		{"not export", "type: other\n        provider: html-presentation", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf("ui:\n  tabs:\n    - id: slides\n      actions:\n      - %s\n", tc.action))
			if got := needsEditablePresentation(body); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	if needsEditablePresentation([]byte("ui: [invalid")) {
		t.Fatal("invalid declaration must not download dependencies")
	}
}
