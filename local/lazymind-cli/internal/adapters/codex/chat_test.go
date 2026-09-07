package codex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"lazymind/agentconnector/internal/chatagent"
)

func TestCodexRunEmitsGeneratedImageBeforeTurnCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	stateHome := filepath.Join(root, "lazymind")
	codexStateHome := filepath.Join(root, "codex")
	t.Setenv("LAZYMIND_HOME", stateHome)
	t.Setenv("CODEX_HOME", codexStateHome)
	binary := writeExecutable(t, filepath.Join(root, "bin"), "codex", `#!/bin/sh
mkdir -p "$CODEX_HOME/generated_images/thread-1"
printf '\211PNG\r\n\032\nfixture' > "$CODEX_HOME/generated_images/thread-1/image-1.png"
echo '{"type":"thread.started","thread_id":"thread-1"}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"done"}}'
echo '{"type":"turn.completed"}'
`)
	runner := &ChatRunner{binary: binary, self: binary, home: stateHome}
	var events []chatagent.Event
	err := runner.Run(context.Background(), chatagent.Run{
		RunID: "run-1", ConversationID: "conversation-1", Action: "start",
		LeaseToken: "lease-1", HostID: "host-1", Prompt: "generate an image",
	}, func(event chatagent.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].Type != "thread_started" || events[1].Type != "message" ||
		events[2].Type != "attachment" || events[3].Type != "turn_completed" {
		t.Fatalf("events=%#v", events)
	}
	attachment := events[2].Attachment
	if attachment == nil || attachment.Filename != "image-1.png" || attachment.MediaType != "image/png" {
		t.Fatalf("attachment=%#v", attachment)
	}
	if content, err := os.ReadFile(attachment.Path); err != nil || len(content) == 0 {
		t.Fatalf("read attachment: size=%d err=%v", len(content), err)
	}
}
