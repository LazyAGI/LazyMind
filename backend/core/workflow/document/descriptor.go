// Package document projects confirmed document representations, without executing actions.
package document

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"lazymind/core/algo"
	"lazymind/core/doc"
)

const IRSchema = "application/vnd.lazymind.writer+json"
const maxDocumentBytes = 20 << 20

type Descriptor struct {
	Representation string                      `json:"representation"`
	Schema         string                      `json:"schema"`
	Editable       bool                        `json:"editable"`
	Capabilities   []string                    `json:"capabilities" required:"true"`
	RenderContext  *algo.DocumentRenderContext `json:"render_context,omitempty"`
}

type ProjectionError struct {
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

func (e *ProjectionError) Error() string { return e.Code }

func Invalid() *ProjectionError { return &ProjectionError{Code: "DOCUMENT_INVALID"} }
func Unavailable() *ProjectionError {
	return &ProjectionError{Code: "DOCUMENT_INSPECTION_FAILED", Retryable: true}
}
func unreadable() *ProjectionError { return &ProjectionError{Code: "DOCUMENT_READ_FAILED"} }

// Project never rewrites the carrier. hint is read lazily, only when generic text
// needs its pinned workflow presentation metadata to establish Markdown intent.
func Project(ctx context.Context, raw json.RawMessage, contentType string, writable bool, hint func() (bool, error), targets ...json.RawMessage) (*Descriptor, *ProjectionError) {
	content, projectionError := InspectContent(ctx, raw, contentType, hint, targets...)
	if content == nil {
		return nil, projectionError
	}

	capabilities := []string{}
	if writable {
		capabilities = append(capabilities, "save")
	}
	return &Descriptor{Representation: content.Representation, Schema: content.Schema, Editable: writable, Capabilities: capabilities, RenderContext: content.RenderContext}, nil
}

// Content is the confirmed logical document, never a file locator. Action
// callers must put Value inside a data envelope at the Algorithm boundary.
type Content struct {
	Value          json.RawMessage
	Representation string
	Schema         string
	RenderContext  *algo.DocumentRenderContext
}

func InspectContent(ctx context.Context, raw json.RawMessage, contentType string, hint func() (bool, error), targets ...json.RawMessage) (*Content, *ProjectionError) {
	value, schema, candidate, projectionError := prepare(raw, contentType, hint)
	if projectionError != nil || !candidate {
		return nil, projectionError
	}
	artifact := inspectionArtifact(raw, value, contentType, targets)
	result, status, err := algo.InspectDocument(ctx, algo.DocumentInspectRequest{Artifact: artifact, Schema: schema})
	if err != nil {
		if status == 422 {
			return nil, Invalid()
		}
		return nil, Unavailable()
	}
	if !*result.IsDocument {
		if schema != "" {
			return nil, Unavailable()
		}
		return nil, nil
	}
	if schema != "" && schema != *result.Schema {
		return nil, Unavailable()
	}
	if schema == "" && *result.Representation != "ir" {
		return nil, nil
	}
	if result.RenderContext != nil && !result.RenderContext.ValidFor(value) {
		return nil, Unavailable()
	}
	return &Content{Value: value, Representation: *result.Representation, Schema: *result.Schema, RenderContext: result.RenderContext}, nil
}

func schemaName(value string) string {
	switch strings.TrimSpace(value) {
	case "markdown", "text/markdown":
		return "text/markdown"
	case IRSchema, "lazyllm.tools.writer.data_models.writer_ir.WriterDocument":
		return IRSchema
	default:
		return strings.TrimSpace(value)
	}
}

func prepare(raw json.RawMessage, contentType string, hint func() (bool, error)) (json.RawMessage, string, bool, *ProjectionError) {
	if len(raw) == 0 {
		return nil, "", false, nil
	}
	if len(raw) > maxDocumentBytes {
		return nil, "", false, Invalid()
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil, "", false, Invalid()
	}
	value, schema, unpackError := unpack(value, "")
	if unpackError != nil {
		return nil, "", false, unpackError
	}
	if schema != "" && schema != "text/markdown" && schema != IRSchema {
		return nil, "", false, nil
	}

	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if schema == "" {
		switch ct {
		case "markdown", "text/markdown":
			schema = "text/markdown"
		case IRSchema:
			schema = IRSchema
		case "", "text", "json", "application/json", "file":
		default:
			return nil, "", false, nil
		}
	}
	if object, ok := value.(map[string]any); ok {
		path, _ := object["path"].(string)
		if path == "" {
			path, _ = object["url"].(string)
		}
		if path != "" {
			if schema == "" {
				switch strings.ToLower(filepath.Ext(path)) {
				case ".md", ".markdown":
					schema = "text/markdown"
				case ".lmd":
					schema = IRSchema
				default:
					return nil, "", false, nil
				}
			}
			content, err := doc.ReadLocalArtifactFile(path, maxDocumentBytes)
			if err != nil {
				return nil, "", false, unreadable()
			}
			if !utf8.Valid(content) {
				return nil, "", false, Invalid()
			}
			if schema == "text/markdown" {
				value = string(content)
			} else {
				if json.Unmarshal(content, &value) != nil {
					return nil, "", false, Invalid()
				}
				value, schema, unpackError = unpack(value, schema)
				if unpackError != nil {
					return nil, "", false, unpackError
				}
				// A file envelope may contain inline data, never another file carrier.
				if obj, ok := value.(map[string]any); ok && (obj["path"] != nil || obj["url"] != nil) {
					return nil, "", false, Invalid()
				}
			}
		}
	}

	if _, ok := value.(string); ok && schema == "" {
		if ct != "" && ct != "text" {
			return nil, "", false, nil
		}
		markdown, err := hint()
		if err != nil {
			return nil, "", false, Unavailable()
		}
		if !markdown {
			return nil, "", false, nil
		}
		schema = "text/markdown"
	}
	switch value.(type) {
	case string:
		if schema == IRSchema {
			return nil, "", false, Invalid()
		}
	case map[string]any:
	default:
		if schema != "" {
			return nil, "", false, Invalid()
		}
		return nil, "", false, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, "", false, Invalid()
	}
	return encoded, schema, true, nil
}

// Fully resolve carrier envelopes before Algorithm can perform its own single
// data unwrap. Explicit declarations at any layer must agree.
func unpack(value any, schema string) (any, string, *ProjectionError) {
	for {
		object, ok := value.(map[string]any)
		if !ok {
			return value, schema, nil
		}
		for _, key := range []string{"schema", "schema_name"} {
			if declaration, exists := object[key]; exists {
				name, ok := declaration.(string)
				if !ok {
					return nil, "", Invalid()
				}
				name = schemaName(name)
				if schema != "" && name != "" && schema != name {
					return nil, "", Invalid()
				}
				if name != "" {
					schema = name
				}
			}
		}
		if data, exists := object["data"]; exists {
			value = data
			continue
		}
		if text, exists := object["text"]; exists {
			value = text
			continue
		}
		return value, schema, nil
	}
}

// ReadContent resolves local carriers for transaction-time baseline checks.
// Full semantic inspection remains at the Algorithm boundary; this function
// never calls a service while a Session transaction is held.
func ReadContent(raw json.RawMessage, contentType string, hint func() (bool, error)) (*Content, *ProjectionError) {
	value, schema, candidate, failure := prepare(raw, contentType, hint)
	if failure != nil || !candidate {
		return nil, failure
	}
	if schema == "" {
		var ir map[string]json.RawMessage
		if json.Unmarshal(value, &ir) != nil || ir["document_id"] == nil || ir["blocks"] == nil {
			return nil, nil
		}
		schema = IRSchema
	}
	representation := "markdown"
	if schema == IRSchema {
		var ir struct {
			DocumentID string            `json:"document_id"`
			Blocks     []json.RawMessage `json:"blocks"`
		}
		if json.Unmarshal(value, &ir) != nil || ir.DocumentID == "" || ir.Blocks == nil {
			return nil, Invalid()
		}
		representation = "ir"
	}
	return &Content{Value: value, Schema: schema, Representation: representation}, nil
}
