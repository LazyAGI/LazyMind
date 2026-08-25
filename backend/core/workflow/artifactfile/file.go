package artifactfile

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"lazymind/core/doc"
)

const (
	inlineStorage  = "inline_base64"
	managedStorage = "managed_file"
	maxFileBytes   = 20 << 20
)

// Materialize moves an inline file submitted by an external Host into
// LazyMind-owned storage. The returned directory is non-empty only when this
// call created files; callers should remove it if their database transaction
// later rolls back.
func Materialize(sessionID, artifactID string, raw json.RawMessage) (json.RawMessage, string, error) {
	value, ok := object(raw)
	if !ok || strings.TrimSpace(stringField(value, "storage")) != inlineStorage {
		return clone(raw), "", nil
	}
	encoded := strings.TrimSpace(stringField(value, "content_base64"))
	if encoded == "" || len(encoded) > base64.StdEncoding.EncodedLen(maxFileBytes) {
		return nil, "", errors.New("inline artifact content is missing or exceeds 20 MiB")
	}
	content, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(content) > maxFileBytes {
		return nil, "", errors.New("inline artifact content is invalid or exceeds 20 MiB")
	}
	name := strings.TrimSpace(stringField(value, "name"))
	if name == "" {
		name = strings.TrimSpace(stringField(value, "filename"))
	}
	if !validFilename(name) {
		return nil, "", errors.New("inline artifact filename is invalid")
	}
	if size, present := int64Field(value, "size"); present && size != int64(len(content)) {
		return nil, "", errors.New("inline artifact size does not match content")
	}
	sum := sha256.Sum256(content)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	if expected := strings.TrimSpace(stringField(value, "sha256")); expected != "" && !strings.EqualFold(expected, hash) {
		return nil, "", errors.New("inline artifact sha256 does not match content")
	}

	scope := sha256.Sum256([]byte(sessionID))
	directory := filepath.Join(doc.UploadRoot(), "workflow-artifacts", hex.EncodeToString(scope[:16]), artifactID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, "", fmt.Errorf("create Workflow artifact directory: %w", err)
	}
	fullPath := filepath.Join(directory, name)
	if err := writeAtomic(fullPath, content); err != nil {
		_ = os.RemoveAll(directory)
		return nil, "", err
	}

	delete(value, "content_base64")
	value["storage"] = managedStorage
	value["name"] = name
	value["filename"] = name
	value["size"] = len(content)
	value["sha256"] = hash
	value["path"] = fullPath
	normalized, err := json.Marshal(value)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, "", errors.New("encode managed artifact metadata")
	}
	return normalized, directory, nil
}

// Inline returns file bytes only for an explicit single-artifact read. Lists
// and Workflow projections keep using metadata or signed browser URLs.
func Inline(raw json.RawMessage) (json.RawMessage, error) {
	value, ok := object(raw)
	if !ok || strings.TrimSpace(stringField(value, "storage")) != managedStorage {
		return clone(raw), nil
	}
	fullPath, ok := managedFilePath(strings.TrimSpace(stringField(value, "path")))
	if !ok {
		return nil, errors.New("managed artifact path is outside LazyMind storage")
	}
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("read managed artifact: %w", err)
	}
	if len(content) > maxFileBytes {
		return nil, errors.New("managed artifact exceeds 20 MiB")
	}
	sum := sha256.Sum256(content)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	if expected := strings.TrimSpace(stringField(value, "sha256")); expected != "" && !strings.EqualFold(expected, hash) {
		return nil, errors.New("managed artifact sha256 does not match content")
	}
	delete(value, "path")
	delete(value, "url")
	value["storage"] = inlineStorage
	value["size"] = len(content)
	value["sha256"] = hash
	value["content_base64"] = base64.StdEncoding.EncodeToString(content)
	return json.Marshal(value)
}

// Metadata removes file bytes and server-private paths from list responses.
func Metadata(raw json.RawMessage) json.RawMessage {
	value, ok := object(raw)
	if !ok {
		return clone(raw)
	}
	storage := strings.TrimSpace(stringField(value, "storage"))
	if storage != managedStorage && storage != inlineStorage {
		return clone(raw)
	}
	reference := ""
	if storage == managedStorage {
		reference = doc.StaticFileReferenceFromAnyStoragePath(strings.TrimSpace(stringField(value, "path")))
	}
	delete(value, "content_base64")
	delete(value, "path")
	delete(value, "url")
	if reference != "" {
		value["url"] = reference
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return clone(raw)
	}
	return encoded
}

// Reference returns a stable, unsigned browser reference and its filename.
// The frontend exchanges the reference for a short-lived signed URL.
func Reference(raw json.RawMessage) (string, string, bool) {
	value, ok := object(raw)
	if !ok || strings.TrimSpace(stringField(value, "storage")) != managedStorage {
		return "", "", false
	}
	fullPath, ok := managedFilePath(strings.TrimSpace(stringField(value, "path")))
	if !ok {
		return "", "", false
	}
	reference := doc.StaticFileReferenceFromAnyStoragePath(fullPath)
	if reference == "" {
		return "", "", false
	}
	name := strings.TrimSpace(stringField(value, "name"))
	if name == "" {
		name = filepath.Base(fullPath)
	}
	return name, reference, true
}

func object(raw json.RawMessage) (map[string]any, bool) {
	var value map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, false
	}
	return value, true
}

func clone(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

func stringField(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return text
}

func int64Field(value map[string]any, key string) (int64, bool) {
	switch number := value[key].(type) {
	case float64:
		if number < 0 || number != float64(int64(number)) {
			return 0, true
		}
		return int64(number), true
	case json.Number:
		result, err := number.Int64()
		return result, err == nil
	default:
		return 0, false
	}
}

func validFilename(name string) bool {
	if name == "" || name == "." || name == ".." || utf8.RuneCountInString(name) > 255 ||
		filepath.Base(name) != name || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, char := range name {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func managedFilePath(fullPath string) (string, bool) {
	path, err := filepath.Abs(strings.TrimSpace(fullPath))
	if err != nil {
		return "", false
	}
	root, err := filepath.Abs(filepath.Join(doc.UploadRoot(), "workflow-artifacts"))
	if err != nil {
		return "", false
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	info, err := os.Stat(path)
	return path, err == nil && info.Mode().IsRegular()
}

func writeAtomic(fullPath string, content []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(fullPath), ".artifact-*")
	if err != nil {
		return fmt.Errorf("create managed artifact: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write managed artifact: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, fullPath); err != nil {
		return fmt.Errorf("commit managed artifact: %w", err)
	}
	return nil
}
