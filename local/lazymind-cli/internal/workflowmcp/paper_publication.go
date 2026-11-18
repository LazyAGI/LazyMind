package workflowmcp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Preserve the validated paper bundle as directly usable MCP outputs. No model rewrites file payloads.
func savePaperPublicationOutputs(directory string, artifacts any) ([]map[string]any, error) {
	raw, err := json.Marshal(artifacts)
	if err != nil {
		return nil, err
	}
	var items []struct {
		Slot        string          `json:"slot"`
		Seq         int             `json:"seq"`
		ContentType string          `json:"content_type"`
		Value       json.RawMessage `json:"value"`
	}
	if json.Unmarshal(raw, &items) != nil || len(items) == 0 || len(items) > 128 {
		return nil, errors.New("invalid validated paper bundle")
	}
	outputs := make([]map[string]any, 0, len(items))
	for index, item := range items {
		if item.Slot == "" || item.Seq < 1 || len(item.Value) == 0 || string(item.Value) == "null" {
			return nil, errors.New("paper artifact requires slot, positive seq and exact value")
		}
		folder := filepath.Join(directory, fmt.Sprintf("publication-%03d", index+1))
		if err := os.Mkdir(folder, 0700); err != nil {
			return nil, err
		}
		output := map[string]any{"slot": item.Slot, "seq": item.Seq, "content_type": item.ContentType}
		switch item.ContentType {
		case "file":
			var value struct {
				Name    string `json:"name"`
				Content string `json:"content_base64"`
				SHA256  string `json:"sha256"`
				Size    int    `json:"size"`
			}
			if json.Unmarshal(item.Value, &value) != nil || value.Name == "" || value.Name == "." || value.Name == ".." ||
				filepath.Base(value.Name) != value.Name || strings.ContainsAny(value.Name, "\\\x00") {
				return nil, errors.New("unsafe paper artifact filename")
			}
			data, err := base64.StdEncoding.Strict().DecodeString(value.Content)
			if err != nil || len(data) > maxLocalArtifactBytes || len(data) != value.Size ||
				fmt.Sprintf("%x", sha256.Sum256(data)) != strings.TrimPrefix(value.SHA256, "sha256:") {
				return nil, errors.New("paper artifact size or content hash mismatch")
			}
			path := filepath.Join(folder, value.Name)
			if err := os.WriteFile(path, data, 0600); err != nil {
				return nil, err
			}
			output["local_path"] = path
		case "json":
			path := filepath.Join(folder, "value.json")
			if err := os.WriteFile(path, item.Value, 0600); err != nil {
				return nil, err
			}
			output["value_file"] = path
		default:
			return nil, errors.New("unsupported validated paper artifact type")
		}
		outputs = append(outputs, output)
	}
	return outputs, nil
}
