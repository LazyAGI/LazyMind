package desktopupdate

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestUpdateJournalChild(t *testing.T) {
	mode := os.Getenv("LAZYMIND_UNIT_UPDATE_JOURNAL_CHILD")
	if mode == "" {
		return
	}
	var request struct {
		Root  string
		Plan  TransactionPlan
		Owner string
	}
	if err := json.Unmarshal([]byte(os.Getenv("LAZYMIND_UNIT_UPDATE_JOURNAL_INPUT")), &request); err != nil {
		os.Exit(2)
	}
	request.Plan.OwnerToken = request.Owner
	var closeFn func() error
	var result struct {
		ID      string          `json:"id"`
		Error   string          `json:"error,omitempty"`
		Process ProcessIdentity `json:"process"`
	}
	identity, err := CurrentProcessIdentity()
	if err == nil {
		result.Process = identity
		if mode == "transaction" {
			var tx *UpdateTransaction
			tx, err = BeginUpdateTransaction(context.Background(), request.Root, request.Plan)
			if err == nil {
				result.ID = tx.Journal().ID
				closeFn = tx.Close
			}
		} else {
			var lease *StartupLease
			lease, err = BeginStartup(context.Background(), request.Root, request.Plan.Scope, request.Owner, "writer", identity)
			if err == nil {
				closeFn = lease.Close
			}
		}
	}
	if err != nil {
		result.Error = ErrorCode(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
	if err != nil {
		os.Exit(3)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if closeFn() != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func journalChild(t *testing.T, mode, root string, plan TransactionPlan) (*exec.Cmd, io.WriteCloser, string, ProcessIdentity) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	request, _ := json.Marshal(struct {
		Root  string
		Plan  TransactionPlan
		Owner string
	}{root, plan, plan.OwnerToken})
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestUpdateJournalChild$")
	cmd.Env = append(os.Environ(), "LAZYMIND_UNIT_UPDATE_JOURNAL_CHILD="+mode, "LAZYMIND_UNIT_UPDATE_JOURNAL_INPUT="+string(request))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill() })
	line, err := bufio.NewReader(stdout).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		ID      string          `json:"id"`
		Error   string          `json:"error"`
		Process ProcessIdentity `json:"process"`
	}
	if json.Unmarshal(line, &response) != nil || response.Error != "" {
		t.Fatalf("unit child: %s", line)
	}
	return cmd, stdin, response.ID, response.Process
}

func TestUpdateJournalLocksAndCrashRecoveryCrossProcesses(t *testing.T) {
	root, plan := transactionFixture(t)
	cmd, stdin, id, identity := journalChild(t, "transaction", root, plan)
	if err := CheckStartup(context.Background(), root, plan.Scope); ErrorCode(err) != "update_in_progress" {
		t.Fatalf("other-process transaction invisible: %v", err)
	}
	if _, err := BeginUpdateTransaction(context.Background(), root, plan); err == nil {
		t.Fatal("cross-process double writer admitted")
	}
	// Terminate only the synthetic Go test child, simulating helper loss.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	_ = cmd.Wait()
	if alive, err := SameProcess(identity); err != nil || alive {
		t.Fatalf("exited helper still alive: %v %v", alive, err)
	}
	if err := CheckStartup(context.Background(), root, plan.Scope); ErrorCode(err) != "update_in_progress" {
		t.Fatalf("crash opened normal startup: %v", err)
	}
	tx, err := ResumeUpdateTransaction(context.Background(), root, id)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	if err = tx.AbortBeforeInstall(); err != nil {
		t.Fatal(err)
	}
	if err = CheckStartup(context.Background(), root, plan.Scope); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateAdmissionDetectsAnotherProcessAndClearsOnlyDeadLease(t *testing.T) {
	root, plan := transactionFixture(t)
	foreign := plan
	foreign.OwnerToken = "foreign-runtime"
	cmd, stdin, _, _ := journalChild(t, "lease", root, foreign)
	if _, err := BeginUpdateTransaction(context.Background(), root, plan); ErrorCode(err) != "runtime_busy" {
		t.Fatalf("foreign process not detected: %v", err)
	}
	// A normal close releases its startup lease, without deleting journals.
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	tx, err := BeginUpdateTransaction(context.Background(), root, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	if err = tx.AbortBeforeInstall(); err != nil {
		t.Fatal(err)
	}
}
