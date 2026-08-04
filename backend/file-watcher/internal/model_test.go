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

func TestCommandAndAckPreserveExactStringID(t *testing.T) {
	t.Parallel()

	var command Command
	if err := json.Unmarshal([]byte(`{"id":9007199254740993,"command_id":"9007199254740993","type":"start_source"}`), &command); err != nil {
		t.Fatalf("decode command: %v", err)
	}
	if command.ID != 9007199254740993 || command.CommandID != "9007199254740993" {
		t.Fatalf("command id was not preserved: %+v", command)
	}
	body, err := json.Marshal(AckCommandRequest{AgentID: "agent-1", CommandID: command.CommandID, LegacyCommandID: command.ID, Success: true})
	if err != nil {
		t.Fatalf("marshal string ack: %v", err)
	}
	if !strings.Contains(string(body), `"command_id":"9007199254740993"`) {
		t.Fatalf("new command ack should use canonical string id: %s", body)
	}
}

func TestAckFallsBackToLegacyNumericID(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(AckCommandRequest{AgentID: "agent-1", LegacyCommandID: 42, Success: true})
	if err != nil {
		t.Fatalf("marshal numeric ack: %v", err)
	}
	if !strings.Contains(string(body), `"command_id":42`) {
		t.Fatalf("old control-plane compatibility requires numeric ack: %s", body)
	}
}
