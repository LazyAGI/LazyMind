package paperguide

import (
	"strings"
	"testing"

	"lazymind/core/pdfir"
)

func TestPlanAssetDecisionsDoNotPromoteMissingCaptionText(t *testing.T) {
	for _, kind := range []string{"chart", "image", "table", "formula"} {
		t.Run(kind, func(t *testing.T) {
			d, id := fixture(t)
			d.Blocks = []pdfir.Block{
				{ID: "actual", Type: kind, Page: 0, BBox: []float64{0.1, 0.2, 0.7, 0.8}},
				{ID: "caption", Type: "text", Page: 0, Text: "Supplement figure caption; the image itself is not supplied."},
			}
			snap, err := BuildSourceSnapshot(d, id)
			if err != nil {
				t.Fatal(err)
			}
			if len(snap.Assets) != 1 || len(snap.Coverage.Warnings) == 0 {
				t.Fatal("promoted caption or hid missing asset metadata")
			}
			for _, included := range []bool{false, true} {
				plan := baselinePlan(snap)
				plan.Sections[0].EvidenceIDs = append(plan.Sections[0].EvidenceIDs, snap.Evidence[1].ID)
				plan.RequiredAssets = []GuideAssetDecision{{ID: snap.Assets[0].ID, Included: included, Reason: "Supplied region; describe only available material."}}
				if included {
					plan.Sections[0].AssetIDs = []string{snap.Assets[0].ID}
				}
				if err := ValidatePlan(snap, plan); err != nil {
					t.Fatalf("rejected actual %s with included=%v: %v", kind, included, err)
				}
				// Both real failures used false for a genuine but text-only caption.
				// Neither false nor true can bypass the catalog asset boundary.
				for _, badID := range []string{snap.Evidence[1].ID, "ev_" + strings.Repeat("f", 64)} {
					plan.RequiredAssets[0].ID = badID
					if included {
						plan.Sections[0].AssetIDs = []string{badID}
					}
					if ValidatePlan(snap, plan) == nil {
						t.Fatalf("accepted non-asset with included=%v", included)
					}
				}
			}
		})
	}
}
