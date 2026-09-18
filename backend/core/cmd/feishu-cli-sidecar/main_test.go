package main

import "testing"

func TestSidecarAuthServiceBaseURLUsesCanonicalAPIPrefix(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", "http://auth-service:8000")
	if got, want := sidecarAuthServiceBaseURL(), "http://auth-service:8000/api/authservice"; got != want {
		t.Fatalf("auth-service base URL = %q, want %q", got, want)
	}

	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", "http://auth-service:8000/api/authservice")
	if got, want := sidecarAuthServiceBaseURL(), "http://auth-service:8000/api/authservice"; got != want {
		t.Fatalf("prefixed auth-service base URL = %q, want %q", got, want)
	}
}
