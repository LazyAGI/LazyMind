package agentexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsBackgroundInvocationsDoNotCreateConsole(t *testing.T) {
	if os.Getenv("LAZYMIND_CONSOLE_TEST_CHILD") == "1" {
		console, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
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
	for _, extension := range []string{".exe", ".cmd", ".bat"} {
		t.Run(extension, func(t *testing.T) {
			binary := self
			args := []string{"-test.run=TestWindowsBackgroundInvocationsDoNotCreateConsole"}
			if extension != ".exe" {
				binary = filepath.Join(t.TempDir(), "console probe"+extension)
				body := "@echo off\r\n\"%LAZYMIND_CONSOLE_TEST_EXECUTABLE%\" -test.run=TestWindowsBackgroundInvocationsDoNotCreateConsole\r\nexit /b %errorlevel%\r\n"
				if err := os.WriteFile(binary, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				args = nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd, invocationEnvironment, err := commandContext(ctx, binary, args...)
			if err != nil {
				t.Fatal(err)
			}
			cmd.Env = append(os.Environ(), invocationEnvironment...)
			cmd.Env = append(cmd.Env, "LAZYMIND_CONSOLE_TEST_CHILD=1", "LAZYMIND_CONSOLE_TEST_EXECUTABLE="+self)
			output, err := cmd.CombinedOutput()
			if err != nil || strings.TrimSpace(string(output)) != "no console" {
				t.Fatalf("background %s console check: %q, %v", extension, output, err)
			}
		})
	}
}
