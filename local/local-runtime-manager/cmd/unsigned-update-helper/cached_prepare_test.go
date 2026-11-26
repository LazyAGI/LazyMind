//go:build darwin && lazymind_unsigned_test

package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// Exercise main -> run("prepare") in the cached executable. Only the OS
// account locations are supplied by the test build. The CLI has no custom
// runtime argument; it must recover the runtime from the active transaction.
func TestCachedUnsignedHelperPreparesFromActiveRuntime(t *testing.T) {
	source, key := inPlaceSourceConfig(t)
	target := source
	target.ReleaseID, target.Version = "fixture-target", "0.5.230"
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The named product is covered by separate fixed ordinary/legacy tests.
	// Use the currently verifiable subject here to isolate runtime recovery.
	sourceTemplate := inPlaceBundle(t, filepath.Join(root, "origin"), source, update.TestAppName, update.TestAppID)
	targetTemplate := inPlaceBundle(t, filepath.Join(root, "candidate"), target, update.TestAppName, update.TestAppID)
	actorSource := filepath.Join(root, "prepare_caller.go")
	body := `package main
import ("io"; "os"; "os/exec")
func main(){
	var ready [1]byte
	if _,err:=io.ReadFull(os.Stdin,ready[:]);err!=nil{os.Exit(2)}
	cmd:=exec.Command(os.Args[1],"prepare")
	cmd.Stdout,cmd.Stderr=os.Stdout,os.Stderr
	if err:=cmd.Run();err!=nil{os.Exit(1)}
}`
	if err := os.WriteFile(actorSource, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	actorImage := filepath.Join(root, "source-actor")
	runInPlaceFixture(t, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", actorImage, actorSource)
	for _, kind := range []string{"default control", "previously selected custom runtime", "another actor", "other test round", "custom pending receipt without accepted policy", "package unavailable after signing", "old updater root"} {
		t.Run(kind, func(t *testing.T) {
			sandbox, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(sandbox, "account")
			support := filepath.Join(home, "Library", "Application Support")
			updater := filepath.Join(support, "LazyMindUpdater")
			app := filepath.Join(home, "Applications", update.TestAppName+".app")
			if err := os.MkdirAll(filepath.Dir(app), 0700); err != nil {
				t.Fatal(err)
			}
			runInPlaceFixture(t, "/usr/bin/ditto", sourceTemplate, app)
			resources := filepath.Join(app, "Contents", "Resources", "runtime")
			actor := filepath.Join(app, "Contents", "MacOS", "LazyMindApp")
			runInPlaceFixture(t, "/bin/cp", actorImage, actor)
			overlay := unsignedLocationOverlay(t, sandbox, home)
			raw, err := json.Marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			helper := filepath.Join(resources, "bin", "unsigned-update-helper")
			runInPlaceFixture(t, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-overlay", overlay, "-tags", "lazymind_unsigned_test", "-ldflags", "-X main.embeddedConfig="+base64.StdEncoding.EncodeToString(raw), "-o", helper, ".")
			runInPlaceFixture(t, "/usr/bin/codesign", "--force", "--deep", "--sign", "-", app)
			helperBytes, err := os.ReadFile(helper)
			if err != nil {
				t.Fatal(err)
			}
			cached := filepath.Join(updater, "helpers", fmt.Sprintf("%x", sha256.Sum256(helperBytes)), "unsigned-update-helper")
			if err := os.MkdirAll(filepath.Dir(cached), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cached, helperBytes, 0700); err != nil {
				t.Fatal(err)
			}
			var output, diagnostics bytes.Buffer
			proc := exec.CommandContext(t.Context(), actor, cached)
			proc.Stdout, proc.Stderr = &output, &diagnostics
			in, err := proc.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := proc.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = in.Close()
				if proc.ProcessState == nil {
					_ = proc.Process.Kill()
					_ = proc.Wait()
				}
			}()
			live, err := update.IdentifyProcess(proc.Process.Pid)
			if err != nil {
				t.Fatal(err)
			}
			data := filepath.Join(sandbox, "existing external runtime")
			if kind == "default control" {
				data = filepath.Join(support, "LazyMind")
			}
			receiptRoot := updater
			if kind == "old updater root" {
				receiptRoot = filepath.Join(support, "LazyMindTestUpdater")
			}
			r := seedCachedPrepareRecord(t, source, target, key, receiptRoot, data, resources, targetTemplate, live, kind)
			if kind == "package unavailable after signing" {
				if err := os.Remove(r.PackagePath); err != nil {
					t.Fatal(err)
				}
			}
			journal := update.NativeRecordPath(receiptRoot)
			before, err := os.ReadFile(journal)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := in.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			_ = in.Close()
			runErr := proc.Wait()
			after, err := os.ReadFile(journal)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("prepare changed the pending transaction")
			}
			var ack struct{ Status, ID, ReleaseID string }
			decodeErr := json.Unmarshal(output.Bytes(), &ack)
			if kind == "default control" || kind == "previously selected custom runtime" {
				if runErr != nil || decodeErr != nil || ack.Status != "prepared" || ack.ID != r.TransactionID || ack.ReleaseID != target.ReleaseID {
					t.Fatalf("cached helper preparing the accepted runtime: %v stdout=%q stderr=%q", runErr, output.String(), diagnostics.String())
				}
			} else if runErr == nil || ack.Status == "prepared" {
				t.Fatalf("unexpected acceptance: %v %s", runErr, output.String())
			}
			if got, e := os.ReadFile(filepath.Join(data, "existing-document")); e != nil || string(got) != "previous data" {
				t.Fatalf("pending runtime affected: %q %v", got, e)
			}
		})
	}
}

// This file substitutes only the OS account's fixed locations. It has no
// access to the pending record, caller PID or selected custom runtime.
func unsignedLocationOverlay(t *testing.T, dir, home string) string {
	t.Helper()
	file := filepath.Join(dir, "locations.go")
	body := fmt.Sprintf(`package desktopupdate
import "path/filepath"
func UnsignedApplicationLocations() (UnsignedTestPaths,string,error){
	home:=%q
	base:=filepath.Join(home,"Library","Application Support")
	return UnsignedTestPaths{Home:home, Application:filepath.Join(home,"Applications",TestAppName+".app"),Runtime:filepath.Join(base,"LazyMind"),Updater:filepath.Join(base,"LazyMindUpdater"),Profile:filepath.Join(base,"lazymind-desktop"),Cache:filepath.Join(home,"Library","Caches","LazyMind"),Logs:filepath.Join(home,"Library","Logs","LazyMind"),Documents:filepath.Join(home,"Documents","LazyMind"),CLI:filepath.Join(home,".lazymind")},%q,nil
}`, home, filepath.Join(dir, "System", "Applications"))
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	original, err := filepath.Abs("../../internal/desktopupdate/unsigned_locations.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"Replace": map[string]string{original: file}})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlay, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return overlay
}

func seedCachedPrepareRecord(t *testing.T, c, target update.Config, key ed25519.PrivateKey, root, data, install, bundle string, actor update.ProcessIdentity, kind string) *update.NativeRecord {
	t.Helper()
	if err := os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "existing-document"), []byte("previous data"), 0600); err != nil {
		t.Fatal(err)
	}
	partition := fmt.Sprintf("%x", sha256.Sum256([]byte(c.TrustDomain+"\x00"+c.Audience+"\x00"+c.Channel)))
	cache := filepath.Join(root, "downloads", partition)
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(cache, "package.zip")
	runInPlaceFixture(t, "/usr/bin/ditto", "-c", "-k", "--norsrc", "--noextattr", "--keepParent", bundle, archive)
	pkg, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	databases := map[string]string{}
	for _, family := range []string{"core", "auth", "lazyllm", "segments", "channel", "scan", "core-state", "auth-state", "scan-state"} {
		databases[family] = filepath.Join(data, family+".db")
	}
	r := &update.NativeRecord{Version: 2, Phase: "pending", TransactionID: strings.Repeat("1", 32), SourceReleaseID: c.ReleaseID, SourceVersion: c.Version, TargetReleaseID: target.ReleaseID, TargetVersion: target.Version, TrustDomain: c.TrustDomain, Audience: c.Audience, Channel: c.Channel, SigningIdentity: c.SigningIdentity, Platform: c.Platform, Arch: c.Arch,
		RuntimeRoot: data, InstallationRoot: install, Scope: update.UpdateScope{Installation: install, DataRoots: []string{data}}, SourceActor: &actor, SourceOwnerDigest: fmt.Sprintf("%x", sha256.Sum256([]byte("fixture-owner"))), SourceDataProfile: "format-v2-fixture", TargetDataProfile: "format-v2-fixture", SourceStorage: update.NativeStorage{Version: 2, Databases: databases, SegmentStore: "SQLiteStore", VectorEngine: "milvus-lite", VectorPath: filepath.Join(data, "vector"), LayoutSource: "accepted-source-runtime"}, ComponentMode: "absent", ExpectedPeers: []string{"core"},
		PackagePath: archive, PackageSize: int64(len(pkg)), PackageSHA256: fmt.Sprintf("%x", sha256.Sum256(pkg))}
	policyRoot := root
	if kind == "custom pending receipt without accepted policy" {
		policyRoot = t.TempDir()
	}
	selected := inPlaceSignedOffer(t, c, key, policyRoot, bundle, r)
	if kind == "another actor" {
		different, e := update.CurrentProcessIdentity()
		if e != nil {
			t.Fatal(e)
		}
		r.SourceActor = &different
	}
	if kind == "other test round" {
		r.TrustDomain = "inplace-418-" + strings.Repeat("b", 64)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	res, err := update.NativeUpdateStateCommand(t.Context(), root, "create", raw)
	var result struct{ Status string }
	if err != nil || json.Unmarshal(res, &result) != nil || result.Status != "pending" {
		t.Fatalf("genuine pending record prerequisite: %s %v", res, err)
	}
	stored, err := update.ReadNativeRecord(root)
	if err != nil || stored == nil || stored.SourceHadRAG || stored.ComponentMode != "absent" || len(stored.SourceComponents()) != 0 || len(stored.EffectiveComponents()) != 0 ||
		stored.PackageSHA256 != selected.Package.SHA256 || stored.PackageSize != selected.Package.Size || stored.PackageUnpackedBytes <= 0 || stored.PackageUnpackedBytes != selected.Package.UnpackedBytes ||
		stored.ComponentsSHA256 != selected.Components.SHA256 || stored.ComponentsSize != selected.Components.Size {
		t.Fatalf("persisted no-RAG transaction differs from the verified target: %+v %v", selected, err)
	}
	return stored
}
