//go:build windows

package agentexec

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestInteractiveBatchCommandUsesANewConsoleWithoutStart(t *testing.T) {
	binary := `\\?\C:\Program Files\CodeBuddy\codebuddy.cmd`
	command := interactiveCommand(binary)

	if !strings.EqualFold(filepath.Base(command.Path), "cmd.exe") {
		t.Fatalf("command path=%q", command.Path)
	}
	wantArguments := []string{"cmd.exe", "/d", "/k", `call "%LAZYMIND_INTERACTIVE_COMMAND%"`}
	if len(command.Args) != len(wantArguments) {
		t.Fatalf("arguments=%#v", command.Args)
	}
	for index := range wantArguments {
		if !strings.EqualFold(command.Args[index], wantArguments[index]) {
			t.Fatalf("arguments[%d]=%q want %q", index, command.Args[index], wantArguments[index])
		}
	}
	if command.SysProcAttr == nil || command.SysProcAttr.CreationFlags&windows.CREATE_NEW_CONSOLE == 0 {
		t.Fatal("interactive batch command must create a new console")
	}
	if !containsEnvironment(command.Env, `LAZYMIND_INTERACTIVE_COMMAND=C:\Program Files\CodeBuddy\codebuddy.cmd`) {
		t.Fatalf("environment does not contain the normalized command path: %#v", command.Env)
	}
}

func TestInteractiveExecutableRunsDirectlyInANewConsole(t *testing.T) {
	binary := `C:\Program Files\CodeBuddy\codebuddy.exe`
	command := interactiveCommand(binary)

	if command.Path != binary || len(command.Args) != 1 || command.Args[0] != binary {
		t.Fatalf("command=%q arguments=%#v", command.Path, command.Args)
	}
	if command.SysProcAttr == nil || command.SysProcAttr.CreationFlags&windows.CREATE_NEW_CONSOLE == 0 {
		t.Fatal("interactive executable must create a new console")
	}
}

func TestWindowsShellPathRemovesExtendedPathPrefixes(t *testing.T) {
	tests := map[string]string{
		`\\?\C:\Users\user\codebuddy.cmd`:              `C:\Users\user\codebuddy.cmd`,
		`\\?\UNC\server\share\codebuddy.cmd`:           `\\server\share\codebuddy.cmd`,
		`\\?\unc\server\share\lowercase-codebuddy.cmd`: `\\server\share\lowercase-codebuddy.cmd`,
		`C:\Users\user\codebuddy.cmd`:                  `C:\Users\user\codebuddy.cmd`,
	}
	for input, want := range tests {
		if got := windowsShellPath(input); got != want {
			t.Errorf("windowsShellPath(%q)=%q want %q", input, got, want)
		}
	}
}

func containsEnvironment(environment []string, want string) bool {
	for _, entry := range environment {
		if strings.EqualFold(entry, want) {
			return true
		}
	}
	return false
}
