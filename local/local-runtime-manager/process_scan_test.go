package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

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
