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

func SchemaEnabled() bool     { return envEnabled("LAZYMIND_ARTIFACT_V2_SCHEMA_ENABLED") }
func ProjectionEnabled() bool { return envEnabled("LAZYMIND_ARTIFACT_V2_PROJECTION_ENABLED") }
func ChatDualWriteEnabled() bool {
	return envEnabled("LAZYMIND_ARTIFACT_V2_CHAT_DUAL_WRITE")
}
func ReadPreferV2() bool { return envEnabled("LAZYMIND_ARTIFACT_V2_READ_PREFER_V2") }
func WriteV2Only() bool  { return envEnabled("LAZYMIND_ARTIFACT_V2_WRITE_V2_ONLY") }
