package document

import "encoding/json"

// Only source spellings needed for display cross the inspection boundary.
// Target identity, resource paths and credentials are never display metadata.
type displaySource struct {
	CodeFences []struct {
		Display  string `json:"display"`
		Language string `json:"language"`
	} `json:"code_fences,omitempty"`
	Images map[string]struct {
		RawVariants []string `json:"raw_variants"`
	} `json:"images,omitempty"`
}

func inspectionArtifact(raw, value json.RawMessage, contentType string, targets []json.RawMessage) json.RawMessage {
	resolved, err := ReadArtifactValue(raw, contentType)
	if err != nil {
		return value
	}
	var target json.RawMessage
	for {
		var envelope struct {
			Data json.RawMessage `json:"data"`
			Meta struct {
				Sync struct {
					Target json.RawMessage `json:"target_document"`
				} `json:"lazymind_provider_sync"`
			} `json:"meta"`
		}
		if json.Unmarshal(resolved, &envelope) != nil {
			break
		}
		if len(envelope.Meta.Sync.Target) > 0 {
			target = envelope.Meta.Sync.Target
			break
		}
		if len(envelope.Data) == 0 {
			break
		}
		resolved = envelope.Data
	}
	if len(target) == 0 && len(targets) > 0 {
		target = targets[0]
	}
	var source struct {
		Adapter string                     `json:"adapter"`
		Meta    map[string]json.RawMessage `json:"meta"`
	}
	if json.Unmarshal(target, &source) != nil {
		return value
	}
	hints := displaySource{}
	switch source.Adapter {
	case "github":
		_ = json.Unmarshal(source.Meta["github_writer_code_fences"], &hints.CodeFences)
	case "obsidian":
		_ = json.Unmarshal(source.Meta["obsidian_bridge"], &hints)
	}
	if len(hints.CodeFences) == 0 && len(hints.Images) == 0 {
		return value
	}
	encoded, err := json.Marshal(map[string]any{"data": value, "meta": map[string]any{"writer_source": hints}})
	if err != nil || len(encoded) > maxDocumentBytes {
		return value
	}
	return encoded
}
