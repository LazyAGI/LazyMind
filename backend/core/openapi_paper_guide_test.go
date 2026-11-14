package main

import (
	"testing"
)

func TestPaperGuideOpenAPIHasTypedContracts(t *testing.T) {
	schemas := paperGuideOpenAPISchemas()
	for _, name := range []string{"PaperGuideStatus", "PaperGuideRevision", "PaperGuideGenerateRequest", "PaperGuideSelectionContext", "PaperGuideEvidenceResponse"} {
		if schemas[name] == nil {
			t.Fatalf("missing %s", name)
		}
	}
	paths := paperGuideOpenAPIPaths()
	if len(paths) != 8 {
		t.Fatalf("expected 8 API operations, got %d", len(paths))
	}
}
