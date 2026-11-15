package paperguide

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestSourceAliasBudgetPathContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/source-alias-budget-paths.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract map[string][]string
	if err = json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	for _, task := range []string{"paper.guide.extract", "paper.guide.plan", "paper.guide.section", "paper.guide.review"} {
		paths := []string{}
		for _, group := range sourceReferencePaths(task) {
			for _, path := range group {
				if slices.Contains(paths, path.path) {
					t.Fatalf("%s counts %s twice", task, path.path)
				}
				paths = append(paths, path.path)
			}
		}
		if len(paths) == 0 || paths[0] != "evidence.*.id" || !slices.Equal(paths, contract[task]) {
			t.Fatalf("%s differs from the shared SourceReferences contract: %v", task, paths)
		}
	}
}

func TestSourceAliasBudgetUsesActualTaskPathsAndLeavesProseAlone(t *testing.T) {
	a, b := "ev_"+strings.Repeat("a", 64), "ev_"+strings.Repeat("b", 64)
	// The same superset passed under distinct explicit task names catches
	// inference from fields and accidental inclusion of paths from other tasks.
	var input map[string]any
	raw := fmt.Sprintf(`{
		"evidence":[{"id":%[1]q,"parent_asset_id":null,"fragment_evidence_ids":null},{"id":%[2]q}],
		"source_sections":[{"id":%[1]q,"evidence_ids":[%[1]q],"title":%[1]q}],
		"original_asset_candidates":null,
		"extracts":[{"claims":[{"evidence_ids":[%[2]q,%[2]q]}]}],
		"plan":{"claims":[{"evidence_ids":[%[1]q]}],
			"sections":[{"evidence_ids":[%[2]q,%[2]q],"asset_ids":[%[1]q]}],
			"required_assets":[{"id":%[2]q}]},
		"sections":[{"claims":[{"evidence_ids":[%[1]q]}], "blocks":[
			{"id":%[1]q,"evidence_ids":[%[2]q],"asset_id":%[1]q},
			{"id":%[2]q,"evidence_ids":[],"asset_id":null}]}],
		"extra":{"evidence_ids":[%[1]q,%[1]q],"asset_id":%[2]q}
	}`, a, b)
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	for _, withProse := range []bool{false, true} {
		if withProse {
			text := strings.Repeat("测量 < & > \u2028 \u2029 \"e1\" "+a+" "+b+` $\frac{x}{y}$\`, 100)
			for _, evidence := range input["evidence"].([]any) {
				evidence.(map[string]any)["text"] = text
				evidence.(map[string]any)["locator"] = map[string]any{"latex": text}
			}
		}
		original := digest(input)
		for task, credit := range map[string]int{
			"paper.guide.extract": 65,
			"paper.guide.plan":    195,
			"paper.guide.section": 390,
			"paper.guide.review":  576, // N=11, U=2, maximum alias is three bytes
			"paper.guide.unknown": 0,
		} {
			if got := sourceAliasRepeatCredit(task, input); got != credit {
				t.Errorf("%s (prose=%t) credit=%d, want %d", task, withProse, got, credit)
			}
		}
		if digest(input) != original {
			t.Fatal("alias accounting changed canonical input")
		}
	}
}

func TestSourceAliasBudgetIncludesContextOnlyIDsAtDigitBoundaries(t *testing.T) {
	for _, tc := range []struct{ count, credit int }{
		{1, 0}, {9, 65}, {10, 64}, {99, 64}, {100, 63}, {999, 63}, {1000, 62}, {9999, 62}, {10000, 61},
	} {
		t.Run(strconv.Itoa(tc.count), func(t *testing.T) {
			id := "ev_" + strings.Repeat("a", 64)
			refs := make([]any, tc.count-1)
			for i := range refs {
				refs[i] = fmt.Sprintf("ev_%064x", i)
			}
			if len(refs) > 0 {
				refs[len(refs)-1] = id
			}
			input := map[string]any{
				"evidence":        []any{map[string]any{"id": id}},
				"source_sections": []any{map[string]any{"evidence_ids": refs}},
			}
			raw, err := json.MarshalIndent(input, "", "")
			if err != nil {
				t.Fatal(err)
			}
			bound, ok := guideInputBound("paper.guide.plan", raw)
			if got := len(raw) + 256 + 64 + 10240 - bound; !ok || got != tc.credit {
				t.Fatalf("checked bound=%t credit=%d, want %d", ok, got, tc.credit)
			}
		})
	}
}

func TestSourceAliasBudgetNeverCreditsMalformedTypedReferences(t *testing.T) {
	id := "ev_" + strings.Repeat("a", 64)
	for _, tc := range []struct{ name, shape string }{
		{"root list null", `"source_sections":null`},
		{"wrong wildcard object", `"source_sections":{}`},
		{"null wildcard item", `"source_sections":[null]`},
		{"required list absent", `"source_sections":[{}]`},
		{"required list null", `"source_sections":[{"evidence_ids":null}]`},
		{"wrong ID uppercase", fmt.Sprintf(`"source_sections":[{"evidence_ids":[%q]}]`, strings.ToUpper(id))},
		{"wrong ID short", fmt.Sprintf(`"source_sections":[{"evidence_ids":[%q]}]`, id[:len(id)-1])},
		{"wrong ID newline", fmt.Sprintf(`"source_sections":[{"evidence_ids":[%q]}]`, id+"\n")},
		{"null list item", `"source_sections":[{"evidence_ids":[null]}]`},
		{"numeric leaf", `"source_sections":[{"evidence_ids":[1]}]`},
		{"object leaf", `"source_sections":[{"evidence_ids":[{}]}]`},
		{"list leaf", `"source_sections":[{"evidence_ids":[[]]}]`},
		{"malformed nullable", `"original_asset_candidates":false`},
		{"required array absent", `"original_asset_candidates":{"figure":[]}`},
		{"required array null", `"original_asset_candidates":{"figure":[],"formula":null}`},
		{"required extracts null", `"extracts":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`{"evidence":[{"id":%[1]q,"parent_asset_id":%[1]q}],%[2]s}`, id, tc.shape))
			// An otherwise eligible common repeat occurs before the bad position.
			bound, ok := guideInputBound("paper.guide.plan", raw)
			if !ok || bound != len(raw)+256+64+10240 {
				t.Fatalf("partial credit or wrong raw bound: %d, %v", bound, ok)
			}
		})
	}
}

func TestSourceAliasBudgetNeverCompensatesForTransportCap(t *testing.T) {
	id := "ev_" + strings.Repeat("a", 64)
	evidence := []any{map[string]any{"id": id}}
	refs := make([]string, 8000)
	for i := range refs {
		refs[i] = id
	}
	input := map[string]any{
		"evidence":        evidence,
		"source_sections": []any{map[string]any{"evidence_ids": refs}},
		"text":            "",
	}
	raw, err := json.MarshalIndent(input, "", "")
	if err != nil {
		t.Fatal(err)
	}
	bound, ok := guideInputBound("paper.guide.plan", raw)
	if !ok || bound >= len(raw) {
		t.Fatal("test needs a large eligible repeat credit")
	}
	// Choose the raw size so the expansion reserve brings B to exactly 1 MiB.
	input["text"] = strings.Repeat("x", (1<<20)-len(raw)-256-64)
	x := execution{model: ModelIdentity{ContextTokens: 10_000_000}}
	if !x.inputFits("paper.guide.plan", input) {
		t.Fatal("exact pre-credit cap rejected")
	}
	input["text"] = input["text"].(string) + "x"
	if x.inputFits("paper.guide.plan", input) {
		t.Fatal("repeat credit reduced the transport cap")
	}
	for _, bad := range []any{nil, "array", map[string]any{}, make(chan int)} {
		if x.inputFits("paper.guide.plan", map[string]any{"evidence": bad}) {
			t.Fatalf("unrecognizable or unserializable evidence admitted: %v", reflect.TypeOf(bad))
		}
	}
}

// The offline Python renderer test supplies schema-valid packets and consumes
// the exact bytes and upper bound computed by Core.
func TestExportGuideSourceAliasBudget(t *testing.T) {
	path := os.Getenv("LAZYMIND_GUIDE_ALIAS_CASES")
	if path == "" {
		t.Skip("cross-language test fixture not requested")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var inputs map[string]struct {
		Task  string
		Input map[string]any
	}
	if err = json.Unmarshal(payload, &inputs); err != nil {
		t.Fatal(err)
	}
	type result struct {
		Raw      []byte
		Bound    int
		Eligible bool
	}
	results := make(map[string]result, len(inputs))
	for name, request := range inputs {
		raw, err := json.MarshalIndent(request.Input, "", "")
		if err != nil {
			t.Fatal(err)
		}
		bound, eligible := guideInputBound("paper.guide."+request.Task, raw)
		results[name] = result{raw, bound, eligible}
	}
	output, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".result", output, 0600); err != nil {
		t.Fatal(err)
	}
}
