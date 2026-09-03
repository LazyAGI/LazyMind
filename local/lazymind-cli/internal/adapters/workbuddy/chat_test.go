package workbuddy

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/chatagent"
)

func TestWorkBuddyRunReusesDesktopStateAndEmitsGeneratedImage(t *testing.T) {
	root := t.TempDir()
	stateHome := filepath.Join(root, "lazymind")
	workBuddyHome := filepath.Join(root, "workbuddy")
	t.Setenv("LAZYMIND_HOME", stateHome)
	if err := os.MkdirAll(workBuddyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workBuddyHome, "fixture"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "codebuddy"
	script := `#!/bin/sh
if [ ! -f "$WORKBUDDY_CONFIG_DIR/fixture" ] ||
   [ ! -f "$CODEBUDDY_CONFIG_DIR/fixture" ]; then
  echo "WorkBuddy state was not reused" >&2
  exit 28
fi
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
	if runtime.GOOS == "windows" {
		name = "codebuddy.cmd"
		script = `@echo off
if not exist "%WORKBUDDY_CONFIG_DIR%\fixture" exit /b 28
if not exist "%CODEBUDDY_CONFIG_DIR%\fixture" exit /b 28
echo %7 | findstr /C:"ImageGen" >nul
if errorlevel 1 exit /b 29
if not exist generated-images mkdir generated-images
echo fixture>generated-images\workbuddy.png
echo {"type":"system","subtype":"init","session_id":"thread-1"}
echo {"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}
echo {"type":"result","subtype":"success","is_error":false}
`
	}
	binary := filepath.Join(root, name)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &ChatRunner{
		binary: binary, self: binary, home: stateHome,
		workBuddyHome: workBuddyHome,
	}
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

func TestFindRuntimeUsesWorkBuddyManagedNodeAndBundledEngine(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("fixture covers the macOS WorkBuddy bundle layout")
	}
	root := t.TempDir()
	script := filepath.Join(
		root, "WorkBuddy.app", "Contents", "Resources",
		"app.asar.unpacked", "cli", "bin", "codebuddy",
	)
	workBuddyHome := filepath.Join(root, "workbuddy-home")
	node := filepath.Join(workBuddyHome, "binaries", "node", "versions", "22.22.2-2", "bin", "node")
	for _, path := range []string{script, node} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho 5.4.7\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(workBuddyHome, "binaries", "node", "versions", "current"),
		[]byte("22.22.2-2"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	pathBinary := filepath.Join(root, "bin", "codebuddy")
	if err := os.MkdirAll(filepath.Dir(pathBinary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathBinary, []byte("#!/bin/sh\necho standalone\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", root)
	t.Setenv("WORKBUDDY_CONFIG_DIR", workBuddyHome)
	t.Setenv("PATH", filepath.Dir(pathBinary))

	resolved, err := findRuntime("", workBuddyHome)
	if err != nil {
		t.Fatal(err)
	}
	if !agentexec.SameExecutable(resolved.Binary, node) ||
		len(resolved.Arguments) != 1 || resolved.Arguments[0] != script {
		t.Fatalf("runtime=%#v", resolved)
	}
	if len(resolved.Environment) != 0 {
		t.Fatalf("runtime environment=%v", resolved.Environment)
	}
}

func TestAvailabilityRequiresWorkBuddyDesktopAuthentication(t *testing.T) {
	auth := filepath.Join(t.TempDir(), "workbuddy-desktop.info")
	if ready, reason := availability(auth); ready || !strings.Contains(reason, "open WorkBuddy") {
		t.Fatalf("signed-out availability=(%v, %q)", ready, reason)
	}
	if err := os.WriteFile(auth, []byte("authenticated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ready, reason := availability(auth); !ready || reason != "" {
		t.Fatalf("signed-in availability=(%v, %q)", ready, reason)
	}
}
