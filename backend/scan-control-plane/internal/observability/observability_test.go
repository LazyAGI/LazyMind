package observability

import (
	"bytes"
	"strings"
	"testing"
)

func TestRegistryWritesPrometheusCounters(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.Inc("sourceengine_compensation_total", Labels{"resource_type": "binding", "status": "failed"})
	registry.Add("sourceengine_compensation_total", Labels{"status": "failed", "resource_type": "binding"}, 2)

	var out bytes.Buffer
	if err := registry.Write(&out); err != nil {
		t.Fatalf("write metrics: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `sourceengine_compensation_total{resource_type="binding",status="failed"} 3`) {
		t.Fatalf("unexpected metrics output: %s", got)
	}
}

func TestAuditLoggerFiltersSensitiveFields(t *testing.T) {
	t.Parallel()

	fields := AuditFields("dead_letter_retry", map[string]any{
		"source_id":          "source-1",
		"access_token":       "secret",
		"temp_uri":           "scan-temp://abc",
		"local_path":         "/Users/alice/private.md",
		"binding_id":         "binding-1",
		"error_code":         "CORE_SUBMIT_FAILED",
		"refresh_token":      "refresh",
		"provider_url":       "https://download.test/private",
		"object_key":         "obj-1",
		"binding_generation": int64(1),
	})
	body := JSONFields(fields)
	for _, forbidden := range []string{"secret", "scan-temp://", "/Users/alice", "refresh", "download.test"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("audit fields leaked sensitive value %q in %s", forbidden, body)
		}
	}
	for _, required := range []string{"dead_letter_retry", "source-1", "binding-1", "CORE_SUBMIT_FAILED", "obj-1"} {
		if !strings.Contains(body, required) {
			t.Fatalf("audit fields missing required value %q in %s", required, body)
		}
	}
}
