// Package source defines the adapter boundary between downloaded packages and
// the knowledge-market ingestion pipeline. Each adapter encapsulates one data
// source's file selection and materialization rules.
package source

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// FileEntry is a materialized file inside a downloaded package.
type FileEntry struct {
	Path   string // slash-separated path relative to the package root
	Size   int64
	SHA256 string
}

// IngestUnit is one document submitted to the parsing/vectorizing pipeline.
type IngestUnit struct {
	LocalPath    string   // file that the parser should read
	DisplayName  string   // user-facing document name
	RelativePath string   // folder path inside the personal dataset
	Tags         []string // document tags forwarded to ingestion
}

// Adapter filters a source's raw files and converts selected files into
// ingestable document units.
type Adapter interface {
	ID() string
	// Match reports whether a package file belongs to this knowledge source.
	Match(path string) bool
	// Materialize converts selected files into document units.
	Materialize(ctx context.Context, root string, files []FileEntry) ([]IngestUnit, error)
}

// Options carries the free-form YAML options configured for an adapter.
type Options map[string]any

// Factory constructs an adapter from its configured options.
type Factory func(Options) (Adapter, error)

var registry = map[string]Factory{}

// Register installs an adapter factory. It is called from adapter init blocks.
func Register(id string, factory Factory) {
	id = strings.TrimSpace(id)
	if id == "" || factory == nil {
		panic("source: register adapter with empty id or nil factory")
	}
	if _, exists := registry[id]; exists {
		panic(fmt.Sprintf("source: duplicate adapter id %q", id))
	}
	registry[id] = factory
}

// New creates an adapter by its registered id.
func New(id string, options Options) (Adapter, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	factory, ok := registry[id]
	if !ok {
		return nil, fmt.Errorf("unknown source adapter %q", id)
	}
	return factory(options)
}

// RegisteredIDs returns the registered adapter ids for diagnostics.
func RegisteredIDs() []string {
	ids := make([]string, 0, len(registry))
	for id := range registry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Filter keeps only files accepted by the adapter.
func Filter(files []FileEntry, adapter Adapter) []FileEntry {
	if adapter == nil {
		return files
	}
	out := make([]FileEntry, 0, len(files))
	for _, file := range files {
		if adapter.Match(file.Path) {
			out = append(out, file)
		}
	}
	return out
}

func stringOption(options Options, key, fallback string) string {
	if options == nil {
		return fallback
	}
	switch value := options[key].(type) {
	case string:
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	case []any:
		if len(value) > 0 {
			if s, ok := value[0].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return fallback
}

func boolOption(options Options, key string, fallback bool) bool {
	if options == nil {
		return fallback
	}
	switch value := options[key].(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "yes", "1":
			return true
		case "false", "no", "0":
			return false
		}
	}
	return fallback
}

func stringSliceOption(options Options, key string, fallback []string) []string {
	if options == nil {
		return fallback
	}
	raw, ok := options[key]
	if !ok {
		return fallback
	}
	values, ok := raw.([]any)
	if !ok {
		if text, ok := raw.(string); ok {
			text = strings.TrimSpace(text)
			if text == "" {
				return fallback
			}
			return []string{text}
		}
		return fallback
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			if text = strings.TrimSpace(text); text != "" {
				out = append(out, text)
			}
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}
