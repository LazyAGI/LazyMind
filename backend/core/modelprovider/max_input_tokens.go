package modelprovider

import (
	"errors"
	"strings"
)

const (
	defaultLLMMaxInputTokens = "128K"
	maxInputTokensMaxLen     = 16
)

func supportsUserMaxInputTokens(modelType string) bool {
	return strings.EqualFold(strings.TrimSpace(modelType), "llm")
}

func parseMaxInputTokens(raw string) (string, error) {
	value := strings.ToUpper(strings.TrimSpace(raw))
	if len(value) == 0 || len(value) > maxInputTokensMaxLen || !maxInputTokensPattern.MatchString(value) {
		return "", errors.New("model max_input_tokens must be a positive integer or use a K or M suffix, for example 512, 128K, or 1M")
	}
	return value, nil
}

func applyCatalogMaxInputTokens(modelType string, raw *string) (*string, error) {
	if raw != nil && strings.TrimSpace(*raw) != "" {
		switch strings.TrimSpace(modelType) {
		case "llm", "vlm", "embed":
		default:
			return nil, errors.New("model max_input_tokens is only supported for llm, vlm, or embed models")
		}
		normalized, err := parseMaxInputTokens(*raw)
		if err != nil {
			return nil, err
		}
		return &normalized, nil
	}
	if supportsUserMaxInputTokens(modelType) {
		value := defaultLLMMaxInputTokens
		return &value, nil
	}
	return nil, nil
}

func resolveUserMaxInputTokens(modelType string, raw *string) (*string, error) {
	trimmed := ""
	if raw != nil {
		trimmed = strings.TrimSpace(*raw)
	}
	if !supportsUserMaxInputTokens(modelType) {
		if trimmed != "" {
			return nil, errors.New("model max_input_tokens is only supported for llm or vlm models")
		}
		return nil, nil
	}
	if trimmed == "" {
		trimmed = defaultLLMMaxInputTokens
	}
	normalized, err := parseMaxInputTokens(trimmed)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func resolveRequiredUserMaxInputTokens(modelType string, raw *string) (*string, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, errors.New("max_input_tokens is required")
	}
	if !supportsUserMaxInputTokens(modelType) {
		return nil, errors.New("model max_input_tokens is only supported for llm or vlm models")
	}
	normalized, err := parseMaxInputTokens(*raw)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}
