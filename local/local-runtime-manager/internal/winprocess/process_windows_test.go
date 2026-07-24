//go:build windows

package winprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestExcludedAncestorsExcludesOnlySelfAndAncestorChain(t *testing.T) {
	processes := []Info{
		{ProcessID: 10, ParentProcessID: 0},
		{ProcessID: 20, ParentProcessID: 10},
		{ProcessID: 30, ParentProcessID: 20},
		{ProcessID: 40, ParentProcessID: 20},
		{ProcessID: 50, ParentProcessID: 40},
	}
	excluded := ExcludedAncestors(processes, 30)
	for _, pid := range []int{10, 20, 30} {
		if !excluded[pid] {
			t.Errorf("pid %d was not excluded", pid)
		}
	}
	for _, pid := range []int{40, 50} {
		if excluded[pid] {
			t.Errorf("sibling/descendant pid %d was unexpectedly excluded", pid)
		}
	}
}

func TestExcludedAncestorsStopsAtParentCycle(t *testing.T) {
	processes := []Info{
		{ProcessID: 10, ParentProcessID: 20},
		{ProcessID: 20, ParentProcessID: 10},
	}
	excluded := ExcludedAncestors(processes, 10)
	if !excluded[10] || !excluded[20] || len(excluded) != 2 {
		t.Fatalf("excluded = %#v", excluded)
	}
}

func TestSnapshotPairsCurrentExecutableWithStartIdentity(t *testing.T) {
	processes, err := Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantStartID := StartIdentity(os.Getpid())
	for _, process := range processes {
		if int(process.ProcessID) != os.Getpid() {
			continue
		}
		if process.StartID == 0 || process.StartID != wantStartID {
			t.Fatalf("snapshot start identity = %d, want %d", process.StartID, wantStartID)
		}
		if Text(process.ExecutablePath) == "" {
			t.Fatal("snapshot executable path is empty")
		}
		return
	}
	t.Fatal("current process is missing from snapshot")
}

func TestForceKillTreeRejectsReusedPIDIdentity(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestWinprocessHelperProcess$")
	cmd.Env = append(os.Environ(), "LAZYMIND_WINPROCESS_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	startID := StartIdentity(cmd.Process.Pid)
	if startID == 0 {
		t.Fatal("could not read helper process start identity")
	}
	err := ForceKillTree(context.Background(), cmd.Process.Pid, startID+1)
	if !errors.Is(err, ErrProcessIdentityChanged) {
		t.Fatalf("ForceKillTree error = %v", err)
	}
	if !Alive(cmd.Process.Pid) {
		t.Fatal("identity mismatch terminated the replacement process")
	}
}

func TestWinprocessHelperProcess(t *testing.T) {
	if os.Getenv("LAZYMIND_WINPROCESS_HELPER") != "1" {
		return
	}
	time.Sleep(time.Minute)
}
