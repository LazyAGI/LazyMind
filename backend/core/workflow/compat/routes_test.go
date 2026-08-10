package compat

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLegacyRoutesFlagDefaultsOnAndCanRollback(t *testing.T) {
	t.Setenv(LegacyRoutesEnv, "")
	if !LegacyRoutesEnabled() {
		t.Fatal("compatibility must default on during migration")
	}
	t.Setenv(LegacyRoutesEnv, "false")
	if LegacyRoutesEnabled() {
		t.Fatal("legacy route flag did not disable aliases")
	}
}

func TestLegacyRouteDeletionGateRequiresWindowAndZeroCalls(t *testing.T) {
	metrics := NewRouteMetrics()
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	if metrics.CanRemoveLegacyRoutes(now.Add(-23*time.Hour), now, 24*time.Hour) {
		t.Fatal("incomplete observation window must block deletion")
	}
	if !metrics.CanRemoveLegacyRoutes(now.Add(-24*time.Hour), now, 24*time.Hour) {
		t.Fatal("zero calls over a complete window should satisfy the metrics gate")
	}
	metrics.counts["frontend|/workflows"] = 1
	if metrics.CanRemoveLegacyRoutes(now.Add(-48*time.Hour), now, 24*time.Hour) {
		t.Fatal("observed legacy callers must block deletion")
	}
}

func TestLegacyRouteMetricsTrackCallerAndRoute(t *testing.T) {
	metrics := NewRouteMetrics()
	handler := metrics.Wrap("/workflows", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	r := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	r.Header.Set("X-LazyMind-Workflow-Caller", "algorithm-chat")
	handler(httptest.NewRecorder(), r)
	if got := metrics.Count("algorithm-chat", "/workflows"); got != 1 {
		t.Fatalf("count = %d", got)
	}
}

func TestCallerUsesBoundedMetadata(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if Caller(r) != "unknown" {
		t.Fatal(Caller(r))
	}
	r.Header.Set("X-LazyMind-Workflow-Caller", "frontend")
	if Caller(r) != "frontend" {
		t.Fatal(Caller(r))
	}
}
