package taskdisplay

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProcessStepRejectsInvalidPublicContract(t *testing.T) {
	valid := PublicProcessStep{StepID: "read-input", Revision: 1, Order: 0, Title: "读取输入", Status: "running"}
	if err := ValidateProcessStep(valid); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	earlier := now.Add(-time.Second)
	negative := int64(-1)
	cases := map[string]func(*PublicProcessStep){"empty title": func(s *PublicProcessStep) { s.Title = " " }, "overlong title": func(s *PublicProcessStep) { s.Title = strings.Repeat("中", 101) }, "invalid status": func(s *PublicProcessStep) { s.Status = "complete" }, "invalid ID": func(s *PublicProcessStep) { s.StepID = "../secret" }, "negative duration": func(s *PublicProcessStep) { s.ElapsedMS = &negative }, "reversed time": func(s *PublicProcessStep) { s.Status = "succeeded"; s.StartedAt = &now; s.FinishedAt = &earlier }, "running finished": func(s *PublicProcessStep) { s.FinishedAt = &now }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			step := valid
			change(&step)
			if ValidateProcessStep(step) == nil {
				t.Fatal("invalid public step accepted")
			}
		})
	}
	terminal := valid
	terminal.Status = "succeeded"
	valid.Revision = 2
	if ValidateTransition(terminal, valid) == nil {
		t.Fatal("terminal status regressed")
	}
}

func TestSourceWhitelistPreservesMeaningfulQueriesAndRejectsSecrets(t *testing.T) {
	raw := json.RawMessage(`[
 {"url":"https://example.com/read?id=1#section","title":"One","snippet":"Public summary","content":"SECRET BODY","prompt":"SECRET PROMPT"},
 {"url":"https://example.com/read?id=1#other","title":"duplicate"},
 {"url":"https://example.com/read?id=2","title":"Two"},
 {"url":"javascript:alert(1)"},{"url":"https://user:pass@example.com/"},
 {"url":"https://example.com/?token=SECRET"},{"url":"http://127.0.0.1/private"},
 {"url":"https://example.com/?X-Amz-Credential=SECRET"},
 {"document_id":"doc-1","dataset_id":"kb-1","file_name":"Document","content":"SECRET BODY"}
 ]`)
	sources := NormalizeSources(raw)
	if len(sources) != 3 {
		t.Fatalf("expected 3 distinct safe sources, got %#v", sources)
	}
	if sources[0].Snippet != "Public summary" || sources[0].Domain == nil || *sources[0].Domain != "example.com" {
		t.Fatalf("public metadata lost: %#v", sources[0])
	}
	encoded, _ := json.Marshal(sources)
	if strings.Contains(string(encoded), "SECRET") || strings.Contains(string(encoded), "content") {
		t.Fatalf("private fields exported: %s", encoded)
	}
}
