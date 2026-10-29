package doc

import (
	"encoding/json"
	"lazymind/core/pdfir"
	"strings"
	"testing"
)

func TestFormulaExplanationContextBoundsAndAuthoritativeFormula(t *testing.T) {
	d := pdfir.Document{SourceSHA256: "source", Blocks: []pdfir.Block{{ID: "intro", Type: "text", Text: "x is the distance.", Page: 0}, {ID: "eq", Type: "formula", Latex: "x^2", Page: 1, BBox: []float64{.1, .2, .5, .3}}, {ID: "after", Type: "text", Text: strings.Repeat("a", 30000), Page: 1}}}
	formula, evidence, err := formulaExplanationContext(d, "eq")
	if err != nil || formula.ID != "eq" || len(evidence) == 0 {
		t.Fatalf("context: %v %v", formula, err)
	}
	size := 0
	for _, e := range evidence {
		size += len([]rune(e.Text))
		if e.ID == "eq" && (e.Text != "x^2" || e.Page != 1 || len(e.BBox) != 4) {
			t.Fatal("formula evidence changed")
		}
	}
	if size > 20000 {
		t.Fatalf("unbounded context: %d", size)
	}
	if _, _, err := formulaExplanationContext(d, "intro"); err == nil {
		t.Fatal("nonformula accepted")
	}
	if _, _, err := formulaExplanationContext(d, "invented"); err == nil {
		t.Fatal("invented block accepted")
	}
}
func TestFormulaExplanationValidatesCitationsAndNeverTrustsModelEvidence(t *testing.T) {
	supplied := []formulaEvidence{{ID: "p", Text: "x is distance.", Page: 2, BBox: []float64{.1, .2, .7, .3}}}
	raw := `{"summary":"Meaning","symbols":[{"symbol":"x","meaning":"x is distance.","evidence_ids":["p"],"inferred":false},{"symbol":"y","meaning":"invented","evidence_ids":["fake"],"inferred":false}],"steps":[{"text":"An interpretation","evidence_ids":["p"],"inferred":false}],"evidence":[{"id":"p","text":"fabricated","page":999}],"warnings":[]}`
	got, err := validateFormulaExplanation(raw, supplied)
	if err != nil {
		t.Fatal(err)
	}
	if got.Evidence[0].Text != "x is distance." || got.Evidence[0].Page != 2 {
		t.Fatal("model forged evidence")
	}
	if !got.Symbols[0].Inferred || !got.Symbols[1].Inferred || len(got.Symbols[1].EvidenceIDs) != 0 || !got.Steps[0].Inferred {
		t.Fatalf("unsupported claim presented as sourced: %+v", got)
	}
	if len(got.Warnings) == 0 {
		t.Fatal("missing limitation warning")
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "fake") || strings.Contains(string(b), "fabricated") {
		t.Fatal("untrusted evidence leaked")
	}
	for _, bad := range []string{`{}`, `not json`, `{"summary":"a","symbols":[],"steps":[]}` + strings.Repeat("x", 300000)} {
		if _, err := validateFormulaExplanation(bad, supplied); err == nil {
			t.Fatal("invalid output accepted")
		}
	}
}
func TestFormulaExplanationCacheIdentity(t *testing.T) {
	d := pdfir.Document{SourceSHA256: "sha", Parser: pdfir.Parser{Name: "mineru", Model: "vlm"}}
	evidence := []formulaEvidence{{ID: "f", Text: "x"}}
	key := formulaExplanationCacheKey(d, "f", "own:model", evidence)
	if key == formulaExplanationCacheKey(d, "f", "own:other", evidence) {
		t.Fatal("model not in cache key")
	}
	d.SourceSHA256 = "new"
	if key == formulaExplanationCacheKey(d, "f", "own:model", evidence) {
		t.Fatal("source not in cache key")
	}
	d.SourceSHA256 = "sha"
	evidence[0].Text = "changed"
	if key == formulaExplanationCacheKey(d, "f", "own:model", evidence) {
		t.Fatal("parser context not in cache key")
	}
}
