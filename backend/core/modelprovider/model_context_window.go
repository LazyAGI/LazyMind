package modelprovider

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"lazymind/core/log"
)

var (
	contextWindowsMu       sync.RWMutex
	contextWindowsByType   map[string]map[string]string
	contextWindowTypeOrder []string
)

func normalizeModelType(modelType string) string {
	value := strings.ToLower(strings.TrimSpace(modelType))
	if value == "embedding" {
		return "embed"
	}
	return value
}

func normalizeContextWindowKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func contextWindowLookupKeys(name string) []string {
	key := normalizeContextWindowKey(name)
	if key == "" {
		return nil
	}
	keys := []string{key}
	if i := strings.LastIndex(key, "/"); i >= 0 && i+1 < len(key) {
		if tail := key[i+1:]; tail != "" && tail != key {
			keys = append(keys, tail)
		}
	}
	dotted := strings.ReplaceAll(key, ".", "-")
	if dotted != key {
		keys = append(keys, dotted)
	}
	return keys
}

func lookupInType(byKey map[string]string, name string) (string, bool) {
	if len(byKey) == 0 {
		return "", false
	}
	for _, key := range contextWindowLookupKeys(name) {
		if tokens, ok := byKey[key]; ok {
			return tokens, true
		}
	}
	return "", false
}

// LoadContextWindows loads typed model-name → max_input_tokens mappings.
func LoadContextWindows(yamlPath string) error {
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		return fmt.Errorf("read context windows: %w", err)
	}
	var file map[string]map[string]string
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("parse context windows: %w", err)
	}
	next := make(map[string]map[string]string, len(file))
	for section, models := range file {
		modelType := normalizeModelType(section)
		if modelType == "" {
			return fmt.Errorf("context windows entry is missing a model type")
		}
		bucket := next[modelType]
		if bucket == nil {
			bucket = make(map[string]string, len(models))
			next[modelType] = bucket
		}
		for name, tokens := range models {
			key := normalizeContextWindowKey(name)
			if key == "" {
				return fmt.Errorf("context windows %s entry is missing a model name", modelType)
			}
			normalized, err := parseMaxInputTokens(tokens)
			if err != nil {
				return fmt.Errorf("context windows %s %q: %w", modelType, name, err)
			}
			if prev, ok := bucket[key]; ok && prev != normalized {
				return fmt.Errorf("context windows has conflicting %s values for %q: %s and %s", modelType, key, prev, normalized)
			}
			bucket[key] = normalized
		}
	}

	order := make([]string, 0, len(next))
	for _, preferred := range []string{"llm", "vlm", "embed"} {
		if _, ok := next[preferred]; ok {
			order = append(order, preferred)
		}
	}
	rest := make([]string, 0, len(next))
	for modelType := range next {
		if modelType != "llm" && modelType != "vlm" && modelType != "embed" {
			rest = append(rest, modelType)
		}
	}
	sort.Strings(rest)
	order = append(order, rest...)

	contextWindowsMu.Lock()
	contextWindowsByType = next
	contextWindowTypeOrder = order
	contextWindowsMu.Unlock()
	return nil
}

// MustLoadContextWindows loads config/model_context_windows.yaml or exits the process.
func MustLoadContextWindows(yamlPath string) {
	if err := LoadContextWindows(yamlPath); err != nil {
		log.Logger.Fatal().Err(err).Str("path", yamlPath).Msg("load model context windows failed")
	}
	contextWindowsMu.RLock()
	count := 0
	for _, bucket := range contextWindowsByType {
		count += len(bucket)
	}
	types := append([]string(nil), contextWindowTypeOrder...)
	contextWindowsMu.RUnlock()
	log.Logger.Info().Str("path", yamlPath).Int("models", count).Strs("types", types).Msg("model context windows loaded from YAML")
}

// lookupMaxInputTokens returns the YAML max_input_tokens for a model name.
// An empty modelType searches llm, vlm, embed, then any other typed sections.
func lookupMaxInputTokens(name, modelType string) (string, bool) {
	contextWindowsMu.RLock()
	defer contextWindowsMu.RUnlock()
	if len(contextWindowsByType) == 0 {
		return "", false
	}
	if wanted := normalizeModelType(modelType); wanted != "" {
		return lookupInType(contextWindowsByType[wanted], name)
	}
	for _, section := range contextWindowTypeOrder {
		if tokens, ok := lookupInType(contextWindowsByType[section], name); ok {
			return tokens, true
		}
	}
	return "", false
}

func resolveAddModelMaxInputTokens(modelType, modelName string, raw *string) (*string, error) {
	if !supportsLookupMaxInputTokens(modelType) {
		return nil, nil
	}
	if raw != nil && strings.TrimSpace(*raw) != "" {
		normalized, err := parseMaxInputTokens(*raw)
		if err != nil {
			return nil, err
		}
		return &normalized, nil
	}
	if lookedUp, ok := lookupMaxInputTokens(modelName, modelType); ok {
		return &lookedUp, nil
	}
	value := DefaultLLMMaxInputTokens
	return &value, nil
}

func resolveSeededMaxInputTokens(modelType, modelName string) (*string, error) {
	if lookedUp, ok := lookupMaxInputTokens(modelName, modelType); ok {
		return &lookedUp, nil
	}
	if supportsUserMaxInputTokens(modelType) {
		value := DefaultLLMMaxInputTokens
		return &value, nil
	}
	return nil, nil
}
