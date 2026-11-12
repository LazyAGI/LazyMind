package codex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/chatagent"
)

func TestPublishPPTLinksTransfersWorkspaceFile(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "output"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(workspace, "output", "测试 deck.pptx")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"output/测试%20deck.pptx", "<output/测试 deck.pptx>", "<" + file + ">"} {
		t.Run(target, func(t *testing.T) {
			var events []chatagent.Event
			text, err := publishPPTLinks("[下载]("+target+") [再次下载]("+target+")", workspace, func(event chatagent.Event) error {
				events = append(events, event)
				return nil
			})
			if err != nil || text != "下载 再次下载" || len(events) != 1 {
				t.Fatalf("text=%q events=%#v err=%v", text, events, err)
			}
			attachment := events[0].Attachment
			if events[0].Type != "attachment" || attachment == nil || attachment.Filename != "测试 deck.pptx" ||
				attachment.MediaType != "application/vnd.openxmlformats-officedocument.presentationml.presentation" {
				t.Fatalf("event=%#v", events[0])
			}
			if content, err := os.ReadFile(attachment.Path); err != nil || string(content) != "fixture" {
				t.Fatalf("content=%s err=%v", content, err)
			}
		})
	}
	transportErr := errors.New("upload failed")
	if _, err := publishPPTLinks("[下载](output/测试%20deck.pptx)", workspace, func(chatagent.Event) error { return transportErr }); !errors.Is(err, transportErr) {
		t.Fatalf("lost upload failure: %v", err)
	}
}

func TestPublishPPTLinksDoesNotUploadExternalFiles(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "private.pptx")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	targets := []string{"../private.pptx", outside, "file://" + outside, "missing.pptx", "/static-files/deck.pptx", "https://example.com/deck.pptx", "//example.com/deck.pptx"}
	if err := os.Symlink(outside, filepath.Join(workspace, "linked.pptx")); err == nil {
		targets = append(targets, "linked.pptx")
	}
	for _, target := range targets {
		text := "[下载](" + target + ")"
		got, err := publishPPTLinks(text, workspace, func(chatagent.Event) error { t.Fatal("uploaded external or missing file"); return nil })
		if err != nil || got != text {
			t.Fatalf("target=%q got=%q err=%v", target, got, err)
		}
	}
}

func TestTurnModelSinceUsesOnlyCurrentTurn(t *testing.T) {
	file := filepath.Join(t.TempDir(), "rollout.jsonl")
	old := `{"type":"turn_context","timestamp":"2026-11-09T00:00:00Z","payload":{"model":"previous-model"}}`
	current := `{"type":"turn_context","timestamp":"2026-11-09T00:01:00Z","payload":{"model":"actual-model"}}`
	since := time.Date(2026, 11, 9, 0, 0, 30, 0, time.UTC)
	for _, tc := range []struct{ content, want string }{
		{old, ""}, {old + "\n" + current, "actual-model"},
		{old + "\ninvalid JSON\n" + current, "actual-model"},
		{strings.ReplaceAll(current, "actual-model", strings.Repeat("m", 257)), ""},
	} {
		if err := os.WriteFile(file, []byte(tc.content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := turnModelSince(file, since); got != tc.want {
			t.Fatalf("got=%q want=%q", got, tc.want)
		}
	}
	if got := turnModelSince(file+".missing", since); got != "" {
		t.Fatalf("missing metadata returned %q", got)
	}
}
