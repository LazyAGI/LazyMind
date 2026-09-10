package artifactfile

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"lazymind/core/doc"
)

// IsPublicReference identifies stored URLs that must not be read as host files.
func IsPublicReference(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") ||
		strings.HasPrefix(value, "data:") || strings.HasPrefix(value, "/static-files/") ||
		strings.HasPrefix(value, "/api/core/static-files/")
}

// Snapshot copies a local file into a new managed artifact before publishing it.
// Editors may replace their upload or working file later without altering a
// confirmed revision. Remote URLs remain references; no remote bytes are fetched.
func Snapshot(sessionID, artifactID, contentType string, raw json.RawMessage) (json.RawMessage, string, error) {
	value, ok := object(raw)
	if !ok {
		return clone(raw), "", nil
	}
	if stringField(value, "storage") == inlineStorage {
		return Materialize(sessionID, artifactID, raw)
	}
	source := strings.TrimSpace(stringField(value, "path"))
	if source == "" || IsPublicReference(source) || contentType == "json" || contentType == "application/json" || contentType == "text" || strings.HasPrefix(contentType, "text/") {
		return clone(raw), "", nil
	}
	// Reuse the Core storage boundary after resolving symlinks. Never snapshot an
	// arbitrary host path supplied through a public artifact-edit request.
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		return nil, "", err
	}
	if !filepath.IsAbs(source) || doc.StaticFileReferenceFromAnyStoragePath(source) == "" {
		return nil, "", errors.New("artifact file is outside LazyMind storage")
	}
	file, err := os.Open(source)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return nil, "", errors.New("artifact must be a regular file of at most 20 MiB")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(content) > maxFileBytes {
		return nil, "", errors.New("artifact exceeds 20 MiB")
	}
	value["storage"] = inlineStorage
	value["content_base64"] = base64.StdEncoding.EncodeToString(content)
	if stringField(value, "name") == "" && stringField(value, "filename") == "" {
		value["name"] = filepath.Base(source)
	}
	delete(value, "url")
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	return Materialize(sessionID, artifactID, encoded)
}
