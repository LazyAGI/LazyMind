package modelprovider

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"lazymind/core/common"
	"lazymind/core/log"
)

type contextWindowsFile struct {
	Models map[string]string `yaml:"models"`
}

var (
	contextWindowsMu    sync.RWMutex
	contextWindowsByKey map[string]string
)

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

// LoadContextWindows loads model name → max_input_tokens mappings used when adding custom models.
func LoadContextWindows(yamlPath string) error {
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		return fmt.Errorf("read context windows: %w", err)
	}
	var file contextWindowsFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("parse context windows: %w", err)
	}
	next := make(map[string]string, len(file.Models))
	for name, tokens := range file.Models {
		key := normalizeContextWindowKey(name)
		if key == "" {
			return fmt.Errorf("context windows entry is missing a model name")
		}
		normalized, err := parseMaxInputTokens(tokens)
		if err != nil {
			return fmt.Errorf("context windows %q: %w", name, err)
		}
		if prev, ok := next[key]; ok && prev != normalized {
			return fmt.Errorf("context windows has conflicting values for %q: %s and %s", key, prev, normalized)
		}
		next[key] = normalized
	}

	contextWindowsMu.Lock()
	contextWindowsByKey = next
	contextWindowsMu.Unlock()
	return nil
}

// MustLoadContextWindows loads config/model_context_windows.yaml or exits the process.
func MustLoadContextWindows(yamlPath string) {
	if err := LoadContextWindows(yamlPath); err != nil {
		log.Logger.Fatal().Err(err).Str("path", yamlPath).Msg("load model context windows failed")
	}
	contextWindowsMu.RLock()
	count := len(contextWindowsByKey)
	contextWindowsMu.RUnlock()
	log.Logger.Info().Str("path", yamlPath).Int("models", count).Msg("model context windows loaded from YAML")
}

// LookupContextWindow returns the catalogued max_input_tokens for a model name.
func LookupContextWindow(name string) (string, bool) {
	contextWindowsMu.RLock()
	defer contextWindowsMu.RUnlock()
	if len(contextWindowsByKey) == 0 {
		return "", false
	}
	for _, key := range contextWindowLookupKeys(name) {
		if tokens, ok := contextWindowsByKey[key]; ok {
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
		if normalized != defaultLLMMaxInputTokens {
			return &normalized, nil
		}
	}
	if lookedUp, ok := LookupContextWindow(modelName); ok {
		return &lookedUp, nil
	}
	value := defaultLLMMaxInputTokens
	return &value, nil
}

type lookupContextWindowResponse struct {
	Name           string `json:"name"`
	MaxInputTokens string `json:"max_input_tokens"`
	Matched        bool   `json:"matched"`
}

// LookupContextWindowHTTP returns the YAML context window for a model name.
func LookupContextWindowHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		common.ReplyErr(w, "name is required", http.StatusBadRequest)
		return
	}
	tokens, matched := LookupContextWindow(name)
	if !matched {
		tokens = defaultLLMMaxInputTokens
	}
	common.ReplyOK(w, lookupContextWindowResponse{
		Name:           name,
		MaxInputTokens: tokens,
		Matched:        matched,
	})
}
