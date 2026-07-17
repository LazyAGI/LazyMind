package subagent

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"lazymind/core/doc"
)

// SignArtifactImageValue adds a fresh signed url for file-backed artifact values.
// Stored paths may carry expired /static-files/ signatures; always re-sign on read.
// Pass contentType "" to attempt image signing (legacy snapshot rows).
func SignArtifactImageValue(contentType string, raw json.RawMessage) json.RawMessage {
	return SignArtifactValue(contentType, raw, "")
}

// SignArtifactValue adds fresh signed URLs to file-backed artifact values.
// workspacePath resolves relative paths and constrains them to the task workspace.
func SignArtifactValue(contentType string, raw json.RawMessage, workspacePath string) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	raw = resolveArtifactPaths(raw, workspacePath)
	ct := strings.TrimSpace(contentType)
	if ct == "" || ct == "image" {
		return signImageArtifactValue(raw)
	}
	if ct == "file_list" {
		return signFileListArtifactValue(raw)
	}
	if ct == "file" {
		return signFileArtifactValue(raw)
	}
	return raw
}

func resolveArtifactPaths(raw json.RawMessage, workspacePath string) json.RawMessage {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" || len(raw) == 0 {
		return raw
	}
	workspaceRoot, err := filepath.Abs(workspacePath)
	if err != nil {
		return raw
	}
	workspaceRoot = filepath.Clean(workspaceRoot)
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	resolve := func(path string) string {
		path = strings.TrimSpace(path)
		if path == "" || strings.HasPrefix(path, "/static-files/") ||
			strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") ||
			strings.HasPrefix(path, "data:") {
			return path
		}
		resolved := filepath.Clean(filepath.FromSlash(path))
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Clean(filepath.Join(workspaceRoot, resolved))
		}
		relative, err := filepath.Rel(workspaceRoot, resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return ""
		}
		return resolved
	}
	changed := false
	if path, ok := value["path"].(string); ok {
		resolved := resolve(path)
		if resolved != path {
			value["path"] = resolved
			changed = true
		}
	}
	if paths, ok := value["paths"].([]any); ok {
		resolvedPaths := make([]any, 0, len(paths))
		for _, item := range paths {
			path, ok := item.(string)
			if !ok {
				resolvedPaths = append(resolvedPaths, item)
				continue
			}
			resolved := resolve(path)
			resolvedPaths = append(resolvedPaths, resolved)
			changed = changed || resolved != path
		}
		value["paths"] = resolvedPaths
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return out
}

func signImageArtifactValue(raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	pathVal, _ := m["path"].(string)
	if pathVal == "" {
		delete(m, "path")
		out, err := json.Marshal(m)
		if err != nil {
			return raw
		}
		return out
	}
	if strings.HasPrefix(pathVal, "http://") || strings.HasPrefix(pathVal, "https://") ||
		strings.HasPrefix(pathVal, "data:") {
		if strings.Contains(pathVal, "/static-files/") {
			if signed := doc.StaticFileURLFromAnyStoragePath(pathVal); signed != "" {
				m["url"] = signed
				delete(m, "path")
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
		delete(m, "path")
		out, err := json.Marshal(m)
		if err != nil {
			return raw
		}
		return out
	}
	delete(m, "path")
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
		} else if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") ||
			strings.HasPrefix(p, "data:") || strings.HasPrefix(p, "/static-files/") {
			paths = append(paths, p)
		}
	}
	m["paths"] = paths
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

func signFileArtifactValue(raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	pathVal, _ := m["path"].(string)
	if pathVal == "" {
		pathVal, _ = m["url"].(string)
	}
	if pathVal == "" {
		delete(m, "path")
		out, err := json.Marshal(m)
		if err != nil {
			return raw
		}
		return out
	}
	if strings.HasPrefix(pathVal, "http://") || strings.HasPrefix(pathVal, "https://") ||
		strings.HasPrefix(pathVal, "data:") {
		if !strings.Contains(pathVal, "/static-files/") {
			m["url"] = pathVal
			delete(m, "path")
			out, err := json.Marshal(m)
			if err == nil {
				return out
			}
			return raw
		}
	}
	signed := doc.StaticFileURLFromAnyStoragePath(pathVal)
	if signed == "" {
		delete(m, "path")
		out, err := json.Marshal(m)
		if err != nil {
			return raw
		}
		return out
	}
	delete(m, "path")
	m["url"] = signed
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}
