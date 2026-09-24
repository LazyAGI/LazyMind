package artifact

import (
	"os"
	"strconv"
	"strings"
)

func envEnabled(key string) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return false
	}
	if parsed, err := strconv.ParseBool(raw); err == nil {
		return parsed
	}
	return raw == "1" || strings.EqualFold(raw, "yes") || strings.EqualFold(raw, "on")
}

// Enabled is the sole Artifact V2 rollout switch. When it is off, callers use
// the legacy path; when it is on, all currently supported producers use V2.
func Enabled() bool { return envEnabled("LAZYMIND_ARTIFACT_V2_ENABLED") }
