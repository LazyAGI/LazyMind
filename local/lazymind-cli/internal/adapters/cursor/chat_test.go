package cursor

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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

func writeCursorFixture(t *testing.T, root, body string) string {
	t.Helper()
	path := filepath.Join(root, "cursor-agent")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
