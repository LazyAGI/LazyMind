package document

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"lazymind/core/common"
	"lazymind/core/doc"
)

// ReadArtifactValue resolves a server-owned JSON carrier while retaining its
// envelope metadata. It also handles the target and media library artifacts,
// which are not document representations themselves.
func ReadArtifactValue(raw json.RawMessage, contentType ...string) (json.RawMessage, error) {
	schema := ""
	if len(contentType) > 0 {
		schema = contentType[0]
	}
	return readArtifactValue(raw, false, schema)
}
func readArtifactValue(raw json.RawMessage, fromFile bool, schema string) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return raw, nil
	}
	for _, key := range []string{"schema", "schema_name"} {
		if raw, exists := object[key]; exists {
			var declaration string
			if json.Unmarshal(raw, &declaration) != nil {
				return nil, Invalid()
			}
			name := schemaName(declaration)
			if schema != "" && name != "" && schema != name {
				return nil, Invalid()
			}
			if name != "" {
				schema = name
			}
		}
	}
	if data, exists := object["data"]; exists {
		resolved, err := readArtifactValue(data, fromFile, schema)
		if err != nil {
			return nil, err
		}
		object["data"] = resolved
		return json.Marshal(object)
	}
	var path string
	_ = json.Unmarshal(object["path"], &path)
	if path == "" {
		_ = json.Unmarshal(object["url"], &path)
	}
	if path == "" {
		return raw, nil
	}
	if fromFile {
		return nil, Invalid()
	}
	content, err := doc.ReadLocalArtifactFile(path, maxDocumentBytes)
	if err != nil {
		return nil, err
	}
	if ext := strings.ToLower(filepath.Ext(path)); schema != IRSchema && (ext == ".md" || ext == ".markdown") {
		return json.Marshal(string(content))
	}
	if !json.Valid(content) {
		return nil, Invalid()
	}
	return readArtifactValue(content, true, schema)
}

func ArtifactData(raw json.RawMessage) json.RawMessage {
	for {
		var value map[string]json.RawMessage
		if json.Unmarshal(raw, &value) != nil {
			return raw
		}
		data, ok := value["data"]
		if !ok {
			return raw
		}
		raw = data
	}
}

// PreserveProviderMetadata uses the publication reader's representation rules,
// including explicitly typed carriers whose extension is not .lmd. Resolved
// files become inline snapshots before applying server-owned identity rules.
func PreserveProviderMetadata(previous, next json.RawMessage, contentType string) (json.RawMessage, error) {
	resolve := func(raw json.RawMessage) (json.RawMessage, error) {
		var carrier map[string]json.RawMessage
		if json.Unmarshal(ArtifactData(raw), &carrier) != nil {
			return raw, nil
		}
		if carrier["path"] == nil && carrier["url"] == nil {
			return raw, nil
		}
		_, schema, candidate, failure := prepare(raw, contentType, func() (bool, error) { return false, nil })
		if failure != nil {
			return nil, failure
		}
		if !candidate {
			return raw, nil
		}
		resolved, err := ReadArtifactValue(raw, schema)
		if err != nil {
			return nil, err
		}
		var text string
		if json.Unmarshal(resolved, &text) == nil {
			return json.Marshal(map[string]any{"schema": schema, "data": text})
		}
		return resolved, nil
	}
	previous, err := resolve(previous)
	if err != nil {
		return nil, err
	}
	next, err = resolve(next)
	if err != nil {
		return nil, err
	}
	return common.PreserveDocumentProviderMetadata(previous, next), nil
}
