//go:build darwin && lazymind_unsigned_test

package desktopupdate

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInPlaceTestBuildSharesExistingDesktopDataPaths(t *testing.T) {
	base := fixtureOSRecoveryBase(t)
	home := filepath.Dir(filepath.Dir(base))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LAZYMIND_HOST_HOME", t.TempDir())
	p, err := UnsignedPaths()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []struct{ name, got, want string }{
		{"runtime", p.Runtime, filepath.Join(base, "LazyMind")},
		{"Electron profile", p.Profile, filepath.Join(base, "lazymind-desktop")},
		{"cache", p.Cache, filepath.Join(home, "Library", "Caches", "LazyMind")},
		{"logs", p.Logs, filepath.Join(home, "Library", "Logs", "LazyMind")},
		{"documents", p.Documents, filepath.Join(home, "Documents", "LazyMind")},
		{"CLI", p.CLI, filepath.Join(home, ".lazymind")},
		{"installer updater", p.Updater, filepath.Join(base, "LazyMindUpdater")},
	} {
		if filepath.Clean(path.got) != path.want {
			t.Errorf("%s uses %q; existing desktop uses %q", path.name, path.got, path.want)
		}
	}
	root, err := UpdaterRoot()
	if err != nil || root != filepath.Join(base, "LazyMindUpdater") {
		t.Fatalf("manager and helper must see active desktop transactions: %q %v", root, err)
	}
}

func TestInPlaceHandoffPreservesForeignActiveTransactionAndScope(t *testing.T) {
	for _, kind := range []string{"matching custom runtime", "official record", "other round", "other installation", "other runtime", "other download partition", "different source actor", "unfinished official journal", "completed official journal"} {
		t.Run(kind, func(t *testing.T) {
			f := newNativeStateFixture(t)
			canonicalRoot, err := filepath.EvalSymlinks(f.root)
			if err != nil {
				t.Fatal(err)
			}
			f.root = canonicalRoot
			f.filename = NativeRecordPath(f.root)
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cfg := unsignedConfig(t)
			p := UnsignedTestPaths{Home: base, Application: filepath.Join(base, "Applications", "LazyMind.app"), Runtime: f.runtimeRoot, Updater: f.root}
			managerRoot := filepath.Join(p.Application, "Contents", "Resources", "runtime")
			if err := os.MkdirAll(managerRoot, 0700); err != nil {
				t.Fatal(err)
			}
			executable := filepath.Join(p.Application, "Contents", "MacOS", "LazyMindApp")
			signInPlaceFixtureBundle(t, p.Application, cfg)
			source := exec.Command(executable, "60")
			if err := source.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = source.Process.Kill(); _ = source.Wait() })
			actor, err := IdentifyProcess(source.Process.Pid)
			if err != nil {
				t.Fatal(err)
			}
			f.record["sourceReleaseId"], f.record["sourceVersion"], f.record["sourceActor"] = cfg.ReleaseID, cfg.Version, actor
			f.record["installationRoot"] = managerRoot
			f.record["scope"].(map[string]any)["installation"] = managerRoot
			f.record["trustDomain"], f.record["audience"], f.record["channel"], f.record["signingIdentity"] = cfg.TrustDomain, cfg.Audience, cfg.Channel, cfg.SigningIdentity
			artifact := f.record["packagePath"].(string)
			partition := fmt.Sprintf("%x", sha256.Sum256([]byte(cfg.TrustDomain+"\x00"+cfg.Audience+"\x00"+cfg.Channel)))
			if kind == "other download partition" {
				partition = fmt.Sprintf("%x", sha256.Sum256([]byte("official\x00official\x00stable")))
			}
			cache := filepath.Join(f.root, "downloads", partition, "package.zip")
			if err := os.MkdirAll(filepath.Dir(cache), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(artifact, cache); err != nil {
				t.Fatal(err)
			}
			f.record["packagePath"] = cache
			switch kind {
			case "official record":
				f.record["trustDomain"], f.record["audience"], f.record["channel"], f.record["signingIdentity"] = "official", "official", "stable", "FIXTURE_TEAM"
			case "other round":
				f.record["trustDomain"] = "inplace-418-" + strings.Repeat("b", 64)
			case "other installation":
				p.Application = filepath.Join(base, "another", "Applications", "LazyMind.app")
			case "other runtime":
				p.Runtime = filepath.Join(base, "different-runtime")
			case "different source actor":
				actor, err = CurrentProcessIdentity()
				if err != nil {
					t.Fatal(err)
				}
			}
			f.installing(t)
			if kind == "unfinished official journal" || kind == "completed official journal" {
				_, j := journalReadFixture(t)
				j.Plan.Scope.Installation = managerRoot
				j.ResolvedScope.Installation = managerRoot
				if kind == "completed official journal" {
					j.Stage = "COMMITTED"
				}
				journal := writeJournalReadFixture(t, f.root, j)
				prior, err := os.ReadFile(journal)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					after, e := os.ReadFile(journal)
					if e != nil || !bytes.Equal(after, prior) {
						t.Errorf("foreign journal changed: %v", e)
					}
				})
				e := CheckStartup(t.Context(), f.root, UpdateScope{Installation: managerRoot, DataRoots: []string{p.Runtime}})
				if kind == "unfinished official journal" && ErrorCode(e) != "update_in_progress" || kind == "completed official journal" && e != nil {
					t.Fatalf("invalid official journal precondition: %v", e)
				}
			}
			before, err := os.ReadFile(f.filename)
			if err != nil {
				t.Fatal(err)
			}
			invoked := false
			err = WithUnsignedV2Handoff(t.Context(), cfg, p, actor, func(r *NativeRecord) error { invoked = true; return nil })
			if kind == "matching custom runtime" || kind == "completed official journal" {
				if err != nil || !invoked {
					t.Fatalf("matching signed-source handoff: invoked=%t %v", invoked, err)
				}
				return
			}
			if err == nil || invoked {
				t.Fatalf("foreign active source authorized install: invoked=%t err=%v", invoked, err)
			}
			after, err := os.ReadFile(f.filename)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("foreign active receipt changed: %v", err)
			}
			// A fresh create against the same physical root cannot displace it either.
			f.record["transactionId"] = strings.Repeat("2", 32)
			f.command(t, "create", f.record, "blocked", "update_transaction_conflict")
			after, err = os.ReadFile(f.filename)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("fresh request displaced foreign receipt: %v", err)
			}
		})
	}
}
