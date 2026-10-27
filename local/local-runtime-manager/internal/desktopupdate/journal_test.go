package desktopupdate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestUpdateStartupRegistrationRacesPreparationAtomically(t *testing.T) {
	identity, err := CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 8; attempt++ {
		root, plan := transactionFixture(t)
		start := make(chan struct{})
		var group sync.WaitGroup
		group.Add(2)
		var lease *StartupLease
		var tx *UpdateTransaction
		var leaseErr, txErr error
		go func() {
			defer group.Done()
			<-start
			lease, leaseErr = BeginStartup(context.Background(), root, plan.Scope, "foreign-owner", "writer", identity)
		}()
		go func() {
			defer group.Done()
			<-start
			tx, txErr = BeginUpdateTransaction(context.Background(), root, plan)
		}()
		close(start)
		group.Wait()
		if (leaseErr == nil) == (txErr == nil) {
			t.Fatalf("admission not serialized: startup=%v transaction=%v", leaseErr, txErr)
		}
		if lease != nil {
			if err = lease.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if tx != nil {
			if err = tx.AbortBeforeInstall(); err != nil {
				t.Fatal(err)
			}
			if err = tx.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestUpdateStartupChecksAliasesAndDoesNotCreateUnusedRecoveryRoot(t *testing.T) {
	root, plan := transactionFixture(t)
	if err := CheckStartup(context.Background(), root, plan.Scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("read-only fast-path created recovery state")
	}
	tx, err := BeginUpdateTransaction(context.Background(), root, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	alias := filepath.Join(filepath.Dir(plan.Scope.DataRoots[0]), "data-alias")
	if err = os.Symlink(plan.Scope.DataRoots[0], alias); err != nil {
		t.Fatal(err)
	}
	aliased := UpdateScope{Installation: t.TempDir(), DataRoots: []string{alias}}
	if err = CheckStartup(context.Background(), root, aliased); ErrorCode(err) != "update_in_progress" {
		t.Fatalf("alias bypassed same-data protection: %v", err)
	}
}

func transactionFixture(t *testing.T) (string, TransactionPlan) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "recovery")
	install := filepath.Join(base, "app")
	data := filepath.Join(base, "data")
	for _, p := range []string{install, data} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(data, "records.txt"), []byte("source data"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, TransactionPlan{TrustDomain: "fixture", Audience: "fixture-personal", Channel: "personal-test", Mode: "unsigned-test", Platform: "darwin", Arch: "arm64", SigningIdentity: "adhoc", UpdaterProtocol: 1, Scope: UpdateScope{Installation: install, DataRoots: []string{data}}, SourceID: "source", TargetID: "target", SourceVersion: "1.0.0", TargetVersion: "1.1.0", SourceManifestSHA256: strings.Repeat("a", 64), SourcePackageSHA256: strings.Repeat("b", 64), TargetPackageSHA256: strings.Repeat("c", 64), PolicySequence: 7, PolicyDigest: strings.Repeat("d", 64), DataProfile: "fixture-v1", TargetDataProfile: "fixture-v1", Units: []SnapshotUnit{{Path: data}}, Reservations: []InstallSpaceReservation{{Directory: base, InstallBytes: 16 * 1024, RecoveryBytes: 16 * 1024}}, OwnerToken: "PRIVATE-SOURCE-OWNER"}
}

func TestUpdateJournalCannotSwitchTrustOrRecoveryIdentity(t *testing.T) {
	_, plan, tx := beginTransactionFixture(t)
	c := Config{TrustDomain: plan.TrustDomain, Audience: plan.Audience, Channel: plan.Channel, Mode: plan.Mode, Platform: plan.Platform, Arch: plan.Arch, SigningIdentity: plan.SigningIdentity, UpdaterProtocol: plan.UpdaterProtocol, ReleaseID: plan.SourceID, Version: plan.SourceVersion}
	if err := ValidateTransactionIdentity(c, tx.Journal()); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"domain", "audience", "channel", "mode", "platform", "arch", "signer", "protocol", "release", "version"} {
		changed := c
		switch field {
		case "domain":
			changed.TrustDomain = "another"
		case "audience":
			changed.Audience = "official"
		case "channel":
			changed.Channel = "stable"
		case "mode":
			changed.Mode = "official"
		case "platform":
			changed.Platform = "windows"
		case "arch":
			changed.Arch = "amd64"
		case "signer":
			changed.SigningIdentity = "another"
		case "protocol":
			changed.UpdaterProtocol = 0
		case "release":
			changed.ReleaseID = "unrelated"
		case "version":
			changed.Version = "2.0.0"
		}
		if err := ValidateTransactionIdentity(changed, tx.Journal()); err == nil {
			t.Fatalf("recovery identity changed: %s", field)
		}
	}
}

func beginTransactionFixture(t *testing.T) (string, TransactionPlan, *UpdateTransaction) {
	t.Helper()
	root, plan := transactionFixture(t)
	tx, err := BeginUpdateTransaction(context.Background(), root, plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Close() })
	return root, plan, tx
}

func TestUpdateProcessIdentityRejectsPIDReuse(t *testing.T) {
	identity, err := CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if identity.PID != os.Getpid() || identity.StartID == 0 {
		t.Fatalf("not an OS identity: %+v", identity)
	}
	if alive, err := SameProcess(identity); err != nil || !alive {
		t.Fatalf("self identity missing: %v %v", alive, err)
	}
	identity.StartID++
	if same, err := SameProcess(identity); err != nil || same {
		t.Fatalf("PID reuse accepted: %v %v", same, err)
	}
}

func TestUpdateJournalBlocksNewStartsAndKeepsPlanImmutable(t *testing.T) {
	root, plan, tx := beginTransactionFixture(t)
	j := tx.Journal()
	if j.Stage != "PREPARED" || j.ID == "" || j.Revision == 0 {
		t.Fatalf("bad prepared journal %+v", j)
	}
	j.Plan.SourceID = "modified"
	j.Plan.Scope.DataRoots[0] = "modified"
	if tx.Journal().Plan.SourceID != plan.SourceID || tx.Journal().Plan.Scope.DataRoots[0] != plan.Scope.DataRoots[0] {
		t.Fatal("caller mutated authoritative plan")
	}
	if err := CheckStartup(context.Background(), root, plan.Scope); ErrorCode(err) != "update_in_progress" {
		t.Fatalf("startup bypassed transaction: %v", err)
	}
	identity, err := CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = BeginStartup(context.Background(), root, plan.Scope, "another-owner", "writer", identity); ErrorCode(err) != "update_in_progress" {
		t.Fatalf("late writer registered: %v", err)
	}
	other := plan
	other.Scope.Installation = t.TempDir() // Same data, another installation.
	if _, err = BeginUpdateTransaction(context.Background(), root, other); err == nil {
		t.Fatal("two installations can update the same data")
	}
	other = plan
	other.Scope.DataRoots = []string{t.TempDir()}
	other.Units = []SnapshotUnit{{Path: other.Scope.DataRoots[0]}}
	if _, err = BeginUpdateTransaction(context.Background(), root, other); err == nil {
		t.Fatal("two transactions can replace one installation")
	}
	all, err := ReadUpdateJournals(context.Background(), root)
	if err != nil || len(all) != 1 {
		t.Fatalf("journal not persisted: %+v %v", all, err)
	}
	encoded, _ := json.Marshal(all)
	if strings.Contains(string(encoded), plan.OwnerToken) {
		t.Fatal("owner capability persisted in journal")
	}
}

func TestUpdateAdmissionDetectsOtherOwnersAndLivingSourceWriters(t *testing.T) {
	root, plan := transactionFixture(t)
	identity, err := CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := BeginStartup(context.Background(), root, plan.Scope, "another-owner", "writer", identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = BeginUpdateTransaction(context.Background(), root, plan); ErrorCode(err) != "runtime_busy" {
		t.Fatalf("other writer not protected: %v", err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	lease, err = BeginStartup(context.Background(), root, plan.Scope, plan.OwnerToken, "writer", identity)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	tx, err := BeginUpdateTransaction(context.Background(), root, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	if len(tx.Journal().SourceProcesses) != 1 {
		t.Fatal("source writer not captured")
	}
	if err = tx.BeginDrain(); err != nil {
		t.Fatal(err)
	}
	if err = tx.ConfirmStopped(context.Background()); ErrorCode(err) != "processes_running" {
		t.Fatalf("live writer reported stopped: %v", err)
	}
	// Closing a registration is not proof that the captured process exited.
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err = tx.ConfirmStopped(context.Background()); ErrorCode(err) != "processes_running" {
		t.Fatalf("lease removal bypassed process verification: %v", err)
	}
	if err = tx.AbortBeforeInstall(); err != nil {
		t.Fatal(err)
	}
	if err = CheckStartup(context.Background(), root, plan.Scope); err != nil {
		t.Fatalf("safe abort kept source blocked: %v", err)
	}
}

func TestUpdateJournalSurvivesHelperExitAndRejectsCorruption(t *testing.T) {
	root, plan, tx := beginTransactionFixture(t)
	id := tx.Journal().ID
	if err := tx.BeginDrain(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := CheckStartup(context.Background(), root, plan.Scope); ErrorCode(err) != "update_in_progress" {
		t.Fatalf("dead helper unlocked business: %v", err)
	}
	resumed, err := ResumeUpdateTransaction(context.Background(), root, id)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if resumed.Journal().Stage != "DRAINING" {
		t.Fatal("lost durable stage")
	}
	if err = resumed.AbortBeforeInstall(); err != nil {
		t.Fatal(err)
	}
	if err = CheckStartup(context.Background(), root, plan.Scope); err != nil {
		t.Fatal(err)
	}
	if err = resumed.Close(); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(root, "transactions", "v1", id, "journal.json")
	if err = os.WriteFile(journalPath, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = CheckStartup(context.Background(), root, plan.Scope); ErrorCode(err) != "journal_corrupt" {
		t.Fatalf("corrupt journal silently discarded: %v", err)
	}
}

func TestUpdateTransactionRejectsUnsafeScopesAndTransitions(t *testing.T) {
	for _, kind := range []string{"relative", "root", "overlap-recovery", "unit-outside", "same-version", "bad-hash"} {
		t.Run(kind, func(t *testing.T) {
			root, plan := transactionFixture(t)
			switch kind {
			case "relative":
				plan.Scope.Installation = "relative"
			case "root":
				plan.Scope.DataRoots = []string{string(filepath.Separator)}
			case "overlap-recovery":
				plan.Scope.DataRoots = []string{filepath.Dir(root)}
				plan.Units = []SnapshotUnit{{Path: filepath.Dir(root)}}
			case "unit-outside":
				plan.Units = []SnapshotUnit{{Path: t.TempDir()}}
			case "same-version":
				plan.TargetVersion = plan.SourceVersion
			case "bad-hash":
				plan.TargetPackageSHA256 = "bad"
			}
			if tx, err := BeginUpdateTransaction(context.Background(), root, plan); err == nil {
				tx.Close()
				t.Fatal("unsafe plan accepted")
			}
		})
	}
	_, _, tx := beginTransactionFixture(t)
	for _, action := range []func() error{tx.MarkInstallHandoff, tx.MarkCandidateValidation, func() error { return tx.Commit("target", "fixture-v1") }, func() error { return tx.RestoreData(context.Background()) }} {
		if err := action(); ErrorCode(err) != "transaction_stage" {
			t.Fatalf("illegal phase transition: %v", err)
		}
	}
	if tx.Journal().Stage != "PREPARED" {
		t.Fatal("failed command advanced journal")
	}
}
