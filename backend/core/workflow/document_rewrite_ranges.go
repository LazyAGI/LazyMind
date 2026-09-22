package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"

	"lazymind/core/workflow/document"
)

func documentRewriteRanges(input *DocumentRewritePreviewInput) ([]map[string]any, error) {
	invalid := errors.New("invalid document selection range")
	if (input.Type != "markdown" && input.Type != "ir") || len(input.SelectionRanges) == 0 {
		return nil, invalid
	}
	values := make([]map[string]any, 0, len(input.SelectionRanges))
	for _, raw := range input.SelectionRanges {
		allowed := map[string]bool{"selected_text": true}
		if input.Type == "markdown" {
			allowed["start"], allowed["end"] = true, true
		} else {
			allowed["node_id"] = true
		}
		value := map[string]any{}
		for key, data := range raw {
			if !allowed[key] || string(data) == "null" {
				return nil, invalid
			}
			if key == "start" || key == "end" {
				var offset int
				if json.Unmarshal(data, &offset) != nil || offset < 0 {
					return nil, invalid
				}
				value[key] = offset
			} else {
				var text string
				if json.Unmarshal(data, &text) != nil || strings.TrimSpace(text) == "" {
					return nil, invalid
				}
				value[key] = text
			}
		}
		if input.Type == "ir" {
			if value["node_id"] == nil {
				return nil, invalid
			}
		} else {
			if value["selected_text"] == nil || (value["start"] == nil) != (value["end"] == nil) {
				return nil, invalid
			}
			if value["start"] != nil && value["start"].(int) >= value["end"].(int) {
				return nil, invalid
			}
		}
		values = append(values, value)
	}
	return values, nil
}

func rewriteRanges(arguments any) []map[string]any {
	return arguments.(map[string]any)["selection_ranges"].([]map[string]any)
}

func validDocumentRewriteRangeSource(arguments any, content *document.Content) bool {
	for _, selected := range rewriteRanges(arguments) {
		if !validDocumentRewriteSelectionSource(selected, content) {
			return false
		}
	}
	return true
}

func validDocumentRewriteSelectionSource(selected map[string]any, content *document.Content) bool {
	if content.Representation == "markdown" {
		var source string
		if json.Unmarshal(content.Value, &source) != nil {
			return false
		}
		if start, ok := selected["start"].(int); ok {
			runes := []rune(source)
			end := selected["end"].(int)
			return end <= len(runes) && string(runes[start:end]) == selected["selected_text"]
		}
		return true // Algorithm owns rendered-text matching and ambiguity detection.
	}
	var ir map[string]any
	if json.Unmarshal(content.Value, &ir) != nil {
		return false
	}
	block := rewriteIRBlock(ir["blocks"], selected["node_id"].(string))
	if block == nil || block["editable"] == false {
		return false
	}
	text, _ := block["content"].(string)
	quote, present := selected["selected_text"].(string)
	return !present || strings.Contains(text, quote)
}

func rewriteIRBlock(raw any, id string) map[string]any {
	blocks, _ := raw.([]any)
	for _, value := range blocks {
		block, _ := value.(map[string]any)
		if block["node_id"] == id {
			return block
		}
		if found := rewriteIRBlock(block["children"], id); found != nil {
			return found
		}
	}
	return nil
}

func validDocumentRewriteRangeResult(result DocumentRewriteRangesResult, arguments any, content *document.Content) bool {
	selections := rewriteRanges(arguments)
	if content.Representation == "markdown" {
		var source, candidate string
		if json.Unmarshal(content.Value, &source) != nil || json.Unmarshal(result.Artifact.Value, &candidate) != nil {
			return false
		}
		runes := []rune(source)
		items := append([]DocumentRewriteRangeResult(nil), result.Results...)
		sort.Slice(items, func(i, j int) bool {
			if items[i].Target.TargetStart == nil || items[j].Target.TargetStart == nil {
				return false
			}
			return *items[i].Target.TargetStart < *items[j].Target.TargetStart
		})
		expected, valid := selectedRewriteMarkdownBlocks(source, selections)
		if !valid || len(expected) != len(items) {
			return false
		}
		cursor := 0
		var rebuilt strings.Builder
		for index, item := range items {
			target := item.Target
			block := expected[index]
			if target.BlockType != block.blockType || target.NodeID != nil || target.TargetStart == nil || target.TargetEnd == nil || *target.TargetStart != block.start || *target.TargetEnd != block.end || *item.Preview.OldText != block.raw {
				return false
			}
			rebuilt.WriteString(string(runes[cursor:block.start]))
			rebuilt.WriteString(*item.Preview.NewText)
			cursor = block.end
		}
		rebuilt.WriteString(string(runes[cursor:]))
		return rebuilt.String() == candidate && reflect.DeepEqual(rewriteMarkdownStructure(source), rewriteMarkdownStructure(candidate))
	}
	var source, candidate map[string]any
	if json.Unmarshal(content.Value, &source) != nil || json.Unmarshal(result.Artifact.Value, &candidate) != nil {
		return false
	}
	selectedIDs := map[string]bool{}
	for _, selected := range selections {
		selectedIDs[selected["node_id"].(string)] = true
	}
	seen := map[string]bool{}
	for _, item := range result.Results {
		target := item.Target
		if target.NodeID == nil || !selectedIDs[*target.NodeID] || seen[*target.NodeID] || target.TargetStart != nil || target.TargetEnd != nil {
			return false
		}
		id := *target.NodeID
		seen[id] = true
		oldBlock, newBlock := rewriteIRBlock(source["blocks"], id), rewriteIRBlock(candidate["blocks"], id)
		if oldBlock == nil || newBlock == nil || oldBlock["content"] != *item.Preview.OldText || newBlock["content"] != *item.Preview.NewText {
			return false
		}
		for _, key := range []string{"content", "spans", "references"} {
			if value, exists := newBlock[key]; exists {
				oldBlock[key] = value
			} else {
				delete(oldBlock, key)
			}
		}
	}
	if len(seen) != len(selectedIDs) {
		return false
	}
	normalizeRewriteIRDefaults(source, true)
	normalizeRewriteIRDefaults(candidate, true)
	return reflect.DeepEqual(source, candidate)
}

func (input *DocumentRewritePreviewInput) UnmarshalJSON(raw []byte) error {
	type plain DocumentRewritePreviewInput
	if err := decodeDocumentJSON(bytes.NewReader(raw), (*plain)(input)); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return errors.New("invalid rewrite input")
	}
	if _, legacy := fields["selection"]; legacy {
		if _, mixed := fields["type"]; mixed {
			return errors.New("mixed rewrite input")
		}
		if _, mixed := fields["selection_ranges"]; mixed {
			return errors.New("mixed rewrite input")
		}
	}
	return nil
}

// Algorithm model_dump(exclude_defaults=True) may omit explicit model defaults.
// Only Writer model defaults are equivalent; unknown metadata remains significant.
func normalizeRewriteIRDefaults(value map[string]any, root bool) {
	defaults := map[string]any{"stage": "draft", "provider_binding": map[string]any{}}
	if root {
		defaults["title"], defaults["revision"], defaults["ui_editable"] = "", nil, false
		defaults["metadata"] = map[string]any{}
	} else {
		defaults["content"], defaults["editable"], defaults["target_chars"] = "", true, nil
		defaults["provider_payload"], defaults["numbering"] = map[string]any{}, map[string]any{}
		for _, key := range []string{"spans", "references", "children", "context_relations", "subtasks"} {
			defaults[key] = []any{}
		}
	}
	for key, expected := range defaults {
		if actual, exists := value[key]; exists && reflect.DeepEqual(actual, expected) {
			delete(value, key)
		}
	}
	if !root {
		spans, _ := value["spans"].([]any)
		for _, raw := range spans {
			span, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			// WriterSpan defaults are omitted by model_dump(exclude_defaults=True).
			for key, expected := range map[string]any{"text": "", "style": map[string]any{}} {
				if actual, exists := span[key]; exists && reflect.DeepEqual(actual, expected) {
					delete(span, key)
				}
			}
		}
	}
	key := "children"
	if root {
		key = "blocks"
	}
	blocks, _ := value[key].([]any)
	for _, block := range blocks {
		if object, ok := block.(map[string]any); ok {
			normalizeRewriteIRDefaults(object, false)
		}
	}
}
