package workbuddyhost

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestBackgroundCommandDoesNotCreateConsole(t *testing.T) {
	if os.Getenv("LAZYMIND_WORKBUDDY_CONSOLE_TEST") == "1" {
		getConsoleWindow := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")
		console, _, _ := getConsoleWindow.Call()
		if console != 0 {
			os.Exit(2)
		}
		os.Stdout.WriteString("no console")
		os.Exit(0)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := backgroundCommand(ctx, self, "-test.run=^TestBackgroundCommandDoesNotCreateConsole$")
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("WorkBuddy background process must hide its window and use CREATE_NO_WINDOW")
	}
	cmd.Env = append(os.Environ(), "LAZYMIND_WORKBUDDY_CONSOLE_TEST=1")
	output, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "no console" {
		t.Fatalf("background process console check: %q, %v", output, err)
	}
}
