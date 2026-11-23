package paperguide

import (
	"context"
	"encoding/json"
)

type fixtureSource struct {
	s   GuideSourceSnapshot
	err error
}

func (f *fixtureSource) Load(context.Context, Scope, bool) (SourceState, error) {
	return SourceState{Hash: f.s.Identity.SourceSHA256, Snapshot: &f.s, Path: "test-snapshot"}, f.err
}
func (f *fixtureSource) Read(context.Context, string) (GuideSourceSnapshot, error) { return f.s, nil }
func baselinePlan(s GuideSourceSnapshot) GuidePlan {
	a := ArgumentExperiment
	p := GuidePlan{Profiles: GuideProfiles{Primary: &a, Secondary: []ArgumentType{}}, Questions: []string{"What happened?"}, Claims: []GuideClaim{}, TermPolicy: []string{}, RequiredAssets: []GuideAssetDecision{}}
	for _, role := range Roles() {
		p.Sections = append(p.Sections, GuideSectionPlan{Role: role, Title: string(role), Focus: "Explain the measured sample.", Terms: []string{}, ClaimIDs: []string{}, EvidenceIDs: []string{s.Evidence[0].ID}, AssetIDs: []string{}})
	}
	return p
}
func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func teachingSection(role Role) GuideSection {
	return GuideSection{Role: role, Title: string(role), Blocks: []GuideBlock{{ID: "b_" + string(role), Kind: "paragraph", Text: "The measured sample.", Attribution: AttributionTeaching, ClaimIDs: []string{}, EvidenceIDs: []string{}}}, Claims: []GuideClaim{}, ClaimIDs: []string{}, Summary: "Measured sample."}
}
