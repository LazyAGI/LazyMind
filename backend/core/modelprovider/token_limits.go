package modelprovider

import (
	"errors"
	"math/big"
	"strings"
)

func tokenLimitString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func nonemptyTokenLimit(value *string) bool { return tokenLimitString(value) != "" }

func tokenLimitNumber(value string) *big.Int {
	multiplier := int64(1)
	if strings.HasSuffix(value, "K") {
		value, multiplier = strings.TrimSuffix(value, "K"), 1024
	}
	if strings.HasSuffix(value, "M") {
		value, multiplier = strings.TrimSuffix(value, "M"), 1024*1024
	}
	number, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return nil
	}
	return number.Mul(number, big.NewInt(multiplier))
}

// Context is the existing max_input_tokens field. Optional directional limits
// are independent metadata; an empty string restores the automatic limit.
func resolveModelTokenLimits(modelType string, context, input, output *string) (*string, *string, error) {
	if !supportsUserMaxInputTokens(modelType) {
		if nonemptyTokenLimit(input) || nonemptyTokenLimit(output) {
			return nil, nil, errors.New("model token limits are only supported for llm or vlm models")
		}
		return nil, nil, nil
	}
	window := FallbackMaxInputTokens(modelType, context)
	contextValue, err := parseMaxInputTokens(tokenLimitString(window))
	if err != nil {
		return nil, nil, err
	}
	contextNumber := tokenLimitNumber(contextValue)
	limits := []*string{input, output}
	for index, raw := range limits {
		if !nonemptyTokenLimit(raw) {
			limits[index] = nil
			continue
		}
		normalized, err := parseMaxInputTokens(*raw)
		if err != nil {
			return nil, nil, errors.New("model token limits must be positive integers or use a K or M suffix")
		}
		if tokenLimitNumber(normalized).Cmp(contextNumber) > 0 {
			return nil, nil, errors.New("model input and output token limits must not exceed the context window")
		}
		limits[index] = &normalized
	}
	return limits[0], limits[1], nil
}
