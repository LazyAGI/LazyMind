// Package compat contains the observable, removable HTTP boundary for clients
// that still use legacy Workflow route spellings.
package compat

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const LegacyRoutesEnv = "LAZYMIND_WORKFLOW_LEGACY_ROUTES"

// LegacyRoutesEnabled defaults on during migration. Setting the flag to a
// conventional false value removes aliases without changing the new routes.
func LegacyRoutesEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(LegacyRoutesEnv))) {
	case "0", "false", "off", "disabled":
		return false
	default:
		return true
	}
}

type RouteMetrics struct {
	mu     sync.Mutex
	counts map[string]uint64
}

func NewRouteMetrics() *RouteMetrics {
	return &RouteMetrics{counts: make(map[string]uint64)}
}

func (m *RouteMetrics) Wrap(route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := Caller(r)
		m.mu.Lock()
		m.counts[caller+"|"+route]++
		m.mu.Unlock()
		next(w, r)
	}
}

var LegacyRouteMetrics = NewRouteMetrics()

func (m *RouteMetrics) Count(caller, route string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[caller+"|"+route]
}

// CanRemoveLegacyRoutes is the explicit deletion gate for compatibility
// aliases. A zero count is meaningful only after a complete observation
// window; deployments must also retain the feature flag for rollback until
// the removal change has passed its own canary.
func (m *RouteMetrics) CanRemoveLegacyRoutes(observedSince, now time.Time, minimumWindow time.Duration) bool {
	if minimumWindow <= 0 || now.Sub(observedSince) < minimumWindow {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, count := range m.counts {
		if count != 0 {
			return false
		}
	}
	return true
}

// Caller records an explicit caller header where available and a stable
// unknown bucket otherwise. It never records authorization or request data.
func Caller(r *http.Request) string {
	caller := strings.TrimSpace(r.Header.Get("X-LazyMind-Workflow-Caller"))
	if caller == "" {
		return "unknown"
	}
	return caller
}
