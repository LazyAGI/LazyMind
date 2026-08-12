package executor

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxEmbeddedArtifactBytes = 24 << 20

// materializeEmbeddedArtifactValue moves binary output across the remote
// Executor boundary without assuming that the Executor and Core share a
// filesystem. Only data URLs are materialized; ordinary URLs and Core-owned
// paths remain unchanged.
func materializeEmbeddedArtifactValue(
	raw json.RawMessage, contentType, root, sessionID, attemptID, slot string, seq int,
) (json.RawMessage, error) {
	if contentType != "file" && contentType != "file_list" && contentType != "image" {
		return raw, nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw, nil
	}
	pathsKey := "path"
	filenameKey := "filename"
	paths, scalar := value[pathsKey].(string)
	items := []string{paths}
	if contentType == "file_list" {
		pathsKey = "paths"
		filenameKey = "filenames"
		scalar = false
		items = stringSlice(value[pathsKey])
	}
	if len(items) == 0 {
		return raw, nil
	}
	names := artifactFilenames(value, filenameKey, len(items))
	changed := false
	for index, item := range items {
		if !strings.HasPrefix(strings.TrimSpace(item), "data:") {
			continue
		}
		stored, err := storeEmbeddedArtifact(
			root, sessionID, attemptID, slot, seq, index, names[index], item,
		)
		if err != nil {
			return nil, err
		}
		items[index] = stored
		storedName := filepath.Base(stored)
		internalPrefix := fmt.Sprintf("%s-%d-%d-", safeArtifactComponent(slot), seq, index)
		names[index] = strings.TrimPrefix(storedName, internalPrefix)
		changed = true
	}
	if !changed {
		return raw, nil
	}
	if scalar {
		value[pathsKey] = items[0]
		value[filenameKey] = names[0]
	} else {
		value[pathsKey] = items
		value[filenameKey] = names
	}
	return json.Marshal(value)
}

func storeEmbeddedArtifact(
	root, sessionID, attemptID, slot string, seq, index int, filename, dataURL string,
) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("artifact storage root is not configured")
	}
	header, payload, ok := strings.Cut(strings.TrimSpace(dataURL), ",")
	if !ok || !strings.HasPrefix(header, "data:") || !strings.HasSuffix(header, ";base64") {
		return "", errors.New("embedded artifact must be a base64 data URL")
	}
	mimeType := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	if base64.StdEncoding.DecodedLen(len(payload)) > maxEmbeddedArtifactBytes {
		return "", fmt.Errorf("embedded artifact exceeds %d bytes", maxEmbeddedArtifactBytes)
	}
	content, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", errors.New("embedded artifact contains invalid base64")
	}
	if len(content) > maxEmbeddedArtifactBytes {
		return "", fmt.Errorf("embedded artifact exceeds %d bytes", maxEmbeddedArtifactBytes)
	}
	filename = safeArtifactFilename(filename, mimeType)
	dir := filepath.Join(root, "workflow-artifacts", safeArtifactComponent(sessionID), safeArtifactComponent(attemptID))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	target := filepath.Join(dir, fmt.Sprintf("%s-%d-%d-%s", safeArtifactComponent(slot), seq, index, filename))
	temporary, err := os.CreateTemp(dir, ".artifact-*")
	if err != nil {
		return "", err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o640); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryName, target); err != nil {
		return "", err
	}
	return target, nil
}

func stringSlice(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			text = ""
		}
		result = append(result, text)
	}
	return result
}

func artifactFilenames(value map[string]any, key string, count int) []string {
	names := make([]string, count)
	if key == "filename" {
		names[0], _ = value[key].(string)
		return names
	}
	copy(names, stringSlice(value[key]))
	return names
}

func safeArtifactFilename(name, mimeType string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" || name == "." {
		name = "artifact"
	}
	for utf8.RuneCountInString(name) > 160 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	if filepath.Ext(name) == "" {
		if extensions, _ := mime.ExtensionsByType(mimeType); len(extensions) > 0 {
			name += extensions[0]
		}
	}
	return name
}

func safeArtifactComponent(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, strings.TrimSpace(value))
	if value == "" || value == "." || value == ".." {
		return "unknown"
	}
	return value
}
