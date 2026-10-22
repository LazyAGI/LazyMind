package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/chatagent"
)

func TestChatRunnerUsesCurrentBindingUnlessConstructorOverrideIsExplicit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	for _, explicit := range []bool{false, true} {
		for _, operation := range []string{"availability", "run"} {
			t.Run(fmt.Sprintf("explicit=%v/%s", explicit, operation), func(t *testing.T) {
				root := isolateCodexRuntime(t)
				eventLog := filepath.Join(root, "cli-events")
				fixture := func(name string) string {
					return writeExecutable(t, root, name, fmt.Sprintf(`#!/bin/sh
printf '%%s %%s %%s\n' %q "$1" "$2" >> %q
case "$1" in
  --version) echo 'codex-cli 0.100.0' ;;
  login) exit 0 ;;
  exec)
    echo '{"type":"item.completed","item":{"type":"agent_message","text":"%s"}}'
    echo '{"type":"turn.completed"}' ;;
  *) exit 2 ;;
esac
`, name, eventLog, name))
				}
				first, second := fixture("first"), fixture("second")
				if _, err := agentexec.SetExecutableBinding(agentexec.CodexCLI, first); err != nil {
					t.Fatal(err)
				}
				configured, want := "", "second"
				if explicit {
					configured, want = first, "first"
				}
				runner, err := NewChatRunner(configured)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := agentexec.SetExecutableBinding(agentexec.CodexCLI, second); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(eventLog, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				switch operation {
				case "availability":
					if ready, reason := runner.Availability(); !ready || reason != "" {
						t.Fatalf("ready=%v reason=%q", ready, reason)
					}
					body, err := os.ReadFile(eventLog)
					if err != nil || !strings.Contains(string(body), want+" login status\n") {
						t.Fatalf("availability used stale executable: events=%q err=%v", body, err)
					}
				case "run":
					var messages []string
					err := runner.Run(context.Background(), chatagent.Run{
						RunID: "run-binding", ConversationID: "conversation-binding", Action: "start", Prompt: "hello",
					}, func(event chatagent.Event) error {
						if event.Type == "message" {
							messages = append(messages, event.Text)
						}
						return nil
					})
					if err != nil || len(messages) != 1 || messages[0] != want {
						t.Fatalf("task used stale executable: messages=%q err=%v", messages, err)
					}
				}
			})
		}
	}
}

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
if [ "$1" = "--version" ]; then echo 'codex-cli 0.100.0'; exit 0; fi
mkdir -p "$CODEX_HOME/generated_images/thread-1"
printf '\211PNG\r\n\032\nfixture' > "$CODEX_HOME/generated_images/thread-1/image-1.png"
echo '{"type":"thread.started","thread_id":"thread-1"}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"done"}}'
echo '{"type":"turn.completed"}'
`)
	runner := &ChatRunner{configured: binary, self: binary, home: stateHome}
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
