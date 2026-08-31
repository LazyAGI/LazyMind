//go:build darwin

package agentexec

import (
	"os"
	"strings"
	"testing"
)

func TestOpenInteractiveCommandUsesSelfDeletingTerminalScript(t *testing.T) {
	previous := startInteractiveTerminal
	t.Cleanup(func() { startInteractiveTerminal = previous })
	script := ""
	startInteractiveTerminal = func(path string) error {
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		script = string(body)
		return os.Remove(path)
	}

	binary := "/tmp/Code Buddy's/codebuddy"
	if err := OpenInteractiveCommand(binary); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, `rm -f -- "$0"`) {
		t.Fatalf("script does not remove itself: %q", script)
	}
	if !strings.Contains(script, `exec '/tmp/Code Buddy'"'"'s/codebuddy'`) {
		t.Fatalf("script does not safely quote the executable: %q", script)
	}
}
