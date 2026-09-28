package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProcessStopTargetUsesVerifiedProcessGroup(t *testing.T) {
	record := LocalProcessRecord{PID: 1234, PGID: 5678}
	got := processStopTarget(record, 9999, func(pid int) int {
		if pid != record.PID {
			t.Fatalf("group lookup PID = %d, want %d", pid, record.PID)
		}
		return record.PGID
	})
	if got != record.PGID {
		t.Fatalf("stop target = %d, want PGID %d", got, record.PGID)
	}
}

func TestProcessStopTargetFallsBackToPID(t *testing.T) {
	tests := []struct {
		name        string
		record      LocalProcessRecord
		currentPGID int
		groupID     int
	}{
		{name: "missing PGID", record: LocalProcessRecord{PID: 1234}, currentPGID: 9999, groupID: 0},
		{name: "manager group", record: LocalProcessRecord{PID: 1234, PGID: 5678}, currentPGID: 5678, groupID: 5678},
		{name: "stale PGID", record: LocalProcessRecord{PID: 1234, PGID: 5678}, currentPGID: 9999, groupID: 6789},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := processStopTarget(test.record, test.currentPGID, func(int) int { return test.groupID })
			if got != test.record.PID {
				t.Fatalf("stop target = %d, want PID %d", got, test.record.PID)
			}
		})
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
