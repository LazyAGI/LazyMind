package currentmemory

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	PreferenceContextMaxCharsEnv     = "LAZYMIND_PREFERENCE_CONTEXT_MAX_CHARS"
	DefaultPreferenceContextMaxChars = 5000
)

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

func mustPreferenceContextMaxCharsFromEnv() int {
	value, err := PreferenceContextMaxCharsFromEnv()
	if err != nil {
		panic(err)
	}
	return value
}
