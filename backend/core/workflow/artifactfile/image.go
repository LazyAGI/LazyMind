package artifactfile

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"lazymind/core/doc"
)

// NormalizeImageValue gives publishers and readers one image representation.
// Core image URLs are stored without expiring signatures and signed on read.
func NormalizeImageValue(contentType string, raw json.RawMessage) (json.RawMessage, error) {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if ct != "image" && !strings.HasPrefix(ct, "image/") {
		return raw, nil
	}
	invalid := errors.New("image value requires a non-empty image URL, an object with path/url, or an uploaded image via local_path")
	value, ok := object(raw)
	if !ok {
		var source string
		if json.Unmarshal(raw, &source) != nil || !IsPublicReference(source) {
			return nil, invalid
		}
		value = map[string]any{"path": strings.TrimSpace(source)}
	}
	if stringField(value, "storage") == inlineStorage {
		if strings.TrimSpace(stringField(value, "content_base64")) == "" {
			return nil, invalid
		}
		return json.Marshal(value) // Materialize validates and persists the bytes.
	}
	source := strings.TrimSpace(stringField(value, "path"))
	if source == "" {
		source = strings.TrimSpace(stringField(value, "url"))
	}
	if source == "" || (strings.HasPrefix(source, "data:") && !strings.HasPrefix(source, "data:image/")) {
		return nil, invalid
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		if parsed, err := url.Parse(source); err != nil || parsed.Host == "" {
			return nil, invalid
		}
	}
	if IsPublicReference(source) {
		if reference := doc.StaticFileReferenceFromAnyStoragePath(source); reference != "" {
			source = reference
		}
	}
	value["path"] = source
	delete(value, "url")
	return json.Marshal(value)
}
