package main

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"reflect"
	"testing"
)

func TestDocumentRewriteRangesPublic(t *testing.T) {
	for _, representation := range []string{"markdown", "ir"} {
		t.Run(representation, func(t *testing.T) {
			f := newRewriteFixture(t, representation)
			server := newRewriteServer(t, f)
			contract := rewriteContracts(t)[0]
			if representation == "ir" {
				contract = rewriteContracts(t)[1]
			}
			server.resultOverride = contract.PreviewResult
			selected := map[string]any{"selected_text": "Original.", "start": 0, "end": 9}
			if representation == "ir" {
				selected = map[string]any{"node_id": "p", "selected_text": "Original"}
			}
			body := f.body("preview", "")
			body["input"] = map[string]any{"instruction": "Make it clearer", "type": representation, "selection_ranges": []any{selected}}
			var expectedArguments map[string]any
			json.Unmarshal([]byte(mustJSONRewrite(body["input"])), &expectedArguments)
			server.argumentsOverride = expectedArguments
			data := rewriteData(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body))
			schemaJSON, err := buildOpenAPISpecFromRouter(f.router)
			if err != nil {
				t.Fatal(err)
			}
			var spec map[string]any
			if err := json.Unmarshal(schemaJSON, &spec); err != nil {
				t.Fatal(err)
			}
			schema := spec["components"].(map[string]any)["schemas"].(map[string]any)["DocumentRewriteRangeTarget"].(map[string]any)
			properties := schema["properties"].(map[string]any)
			targetJSON := data["results"].([]any)[0].(map[string]any)["target"].(map[string]any)
			for key := range targetJSON {
				if properties[key] == nil {
					t.Errorf("actual target field %s is absent from OpenAPI", key)
				}
			}
			for _, key := range schemaStringList(schema["required"]) {
				if _, present := targetJSON[key]; !present {
					t.Errorf("OpenAPI requires absent target field %s", key)
				}
			}
			if _, exists := data["target"]; exists {
				t.Fatal("array response exposed legacy target")
			}
			if !reflect.DeepEqual(data["results"], contract.PreviewResult["results"]) {
				t.Fatalf("results lost target metadata: %#v", data)
			}
			expected := expectedArguments
			if !reflect.DeepEqual(server.calls()[0].Arguments, expected) {
				t.Fatal("selection/focus was not passed intact")
			}
		})
	}
}

func TestDocumentRewriteRangesInvalidInput(t *testing.T) {
	for _, selected := range []map[string]any{
		{"selected_text": "Original.", "start": 0},
		{"selected_text": "Original.", "start": nil, "end": 9},
		{"selected_text": "wrong", "start": 0, "end": 5},
		{"selected_text": "Original.", "start": 0, "end": 100},
		{"selected_text": "Original.", "node_id": "p"},
	} {
		f := newRewriteFixture(t, "markdown")
		server := newRewriteServer(t, f)
		body := f.body("preview", "")
		body["input"] = map[string]any{"instruction": "Make it clearer", "type": "markdown", "selection_ranges": []any{selected}}
		rewriteError(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body), 400, "DOCUMENT_ACTION_INVALID")
		if len(server.calls()) != 0 {
			t.Fatal("invalid range reached model")
		}
	}
}

func TestDocumentRewriteRangesUnicodeCandidate(t *testing.T) {
	f := newRewriteFixture(t, "markdown")
	f.source = "😀 Same.\n\n😀 Same."
	f.candidate = "😀 Same.\n\n😀 Clear."
	if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(mustJSONRewrite(f.source))).Error; err != nil {
		t.Fatal(err)
	}
	server := newRewriteServer(t, f)
	start := len([]rune("😀 Same.\n\n"))
	end := len([]rune(f.source.(string)))
	body := f.body("preview", "")
	body["input"] = map[string]any{"instruction": "Make it clearer", "type": "markdown", "selection_ranges": []any{map[string]any{"selected_text": "😀 Same.", "start": start, "end": end}}}
	json.Unmarshal([]byte(mustJSONRewrite(body["input"])), &server.argumentsOverride)
	result := map[string]any{"representation": "markdown", "results": []any{map[string]any{"target": map[string]any{"type": "block", "block_type": "paragraph", "target_start": start, "target_end": end}, "preview": map[string]any{"old_text": "😀 Same.", "new_text": "😀 Clear."}, "patch": map[string]any{"type": "string_replace_set", "payload": map[string]any{}}}}, "artifact": map[string]any{"content_type": "text", "value": f.candidate}, "commit": map[string]any{"token": "00000000000000000000000000000001"}}
	server.resultOverride = result
	rewriteData(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body))
	result["artifact"].(map[string]any)["value"] = "UNSELECTED CHANGED\n\n😀 Clear."
	rewriteError(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body), 502, "DOCUMENT_ACTION_RESULT_INVALID")
}
