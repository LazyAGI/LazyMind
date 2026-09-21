package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

// Internal wire shape only. The public single-selection request is unchanged.
func rewriteCompatibilityArguments(f rewriteFixture) map[string]any {
	selection := map[string]any{}
	for key, value := range f.selection {
		if key != "type" {
			selection[key] = value
		}
	}
	return map[string]any{"type": f.representation, "instruction": "Make it clearer", "selection_ranges": []any{selection}}
}

type rewriteContractCase struct {
	Representation     string         `json:"representation"`
	Source             any            `json:"source"`
	Candidate          any            `json:"candidate"`
	PublicSelection    map[string]any `json:"public_selection"`
	AlgorithmArguments map[string]any `json:"algorithm_arguments"`
	PreviewResult      map[string]any `json:"preview_result"`
	ExecuteResult      map[string]any `json:"execute_result"`
}

func rewriteContracts(t *testing.T) []rewriteContractCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/document-rewrite/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []rewriteContractCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Fatal("need Markdown and IR actual model contracts")
	}
	return cases
}

func TestDocumentRewriteCompatibilityRealContract(t *testing.T) {
	for _, contract := range rewriteContracts(t) {
		t.Run(contract.Representation, func(t *testing.T) {
			f := newRewriteFixture(t, contract.Representation)
			if !reflect.DeepEqual(f.source, contract.Source) || !reflect.DeepEqual(f.candidate, contract.Candidate) || !reflect.DeepEqual(f.selection, contract.PublicSelection) {
				t.Fatal("contract and fixture disagree")
			}
			server := newRewriteServer(t, f)
			server.resultOverride = contract.PreviewResult
			before := rewriteSnapshot(t, f)
			preview := rewriteData(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", f.body("preview", "")))
			item := contract.PreviewResult["results"].([]any)[0].(map[string]any)
			// Algorithm offsets are internal locating metadata. Preserve the existing
			// public target contract instead of introducing offset-based public input.
			target := map[string]any{}
			for _, key := range []string{"type", "block_type", "node_id"} {
				if value, exists := item["target"].(map[string]any)[key]; exists {
					target[key] = value
				}
			}
			expected := map[string]any{"representation": contract.Representation, "target": target, "preview": item["preview"], "patch": item["patch"], "artifact": contract.PreviewResult["artifact"], "commit": contract.PreviewResult["commit"]}
			if !reflect.DeepEqual(preview, expected) {
				t.Errorf("public single result=%#v want=%#v", preview, expected)
			}
			if rewriteSnapshot(t, f) != before {
				t.Error("preview persisted Core state")
			}
			calls := server.calls()
			if len(calls) != 1 || !reflect.DeepEqual(calls[0].Arguments, contract.AlgorithmArguments) {
				t.Fatalf("actual Algorithm request mismatch=%#v", calls)
			}
			server.mu.Lock()
			server.resultOverride = contract.ExecuteResult
			server.mu.Unlock()
			token := preview["commit"].(map[string]any)["token"].(string)
			saved := rewriteData(t, f.post(t.Context(), "execute", "descriptor-artifact", "descriptor-owner", f.body("execute", token)))
			if saved["revision"] != float64(4) || saved["draft_version"] != float64(1) || len(saved) != 3 {
				t.Errorf("execute public identity=%#v", saved)
			}
			calls = server.calls()
			if len(calls) != 2 || calls[1].Phase != "execute" || len(calls[1].LLMConfig) != 0 || !reflect.DeepEqual(calls[1].Arguments, map[string]any{"commit_token": token}) {
				t.Errorf("execute changed token/model contract=%#v", calls)
			}
		})
	}
}

func TestDocumentRewriteCompatibilityRejectsBadResults(t *testing.T) {
	for _, contract := range rewriteContracts(t) {
		for _, kind := range []string{"missing", "null", "empty", "multiple", "object", "null item", "missing target", "missing preview", "missing patch", "wrong patch", "legacy", "mixed"} {
			t.Run(contract.Representation+"/"+kind, func(t *testing.T) {
				f := newRewriteFixture(t, contract.Representation)
				seedRewriteTerminalGraph(t, f)
				server := newRewriteServer(t, f)
				var result map[string]any
				if err := json.Unmarshal([]byte(mustJSONRewrite(contract.PreviewResult)), &result); err != nil {
					t.Fatal(err)
				}
				item := result["results"].([]any)[0].(map[string]any)
				switch kind {
				case "missing":
					delete(result, "results")
				case "null":
					result["results"] = nil
				case "empty":
					result["results"] = []any{}
				case "multiple":
					result["results"] = []any{item, item}
				case "object":
					result["results"] = item
				case "null item":
					result["results"] = []any{nil}
				case "missing target":
					delete(item, "target")
				case "missing preview":
					delete(item, "preview")
				case "missing patch":
					delete(item, "patch")
				case "wrong patch":
					item["patch"].(map[string]any)["type"] = "unexpected_patch"
				case "legacy", "mixed":
					for _, key := range []string{"target", "preview", "patch"} {
						result[key] = item[key]
					}
					if kind == "legacy" {
						delete(result, "results")
					}
				}
				server.resultOverride = result
				before := rewriteSnapshot(t, f)
				w := f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", f.body("preview", ""))
				rewriteError(t, w, 502, "DOCUMENT_ACTION_RESULT_INVALID")
				if strings.Contains(w.Body.String(), "00000000000000000000000000000001") {
					t.Error("invalid result exposed commit token")
				}
				if rewriteSnapshot(t, f) != before {
					t.Error("invalid preview changed revision/graph/state/event")
				}
				if len(server.calls()) != 1 {
					t.Error("did not reach Algorithm boundary")
				}
			})
		}
	}
}

func TestDocumentRewriteCompatibilityKeepsPublicSingleSelection(t *testing.T) {
	for _, representation := range []string{"markdown", "ir"} {
		for _, kind := range []string{"additional range", "offset injection"} {
			t.Run(representation+kind, func(t *testing.T) {
				f := newRewriteFixture(t, representation)
				server := newRewriteServer(t, f)
				body := f.body("preview", "")
				input := body["input"].(map[string]any)
				switch kind {
				case "array input":
					body["input"] = rewriteCompatibilityArguments(f)
				case "additional range":
					input["selection_ranges"] = []any{f.selection}
				case "offset injection":
					input["selection"].(map[string]any)["start"] = 0
				}
				before := rewriteSnapshot(t, f)
				rewriteError(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body), 400, "DOCUMENT_ACTION_INVALID")
				if len(server.calls()) != 0 || rewriteSnapshot(t, f) != before {
					t.Error("unapproved public request reached Algorithm or changed state")
				}
			})
		}
	}
}

func TestDocumentRewriteCompatibilityFixtureControl(t *testing.T) {
	for _, contract := range rewriteContracts(t) {
		t.Run(contract.Representation, func(t *testing.T) {
			f := newRewriteFixture(t, contract.Representation)
			server := newRewriteServer(t, f)
			server.resultOverride = contract.PreviewResult
			body := map[string]any{"reference": rewriteReference, "phase": "preview", "artifact": map[string]any{"data": contract.Source}, "arguments": contract.AlgorithmArguments, "artifact_store": "contract-test-only", "llm_config": map[string]any{"llm": map[string]any{"model": "rewrite-test-model", "api_key": rewriteTestKey}}}
			response, err := http.Post(server.url+"/api/document/actions:invoke", "application/json", strings.NewReader(mustJSONRewrite(body)))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			var data map[string]any
			if err := json.Unmarshal(raw, &data); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 200 || !reflect.DeepEqual(data["result"], contract.PreviewResult) {
				t.Fatalf("fixture did not replay actual model response: %s", raw)
			}
		})
	}
}
