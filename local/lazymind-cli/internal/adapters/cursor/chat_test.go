package cursor

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/chatagent"
)

func TestCursorAgentAvailabilityUsesOfficialStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "lazymind"))
	binary := writeCursorFixture(t, root, `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "status" ]; then exit 0; fi
exit 1
`)
	runner, err := NewChatRunner(binary)
	if err != nil {
		t.Fatal(err)
	}
	if ready, reason := runner.Availability(); !ready || reason != "" {
		t.Fatalf("availability=(%v, %q)", ready, reason)
	}
}

func TestCursorAgentAvailabilityExplainsLoginRequirement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "lazymind"))
	binary := writeCursorFixture(t, root, "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then exit 0; fi\nif [ \"$1\" = \"status\" ]; then echo 'Not logged in'; exit 0; fi\nexit 1\n")
	runner, err := NewChatRunner(binary)
	if err != nil {
		t.Fatal(err)
	}
	if ready, reason := runner.Availability(); ready || reason != "Cursor Agent CLI is not signed in; run `cursor-agent login`" {
		t.Fatalf("availability=(%v, %q)", ready, reason)
	}
}

func TestCursorAgentAvailabilityDoesNotMisreportStatusFailureAsSignedOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "lazymind"))
	binary := writeCursorFixture(t, root, "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then exit 0; fi\nif [ \"$1\" = \"status\" ]; then echo 'ERROR: SecItemCopyMatching failed -50' >&2; exit 139; fi\nexit 1\n")
	runner, err := NewChatRunner(binary)
	if err != nil {
		t.Fatal(err)
	}
	ready, reason := runner.Availability()
	if ready || reason != "Cursor Agent CLI status check failed; retry or run `cursor-agent status`" {
		t.Fatalf("availability=(%v, %q)", ready, reason)
	}
}

func TestCursorAgentAvailabilityRecognizesSignedOutFailureOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "lazymind"))
	binary := writeCursorFixture(t, root, "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then exit 0; fi\nif [ \"$1\" = \"status\" ]; then echo 'Not logged in' >&2; exit 1; fi\nexit 1\n")
	runner, err := NewChatRunner(binary)
	if err != nil {
		t.Fatal(err)
	}
	if ready, reason := runner.Availability(); ready || reason != "Cursor Agent CLI is not signed in; run `cursor-agent login`" {
		t.Fatalf("availability=(%v, %q)", ready, reason)
	}
}

func TestCursorLoginUsesAgentCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	marker := filepath.Join(root, "logged-in")
	binary := writeCursorFixture(t, root, "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then exit 0; fi\nif [ \"$1\" = \"login\" ]; then touch \""+marker+"\"; exit 0; fi\nexit 1\n")
	if err := Login(context.Background(), binary); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("login command was not invoked: %v", err)
	}
}

func TestCursorRunForcesMCPApprovalInsideSandbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "lazymind"))
	argumentsPath := filepath.Join(root, "arguments")
	binary := writeCursorFixture(t, root, `#!/bin/sh
printf '%s\n' "$@" > "`+argumentsPath+`"
echo '{"type":"system","subtype":"init","session_id":"thread-1"}'
echo '{"type":"assistant","timestamp_ms":1,"message":{"content":[{"type":"text","text":"ok"}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok"}'
`)
	runner := &ChatRunner{binary: binary, self: binary, home: filepath.Join(root, "lazymind")}
	err := runner.Run(context.Background(), chatagent.Run{
		RunID: "run-1", ConversationID: "conversation-1", Action: "start",
		LeaseToken: "lease-1", HostID: "host-1", Prompt: "test",
	}, func(chatagent.Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	arguments := "\n" + string(body)
	if !strings.Contains(arguments, "\n--force\n") || !strings.Contains(arguments, "\n--sandbox\nenabled\n") {
		t.Fatalf("Cursor arguments do not force MCP approval inside the sandbox:\n%s", body)
	}
}

func writeCursorFixture(t *testing.T, root, body string) string {
	t.Helper()
	path := filepath.Join(root, "cursor-agent")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
