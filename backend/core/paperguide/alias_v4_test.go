package paperguide

import (
	"encoding/json"
	"os"
	"testing"
)

func TestV4AliasBoundaryReturnsStrictCanonicalGoWire(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/paper-guide-alias-v4.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Evidence     []GuideEvidence            `json:"evidence"`
		Extract      GuideExtract               `json:"extract"`
		Plan         GuidePlan                  `json:"plan"`
		Section      GuideSection               `json:"section"`
		Review       GuideReview                `json:"review"`
		ModelOutputs map[string]json.RawMessage `json:"model_outputs"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	snap := GuideSourceSnapshot{Evidence: fixture.Evidence}
	for _, ev := range fixture.Evidence {
		snap.Assets = append(snap.Assets, GuideAsset{ID: ev.ID, Kind: "table", BlockID: ev.SourceBlockID, Locator: ev.Locator})
	}
	for _, err := range []error{ValidateExtract(snap, fixture.Extract), ValidatePlan(snap, fixture.Plan), ValidateSection(snap, fixture.Plan, RoleBackground, fixture.Section), ValidateReview(fixture.Plan, []GuideSection{fixture.Section}, fixture.Review)} {
		if err != nil {
			t.Fatalf("canonical fixture rejected: %v", err)
		}
	}
	var plan GuidePlan
	if err = json.Unmarshal(fixture.ModelOutputs["plan"], &plan); err != nil {
		t.Fatal(err)
	}
	if ValidatePlan(snap, plan) == nil {
		t.Fatal("Go accepted model-only short references")
	}
	var section GuideSection
	if err = json.Unmarshal(fixture.ModelOutputs["section"], &section); err != nil {
		t.Fatal(err)
	}
	if ValidateSection(snap, fixture.Plan, RoleBackground, section) == nil {
		t.Fatal("Go accepted unexpanded section")
	}
	// These strings are valid claim/block identities, independent of source refs.
	if fixture.Plan.Claims[0].ID != "e1" || fixture.Section.Blocks[0].ID != "e1" || fixture.Review.Findings[0].TargetID != "e1" {
		t.Fatal("rewrote claim/block namespace")
	}
}
