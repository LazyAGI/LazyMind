package internal

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestReportEventsRequestJSONUsesSnakeCase(t *testing.T) {
	t.Parallel()

	req := ReportEventsRequest{
		AgentID: "agent-1",
		Events: []FileEvent{
			{
				SourceID:   "src-1",
				TenantID:   "tenant-1",
				EventType:  FileModified,
				Path:       "/tmp/a.txt",
				ObjectKey:  "local_fs:agent-1:path:/tmp/a.txt",
				IsDir:      false,
				OccurredAt: time.Unix(1_776_166_000, 123).UTC(),
				TraceID:    "trace-1",
			},
		},
	}

	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal report events request failed: %v", err)
	}
	s := string(raw)
	for _, want := range []string{
		`"agent_id"`,
		`"events"`,
		`"source_id"`,
		`"tenant_id"`,
		`"event_type"`,
		`"path"`,
		`"object_key"`,
		`"is_dir"`,
		`"occurred_at"`,
		`"trace_id"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("expected json to contain %s, got %s", want, s)
		}
	}
	if strings.Contains(s, `"SourceID"`) || strings.Contains(s, `"EventType"`) {
		t.Fatalf("expected no PascalCase event fields, got %s", s)
	}
}
