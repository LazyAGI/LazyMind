package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestStopLocalProcessRecordsSignalsPIDInsteadOfGroup(t *testing.T) {
	var target int
	record := LocalProcessRecord{Service: "core", PID: 1234, PGID: 5678}
	err := stopLocalProcessRecordsWith(context.Background(), []LocalProcessRecord{record}, processStopOptions{
		interrupt:       func(pid int) error { target = pid; return nil },
		forceKill:       func(int) error { t.Fatal("unexpected force kill"); return nil },
		alive:           func(int) bool { return false },
		gracefulTimeout: time.Second, forceTimeout: time.Second, pollInterval: time.Millisecond,
	})
	if err != nil || target != record.PID {
		t.Fatalf("signal target = %d, error = %v", target, err)
	}
}

func TestStopLocalProcessRecordsRechecksIdentityBeforeEscalation(t *testing.T) {
	record := LocalProcessRecord{Service: "core", PID: 1234, StartID: 10}
	identity := record.StartID
	err := stopLocalProcessRecordsWith(context.Background(), []LocalProcessRecord{record}, processStopOptions{
		interrupt: func(int) error { identity++; return nil },
		forceKill: func(int) error { t.Fatal("reused PID was force-killed"); return nil },
		alive:     func(int) bool { return true }, identity: func(int) uint64 { return identity },
		gracefulTimeout: time.Millisecond, forceTimeout: time.Second, pollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStopLocalProcessRecordsRejectsMissingOrChangedIdentity(t *testing.T) {
	for _, start := range []uint64{0, 9} {
		err := stopLocalProcessRecordsWith(context.Background(), []LocalProcessRecord{{PID: 1234, StartID: start}}, processStopOptions{
			interrupt: func(int) error { t.Fatal("unverified PID interrupted"); return nil },
			forceKill: func(int) error { t.Fatal("unverified PID killed"); return nil },
			alive:     func(int) bool { return true }, identity: func(int) uint64 { return 10 },
			gracefulTimeout: time.Second, forceTimeout: time.Second, pollInterval: time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestStopLocalProcessRecordsForceKillsUnknownOrphanByPID(t *testing.T) {
	record := LocalProcessRecord{Service: "local-runtime-orphan", PID: 1234, PGID: 5678}
	interrupts := 0
	forceTargets := []int{}
	err := stopLocalProcessRecordsWith(context.Background(), []LocalProcessRecord{record}, processStopOptions{
		interrupt: func(int) error {
			interrupts++
			return nil
		},
		forceKill: func(target int) error {
			forceTargets = append(forceTargets, target)
			return nil
		},
		alive:           func(int) bool { return false },
		gracefulTimeout: time.Second,
		forceTimeout:    time.Second,
		pollInterval:    time.Millisecond,
	})
	if err != nil {
		t.Fatalf("stop orphan: %v", err)
	}
	if interrupts != 0 {
		t.Fatalf("interrupts = %d, want 0", interrupts)
	}
	if len(forceTargets) != 1 || forceTargets[0] != record.PID {
		t.Fatalf("force targets = %v, want [%d]", forceTargets, record.PID)
	}
}

func TestStopLocalProcessRecordsWaitsAfterForceKill(t *testing.T) {
	forced := false
	postKillChecks := 0
	forceKills := 0
	records := []LocalProcessRecord{{Service: "auth-service", PID: 1234}}
	err := stopLocalProcessRecordsWith(context.Background(), records, processStopOptions{
		interrupt: func(int) error { return nil },
		forceKill: func(int) error { forceKills++; forced = true; return nil },
		alive: func(int) bool {
			if !forced {
				return true
			}
			postKillChecks++
			return postKillChecks < 3
		},
		gracefulTimeout: time.Millisecond,
		forceTimeout:    20 * time.Millisecond,
		pollInterval:    time.Millisecond,
	})
	if err != nil {
		t.Fatalf("stop process: %v", err)
	}
	if forceKills != 1 {
		t.Fatalf("force kills = %d, want 1", forceKills)
	}
	if postKillChecks < 3 {
		t.Fatalf("post-kill alive checks = %d, expected verification", postKillChecks)
	}
}

func TestStopLocalProcessRecordsReportsStubbornProcess(t *testing.T) {
	records := []LocalProcessRecord{{Service: "auth-service", PID: 4321}}
	err := stopLocalProcessRecordsWith(context.Background(), records, processStopOptions{
		interrupt:       func(int) error { return nil },
		forceKill:       func(int) error { return nil },
		alive:           func(int) bool { return true },
		gracefulTimeout: time.Millisecond,
		forceTimeout:    3 * time.Millisecond,
		pollInterval:    time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected stubborn process error")
	}
	if !strings.Contains(err.Error(), "auth-service(pid=4321)") {
		t.Fatalf("error = %q, want service and PID", err)
	}
}

func TestStopLocalProcessRecordsUsesGracefulInterrupt(t *testing.T) {
	interrupts := 0
	forceKills := 0
	alive := true
	records := []LocalProcessRecord{{Service: "auth-service", PID: 1234}}
	err := stopLocalProcessRecordsWith(context.Background(), records, processStopOptions{
		interrupt: func(int) error {
			interrupts++
			alive = false
			return nil
		},
		forceKill:       func(int) error { forceKills++; return nil },
		alive:           func(int) bool { return alive },
		gracefulTimeout: time.Second,
		forceTimeout:    time.Second,
		pollInterval:    time.Millisecond,
	})
	if err != nil {
		t.Fatalf("stop process: %v", err)
	}
	if interrupts != 1 {
		t.Fatalf("interrupts = %d, want 1", interrupts)
	}
	if forceKills != 0 {
		t.Fatalf("force kills = %d, want 0", forceKills)
	}
}
