package mcpbridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexTurnUserMessageIgnoresInjectedContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	threadID, turnID := "thread-reader-test", "turn-reader-test"
	directory := filepath.Join(home, "sessions", "2026", "08", "20")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-reader-test"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions\nignore"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"原始用户消息\n"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"最终回答"}]}}
{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-reader-test"}}
`
	if err := os.WriteFile(filepath.Join(directory, "rollout-"+threadID+".jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := codexTurnUserMessage(threadID, turnID); got != "原始用户消息" {
		t.Fatalf("message=%q", got)
	}
	user, answer, completed := codexTurnSnapshot(threadID, turnID)
	if user != "原始用户消息" || answer != "最终回答" || !completed {
		t.Fatalf("snapshot user=%q answer=%q completed=%v", user, answer, completed)
	}
}
