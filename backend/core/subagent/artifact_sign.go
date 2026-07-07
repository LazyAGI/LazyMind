package subagent

import (
	"encoding/json"
	"strings"

	"lazymind/core/doc"
)

// SignArtifactImageValue adds a fresh signed url for image (and file_list) artifact values.
// Stored paths may carry expired /static-files/ signatures; always re-sign on read.
// Pass contentType "" to attempt image signing (legacy snapshot rows).
func SignArtifactImageValue(contentType string, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	ct := strings.TrimSpace(contentType)
	if ct == "" || ct == "image" {
		return signImageArtifactValue(raw)
	}
	if ct == "file_list" {
		return signFileListArtifactValue(raw)
	}
	return raw
}

func signImageArtifactValue(raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	pathVal, _ := m["path"].(string)
	if pathVal == "" {
		return raw
	}
	if strings.HasPrefix(pathVal, "http://") || strings.HasPrefix(pathVal, "https://") ||
		strings.HasPrefix(pathVal, "data:") {
		if strings.Contains(pathVal, "/static-files/") {
			if signed := doc.StaticFileURLFromAnyStoragePath(pathVal); signed != "" {
				m["url"] = signed
				out, err := json.Marshal(m)
				if err == nil {
					return out
				}
			}
		}
		m["url"] = pathVal
		delete(m, "path")
		out, err := json.Marshal(m)
		if err != nil {
			return raw
		}
		return out
	}
	signed := doc.StaticFileURLFromAnyStoragePath(pathVal)
	if signed == "" {
		return raw
	}
	delete(m, "url")
	m["url"] = signed
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

func signFileListArtifactValue(raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	rawPaths, ok := m["paths"].([]any)
	if !ok || len(rawPaths) == 0 {
		return raw
	}
	paths := make([]string, 0, len(rawPaths))
	for _, item := range rawPaths {
		p, _ := item.(string)
		if p == "" {
			continue
		}
		if signed := doc.StaticFileURLFromAnyStoragePath(p); signed != "" {
			paths = append(paths, signed)
		} else {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return raw
	}
	m["paths"] = paths
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}
