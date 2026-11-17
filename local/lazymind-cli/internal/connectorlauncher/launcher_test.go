package connectorlauncher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func executableFixture(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func readTarget(t *testing.T, launcher string) target {
	t.Helper()
	value, err := readTargetFile(launcher)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestPublishMovesTargetWithoutReplacingLauncher(t *testing.T) {
	home := t.TempDir()
	first := executableFixture(t, "old-lazymind", "old binary")
	second := executableFixture(t, "new-lazymind", "new binary")
	launcher, err := Publish(t.Context(), home, first)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(launcher)
	if _, err := Publish(t.Context(), home, second); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(launcher)
	if !bytes.Equal(before, after) || !samePath(readTarget(t, launcher).Executable, second) {
		t.Fatal("publishing an upgrade must only change the current target")
	}
	if _, err := Ensure(t.Context(), home, first); err != nil {
		t.Fatal(err)
	}
	if !samePath(readTarget(t, launcher).Executable, second) {
		t.Fatal("old process rolled the launcher back")
	}
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(t.Context(), home, first); err != nil {
		t.Fatal(err)
	}
	if !samePath(readTarget(t, launcher).Executable, first) {
		t.Fatal("missing target was not recovered")
	}
}

func TestLauncherKeepsHomesSeparateAndRejectsLoops(t *testing.T) {
	self := executableFixture(t, "lazymind", "fixture")
	first, err := Publish(t.Context(), t.TempDir(), self)
	if err != nil {
		t.Fatal(err)
	}
	secondHome := t.TempDir()
	second, err := Publish(t.Context(), secondHome, self)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("homes share a launcher")
	}
	if _, err := Publish(t.Context(), secondHome, first); err == nil {
		t.Fatal("accepted another home's launcher as target")
	}
	if _, err := Publish(t.Context(), secondHome, second); err != nil {
		t.Fatal(err)
	}
	if !samePath(readTarget(t, second).Executable, self) {
		t.Fatal("self publication introduced a loop")
	}
	if runtime.GOOS != "windows" {
		alias := filepath.Join(t.TempDir(), "launcher-alias")
		if err := os.Link(first, alias); err != nil {
			t.Fatal(err)
		}
		if _, err := Publish(t.Context(), filepath.Dir(filepath.Dir(first)), alias); err == nil {
			t.Fatal("accepted hard-linked self target")
		}
	}
}

func TestEnsureDoesNotOverwriteForeignOrCorruptFiles(t *testing.T) {
	self := executableFixture(t, "lazymind", "fixture")
	for _, scenario := range []string{"foreign_executable", "invalid_target", "foreign_home"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			launcher := Path(home)
			if err := os.MkdirAll(filepath.Dir(launcher), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(launcher, []byte("keep"), 0700); err != nil {
				t.Fatal(err)
			}
			if scenario == "invalid_target" {
				_ = os.WriteFile(targetPath(launcher), []byte("broken"), 0600)
			}
			if scenario == "foreign_home" {
				body, _ := json.Marshal(target{Version: 1, Home: t.TempDir(), Executable: self})
				_ = os.WriteFile(targetPath(launcher), body, 0600)
			}
			if _, err := Ensure(t.Context(), home, self); err == nil {
				t.Fatal("overwrote unowned launcher")
			}
			body, _ := os.ReadFile(launcher)
			if string(body) != "keep" {
				t.Fatal("foreign executable changed")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Ensure(ctx, t.TempDir(), self); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestLauncherForwardProcess(t *testing.T) {
	if os.Getenv("LAZYMIND_TEST_LAUNCHER_PROCESS") == "1" {
		handled, code, err := Forward(os.Args[1:])
		if err != nil {
			_, _ = os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		if handled {
			os.Exit(code)
		}
		self, _ := os.Executable()
		version, err := os.ReadFile(filepath.Join(filepath.Dir(self), "version"))
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		_, _ = fmt.Fprintf(os.Stdout, "%s:%s:%s:%s:%s:%s\n", version, os.Getenv("LAZYMIND_TEST_VALUE"), os.Args[4], os.Args[5], os.Args[6], os.Args[7])
		_, _ = io.Copy(os.Stdout, os.Stdin)
		_, _ = os.Stderr.WriteString("target stderr\n")
		os.Exit(17)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	launcher, err := Publish(t.Context(), home, self)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"first", "moved"} {
		name := "target"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		fixture := filepath.Join(t.TempDir(), name)
		if err := copyExecutable(self, fixture); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(fixture), "version"), []byte(version), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Publish(t.Context(), home, fixture); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(launcher, "-test.run=^TestLauncherForwardProcess$", "--", "third", "with spaces", "", "中文", "\"quoted\"")
		command.Env = append(os.Environ(), "LAZYMIND_TEST_LAUNCHER_PROCESS=1", "LAZYMIND_TEST_VALUE=environment", "LAZYMIND_HOME="+t.TempDir())
		input := strings.Repeat("MCP stdin\n", 20000)
		command.Stdin = strings.NewReader(input)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err = command.Run()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 17 {
			t.Fatalf("exit status was not forwarded: %v", err)
		}
		if stdout.String() != version+":environment:with spaces::中文:\"quoted\"\n"+input || stderr.String() != "target stderr\n" {
			t.Fatalf("stdio/args/environment were not forwarded: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	}
}

func TestLauncherRejectsInvalidTargetsWithoutProtocolOutput(t *testing.T) {
	self, _ := os.Executable()
	for _, scenario := range []string{"missing_record", "corrupt_record", "missing_executable", "recursive_executable"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			launcher, err := Publish(t.Context(), home, self)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing_record":
				err = os.Remove(targetPath(launcher))
			case "corrupt_record":
				err = os.WriteFile(targetPath(launcher), []byte("broken"), 0600)
			default:
				record := readTarget(t, launcher)
				record.Executable = filepath.Join(home, "deleted")
				if scenario == "recursive_executable" {
					record.Executable = launcher
				}
				body, _ := json.Marshal(record)
				err = os.WriteFile(targetPath(launcher), body, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(launcher, "-test.run=^TestLauncherForwardProcess$")
			command.Env = append(os.Environ(), "LAZYMIND_TEST_LAUNCHER_PROCESS=1")
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if err := command.Run(); err == nil || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("invalid target must fail without MCP stdout: err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestConcurrentPublicationKeepsCompleteTargetRecord(t *testing.T) {
	home := t.TempDir()
	first := executableFixture(t, "first", "first")
	second := executableFixture(t, "second", "second")
	launcher, err := Publish(t.Context(), home, first)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for _, executable := range []string{first, second} {
		workers.Go(func() {
			for range 12 {
				if _, err := Publish(t.Context(), home, executable); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	for range 100 {
		record, err := readTargetFile(launcher)
		if err != nil || (!samePath(record.Executable, first) && !samePath(record.Executable, second)) {
			t.Fatalf("partial target publication: %+v %v", record, err)
		}
	}
	workers.Wait()
}
