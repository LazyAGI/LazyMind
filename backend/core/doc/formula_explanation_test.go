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
	if size > 5000 {
		t.Fatalf("unbounded context: %d", size)
	}
	if _, _, err := formulaExplanationContext(d, "intro"); err == nil {
		t.Fatal("nonformula accepted")
	}
	if _, _, err := formulaExplanationContext(d, "invented"); err == nil {
		t.Fatal("invented block accepted")
	}
}

func TestFormulaExplanationContextKeepsFocusedProseAndHeading(t *testing.T) {
	long := strings.Repeat("中", 2000)
	d := pdfir.Document{Blocks: []pdfir.Block{
		{ID: "heading", Type: "heading", Text: "Attention method", Page: 1},
		{ID: "old", Type: "text", Text: "irrelevant older paragraph", Page: 1},
		{ID: "before2", Type: "text", Text: long, Page: 2},
		{ID: "image", Type: "image", Text: "unrelated image caption", Page: 2},
		{ID: "before1", Type: "text", Text: long, Page: 2},
		{ID: "eq", Type: "formula", Latex: `\operatorname{Attention}(Q,K,V)`, Page: 2},
		{ID: "after1", Type: "text", Text: "where Q denotes queries. " + long, Page: 2},
		{ID: "other-eq", Type: "formula", Latex: "y=x", Page: 2},
		{ID: "after2", Type: "text", Text: long, Page: 2},
		{ID: "after3", Type: "text", Text: long, Page: 3},
		{ID: "too-far", Type: "text", Text: "not in the formula context", Page: 3},
	}}
	_, evidence, err := formulaExplanationContext(d, "eq")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"heading", "before2", "before1", "eq", "after1", "after2", "after3"}
	if len(evidence) != len(want) {
		t.Fatalf("want %v, got %+v", want, evidence)
	}
	bodySize := 0
	for i, item := range evidence {
		if item.ID != want[i] {
			t.Fatalf("wrong context block %d: %s", i, item.ID)
		}
		if i != 0 && i != 3 {
			length := len([]rune(item.Text))
			if length > 1500 || length == 0 {
				t.Fatalf("body block length %d: %d", i, length)
			}
			bodySize += length
		}
	}
	if bodySize > 5000 {
		t.Fatalf("body context exceeds budget: %d", bodySize)
	}
	if !strings.HasPrefix(evidence[4].Text, "where Q denotes queries") {
		t.Fatal("definition after the formula was lost")
	}
}

func TestFormulaExplanationContextStaysWithinItsSection(t *testing.T) {
	d := pdfir.Document{Blocks: []pdfir.Block{
		{ID: "old", Type: "text", Text: "previous section", Page: 0},
		{ID: "section", Type: "heading", Text: "Current method", Page: 1},
		{ID: "before", Type: "text", Text: "input definition", Page: 1},
		{ID: "eq", Type: "formula", Latex: "y=f(x)", Page: 1},
		{ID: "after", Type: "text", Text: "where y is the output", Page: 1},
		{ID: "next-section", Type: "heading", Text: "Experiments", Page: 2},
		{ID: "unrelated", Type: "text", Text: "accuracy on a dataset", Page: 2},
	}}
	_, evidence, err := formulaExplanationContext(d, "eq")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, item := range evidence {
		ids = append(ids, item.ID)
	}
	if strings.Join(ids, ",") != "section,before,eq,after" {
		t.Fatalf("context crosses a section boundary: %v", ids)
	}
}

func TestFormulaExplanationContextBoundsExtractedFormulaText(t *testing.T) {
	d := pdfir.Document{Blocks: []pdfir.Block{{ID: "eq", Type: "formula", Latex: "x", Text: strings.Repeat("x", 20000), Page: 0}}}
	_, evidence, err := formulaExplanationContext(d, "eq")
	if err != nil || len(evidence) != 1 || len([]rune(evidence[0].Text)) > 8000 {
		t.Fatalf("unbounded extracted formula text: %v %d", err, len(evidence[0].Text))
	}
}

func TestFormulaExplanationContextFindsHeadingInLongSection(t *testing.T) {
	blocks := []pdfir.Block{{ID: "method", Type: "heading", Text: "Long method section", Page: 0}}
	for i := 0; i < 85; i++ {
		blocks = append(blocks, pdfir.Block{ID: strings.Repeat("p", i+1), Type: "text", Text: "paragraph", Page: 0})
	}
	blocks = append(blocks, pdfir.Block{ID: "eq", Type: "formula", Latex: "x", Page: 1})
	_, evidence, err := formulaExplanationContext(pdfir.Document{Blocks: blocks}, "eq")
	if err != nil || len(evidence) == 0 || evidence[0].ID != "method" {
		t.Fatalf("nearest chapter title missing: %+v %v", evidence, err)
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
func TestFormulaExplanationKeepsBoundedDisplayRepairAndOriginalEvidence(t *testing.T) {
	supplied := []formulaEvidence{{ID: "eq", Text: `\frac{x}{`, Page: 1}}
	raw := `{"summary":"A ratio","display_latex":"\\frac{x}{y}","repair_note":"Closed the missing denominator brace from nearby context.","symbols":[{"symbol":"x","meaning":"numerator","evidence_ids":["eq"],"inferred":true}],"steps":[]}`
	got, err := validateFormulaExplanation(raw, supplied)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayLatex != `\frac{x}{y}` || got.RepairNote == "" || got.Evidence[0].Text != `\frac{x}{` {
		t.Fatalf("repair or source lost: %+v", got)
	}
	tooLong := `{"summary":"A ratio","display_latex":"` + strings.Repeat("x", 9000) + `","symbols":[],"steps":[]}`
	if got, err := validateFormulaExplanation(tooLong, supplied); err != nil || got.DisplayLatex != "" {
		t.Fatalf("oversized repair should be ignored without losing explanation: %+v %v", got, err)
	}
}
func TestFormulaExplanationCacheIdentity(t *testing.T) {
	d := pdfir.Document{SourceSHA256: "sha", Parser: pdfir.Parser{Name: "mineru", Model: "vlm"}}
	evidence := []formulaEvidence{{ID: "f", Text: "x"}}
	key := formulaExplanationCacheKey(d, "f", `x+y`, "own:model", evidence)
	if key == formulaExplanationCacheKey(d, "f", `x+y`, "own:other", evidence) {
		t.Fatal("model not in cache key")
	}
	if key == formulaExplanationCacheKey(d, "f", `x-y`, "own:model", evidence) {
		t.Fatal("formula LaTeX not in cache key")
	}
	d.SourceSHA256 = "new"
	if key == formulaExplanationCacheKey(d, "f", `x+y`, "own:model", evidence) {
		t.Fatal("source not in cache key")
	}
	d.SourceSHA256 = "sha"
	evidence[0].Text = "changed"
	if key == formulaExplanationCacheKey(d, "f", `x+y`, "own:model", evidence) {
		t.Fatal("parser context not in cache key")
	}
}

func TestFormulaExplanationCacheRebindsGeometryWithoutRegenerating(t *testing.T) {
	d := pdfir.Document{SourceSHA256: "sha", Parser: pdfir.Parser{Name: "mineru", Model: "vlm"}}
	first := []formulaEvidence{{ID: "f", Text: "x+y", Page: 1, BBox: []float64{.1, .2, .3, .4}}}
	second := []formulaEvidence{{ID: "f", Text: "x+y", Page: 2, BBox: []float64{.5, .6, .7, .8}}}
	if formulaExplanationCacheKey(d, "f", `x+y`, "model", first) != formulaExplanationCacheKey(d, "f", `x+y`, "model", second) {
		t.Fatal("layout-only changes regenerated the formula explanation")
	}
	result, err := validateFormulaExplanation(`{"summary":"sum","symbols":[],"steps":[]}`, second)
	if err != nil || result.Evidence[0].Page != 2 || result.Evidence[0].BBox[0] != .5 {
		t.Fatalf("current source geometry was not rebound: %+v %v", result, err)
	}
}

func TestFormulaExplanationZenMuxRequestLocalReasoning(t *testing.T) {
	config := map[string]any{"llm": map[string]any{"model": "deepseek/deepseek-v4-flash", "base_url": "https://zenmux.ai/api/v1/", "features": map[string]any{"reasoning": true, "vision": true}}}
	got := formulaExplanationGenerationConfig(config)
	features := got["llm"].(map[string]any)["features"].(map[string]any)
	if features["reasoning"] != false || features["vision"] != true {
		t.Fatal("formula configuration is not bounded")
	}
	if config["llm"].(map[string]any)["features"].(map[string]any)["reasoning"] != true {
		t.Fatal("user configuration mutated")
	}
}

func TestFormulaExplanationOtherSupportedRoutes(t *testing.T) {
	for _, test := range []struct {
		host, model string
		disabled    bool
	}{
		{"api.deepseek.com", "deepseek-flash", true},
		{"aihubmix.com", "deepseek-v4-flash-0731-fast", true},
		{"aihubmix.com", "unrelated-model", false},
		{"aihubmix.com.example.test", "deepseek-v4-flash", false},
	} {
		t.Run(test.host+test.model, func(t *testing.T) {
			config := map[string]any{"llm": map[string]any{"model": test.model, "base_url": "https://" + test.host + "/v1", "features": map[string]any{"reasoning": true}}}
			got := formulaExplanationGenerationConfig(config)
			if got["llm"].(map[string]any)["features"].(map[string]any)["reasoning"] != !test.disabled {
				t.Fatal("wrong reasoning policy")
			}
			if config["llm"].(map[string]any)["features"].(map[string]any)["reasoning"] != true {
				t.Fatal("saved config mutated")
			}
		})
	}
}

func TestFormulaExplanationNormalizesPurposeAlias(t *testing.T) {
	for _, test := range []struct {
		raw, summary string
		valid        bool
	}{
		{`{"explain":"Loss perturbation bound","symbols":[{"symbol":"H","meaning":"Hessian"}],"model":"invented"}`, "Loss perturbation bound", true},
		{`{"summary":"Canonical purpose","explain":"Other purpose","symbols":[]}`, "Canonical purpose", true},
		{`{"explain":{"text":"Nested content"},"symbols":[]}`, "", false},
		{`{"symbols":[]}`, "", false},
		{`{"explain":"` + strings.Repeat("x", 12001) + `","symbols":[]}`, "", false},
		{`{"explain":"Purpose","symbols":[{"symbol":"H","meaning":""}]}`, "", false},
	} {
		result, err := validateFormulaExplanation(test.raw, nil)
		if (err == nil) != test.valid {
			t.Fatalf("valid=%v, got %v", test.valid, err)
		}
		if test.valid && (result.Summary != test.summary || result.Model != "") {
			t.Fatalf("incorrect canonical result: %+v", result)
		}
	}
}
