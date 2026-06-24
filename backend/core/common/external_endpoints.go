package common

import (
	"os"
	"strings"
)

// EnvFlagEnabled returns the feature flag value for envName.
// Empty or unrecognized values keep the supplied default.
func EnvFlagEnabled(envName string, defaultValue bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(envName)))
	switch v {
	case "":
		return defaultValue
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}

// EvoEnabled reports whether dedicated Evo features may call evo-api.
func EvoEnabled() bool {
	return EnvFlagEnabled("LAZYMIND_EVO_ENABLED", true)
}

// ChatServiceEndpoint returns the base URL for the chat/generation service.
func ChatServiceEndpoint() string {
	if u := strings.TrimSpace(os.Getenv("LAZYMIND_CHAT_SERVICE_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://chat:8046"
}

// AuthServiceBaseURL returns the base URL for auth-service APIs.
func AuthServiceBaseURL() string {
	if u := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_URL")); u != "" {
		base := strings.TrimRight(u, "/")
		if strings.HasSuffix(base, "/api/authservice") {
			return base
		}
		return base + "/api/authservice"
	}
	return "http://auth-service:8000/api/authservice"
}

// EvoServiceEndpoint returns the base URL for the dedicated evo service.
func EvoServiceEndpoint() string {
	if u := strings.TrimSpace(os.Getenv("LAZYMIND_EVO_SERVICE_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://host.docker.internal:8048"
}

// AlgoServiceEndpoint text base URL（text path）。
// text LAZYMIND_ALGO_SERVICE_URL text；textSettextDefaulttext，text。
func AlgoServiceEndpoint() string {
	if u := strings.TrimSpace(os.Getenv("LAZYMIND_ALGO_SERVICE_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://10.119.24.129:8850"
}

// ParsingServiceEndpoint returns the base URL for the parsing/processor service.
func ParsingServiceEndpoint() string {
	if u := strings.TrimSpace(os.Getenv("LAZYMIND_PARSING_SERVICE_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://localhost:8000"
}

// CoreSelfEndpoint returns the base URL for the Go core service itself.
// Used by internal callers (e.g. plugin EventLoop auto-advance) to route through
// the full chat pipeline including history persistence and runtime config loading.
func CoreSelfEndpoint() string {
	if u := strings.TrimSpace(os.Getenv("LAZYMIND_CORE_SELF_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://localhost:8000"
}

// ScanControlPlaneEndpoint returns the base URL for the scan-control-plane service.
func ScanControlPlaneEndpoint() string {
	if u := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://scan-control-plane:18080"
}
