package common

import (
	"os"
	"strings"
)

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

// ScanControlPlaneEndpoint returns the base URL for the scan-control-plane service.
func ScanControlPlaneEndpoint() string {
	if u := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://scan-control-plane:18080"
}
