//go:build !windows

package unsignedupdate

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestUnsignedHelperCrashReleasesAdmissionWithoutReplaying(t *testing.T) {
	const fixtureEnv = "LAZYMIND_UNSIGNED_CRASH_FIXTURE"
	type state struct {
		Config desktopupdate.Config
		Paths  desktopupdate.TestPaths
		ID     string
	}
	if file := os.Getenv(fixtureEnv); file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var s state
		if err = json.Unmarshal(b, &s); err != nil {
			t.Fatal(err)
		}
		parent, err := desktopupdate.IdentifyProcess(os.Getppid())
		if err != nil {
			t.Fatal(err)
		}
		i := Installer{Config: s.Config, Paths: s.Paths, SameProcess: desktopupdate.SameProcess,
			Inspect: func(context.Context, string) error { return nil }, Verify: func(context.Context, string) error { return nil },
			Launch: func(context.Context, string) error { t.Fatal("must not launch a fixture"); return nil }}
		if err = i.Install(context.Background(), s.ID, parent, func() error { _, err := fmt.Fprintln(os.Stdout, "accepted"); return err }); err != nil {
			t.Fatal(err)
		}
		return
	}
	i, r := fixture(t, nil)
	parent, err := desktopupdate.CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	id, err := i.Prepare(context.Background(), r, parent)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(state{i.Config, i.Paths, id})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(i.Paths.Home, "child-fixture.json")
	if err = os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestUnsignedHelperCrashReleasesAdmissionWithoutReplaying$")
	child.Env = append(os.Environ(), fixtureEnv+"="+file)
	child.Stderr = os.Stderr
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	scanner := bufio.NewScanner(output)
	accepted := false
	for scanner.Scan() {
		if scanner.Text() == "accepted" {
			accepted = true
			break
		}
	}
	if !accepted {
		t.Fatal("helper did not accept handoff", scanner.Err())
	}
	scope := desktopupdate.UpdateScope{Installation: filepath.Join(i.Paths.Application, "Contents", "Resources", "runtime")}
	probe, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
	if err = desktopupdate.CheckStartup(probe, i.Paths.Updater, scope); err == nil {
		t.Error("startup admitted during active handoff")
	}
	stop()
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = child.Wait(); err == nil {
		t.Fatal("helper unexpectedly exited successfully")
	}
	probe, stop = context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err = desktopupdate.CheckStartup(probe, i.Paths.Updater, scope); err != nil {
		t.Fatal("crashed helper retained admission lock", err)
	}
	b, err = os.ReadFile(filepath.Join(i.Paths.Updater, "installs", id, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var plan Plan
	if err = json.Unmarshal(b, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Stage != "installing" {
		t.Fatal("unknown handoff was rewritten", plan.Stage)
	}
	if err = i.Install(context.Background(), id, parent, func() error { t.Error("crashed handoff replayed"); return nil }); err == nil {
		t.Fatal("unknown installation replayed")
	}
	if b, _ = os.ReadFile(filepath.Join(i.Paths.Application, "source-sentinel")); string(b) != "old app" {
		t.Fatal("current app was replaced")
	}
	if b, _ = os.ReadFile(filepath.Join(i.Paths.Runtime, "data-sentinel")); string(b) != "user data" {
		t.Fatal("runtime data changed")
	}
}
