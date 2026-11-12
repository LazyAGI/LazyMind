package desktopupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A persisted v1 record, independent of the retired transaction writer.
func journalReadFixture(t *testing.T) (string, UpdateJournal) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, app, data := filepath.Join(base, "recovery"), filepath.Join(base, "app"), filepath.Join(base, "data")
	for _, p := range []string{app, data} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	scope := UpdateScope{Installation: app, DataRoots: []string{data}}
	plan := TransactionPlan{TrustDomain: "fixture", Audience: "official", Channel: "stable", Mode: "official", Platform: "darwin", Arch: "arm64", SigningIdentity: "FIXTURE_TEAM", UpdaterProtocol: 1,
		Scope: scope, SourceID: "source", TargetID: "target", SourceVersion: "1.0.0", TargetVersion: "1.1.0", SourceManifestSHA256: strings.Repeat("a", 64), SourcePackageSHA256: strings.Repeat("b", 64), TargetPackageSHA256: strings.Repeat("c", 64), PolicySequence: 7, PolicyDigest: strings.Repeat("d", 64), DataProfile: "fixture-v1", TargetDataProfile: "fixture-v1",
		Units: []SnapshotUnit{{Path: data}}, Reservations: []InstallSpaceReservation{{Directory: base, InstallBytes: 1024, RecoveryBytes: 1024}}}
	return root, UpdateJournal{Version: 1, ID: strings.Repeat("1", 32), Revision: 1, Stage: "PREPARED", Plan: plan, ResolvedScope: scope}
}

func writeJournalReadFixture(t *testing.T, root string, j UpdateJournal) string {
	t.Helper()
	name := filepath.Join(root, "transactions", "v1", strings.Repeat("1", 32), "journal.json")
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestPersistedJournalStagesPreserveStartupAdmission(t *testing.T) {
	active := []string{"PREPARED", "DRAINING", "STOPPED", "SNAPSHOT_PREPARING", "SNAPSHOT_READY", "INSTALL_HANDOFF", "CANDIDATE_VALIDATING", "RECOVERING_SOURCE", "SOURCE_RESTORED", "RESTORING_DATA", "RECOVERY_VALIDATING"}
	terminal := []string{"ABORTED", "COMMITTED", "RECOVERED"}
	for _, stage := range append(active, terminal...) {
		t.Run(stage, func(t *testing.T) {
			root, j := journalReadFixture(t)
			j.Stage = stage
			name := writeJournalReadFixture(t, root, j)
			before, _ := os.ReadFile(name)
			want := "update_in_progress"
			if stage == "ABORTED" || stage == "COMMITTED" || stage == "RECOVERED" {
				want = ""
			}
			err := CheckStartup(context.Background(), root, j.Plan.Scope)
			if (err == nil) != (want == "") || err != nil && ErrorCode(err) != want {
				t.Fatalf("startup: %v, want %s", err, want)
			}
			actor, err := CurrentProcessIdentity()
			if err != nil {
				t.Fatal(err)
			}
			lease, err := BeginStartup(context.Background(), root, j.Plan.Scope, "fixture-owner", "writer", actor)
			if (err == nil) != (want == "") || err != nil && ErrorCode(err) != want {
				t.Fatalf("registration: %v, want %s", err, want)
			}
			if lease != nil {
				if err := lease.Close(); err != nil {
					t.Fatal(err)
				}
			}
			all, err := ReadUpdateJournals(context.Background(), root)
			if err != nil || len(all) != 1 || all[0].Stage != stage {
				t.Fatalf("read: %+v %v", all, err)
			}
			after, _ := os.ReadFile(name)
			if !bytes.Equal(before, after) {
				t.Fatal("read changed persisted record")
			}
		})
	}
}

func TestPersistedJournalRejectsCorruptionBeforeStartup(t *testing.T) {
	cases := map[string]func(*UpdateJournal){
		"version": func(j *UpdateJournal) { j.Version = 2 }, "id": func(j *UpdateJournal) { j.ID = strings.Repeat("2", 32) }, "revision": func(j *UpdateJournal) { j.Revision = 0 }, "stage": func(j *UpdateJournal) { j.Stage = "UNKNOWN" },
		"digest": func(j *UpdateJournal) { j.Plan.TargetPackageSHA256 = "bad" }, "same-version": func(j *UpdateJournal) { j.Plan.TargetVersion = j.Plan.SourceVersion }, "mode": func(j *UpdateJournal) { j.Plan.Mode = "unknown" },
		"no-units": func(j *UpdateJournal) { j.Plan.Units = nil }, "no-reservation": func(j *UpdateJournal) { j.Plan.Reservations = nil },
		"relative": func(j *UpdateJournal) { j.Plan.Scope.Installation = "relative" }, "resolved-scope": func(j *UpdateJournal) { j.ResolvedScope.Installation += "-other" },
		"outside-unit": func(j *UpdateJournal) { j.Plan.Units[0].Path = j.Plan.Scope.Installation }, "exclude-parent": func(j *UpdateJournal) { j.Plan.Units[0].Exclude = []string{".."} },
		"sqlite-main-only": func(j *UpdateJournal) {
			db := filepath.Join(j.Plan.Units[0].Path, "state.db")
			j.Plan.SQLiteFiles = []string{db}
			j.Plan.Units = []SnapshotUnit{{Path: db}}
		},
		"sqlite-wal-excluded": func(j *UpdateJournal) {
			j.Plan.SQLiteFiles = []string{filepath.Join(j.Plan.Units[0].Path, "state.db")}
			j.Plan.Units[0].Exclude = []string{"state.db-wal"}
		},
		"snapshot-path": func(j *UpdateJournal) {
			j.SnapshotPath = j.Plan.Scope.Installation
			j.SnapshotSHA256 = strings.Repeat("a", 64)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			root, j := journalReadFixture(t)
			scope := j.ResolvedScope
			mutate(&j)
			writeJournalReadFixture(t, root, j)
			if err := CheckStartup(context.Background(), root, scope); ErrorCode(err) != "journal_corrupt" {
				t.Fatalf("corruption accepted: %v", err)
			}
			if _, err := ReadUpdateJournals(context.Background(), root); ErrorCode(err) != "journal_corrupt" {
				t.Fatalf("invalid status accepted: %v", err)
			}
		})
	}
	for _, raw := range []string{"{broken", strings.Repeat("x", 2*MaxEnvelopeBytes+1)} {
		t.Run("invalid-json", func(t *testing.T) {
			root, j := journalReadFixture(t)
			name := writeJournalReadFixture(t, root, j)
			if err := os.WriteFile(name, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if err := CheckStartup(context.Background(), root, j.Plan.Scope); ErrorCode(err) != "journal_corrupt" {
				t.Fatalf("invalid record allowed: %v", err)
			}
		})
	}
}

func TestPersistedJournalScopesAndAliases(t *testing.T) {
	root, j := journalReadFixture(t)
	if err := CheckStartup(context.Background(), root, j.Plan.Scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("read-only empty startup created state")
	}
	writeJournalReadFixture(t, root, j)
	for _, scope := range []UpdateScope{
		{Installation: j.Plan.Scope.Installation, DataRoots: []string{t.TempDir()}},
		{Installation: t.TempDir(), DataRoots: j.Plan.Scope.DataRoots},
		{Installation: t.TempDir(), DataRoots: []string{filepath.Join(j.Plan.Scope.DataRoots[0], "child")}},
	} {
		if err := CheckStartup(context.Background(), root, scope); ErrorCode(err) != "update_in_progress" {
			t.Fatalf("overlap allowed: %v", err)
		}
	}
	if err := CheckStartup(context.Background(), root, UpdateScope{Installation: t.TempDir(), DataRoots: []string{t.TempDir()}}); err != nil {
		t.Fatalf("unrelated startup blocked: %v", err)
	}
	alias := filepath.Join(filepath.Dir(root), "alias")
	if err := os.Symlink(j.Plan.Scope.DataRoots[0], alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks unavailable")
		}
		t.Fatal(err)
	}
	if err := CheckStartup(context.Background(), root, UpdateScope{Installation: t.TempDir(), DataRoots: []string{alias}}); ErrorCode(err) != "update_in_progress" {
		t.Fatalf("alias allowed: %v", err)
	}
}

func TestPersistedTerminalJournalDoesNotFenceChangedPaths(t *testing.T) {
	for _, stage := range []string{"ABORTED", "COMMITTED", "RECOVERED"} {
		t.Run(stage, func(t *testing.T) {
			root, j := journalReadFixture(t)
			j.Stage = stage
			writeJournalReadFixture(t, root, j)
			data := j.Plan.Scope.DataRoots[0]
			if err := os.Rename(data, data+"-old"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), data); err != nil {
				if runtime.GOOS == "windows" {
					t.Skip("symlinks unavailable")
				}
				t.Fatal(err)
			}
			if err := CheckStartup(context.Background(), root, j.Plan.Scope); err != nil {
				t.Fatalf("terminal record fenced changed paths: %v", err)
			}
		})
	}
}
