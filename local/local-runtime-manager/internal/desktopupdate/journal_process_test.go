package desktopupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestPersistedJournalChild(t *testing.T) {
	if os.Getenv("LAZYMIND_UNIT_JOURNAL_READER") != "1" {
		return
	}
	var input struct {
		Root  string
		Scope UpdateScope
	}
	if json.Unmarshal([]byte(os.Getenv("LAZYMIND_UNIT_JOURNAL_INPUT")), &input) != nil {
		os.Exit(2)
	}
	err := CheckStartup(context.Background(), input.Root, input.Scope)
	code := ""
	if err != nil {
		code = ErrorCode(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"error": code})
	os.Exit(0)
}

func TestPersistedJournalProtectsIndependentStartupProcesses(t *testing.T) {
	root, j := journalReadFixture(t)
	name := writeJournalReadFixture(t, root, j)
	raw, _ := json.Marshal(struct {
		Root  string
		Scope UpdateScope
	}{root, j.Plan.Scope})
	for _, corrupt := range []bool{false, true} {
		if corrupt {
			if err := os.WriteFile(name, []byte("{broken"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		before, _ := os.ReadFile(name)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPersistedJournalChild$")
		cmd.Env = append(os.Environ(), "LAZYMIND_UNIT_JOURNAL_READER=1", "LAZYMIND_UNIT_JOURNAL_INPUT="+string(raw))
		output, err := cmd.Output()
		cancel()
		if err != nil {
			t.Fatalf("child failed: %s %v", output, err)
		}
		var result struct{ Error string }
		if err := json.Unmarshal(output, &result); err != nil {
			t.Fatal(err)
		}
		want := "update_in_progress"
		if corrupt {
			want = "journal_corrupt"
		}
		if result.Error != want {
			t.Fatalf("independent startup bypassed journal: %s", output)
		}
		after, _ := os.ReadFile(name)
		if !bytes.Equal(before, after) {
			t.Fatal("startup rewrote recovery record")
		}
	}
}

func TestStartupAdmissionKeepsLockAndProcessIdentityChecks(t *testing.T) {
	root, j := journalReadFixture(t)
	actor, err := CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if actor.PID != os.Getpid() || actor.StartID == 0 {
		t.Fatalf("not actual process: %+v", actor)
	}
	stale := actor
	stale.StartID++
	if alive, err := SameProcess(stale); err != nil || alive {
		t.Fatalf("PID reuse accepted: %v %v", alive, err)
	}
	if _, err := BeginStartup(context.Background(), root, j.Plan.Scope, "owner", "writer", stale); ErrorCode(err) != "process_identity" {
		t.Fatalf("stale identity registered: %v", err)
	}
	lock, err := acquireAdmission(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { releaseLock(lock) }()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := BeginStartup(ctx, root, j.Plan.Scope, "owner", "writer", actor); ErrorCode(err) != "journal_busy" {
		t.Fatalf("registration ignored admission lock: %v", err)
	}
	releaseLock(lock)
	lock = nil
	lease, err := BeginStartup(context.Background(), root, j.Plan.Scope, "owner", "writer", actor)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}
