package paperguide

import (
	"encoding/json"
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

func TestSectionOutputKeepsStrictAssetAndClaimBinding(t *testing.T) {
	doc, id := fixture(t)
	doc.Blocks = []pdfir.Block{
		{ID: "actual", Type: "table", Page: 0, Text: "Fig. 1. Synthetic measurements."},
		{ID: "other-role", Type: "table", Page: 0, Text: "Synthetic comparison."},
		{ID: "missing", Type: "image", Page: 0, Text: "Synthetic illustration."},
		{ID: "caption", Type: "text", Page: 0, Text: "Caption text without an asset."},
	}
	snap, err := BuildSourceSnapshot(doc, id)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, e := range snap.Evidence {
		ids[e.SourceBlockID] = e.ID
	}
	p := baselinePlan(snap)
	p.Sections[0].AssetIDs = []string{ids["actual"]}
	p.Sections[2].AssetIDs = []string{ids["missing"]}
	p.Sections[1].AssetIDs = []string{ids["other-role"]}
	for _, name := range []string{"actual", "other-role", "missing"} {
		p.RequiredAssets = append(p.RequiredAssets, GuideAssetDecision{ids[name], true, "Supports the explanation."})
	}
	if err := ValidatePlan(snap, p); err != nil {
		t.Fatal(err)
	}
	packet, err := RestrictEvidence(snap, []string{ids["actual"], ids["other-role"], ids["caption"]})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, asset, kind, wantError string
	}{
		{"caption does not determine kind", ids["actual"], "figure", "wrong or unplanned asset"},
		{"non-role asset", ids["other-role"], "table", "wrong or unplanned asset"},
		{"nonasset source", ids["caption"], "table", "wrong or unplanned asset"},
		{"planned original missing from packet", ids["missing"], "figure", "planned original missing from role packet"},
		{"unknown source", "ev_" + strings.Repeat("0", 64), "table", "wrong or unplanned asset"},
		{"unbound claim", ids["actual"], "table", "orphan section claims"},
		{"valid asset and other-role citation", ids["actual"], "table", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := teachingSection(RoleBackground)
			s.Blocks[0].Kind, s.Blocks[0].AssetID = tc.kind, tc.asset
			s.Blocks[0].EvidenceIDs = []string{ids["other-role"]}
			if tc.name == "unbound claim" {
				s.Claims = []GuideClaim{{ID: "orphan", Statement: "A supported measurement.", Attribution: AttributionPaper,
					EvidenceIDs: []string{ids["caption"]}, Conditions: []string{}, SupportStatus: "supported"}}
				s.ClaimIDs = []string{"orphan"}
			}
			var section GuideSection
			if err := decodeStrict(mustJSON(s), &section); err != nil {
				t.Fatal(err)
			}
			if tc.name == "planned original missing from packet" {
				modified := p
				modified.Sections = append([]GuideSectionPlan{}, p.Sections...)
				modified.Sections[0].AssetIDs = []string{ids["actual"], ids["missing"]}
				if err := ValidateSection(packet, modified, RoleBackground, section); err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("want validation failure %q, got %v", tc.wantError, err)
				}
				return
			}
			err := ValidateSection(packet, p, RoleBackground, section)
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("want validation failure %q, got %v", tc.wantError, err)
			}
		})
	}
	t.Run("forged source field", func(t *testing.T) {
		s := teachingSection(RoleBackground)
		s.Blocks[0].Kind, s.Blocks[0].AssetID = "table", ids["actual"]
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(mustJSON(s), &wire); err != nil {
			t.Fatal(err)
		}
		wire["allowed_asset_override"] = mustJSON(ids["other-role"])
		var section GuideSection
		if err := decodeStrict(mustJSON(wire), &section); err == nil {
			t.Fatal("accepted forged output field")
		}
	})
}
