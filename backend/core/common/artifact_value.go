package common

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
)

// CanonicalizeTextArtifactValue gives JSON root strings the stable object shape
// used by text Artifact consumers. Other JSON shapes and non-text values retain
// their original bytes so metadata and typed carriers are not rewritten.
func CanonicalizeTextArtifactValue(contentType string, value json.RawMessage) json.RawMessage {
	if !IsTextArtifactContentType(contentType) {
		return value
	}
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return value
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err != nil {
		return value
	}
	normalized, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: text})
	if err != nil {
		return value
	}
	return normalized
}

// IsTextArtifactContentType reports whether a short logical type or MIME value
// belongs to the text Artifact family. MIME parameters and case are irrelevant.
func IsTextArtifactContentType(contentType string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	return mediaType == "text" || strings.HasPrefix(mediaType, "text/")
}

// PreserveDocumentProviderMetadata retains server-owned publication identity
// across local edits, without treating the edited content as synchronized.
func PreserveDocumentProviderMetadata(previous, next json.RawMessage) json.RawMessage {
	var old, edited any
	if json.Unmarshal(next, &edited) != nil {
		return next
	}
	_ = json.Unmarshal(previous, &old)
	root, ok := edited.(map[string]any)
	if !ok {
		return next
	}
	oldRoot, _ := old.(map[string]any)
	oldMeta, _ := oldRoot["meta"].(map[string]any)
	previousBinding, _ := oldMeta["lazymind_provider_sync"].(map[string]any)
	changed := false
	if meta, ok := root["meta"].(map[string]any); ok {
		if _, exists := meta["lazymind_provider_sync"]; exists {
			delete(meta, "lazymind_provider_sync")
			changed = true
		}
	}
	if previousBinding["target_document"] != nil {
		previousBinding["confirmed"] = false
		schema := root["schema"]
		if schema == nil {
			schema = oldRoot["schema"]
		}
		if schema == nil {
			schema = oldRoot["schema_name"]
		}
		data := edited
		if value, exists := root["data"]; exists {
			data = value
		} else if text, exists := root["text"]; exists {
			data = text
		}
		root = map[string]any{"schema": schema, "data": data, "meta": map[string]any{"lazymind_provider_sync": previousBinding}}
		edited = root
		changed = true
	}
	oldDocument := providerDocumentPayload(oldRoot)
	newDocument := providerDocumentPayload(root)
	if newDocument != nil {
		if oldDocument == nil {
			oldDocument = map[string]any{}
		}
		if binding, ok := oldDocument["provider_binding"].(map[string]any); ok && len(binding) > 0 && !reflect.DeepEqual(oldDocument["document_id"], newDocument["document_id"]) {
			newDocument["document_id"] = oldDocument["document_id"]
			changed = true
		}
		changed = preserveProviderFields(oldDocument, newDocument) || changed
		previousNodes := map[string]map[string]any{}
		visitProviderNodes(oldDocument["blocks"], func(node map[string]any) {
			if id, ok := node["node_id"].(string); ok {
				previousNodes[id] = node
			}
		})
		visitProviderNodes(newDocument["blocks"], func(node map[string]any) {
			id, _ := node["node_id"].(string)
			changed = preserveProviderFields(previousNodes[id], node) || changed
		})
	}
	if !changed {
		return next
	}
	raw, err := json.Marshal(edited)
	if err != nil {
		return next
	}
	return raw
}
func providerDocumentPayload(value map[string]any) map[string]any {
	for value != nil {
		child, exists := value["data"]
		if !exists {
			return value
		}
		value, _ = child.(map[string]any)
	}
	return nil
}
func preserveProviderFields(previous, next map[string]any) bool {
	changed := false
	for _, key := range []string{"provider_binding", "provider_payload"} {
		prior, exists := previous[key]
		if exists {
			if !reflect.DeepEqual(next[key], prior) {
				next[key] = prior
				changed = true
			}
		} else if _, present := next[key]; present {
			delete(next, key)
			changed = true
		}
	}
	return changed
}
func visitProviderNodes(value any, visit func(map[string]any)) {
	nodes, _ := value.([]any)
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		visit(node)
		visitProviderNodes(node["children"], visit)
	}
}
