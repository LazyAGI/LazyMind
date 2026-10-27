// Package modelsettings validates per-model declarations and execution options.
// Unknown support stays distinct from an explicit false, including on old rows.
package modelsettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const MaxPrefixCharacters = 2048
const MaxBatchSize = 2048
const MaxJSONBytes = 24 * 1024

type FeatureFlags struct {
	Vision      *bool `json:"vision,omitempty"`
	ToolCalling *bool `json:"tool_calling,omitempty"`
	Reasoning   *bool `json:"reasoning,omitempty"`
}

type EmbeddingOptions struct {
	QueryPrefix   string `json:"query_prefix,omitempty"`
	PassagePrefix string `json:"passage_prefix,omitempty"`
	BatchSize     *int   `json:"batch_size,omitempty" nullable:"true"`
}

type Settings struct {
	FeatureSupport *FeatureFlags     `json:"feature_support,omitempty"`
	FeatureEnabled *FeatureFlags     `json:"feature_enabled,omitempty"`
	Embedding      *EmbeddingOptions `json:"embedding,omitempty"`
}

func Decode(raw string) (Settings, error) {
	var s Settings
	if strings.TrimSpace(raw) == "" {
		return s, nil
	}
	if len(raw) > MaxJSONBytes {
		return s, errors.New("model settings exceed size limit")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s); err != nil {
		return s, errors.New("invalid model settings")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return s, errors.New("invalid model settings")
	}
	return s, nil
}

func hasFlags(flags *FeatureFlags) bool {
	return flags != nil && (flags.Vision != nil || flags.ToolCalling != nil || flags.Reasoning != nil)
}

func Normalize(modelType string, s *Settings) (string, error) {
	if s == nil {
		return "{}", nil
	}
	normalized := *s
	chat := modelType == "llm" || modelType == "vlm"
	if !chat && (hasFlags(s.FeatureSupport) || hasFlags(s.FeatureEnabled)) {
		return "", errors.New("model features require a chat model")
	}
	if s.FeatureSupport != nil && s.FeatureEnabled != nil {
		for _, pair := range [][2]*bool{{s.FeatureSupport.Vision, s.FeatureEnabled.Vision}, {s.FeatureSupport.ToolCalling, s.FeatureEnabled.ToolCalling}, {s.FeatureSupport.Reasoning, s.FeatureEnabled.Reasoning}} {
			if pair[0] != nil && !*pair[0] && pair[1] != nil && *pair[1] {
				return "", errors.New("cannot enable a feature declared unsupported")
			}
		}
	}
	if modelType == "vlm" && s.FeatureSupport != nil && s.FeatureSupport.Vision != nil && !*s.FeatureSupport.Vision {
		return "", errors.New("vision model type requires vision support")
	}
	if !hasFlags(normalized.FeatureSupport) {
		normalized.FeatureSupport = nil
	}
	if !hasFlags(normalized.FeatureEnabled) {
		normalized.FeatureEnabled = nil
	}
	if s.Embedding != nil {
		if modelType != "embed" && modelType != "embedding" && modelType != "embed_main" {
			return "", errors.New("embedding options require a text embedding model")
		}
		for _, prefix := range []string{s.Embedding.QueryPrefix, s.Embedding.PassagePrefix} {
			if !utf8.ValidString(prefix) || utf8.RuneCountInString(prefix) > MaxPrefixCharacters {
				return "", errors.New("embedding prefix exceeds 2048 characters")
			}
		}
		if s.Embedding.BatchSize != nil && (*s.Embedding.BatchSize < 1 || *s.Embedding.BatchSize > MaxBatchSize) {
			return "", errors.New("embedding batch size must be between 1 and 2048")
		}
		if s.Embedding.QueryPrefix == "" && s.Embedding.PassagePrefix == "" && s.Embedding.BatchSize == nil {
			normalized.Embedding = nil
		}
	}
	raw, err := json.Marshal(normalized)
	if err != nil || len(raw) > MaxJSONBytes {
		return "", errors.New("invalid model settings")
	}
	return string(raw), nil
}

func effective(supported, enabled *bool) *bool {
	if supported != nil && !*supported {
		value := false
		return &value
	}
	if enabled != nil {
		value := *enabled
		return &value
	}
	if supported != nil {
		value := *supported
		return &value
	}
	return nil
}

// Effective returns explicit per-model gates. Nil tools/reasoning retains legacy
// behavior; vision falls back to the existing vision field and model type.
func Effective(s Settings, legacyVision bool, modelType string) FeatureFlags {
	modelType = strings.ToLower(strings.TrimSpace(modelType))
	support, enabled := FeatureFlags{}, FeatureFlags{}
	if s.FeatureSupport != nil {
		support = *s.FeatureSupport
	}
	if s.FeatureEnabled != nil {
		enabled = *s.FeatureEnabled
	}
	result := FeatureFlags{Vision: effective(support.Vision, enabled.Vision), ToolCalling: effective(support.ToolCalling, enabled.ToolCalling), Reasoning: effective(support.Reasoning, enabled.Reasoning)}
	if result.Vision == nil {
		value := legacyVision || modelType == "vlm"
		result.Vision = &value
	}
	return result
}

func EmbeddingIdentityChanged(before, after string) bool {
	a, errA := Decode(before)
	b, errB := Decode(after)
	if errA != nil || errB != nil {
		return !bytes.Equal([]byte(before), []byte(after))
	}
	if a.Embedding == nil {
		a.Embedding = &EmbeddingOptions{}
	}
	if b.Embedding == nil {
		b.Embedding = &EmbeddingOptions{}
	}
	return a.Embedding.QueryPrefix != b.Embedding.QueryPrefix || a.Embedding.PassagePrefix != b.Embedding.PassagePrefix
}
