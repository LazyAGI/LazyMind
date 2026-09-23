package main

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"lazymind/core/workflow"
	"reflect"
	"testing"
)

func multiRewriteFixture(t *testing.T, representation string) (rewriteFixture, *rewriteServer, map[string]any) {
	t.Helper()
	f := newRewriteFixture(t, representation)
	f.source = "😀 First.\n\nKeep this.\n\nLast."
	f.candidate = "😀 Clear.\n\nKeep this.\n\nBetter."
	selections := []any{map[string]any{"selected_text": "First", "start": 2, "end": 7}, map[string]any{"selected_text": "Last", "start": 22, "end": 26}}
	targets := []any{map[string]any{"type": "block", "block_type": "paragraph", "target_start": 0, "target_end": 8}, map[string]any{"type": "block", "block_type": "paragraph", "target_start": 22, "target_end": 27}}
	oldTexts, newTexts := []string{"😀 First.", "Last."}, []string{"😀 Clear.", "Better."}
	patchType, contentType := "string_replace_set", "text"
	if representation == "ir" {
		block := func(id, text string) any { return map[string]any{"node_id": id, "type": "paragraph", "content": text} }
		f.source = map[string]any{"document_id": "rewrite-doc", "blocks": []any{block("p", "First."), block("gap", "Keep this."), block("last", "Last.")}}
		f.candidate = map[string]any{"document_id": "rewrite-doc", "blocks": []any{block("p", "Clear."), block("gap", "Keep this."), block("last", "Better.")}}
		selections = []any{map[string]any{"node_id": "p"}, map[string]any{"node_id": "last"}}
		targets = []any{map[string]any{"type": "block", "block_type": "paragraph", "node_id": "p"}, map[string]any{"type": "block", "block_type": "paragraph", "node_id": "last"}}
		oldTexts[0], newTexts[0] = "First.", "Clear."
		patchType, contentType = "writer_ir_patch", "json"
	}
	stored := f.source
	if representation == "ir" {
		stored = map[string]any{"schema": descriptorIRSchema, "data": f.source}
	}
	if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(mustJSONRewrite(stored))).Error; err != nil {
		t.Fatal(err)
	}
	server := newRewriteServer(t, f)
	body := f.body("preview", "")
	body["input"] = map[string]any{"instruction": "Make it clearer", "type": representation, "selection_ranges": selections}
	json.Unmarshal([]byte(mustJSONRewrite(body["input"])), &server.argumentsOverride)
	results := []any{}
	for i, target := range targets {
		results = append(results, map[string]any{"target": target, "preview": map[string]any{"old_text": oldTexts[i], "new_text": newTexts[i]}, "patch": map[string]any{"type": patchType, "payload": map[string]any{}}})
	}
	server.resultOverride = map[string]any{"representation": representation, "results": results, "artifact": map[string]any{"content_type": contentType, "value": f.candidate}, "commit": map[string]any{"token": "00000000000000000000000000000001"}}
	return f, server, body
}

func TestDocumentRewriteMultipleParagraphs(t *testing.T) {
	for _, kind := range []string{"markdown", "ir"} {
		t.Run(kind, func(t *testing.T) {
			f, server, body := multiRewriteFixture(t, kind)
			before := rewriteSnapshot(t, f)
			result := rewriteData(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body))
			if len(result["results"].([]any)) != 2 {
				t.Fatal("missing paragraph previews")
			}
			if rewriteSnapshot(t, f) != before {
				t.Fatal("preview wrote draft")
			}
			server.resultOverride = nil
			saved := rewriteData(t, f.post(t.Context(), "execute", "descriptor-artifact", "descriptor-owner", f.body("execute", result["commit"].(map[string]any)["token"].(string))))
			if saved["artifact_id"] == nil {
				t.Fatal("candidate not committed")
			}
		})
	}
}

func TestDocumentRewriteMultipleRejectsIncompleteOrExpandedCandidate(t *testing.T) {
	for _, kind := range []string{"markdown", "ir"} {
		for _, bad := range []string{"duplicate", "missing", "gap", "metadata"} {
			t.Run(kind+"/"+bad, func(t *testing.T) {
				f, server, body := multiRewriteFixture(t, kind)
				result := server.resultOverride
				items := result["results"].([]any)
				switch bad {
				case "duplicate":
					result["results"] = []any{items[0], items[0]}
				case "missing":
					result["results"] = items[:1]
				case "gap":
					if kind == "markdown" {
						result["artifact"].(map[string]any)["value"] = "😀 Clear.\n\nChanged gap.\n\nBetter."
					} else {
						result["artifact"].(map[string]any)["value"].(map[string]any)["blocks"].([]any)[1].(map[string]any)["content"] = "Changed gap."
					}
				case "metadata":
					if kind == "markdown" {
						items[1].(map[string]any)["target"].(map[string]any)["target_start"] = 9
					} else {
						result["artifact"].(map[string]any)["value"].(map[string]any)["title"] = "Changed title"
					}
				}
				before := rewriteSnapshot(t, f)
				rewriteError(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body), 502, "DOCUMENT_ACTION_RESULT_INVALID")
				if rewriteSnapshot(t, f) != before {
					t.Fatal("invalid preview changed draft")
				}
			})
		}
	}
}

func TestDocumentRewriteContinuousRangeReturnsAllParagraphs(t *testing.T) {
	f, server, body := multiRewriteFixture(t, "markdown")
	// A single range crosses all three paragraphs; an unchanged paragraph must still be accounted for.
	input := body["input"].(map[string]any)
	input["selection_ranges"] = []any{map[string]any{"selected_text": f.source, "start": 0, "end": 27}}
	json.Unmarshal([]byte(mustJSONRewrite(input)), &server.argumentsOverride)
	items := server.resultOverride["results"].([]any)
	middle := map[string]any{"target": map[string]any{"type": "block", "block_type": "paragraph", "target_start": 10, "target_end": 20}, "preview": map[string]any{"old_text": "Keep this.", "new_text": "Keep this."}, "patch": map[string]any{"type": "string_replace_set", "payload": map[string]any{}}}
	server.resultOverride["results"] = []any{items[0], middle, items[1]}
	rewriteData(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body))
	server.resultOverride["results"] = items
	rewriteError(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body), 502, "DOCUMENT_ACTION_RESULT_INVALID")
}

func TestDocumentRewriteRejectsTargetExpandedWithCandidate(t *testing.T) {
	f, server, body := multiRewriteFixture(t, "markdown")
	candidate := "Everything changed, including the gap."
	server.resultOverride["results"] = []any{map[string]any{"target": map[string]any{"type": "block", "block_type": "paragraph", "target_start": 0, "target_end": 27}, "preview": map[string]any{"old_text": f.source, "new_text": candidate}, "patch": map[string]any{"type": "string_replace_set", "payload": map[string]any{}}}}
	server.resultOverride["artifact"].(map[string]any)["value"] = candidate
	rewriteError(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body), 502, "DOCUMENT_ACTION_RESULT_INVALID")
}

func TestDocumentRewriteFormattedQuotesDeduplicateTargets(t *testing.T) {
	f := newRewriteFixture(t, "markdown")
	first, last := "前缀**原文**后缀。", "第二段*原文*。"
	f.source = first + "\n\n" + last
	f.candidate = "前缀**润色**后缀。\n\n第二段*润色*。"
	if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(mustJSONRewrite(f.source))).Error; err != nil {
		t.Fatal(err)
	}
	server := newRewriteServer(t, f)
	body := f.body("preview", "")
	body["input"] = map[string]any{"instruction": "润色", "type": "markdown", "selection_ranges": []any{map[string]any{"selected_text": "前缀原文"}, map[string]any{"selected_text": "后缀"}, map[string]any{"selected_text": "第二段原文"}}}
	json.Unmarshal([]byte(mustJSONRewrite(body["input"])), &server.argumentsOverride)
	item := func(start int, old, next string) any {
		return map[string]any{"target": map[string]any{"type": "block", "block_type": "paragraph", "target_start": start, "target_end": start + len([]rune(old))}, "preview": map[string]any{"old_text": old, "new_text": next}, "patch": map[string]any{"type": "string_replace_set", "payload": map[string]any{}}}
	}
	server.resultOverride = map[string]any{"representation": "markdown", "results": []any{item(0, first, "前缀**润色**后缀。"), item(len([]rune(first))+2, last, "第二段*润色*。")}, "artifact": map[string]any{"content_type": "text", "value": f.candidate}, "commit": map[string]any{"token": "00000000000000000000000000000001"}}
	result := rewriteData(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body))
	if len(result["results"].([]any)) != 2 {
		t.Fatal("quotes were not grouped by paragraph")
	}
}

func TestDocumentRewritePublicSchemaAllowsMultipleRanges(t *testing.T) {
	schema := inlineSpecialSchema(reflect.TypeOf(workflow.DocumentRewritePreviewInput{}))
	branches := schema["oneOf"].([]any)
	for _, raw := range branches[1:] {
		array := raw.(map[string]any)["properties"].(map[string]any)["selection_ranges"].(map[string]any)
		if maximum, ok := array["maxItems"]; ok && maximum.(int) < 2 {
			t.Fatal("public contract still limits selection to one item")
		}
		if array["minItems"] != 1 {
			t.Fatal("empty ranges must still be rejected")
		}
	}
}

func TestDocumentRewriteEntityQuotesKeepAlgorithmIdentity(t *testing.T) {
	for _, entity := range []string{"&amp;", "&#38;", "&#x26;"} {
		for _, pickEncoded := range []bool{false, true} {
			name := entity + "/plain"
			if pickEncoded {
				name = entity + "/encoded"
			}
			t.Run(name, func(t *testing.T) {
				f := newRewriteFixture(t, "markdown")
				encoded, plain, third := "A "+entity+" B", "A & B", "Third."
				f.source = encoded + "\n\n" + plain + "\n\n" + third
				quote, start := plain, len([]rune(encoded))+2
				f.candidate = encoded + "\n\nUpdated.\n\nThird updated."
				if pickEncoded {
					quote, start = encoded, 0
					f.candidate = "Updated.\n\n" + plain + "\n\nThird updated."
				}
				if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(mustJSONRewrite(f.source))).Error; err != nil {
					t.Fatal(err)
				}
				server := newRewriteServer(t, f)
				body := f.body("preview", "")
				body["input"] = map[string]any{"instruction": "润色", "type": "markdown", "selection_ranges": []any{map[string]any{"selected_text": quote}, map[string]any{"selected_text": "Third"}}}
				json.Unmarshal([]byte(mustJSONRewrite(body["input"])), &server.argumentsOverride)
				item := func(start int, old, next string) any {
					return map[string]any{"target": map[string]any{"type": "block", "block_type": "paragraph", "target_start": start, "target_end": start + len([]rune(old))}, "preview": map[string]any{"old_text": old, "new_text": next}, "patch": map[string]any{"type": "string_replace_set", "payload": map[string]any{}}}
				}
				server.resultOverride = map[string]any{"representation": "markdown", "results": []any{item(start, quote, "Updated."), item(len([]rune(encoded))+len([]rune(plain))+4, third, "Third updated.")}, "artifact": map[string]any{"content_type": "text", "value": f.candidate}, "commit": map[string]any{"token": "00000000000000000000000000000001"}}
				before := rewriteSnapshot(t, f)
				result := rewriteData(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body))
				targets := result["results"].([]any)
				if len(targets) != 2 || targets[0].(map[string]any)["target"].(map[string]any)["target_start"] != float64(start) {
					t.Fatal("entity spelling changed target identity")
				}
				if rewriteSnapshot(t, f) != before {
					t.Fatal("preview modified draft")
				}
				// A target substituted with the other visually similar paragraph must be refused.
				otherStart, otherQuote := 0, encoded
				if pickEncoded {
					otherStart, otherQuote = len([]rune(encoded))+2, plain
				}
				server.resultOverride["results"].([]any)[0] = item(otherStart, otherQuote, "Updated.")
				rewriteError(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body), 502, "DOCUMENT_ACTION_RESULT_INVALID")
			})
		}
	}
}

func TestDocumentRewriteIRSpanDefaultsAreEquivalent(t *testing.T) {
	for _, mutation := range []string{"defaults", "nonempty_style", "unknown_field", "text"} {
		t.Run(mutation, func(t *testing.T) {
			f := newRewriteFixture(t, "ir")
			block := func(id, text string, span map[string]any) any {
				return map[string]any{"node_id": id, "type": "paragraph", "content": text, "spans": []any{span}}
			}
			oldSpan := map[string]any{"text": "Unselected.", "style": map[string]any{}}
			newSpan := map[string]any{"text": "Unselected."}
			switch mutation {
			case "nonempty_style":
				oldSpan["style"] = map[string]any{"bold": true}
			case "unknown_field":
				oldSpan["custom_annotation"] = map[string]any{}
			case "text":
				newSpan["text"] = "Changed outside selection."
			}
			f.source = map[string]any{"document_id": "rewrite-doc", "blocks": []any{block("p", "Original.", map[string]any{"text": "Original.", "style": map[string]any{}}), block("gap", "Unselected.", oldSpan)}}
			f.candidate = map[string]any{"document_id": "rewrite-doc", "blocks": []any{block("p", "Rewritten.", map[string]any{"text": "Rewritten."}), block("gap", "Unselected.", newSpan)}}
			stored := map[string]any{"schema": descriptorIRSchema, "data": f.source}
			if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(mustJSONRewrite(stored))).Error; err != nil {
				t.Fatal(err)
			}
			server := newRewriteServer(t, f)
			body := f.body("preview", "")
			body["input"] = map[string]any{"instruction": "润色", "type": "ir", "selection_ranges": []any{map[string]any{"node_id": "p"}}}
			json.Unmarshal([]byte(mustJSONRewrite(body["input"])), &server.argumentsOverride)
			before := rewriteSnapshot(t, f)
			response := f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body)
			if mutation == "defaults" {
				rewriteData(t, response)
			} else {
				rewriteError(t, response, 502, "DOCUMENT_ACTION_RESULT_INVALID")
			}
			if rewriteSnapshot(t, f) != before {
				t.Fatal("preview changed stored source")
			}
		})
	}
}
