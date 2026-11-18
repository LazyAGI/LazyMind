package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestCodexRunTransfersPPTAndReportsActualModel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	stateHome := filepath.Join(root, "lazymind")
	t.Setenv("LAZYMIND_HOME", stateHome)
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	binary := writeExecutable(t, filepath.Join(root, "bin"), "codex", fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then echo 'codex-cli 0.100.0'; exit 0; fi
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-C" ]; then shift; cd "$1" || exit 1; fi
  shift
done
cat > received-prompt.txt
mkdir -p output "$CODEX_HOME/sessions"
printf 'PPT fixture' > output/deck.pptx
printf '%%s\n' '{"type":"turn_context","timestamp":"%s","payload":{"model":"actual-codex-model"}}' > "$CODEX_HOME/sessions/rollout-fixture-ppt-thread.jsonl"
echo '{"type":"thread.started","thread_id":"fixture-ppt-thread"}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"[下载 PPT](output/deck.pptx)"}}'
echo '{"type":"turn.completed"}'
`, time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)))
	runner := &ChatRunner{configured: binary, self: binary, home: stateHome}
	prompt := "生成四页 PPT\nLazyMind workflow selection: builtin:ppt-workflow"
	var events []chatagent.Event
	err := runner.Run(context.Background(), chatagent.Run{RunID: "run-ppt", ConversationID: "conversation-ppt", Action: "start", Prompt: prompt}, func(event chatagent.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 || events[0].Type != "thread_started" || events[1].Type != "attachment" ||
		events[2].Type != "message" || events[2].Text != "下载 PPT" || events[3].Type != "model" ||
		events[3].Text != "actual-codex-model" || events[4].Type != "turn_completed" {
		t.Fatalf("events=%#v", events)
	}
	workspace, err := agentexec.EnsureConversationWorkspace("conversation-ppt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(workspace, "received-prompt.txt"))
	if err != nil || string(got) != prompt {
		t.Fatalf("workflow handoff lost: prompt=%q err=%v", got, err)
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
