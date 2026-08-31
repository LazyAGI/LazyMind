package historyinjection

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCompactPortableSQLMergesStreamFragmentsByReactRound(t *testing.T) {
	source := `-- LazyMind portable history injection SQL
-- bundle: compact-test-v1

INSERT INTO conversations (id) VALUES ('conversation-1') ON CONFLICT DO NOTHING;
INSERT INTO sub_agent_steps (id, task_id, seq, role, content, created_at) VALUES ('s1', 'task-1', 0, 'think', '{"content":"Let"}', '2026-01-01T00:00:01Z') ON CONFLICT DO NOTHING;
INSERT INTO sub_agent_steps (id, task_id, seq, role, content, created_at) VALUES ('s2', 'task-1', 1, 'text', '{"content":"I"}', '2026-01-01T00:00:02Z') ON CONFLICT DO NOTHING;
INSERT INTO sub_agent_steps (id, task_id, seq, role, content, created_at) VALUES ('s3', 'task-1', 2, 'think', '{"content":"''s reason"}', '2026-01-01T00:00:03Z') ON CONFLICT DO NOTHING;
INSERT INTO sub_agent_steps (id, task_id, seq, role, content, created_at) VALUES ('s4', 'task-1', 3, 'text', '{"content":" can help"}', '2026-01-01T00:00:04Z') ON CONFLICT DO NOTHING;
INSERT INTO sub_agent_steps (id, task_id, seq, role, content, created_at) VALUES ('s5', 'task-1', 4, 'assistant', '{"tool_calls":[{"id":"call-1","name":"demo"}]}', '2026-01-01T00:00:05Z') ON CONFLICT DO NOTHING;
INSERT INTO sub_agent_steps (id, task_id, seq, role, content, created_at) VALUES ('s6', 'task-1', 5, 'tool', '{"tool_results":[{"id":"call-1","name":"demo","result":"ok"}]}', '2026-01-01T00:00:06Z') ON CONFLICT DO NOTHING;
INSERT INTO sub_agent_steps (id, task_id, seq, role, content, created_at) VALUES ('s7', 'task-1', 6, 'text', '{"content":"done"}', '2026-01-01T00:00:07Z') ON CONFLICT DO NOTHING;
INSERT INTO sub_agent_steps (id, task_id, seq, role, content, created_at) VALUES ('s8', 'task-1', 7, 'text', '{"content":""}', '2026-01-01T00:00:08Z') ON CONFLICT DO NOTHING;
INSERT INTO workflow_events (session_id, payload_json) VALUES ('session-1', '{"status":"done"}') ON CONFLICT DO NOTHING;
`

	compacted, stats, err := CompactPortableSQL(source)
	if err != nil {
		t.Fatal(err)
	}
	if stats.InputSteps != 8 || stats.OutputSteps != 5 || stats.MergedSteps() != 3 {
		t.Fatalf("unexpected compact stats: %#v", stats)
	}
	if !strings.Contains(compacted, `'{"content":"Let''s reason"}'`) {
		t.Fatalf("merged think content missing:\n%s", compacted)
	}
	if !strings.Contains(compacted, `'{"content":"I can help"}'`) {
		t.Fatalf("merged text content missing:\n%s", compacted)
	}
	if strings.Contains(compacted, `'{"content":""}'`) {
		t.Fatalf("empty stream row was retained:\n%s", compacted)
	}
	if !strings.Contains(compacted, `VALUES ('s7', 'task-1', 4, 'text', '{"content":"done"}'`) {
		t.Fatalf("sequence was not rebuilt:\n%s", compacted)
	}
	if strings.Count(compacted, "sub_agent_steps normalized to buffered SubAgent persistence") != 1 {
		t.Fatalf("compact marker count is not stable:\n%s", compacted)
	}

	second, secondStats, err := CompactPortableSQL(compacted)
	if err != nil {
		t.Fatal(err)
	}
	if second != compacted || secondStats.InputSteps != 5 || secondStats.OutputSteps != 5 {
		t.Fatalf("compaction is not idempotent: stats=%#v\n%s", secondStats, second)
	}
}

func TestPackCompactsPortableSQL(t *testing.T) {
	source := t.TempDir()
	data := `INSERT INTO sub_agent_steps (id, task_id, seq, role, content, created_at) VALUES ('s1', 'task-1', 0, 'text', '{"content":"a"}', '2026-01-01T00:00:00Z') ON CONFLICT DO NOTHING;
INSERT INTO sub_agent_steps (id, task_id, seq, role, content, created_at) VALUES ('s2', 'task-1', 1, 'text', '{"content":"b"}', '2026-01-01T00:00:01Z') ON CONFLICT DO NOTHING;
`
	if err := os.WriteFile(filepath.Join(source, "data.sql"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "manifest.json"), []byte(`{"fixture":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "bundle.zip")
	if err := Pack(source, output); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name != "data.sql" {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(body), "INSERT INTO sub_agent_steps") != 1 ||
			!strings.Contains(string(body), `'{"content":"ab"}'`) {
			t.Fatalf("packed SQL was not compacted:\n%s", body)
		}
		return
	}
	t.Fatal("packed archive has no data.sql; files=" + strconv.Itoa(len(reader.File)))
}
