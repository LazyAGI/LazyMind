package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

const maxAgentResultFileBytes int64 = 50 << 20

type agentFileContentResponse struct {
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Content  string `json:"content"`
	FileSize int64  `json:"file_size"`
}

func attachAnalysisMarkdownResult(payload any) (bool, error) {
	path := findLocalFilePath(payload, []string{".md", ".markdown"}, []string{
		"analysis_report_path",
		"analysis_report_file",
		"markdown_path",
		"md_path",
		"report_path",
		"result_path",
		"file_path",
		"path",
	})
	if path == "" {
		return false, nil
	}
	content, stat, err := readAgentResultTextFile(path)
	if err != nil {
		return true, err
	}
	if container, ok := payload.(map[string]any); ok {
		container["markdown"] = content
		container["content"] = content
		container["markdown_path"] = path
		container["file_size"] = stat.Size()
		return true, nil
	}
	return false, nil
}

func buildAnalysisMarkdownResult(payload any) (any, bool, error) {
	path := findLocalFilePath(payload, []string{".md", ".markdown"}, []string{
		"analysis_report_path",
		"analysis_report_file",
		"markdown_path",
		"md_path",
		"report_path",
		"result_path",
		"file_path",
		"path",
	})
	if path == "" {
		return payload, false, nil
	}
	content, stat, err := readAgentResultTextFile(path)
	if err != nil {
		return payload, true, err
	}
	if container, ok := payload.(map[string]any); ok {
		container["markdown"] = content
		container["content"] = content
		container["markdown_path"] = path
		container["file_size"] = stat.Size()
		return container, true, nil
	}
	return map[string]any{
		"markdown":      content,
		"content":       content,
		"markdown_path": path,
		"filename":      filepath.Base(path),
		"file_size":     stat.Size(),
	}, true, nil
}

func buildDiffJSONResult(payload any) (any, bool, error) {
	path := findLocalFilePath(payload, []string{".json"}, []string{
		"diff_json_path",
		"diffs_json_path",
		"json_path",
		"diff_path",
		"diffs_path",
		"result_path",
		"file_path",
		"path",
	})
	if path == "" {
		return payload, false, nil
	}
	raw, stat, err := readAgentResultFile(path)
	if err != nil {
		return payload, true, err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return payload, true, fmt.Errorf("decode json result file failed: %w", err)
	}
	if object, ok := decoded.(map[string]any); ok {
		object["json_path"] = path
		object["file_size"] = stat.Size()
		return object, true, nil
	}
	return map[string]any{
		"json_path": path,
		"filename":  filepath.Base(path),
		"file_size": stat.Size(),
		"content":   decoded,
	}, true, nil
}

func findClassificationReportResult(payload any) (any, bool) {
	switch value := payload.(type) {
	case []any:
		for _, item := range value {
			if report, ok := classificationReportFromValue(item); ok {
				return report, true
			}
		}
	case map[string]any:
		if report, ok := classificationReportFromValue(value); ok {
			return report, true
		}
	}
	return nil, false
}

func classificationReportFromValue(value any) (map[string]any, bool) {
	report, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	artifactID, ok := report["artifact_id"].(string)
	if !ok || strings.TrimSpace(artifactID) != "classification_report" {
		return nil, false
	}
	return report, true
}

func buildAgentFileContentResult(path string) (*agentFileContentResponse, error) {
	content, stat, err := readAgentResultTextFile(path)
	if err != nil {
		return nil, err
	}
	return &agentFileContentResponse{
		Path:     cleanAgentResultPath(path),
		Filename: filepath.Base(cleanAgentResultPath(path)),
		Content:  content,
		FileSize: stat.Size(),
	}, nil
}

func readAgentResultTextFile(path string) (string, os.FileInfo, error) {
	raw, stat, err := readAgentResultFile(path)
	if err != nil {
		return "", nil, err
	}
	return string(raw), stat, nil
}

func readAgentResultJSONFile(path string) (any, error) {
	raw, _, err := readAgentResultFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode json file failed: %w", err)
	}
	return payload, nil
}

func readAgentResultFile(path string) ([]byte, os.FileInfo, error) {
	cleanPath := cleanAgentResultPath(path)
	if cleanPath == "" {
		return nil, nil, fmt.Errorf("path required")
	}
	if strings.Contains(cleanPath, "\x00") {
		return nil, nil, fmt.Errorf("path is invalid")
	}
	if strings.HasPrefix(strings.ToLower(cleanPath), "http://") || strings.HasPrefix(strings.ToLower(cleanPath), "https://") {
		return nil, nil, fmt.Errorf("only local file paths are supported")
	}
	stat, err := os.Stat(cleanPath)
	if err != nil {
		return nil, nil, fmt.Errorf("stat file failed: %w", err)
	}
	if stat.IsDir() {
		return nil, nil, fmt.Errorf("path is a directory")
	}
	if !stat.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("path is not a regular file")
	}
	if stat.Size() > maxAgentResultFileBytes {
		return nil, nil, fmt.Errorf("file is too large")
	}
	raw, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read file failed: %w", err)
	}
	return raw, stat, nil
}

func cleanAgentResultPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "\"'")
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "file://") {
		if parsed, err := url.Parse(path); err == nil {
			path = parsed.Path
		}
	}
	return filepath.Clean(path)
}

func findLocalFilePath(root any, allowedExts []string, preferredKeys []string) string {
	if path := localFilePathFromValue(root, allowedExts); path != "" {
		return path
	}
	seen := map[any]struct{}{}
	if path := findLocalFilePathByKeys(root, normalizePreferredPathKeys(preferredKeys), allowedExts, seen); path != "" {
		return path
	}
	seen = map[any]struct{}{}
	return findAnyLocalFilePath(root, allowedExts, seen)
}

func normalizePreferredPathKeys(keys []string) []string {
	result := make([]string, 0, len(keys))
	seen := map[string]struct{}{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result
}

func findLocalFilePathByKeys(root any, keys []string, allowedExts []string, seen map[any]struct{}) string {
	switch value := root.(type) {
	case map[string]any:
		ptr := reflectMapPointer(value)
		if _, ok := seen[ptr]; ok {
			return ""
		}
		seen[ptr] = struct{}{}
		for _, key := range keys {
			if child, ok := value[key]; ok {
				if path := localFilePathFromValue(child, allowedExts); path != "" {
					return path
				}
			}
		}
		for _, key := range sortedMapKeys(value) {
			if path := findLocalFilePathByKeys(value[key], keys, allowedExts, seen); path != "" {
				return path
			}
		}
	case []any:
		for _, child := range value {
			if path := findLocalFilePathByKeys(child, keys, allowedExts, seen); path != "" {
				return path
			}
		}
	}
	return ""
}

func findAnyLocalFilePath(root any, allowedExts []string, seen map[any]struct{}) string {
	if path := localFilePathFromValue(root, allowedExts); path != "" {
		return path
	}
	switch value := root.(type) {
	case map[string]any:
		ptr := reflectMapPointer(value)
		if _, ok := seen[ptr]; ok {
			return ""
		}
		seen[ptr] = struct{}{}
		for _, key := range sortedMapKeys(value) {
			if path := findAnyLocalFilePath(value[key], allowedExts, seen); path != "" {
				return path
			}
		}
	case []any:
		for _, child := range value {
			if path := findAnyLocalFilePath(child, allowedExts, seen); path != "" {
				return path
			}
		}
	}
	return ""
}

func localFilePathFromValue(value any, allowedExts []string) string {
	raw, ok := value.(string)
	if !ok {
		return ""
	}
	path := cleanAgentResultPath(raw)
	if path == "" {
		return ""
	}
	lower := strings.ToLower(path)
	for _, ext := range allowedExts {
		if strings.HasSuffix(lower, strings.ToLower(ext)) {
			return path
		}
	}
	return ""
}

func visitJSONPathContainers(root any, preferredKeys []string, visit func(container map[string]any, path string) bool) {
	if visit == nil {
		return
	}
	seen := map[any]struct{}{}
	visitJSONPathContainersWalk(root, normalizePreferredPathKeys(preferredKeys), seen, visit)
}

func visitJSONPathContainersWalk(root any, preferredKeys []string, seen map[any]struct{}, visit func(container map[string]any, path string) bool) bool {
	switch value := root.(type) {
	case map[string]any:
		ptr := reflectMapPointer(value)
		if _, ok := seen[ptr]; ok {
			return true
		}
		seen[ptr] = struct{}{}
		for _, key := range preferredKeys {
			if child, ok := value[key]; ok {
				if path := localFilePathFromValue(child, []string{".json"}); path != "" {
					if !visit(value, path) {
						return false
					}
					break
				}
			}
		}
		for _, key := range sortedMapKeys(value) {
			if !visitJSONPathContainersWalk(value[key], preferredKeys, seen, visit) {
				return false
			}
		}
	case []any:
		for _, child := range value {
			if !visitJSONPathContainersWalk(child, preferredKeys, seen, visit) {
				return false
			}
		}
	}
	return true
}

func sortedMapKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func reflectMapPointer(value map[string]any) uintptr {
	return reflect.ValueOf(value).Pointer()
}
