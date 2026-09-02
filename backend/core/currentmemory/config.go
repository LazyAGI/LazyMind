package currentmemory

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	PreferenceIndexMaxItemsEnv       = "LAZYMIND_PREFERENCE_INDEX_MAX_ITEMS"
	DefaultPreferenceIndexMaxItems   = 100
	PreferenceContextMaxCharsEnv     = "LAZYMIND_PREFERENCE_CONTEXT_MAX_CHARS"
	DefaultPreferenceContextMaxChars = 5000
)

func PreferenceIndexMaxItemsFromEnv() (int, error) {
	raw, configured := os.LookupEnv(PreferenceIndexMaxItemsEnv)
	if !configured {
		return DefaultPreferenceIndexMaxItems, nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("%s must be a positive integer", PreferenceIndexMaxItemsEnv)
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf(
			"%s must be a positive integer, got %q",
			PreferenceIndexMaxItemsEnv,
			raw,
		)
	}
	return value, nil
}

func PreferenceContextMaxCharsFromEnv() (int, error) {
	raw, configured := os.LookupEnv(PreferenceContextMaxCharsEnv)
	if !configured {
		return DefaultPreferenceContextMaxChars, nil
	}
	raw = strings.TrimSpace(raw)
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", PreferenceContextMaxCharsEnv, raw)
	}
	return value, nil
}

func mustPreferenceIndexMaxItemsFromEnv() int {
	value, err := PreferenceIndexMaxItemsFromEnv()
	if err != nil {
		panic(err)
	}
	return value
}
