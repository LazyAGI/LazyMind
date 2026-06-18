package evolution

import (
	"encoding/json"
	"strings"
)

func DraftSuggestionIDs(ext json.RawMessage) []string {
	var payload map[string]any
	if len(ext) > 0 && json.Unmarshal(ext, &payload) == nil {
		raw, _ := payload["draft_suggestion_ids"].([]any)
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				out = append(out, strings.TrimSpace(value))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func WithDraftSuggestionIDs(ext json.RawMessage, ids []string) json.RawMessage {
	payload := map[string]any{}
	if len(ext) > 0 {
		_ = json.Unmarshal(ext, &payload)
	}
	if len(ids) == 0 {
		delete(payload, "draft_suggestion_ids")
	} else {
		payload["draft_suggestion_ids"] = ids
	}
	if len(payload) == 0 {
		return nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ext
	}
	return b
}
