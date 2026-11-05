package common

import "net/http"

func init() {
	for _, source := range []string{
		"answer length is outside the allowed range",
		"answer must be an integer in the allowed range",
		"unsupported answer format",
		"invalid answer validation range",
	} {
		registerAdditionalErrorAlias(source, "Invalid request", http.StatusBadRequest, 2000103)
	}
	for _, source := range []string{
		"%s requires at least one selection",
		"%s requires a scalar answer",
		"%s requires scalar selections",
		"%s contains duplicate selections",
		"%s requires a non-empty answer",
		"%s requires a yes/no answer",
		"%s has an unsupported choice",
	} {
		registerAdditionalErrorPattern(source, "Invalid request", http.StatusBadRequest, 2000103)
	}
}
