package workbuddy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/chatagent"
)

func TestWorkBuddyRunEmitsGeneratedImage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	root := t.TempDir()
	stateHome := filepath.Join(root, "lazymind")
	t.Setenv("LAZYMIND_HOME", stateHome)
	binary := filepath.Join(root, "codebuddy")
	script := `#!/bin/sh
case ",$7," in
  *,ImageGen,*) ;;
  *) echo "ImageGen was not enabled" >&2; exit 29 ;;
esac
mkdir -p generated-images
printf '\211PNG\r\n\032\nfixture' > generated-images/workbuddy.png
echo '{"type":"system","subtype":"init","session_id":"thread-1"}'
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}'
echo '{"type":"result","subtype":"success","is_error":false}'
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
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
	if len(events) != 3 || events[0].Type != "thread_started" || events[1].Type != "message" ||
		events[2].Type != "attachment" {
		t.Fatalf("events=%#v", events)
	}
	attachment := events[2].Attachment
	if attachment == nil || attachment.Filename != "workbuddy.png" || attachment.MediaType != "image/png" {
		t.Fatalf("attachment=%#v", attachment)
	}
}

func TestLoginOpensDiscoveredCodeBuddyInteractively(t *testing.T) {
	name, body := "codebuddy", "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		name, body = "codebuddy.cmd", "@echo off\r\nexit /b 0\r\n"
	}
	binary := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(binary, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	previous := openInteractiveCommand
	t.Cleanup(func() { openInteractiveCommand = previous })
	opened := ""
	authenticationFile := filepath.Join(t.TempDir(), "authenticated")
	openInteractiveCommand = func(path string) error {
		opened = path
		return os.WriteFile(authenticationFile, []byte("authenticated"), 0o600)
	}

	if err := login(context.Background(), binary, authenticationFile); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	if !agentexec.SameExecutable(opened, resolved) {
		t.Fatalf("opened=%q want %q", opened, resolved)
	}
}

func TestLoginWaitsForCodeBuddyAuthentication(t *testing.T) {
	name, body := "codebuddy", "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		name, body = "codebuddy.cmd", "@echo off\r\nexit /b 0\r\n"
	}
	binary := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(binary, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	previous := openInteractiveCommand
	t.Cleanup(func() { openInteractiveCommand = previous })
	openInteractiveCommand = func(string) error { return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := login(ctx, binary, filepath.Join(t.TempDir(), "missing-auth")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("login error=%v, want deadline exceeded", err)
	}
}

func TestAvailabilityRequiresCodeBuddyAuthenticationFile(t *testing.T) {
	auth := filepath.Join(t.TempDir(), "Tencent-Cloud.coding-copilot.info")
	runner := &ChatRunner{auth: auth}
	if ready, reason := runner.Availability(); ready || reason == "" {
		t.Fatalf("signed-out availability=(%v, %q)", ready, reason)
	}
	if err := os.WriteFile(auth, []byte("authenticated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ready, reason := runner.Availability(); !ready || reason != "" {
		t.Fatalf("signed-in availability=(%v, %q)", ready, reason)
	}
}
