package common

import "testing"

func TestEvoServiceEndpointUsesDedicatedEnv(t *testing.T) {
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo-service.invalid")
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", "http://evo-service:8048/")

	got := EvoServiceEndpoint()
	want := "http://evo-service:8048"
	if got != want {
		t.Fatalf("expected evo service endpoint %q, got %q", want, got)
	}
}

func TestEvoServiceEndpointDoesNotFallBackToAlgoService(t *testing.T) {
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo-service.invalid")
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", "")

	got := EvoServiceEndpoint()
	want := "http://host.docker.internal:8048"
	if got != want {
		t.Fatalf("expected evo service endpoint %q, got %q", want, got)
	}
}

func TestEvoEnabledDefaultsToTrue(t *testing.T) {
	if !EvoEnabled() {
		t.Fatal("expected Evo to be enabled by default")
	}
}

func TestEvoEnabledCanBeDisabled(t *testing.T) {
	t.Setenv("LAZYMIND_EVO_ENABLED", "false")

	if EvoEnabled() {
		t.Fatal("expected Evo to be disabled")
	}
}
