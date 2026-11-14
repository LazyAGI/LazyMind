package paperguide

import (
	"encoding/json"
	"os"
	"testing"
)

func TestV3PythonCanonicalPlanKeepsStrictGoWire(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/paper-guide-plan-v3.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Input struct {
			Evidence []GuideEvidence `json:"evidence"`
		} `json:"input"`
		Raw       GuidePlan `json:"raw_plan"`
		Canonical GuidePlan `json:"canonical_plan"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	snap := GuideSourceSnapshot{Evidence: fixture.Input.Evidence}
	for _, e := range snap.Evidence {
		if e.SourceType == "table" {
			snap.Assets = append(snap.Assets, GuideAsset{ID: e.ID, Kind: "table", BlockID: e.SourceBlockID, Locator: e.Locator})
		}
	}
	if err = ValidatePlan(snap, fixture.Canonical); err != nil {
		t.Fatalf("canonical Python response rejected: %v", err)
	}
	if err = ValidatePlan(snap, fixture.Raw); err == nil {
		t.Fatal("Go wire accepted unnormalized references")
	}
	fixture.Canonical.Claims = append(fixture.Canonical.Claims, fixture.Canonical.Claims[0])
	if err = ValidatePlan(snap, fixture.Canonical); err == nil {
		t.Fatal("duplicate claim definition accepted")
	}
}
