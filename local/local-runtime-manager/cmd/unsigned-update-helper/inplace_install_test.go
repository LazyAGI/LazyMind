//go:build darwin && lazymind_unsigned_test

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// The target's real compiled helper supplies its own channel configuration;
// the package copy, extraction, macOS deep verification and replacement run
// just as they do after a source transaction has bound its artifacts.
func TestInPlaceInstallerReplacesOnlyBoundApplication(t *testing.T) {
	source, private := inPlaceSourceConfig(t)
	target := source
	target.Version, target.ReleaseID = "0.5.230", "fixture-target"
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := inPlaceBundle(t, filepath.Join(base, "source"), source, "LazyMind", "ai.lazymind.desktop")
	dst := inPlaceBundle(t, filepath.Join(base, "target"), target, "LazyMind", "ai.lazymind.desktop")
	other := target
	other.TrustDomain = "inplace-418-" + strings.Repeat("b", 64)
	other.PolicyURL = strings.Replace(target.PolicyURL, "round-417", "round-418", 1)
	foreign := inPlaceBundle(t, filepath.Join(base, "foreign"), other, "LazyMind", "ai.lazymind.desktop")
	// Permission and volume tests require a bundle the current build can
	// already verify. Fixed ordinary/legacy identity tests cover the name change.
	acceptedSource, acceptedTarget := src, dst
	if update.TestAppName != "LazyMind" || update.TestAppID != "ai.lazymind.desktop" {
		acceptedSource = inPlaceBundle(t, filepath.Join(base, "accepted-source"), source, update.TestAppName, update.TestAppID)
		acceptedTarget = inPlaceBundle(t, filepath.Join(base, "accepted-target"), target, update.TestAppName, update.TestAppID)
	}
	for _, scenario := range []string{"user application", "system application", "different signed round", "not writable at preparation", "write access lost after preparation", "data on another volume"} {
		t.Run(scenario, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(root, "Users", "fixture")
			origin, targetApp, name := src, dst, "LazyMind.app"
			if scenario == "not writable at preparation" || scenario == "write access lost after preparation" || scenario == "data on another volume" {
				origin, targetApp, name = acceptedSource, acceptedTarget, update.TestAppName+".app"
			}
			selected, neighbor := filepath.Join(home, "Applications", name), filepath.Join(root, "Applications", name)
			if scenario == "system application" {
				selected, neighbor = neighbor, selected
			}
			if scenario == "data on another volume" {
				selected = filepath.Join(inPlaceSecondVolume(t, root), "Applications", name)
			}
			for _, path := range []string{selected, neighbor} {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				runInPlaceFixture(t, "/usr/bin/ditto", origin, path)
			}
			var original map[string]inPlaceBackupFile
			if scenario == "data on another volume" {
				// Distinguish the actual selected source from the installed neighbor.
				marker := filepath.Join(selected, "Contents", "Resources", "selected-source-copy")
				if err := os.WriteFile(marker, []byte("bound source on test volume"), 0600); err != nil {
					t.Fatal(err)
				}
				runInPlaceFixture(t, "/usr/bin/codesign", "--force", "--deep", "--sign", "-", selected)
				original = inPlaceSourceFiles(t, selected)
			}
			p := update.UnsignedTestPaths{Home: home, Application: selected, Runtime: filepath.Join(root, "existing custom data"), Updater: filepath.Join(home, "Library", "Application Support", "LazyMindUpdater")}
			if err := os.MkdirAll(p.Runtime, 0700); err != nil {
				t.Fatal(err)
			}
			witness := filepath.Join(p.Runtime, "existing-document")
			if err := os.WriteFile(witness, []byte("existing data"), 0600); err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(p.Updater, "downloads", "fixture", "package.zip")
			if err := os.MkdirAll(filepath.Dir(artifact), 0700); err != nil {
				t.Fatal(err)
			}
			candidateApp := targetApp
			if scenario == "different signed round" {
				candidateApp = foreign
			}
			runInPlaceFixture(t, "/usr/bin/ditto", "-c", "-k", "--norsrc", "--noextattr", "--keepParent", candidateApp, artifact)
			raw, err := os.ReadFile(artifact)
			if err != nil {
				t.Fatal(err)
			}
			r := &update.NativeRecord{TransactionID: strings.Repeat("1", 32), SourceReleaseID: source.ReleaseID, SourceVersion: source.Version, TargetReleaseID: target.ReleaseID, TargetVersion: target.Version,
				InstallationRoot: filepath.Join(selected, "Contents", "Resources", "runtime"), RuntimeRoot: p.Runtime, PackagePath: artifact, PackageSize: int64(len(raw)), PackageSHA256: fmt.Sprintf("%x", sha256.Sum256(raw)),
				TrustDomain: source.TrustDomain, Audience: source.Audience, Channel: source.Channel, SigningIdentity: source.SigningIdentity, Platform: source.Platform, Arch: source.Arch}
			inPlaceSignedOffer(t, source, private, p.Updater, candidateApp, r)
			proc := exec.CommandContext(t.Context(), filepath.Join(selected, "Contents", "MacOS", "LazyMindApp"), "60")
			if err := proc.Start(); err != nil {
				t.Fatal(err)
			}
			stop := sync.OnceFunc(func() { _ = proc.Process.Kill(); _ = proc.Wait() })
			t.Cleanup(stop)
			actor, err := update.IdentifyProcess(proc.Process.Pid)
			if err != nil {
				t.Fatal(err)
			}
			r.SourceActor = &actor
			readOnly := func() {
				t.Helper()
				parent := filepath.Dir(selected)
				if err := os.Chmod(parent, 0500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(parent, 0700); err != nil {
						t.Error(err)
					}
				})
			}
			if scenario == "not writable at preparation" {
				// Same bundle and signed offer are installable while writable.
				if err := prepare(t.Context(), p, source, r); err != nil {
					t.Fatalf("writable preparation control: %v", err)
				}
				readOnly()
			}
			prepareErr := prepare(t.Context(), p, source, r)
			wantVersion := source.ReleaseID
			switch {
			case scenario == "different signed round" || scenario == "not writable at preparation":
				if prepareErr == nil {
					t.Fatalf("%s accepted for installation", scenario)
				}
				if live, e := update.SameProcess(actor); e != nil || !live {
					t.Fatalf("preparation stopped the source: %v", e)
				}
			default:
				if prepareErr != nil {
					t.Fatalf("could not prepare the normal signed application: %v", prepareErr)
				}
				if scenario == "write access lost after preparation" {
					readOnly()
					// Keep the real source alive. Waiting for source exit after
					// emitting an ACK would stop an otherwise usable application.
					bounded, cancel := context.WithTimeout(t.Context(), 2*time.Second)
					var out bytes.Buffer
					e := install(bounded, p, source, r, actor, &out)
					expired := bounded.Err() != nil
					cancel()
					if e == nil || expired || out.Len() != 0 {
						t.Errorf("known non-writable target must be rejected before ACK or waiting for exit: output=%q error=%v expired=%v", out.Bytes(), e, expired)
					}
					if live, e := update.SameProcess(actor); e != nil || !live {
						t.Errorf("pre-ACK rejection lost the original process: %v", e)
					}
				} else if err := inPlaceDispatch(t, p, source, r, actor, stop); err != nil {
					t.Errorf("accepted handoff failed: %v", err)
				} else {
					wantVersion = target.ReleaseID
					if original != nil && !hasInPlaceSourceBackup(t, root, original) {
						t.Error("pending cross-volume update no longer retains the complete selected A for recovery")
					}
					if e := update.VerifyUnsignedMacBundle(t.Context(), selected, target); e != nil {
						t.Fatalf("installed copy did not verify as target: %v", e)
					}
				}
			}
			for path, release := range map[string]string{selected: wantVersion, neighbor: source.ReleaseID} {
				got, e := os.ReadFile(filepath.Join(path, "Contents", "Resources", "release-witness"))
				if e != nil || string(got) != release {
					t.Fatalf("unexpected installed copy %s: %q, %v", path, got, e)
				}
				runInPlaceFixture(t, "/usr/bin/codesign", "--verify", "--deep", "--strict", path)
			}
			if got, err := os.ReadFile(witness); err != nil || string(got) != "existing data" {
				t.Fatalf("existing data changed: %q %v", got, err)
			}
		})
	}
}

// Exercise the old process-exit handshake without opening a GUI or touching an installed App.
func inPlaceDispatch(t *testing.T, p update.UnsignedTestPaths, c update.Config, r *update.NativeRecord, actor update.ProcessIdentity, stop func()) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	done := make(chan error, 1)
	go func() { err := install(ctx, p, c, r, actor, writer); _ = writer.CloseWithError(err); done <- err }()
	var ack struct{ Status string }
	if err := json.NewDecoder(reader).Decode(&ack); err != nil {
		stop()
		<-done
		return err
	}
	if ack.Status != "accepted" {
		stop()
		t.Fatalf("unexpected handoff: %q", ack.Status)
	}
	stop()
	return <-done
}

// Match the complete original signed fixture, including its seal and
// executables, without prescribing the backup directory or ZIP pathname.
type inPlaceBackupFile struct {
	digest     [32]byte
	executable fs.FileMode
}

func inPlaceSourceFiles(t *testing.T, bundle string) map[string]inPlaceBackupFile {
	t.Helper()
	files := map[string]inPlaceBackupFile{}
	tree := os.DirFS(bundle)
	err := fs.WalkDir(tree, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, e := fs.ReadFile(tree, name)
		if e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		files[name] = inPlaceBackupFile{sha256.Sum256(data), info.Mode().Perm() & 0111}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func hasInPlaceSourceBackup(t *testing.T, root string, want map[string]inPlaceBackupFile) bool {
	t.Helper()
	const marker = "Contents/Resources/selected-source-copy"
	tree := os.DirFS(root)
	var found bool
	match := func(fsys fs.FS, prefix string) bool {
		for rel, digest := range want {
			name := filepath.ToSlash(filepath.Join(prefix, rel))
			data, e := fs.ReadFile(fsys, name)
			if e != nil || sha256.Sum256(data) != digest.digest {
				return false
			}
			info, e := fs.Stat(fsys, name)
			if e != nil || info.Mode().Perm()&0111 != digest.executable {
				return false
			}
		}
		return true
	}
	err := fs.WalkDir(tree, ".", func(name string, d fs.DirEntry, e error) error {
		if e != nil || d.IsDir() || found {
			return e
		}
		if strings.HasSuffix(name, "/"+marker) {
			found = match(tree, strings.TrimSuffix(name, "/"+marker))
			return nil
		}
		if strings.EqualFold(filepath.Ext(name), ".zip") {
			z, e := zip.OpenReader(filepath.Join(root, name))
			if e != nil {
				return e
			}
			defer z.Close()
			for _, f := range z.File {
				if f.Name == marker || strings.HasSuffix(f.Name, "/"+marker) {
					prefix := strings.TrimSuffix(strings.TrimSuffix(f.Name, marker), "/")
					if match(&z.Reader, prefix) {
						found = true
						break
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func inPlaceSecondVolume(t *testing.T, root string) string {
	t.Helper()
	image := filepath.Join(root, "inplace-test.dmg")
	mount := filepath.Join(root, "test-volume")
	if err := os.Mkdir(mount, 0700); err != nil {
		t.Fatal(err)
	}
	if out, e := exec.Command("/usr/bin/hdiutil", "create", "-quiet", "-size", "160m", "-fs", "HFS+", "-volname", "InPlaceTest", image).CombinedOutput(); e != nil {
		t.Skipf("temporary second volume unavailable: %v %s", e, out)
	}
	if out, e := exec.Command("/usr/bin/hdiutil", "attach", "-quiet", "-nobrowse", "-mountpoint", mount, image).CombinedOutput(); e != nil {
		t.Skipf("temporary image could not be mounted: %v %s", e, out)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "/usr/bin/hdiutil", "detach", mount).CombinedOutput(); err != nil {
			if forced, e := exec.Command("/usr/bin/hdiutil", "detach", "-force", mount).CombinedOutput(); e != nil {
				t.Errorf("detach disposable image %s: %v %s; retry %v %s", mount, err, out, e, forced)
			}
		}
	})
	var original, other syscall.Stat_t
	if err := syscall.Stat(root, &original); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Stat(mount, &other); err != nil {
		t.Fatal(err)
	}
	if original.Dev == other.Dev {
		t.Fatal("temporary app and data volumes must be distinct")
	}
	return mount
}

const inPlaceCatalogMember = "Contents/Resources/runtime/config/python-components.json"

// Measure the built App as prepare-update-release.mjs does. The ZIP provides
// a separate lower bound (sum of its uncompressed members).
func inPlaceAppAllocation(t *testing.T, app string) int64 {
	t.Helper()
	var size int64
	err := filepath.WalkDir(app, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		st := info.Sys().(*syscall.Stat_t)
		size += max(info.Size(), st.Blocks*512)
		return nil
	})
	if err != nil || size <= 0 {
		t.Fatalf("cannot measure built application: %d, %v", size, err)
	}
	return size
}

// Fingerprint a materialized release rather than signing values copied from
// the client's pending record. Every release owns its own archive and catalog.
func inPlacePublishedRelease(t *testing.T, c update.Config, id, version, app, archive string) (update.Release, []byte) {
	t.Helper()
	prefix := strings.TrimSuffix(c.PolicyURL, "policy.json") + "releases/" + id + "/"
	builtCatalog, err := os.ReadFile(filepath.Join(app, inPlaceCatalogMember))
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	archivedCatalog, err := fs.ReadFile(&z.Reader, filepath.ToSlash(filepath.Join(filepath.Base(app), inPlaceCatalogMember)))
	if err != nil || !bytes.Equal(archivedCatalog, builtCatalog) {
		t.Fatalf("ZIP and signed App disagree on the target component catalog: %v", err)
	}
	var archiveMinimum int64
	for _, item := range z.File {
		archiveMinimum += int64(item.UncompressedSize64)
	}
	allocation := inPlaceAppAllocation(t, app)
	if archiveMinimum <= 0 || allocation < archiveMinimum {
		t.Fatalf("invalid expanded size: signed tree=%d, ZIP members=%d", allocation, archiveMinimum)
	}

	packageFile, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer packageFile.Close()
	pkgHash := sha256.New()
	pkgSize, err := io.Copy(pkgHash, packageFile)
	if err != nil {
		t.Fatal(err)
	}
	feed := []byte(fmt.Sprintf(`{"releaseId":%q,"version":%q}`, id, version))
	return update.Release{ID: id, Version: version, Platform: c.Platform, Arch: c.Arch, SigningIdentity: c.SigningIdentity, MinimumSystemVersion: "12.0.0", MinUpdaterProtocol: 2, UpdaterProtocol: 2,
		Package:    update.Artifact{URL: prefix + "package.zip", SHA256: fmt.Sprintf("%x", pkgHash.Sum(nil)), Size: pkgSize, UnpackedBytes: allocation},
		Feed:       update.Artifact{URL: prefix + "feed.json", Size: int64(len(feed)), SHA256: fmt.Sprintf("%x", sha256.Sum256(feed))},
		Components: update.Artifact{URL: prefix + "components.json", Size: int64(len(builtCatalog)), SHA256: fmt.Sprintf("%x", sha256.Sum256(builtCatalog))}}, archivedCatalog
}

func inPlaceSignedOffer(t *testing.T, c update.Config, key ed25519.PrivateKey, updater, targetApp string, r *update.NativeRecord) *update.Release {
	t.Helper()
	now := time.Now().UTC()
	sourceApp := filepath.Clean(filepath.Join(r.InstallationRoot, "..", "..", ".."))
	sourceZip := filepath.Join(t.TempDir(), "source.zip")
	runInPlaceFixture(t, "/usr/bin/ditto", "-c", "-k", "--norsrc", "--noextattr", "--keepParent", sourceApp, sourceZip)
	source, sourceCatalog := inPlacePublishedRelease(t, c, c.ReleaseID, c.Version, sourceApp, sourceZip)
	target, archivedCatalog := inPlacePublishedRelease(t, c, r.TargetReleaseID, r.TargetVersion, targetApp, r.PackagePath)
	var available struct {
		Components map[string]update.ComponentDescriptor
	}
	if err := json.Unmarshal(sourceCatalog, &available); err != nil {
		t.Fatal(err)
	}
	rag, ok := available.Components["rag"]
	if !ok {
		t.Fatal("missing available source RAG catalog entry")
	}
	// The downloaded catalog comes from the actual ZIP. The signed hash and
	// size above were measured independently from the built bundle resource.
	r.ComponentsPath = filepath.Join(filepath.Dir(r.PackagePath), "components.json")
	if err := os.WriteFile(r.ComponentsPath, archivedCatalog, 0600); err != nil {
		t.Fatal(err)
	}
	cacheCatalog, err := os.ReadFile(r.ComponentsPath)
	if err != nil {
		t.Fatal(err)
	}
	r.ComponentsSize = int64(len(cacheCatalog))
	r.ComponentsSHA256 = fmt.Sprintf("%x", sha256.Sum256(cacheCatalog))
	profile := "format-v2-fixture"
	policy := map[string]any{"schemaVersion": 2, "minUpdaterProtocol": 2, "trustDomain": c.TrustDomain, "audience": c.Audience, "channel": c.Channel, "policySequence": 1, "issuedAt": now.Add(-time.Minute).Format(time.RFC3339), "expiresAt": now.Add(time.Hour).Format(time.RFC3339),
		"releases": []update.Release{source, target}, "revoked": []string{}, "repairAcceptances": []any{},
		"edges": []any{map[string]any{"sourceReleaseId": c.ReleaseID, "targetReleaseId": r.TargetReleaseID, "priority": 1, "migrationProfile": "format-v2", "sourceDataProfiles": []string{profile}, "targetDataProfile": profile, "componentTransitions": map[string]any{"rag": map[string]any{"sourceClasses": []any{map[string]any{"tag": rag.Tag, "pythonAbi": rag.PythonABI, "baseFingerprint": rag.BaseFingerprint, "packages": rag.Packages}}, "targetComponentsSHA256": target.Components.SHA256}}}}}
	payload, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(map[string]string{"keyId": "fixture", "payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := update.Check(t.Context(), envelope, c, update.LocalState{DataProfile: profile, SystemVersion: "15.0.0", Components: map[string]string{}}, updater, false, now)
	if err != nil || offer.Status != "direct" || offer.Next == nil || offer.Next.ID != r.TargetReleaseID ||
		offer.Next.Package.UnpackedBytes <= 0 || offer.Next.Package.UnpackedBytes != target.Package.UnpackedBytes ||
		offer.Next.Package.Size != r.PackageSize || offer.Next.Package.SHA256 != r.PackageSHA256 ||
		offer.Next.Components.Size != r.ComponentsSize || offer.Next.Components.SHA256 != r.ComponentsSHA256 {
		t.Fatalf("signed target disagrees with the staged artifacts: %+v %v", offer, err)
	}
	r.PackageUnpackedBytes = offer.Next.Package.UnpackedBytes
	r.PolicyDigest, r.PolicySequence = offer.PolicyDigest, offer.PolicySequence
	return offer.Next
}

// The helper is dispatched from the updater cache; the source is the live
// Electron process. Give the resolver real process instances in two complete
// temporary Applications trees and an unrelated tree with the same basename.
func TestCachedUnsignedHelperSelectsRunningSource(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, system := filepath.Join(root, "User"), filepath.Join(root, "System", "Applications")
	user := filepath.Join(home, "Applications", "LazyMind.app")
	paths := update.UnsignedTestPaths{Home: home, Application: user, Updater: filepath.Join(home, "Library", "Application Support", "LazyMindUpdater")}
	cfg, _ := inPlaceSourceConfig(t)
	app := inPlaceBundle(t, filepath.Join(root, "fixture"), cfg, "LazyMind", "ai.lazymind.desktop")
	oldApp := inPlaceBundle(t, filepath.Join(root, "isolated"), cfg, "LazyMind Test", "ai.lazymind.desktop.test")
	type location struct {
		name, bundle string
		allowed      bool
	}
	locations := []location{{"user", user, true}, {"system", filepath.Join(system, "LazyMind.app"), true}, {"outside", filepath.Join(root, "Downloads", "LazyMind.app"), false}, {"old isolated product", filepath.Join(home, "Applications", "LazyMind Test.app"), false}}
	for _, l := range locations {
		template := app
		if l.name == "old isolated product" {
			template = oldApp
		}
		if err := os.MkdirAll(filepath.Dir(l.bundle), 0700); err != nil {
			t.Fatal(err)
		}
		runInPlaceFixture(t, "/usr/bin/ditto", template, l.bundle)
	}
	for _, l := range locations {
		t.Run(l.name, func(t *testing.T) {
			cmd := exec.Command(filepath.Join(l.bundle, "Contents", "MacOS", "LazyMindApp"), "30")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
			actor, err := update.IdentifyProcess(cmd.Process.Pid)
			if err != nil {
				t.Fatal(err)
			}
			got, err := unsignedSourceApplication(paths, system, actor)
			if l.allowed {
				if err != nil || got != l.bundle {
					t.Fatalf("running source %s resolved as %s: %v", l.bundle, got, err)
				}
			} else if err == nil {
				t.Fatalf("unrelated process got an install target %q", got)
			}
			actor.StartID++
			if got, err := unsignedSourceApplication(paths, system, actor); err == nil {
				t.Fatalf("different process incarnation got target %q", got)
			}
		})
	}
}

func inPlaceSourceConfig(t *testing.T) (update.Config, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	source := update.Config{Mode: "unsigned-test", Channel: "personal-test", TrustDomain: "inplace-417-" + strings.Repeat("a", 64), Audience: "fork-test", SigningIdentity: "adhoc", Platform: "darwin", Arch: runtime.GOARCH, Version: "0.5.229", ReleaseID: "fixture-source", UpdaterProtocol: 2, PolicyURL: "https://modelscope.cn/datasets/example/releases/resolve/master/updates/unsigned-test/in-place/round-417/policy.json", AllowedOrigins: []string{"https://modelscope.cn"}, PublicKeys: map[string]string{"fixture": base64.StdEncoding.EncodeToString(public)}}
	return source, private
}

// The update makes RAG available without installing it into the source runtime.
func inPlaceRAGCatalog(t *testing.T, c update.Config) []byte {
	t.Helper()
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	f, err := w.Create("fixture_rag/__init__.py")
	if err != nil {
		t.Fatal(err)
	}
	unpacked, err := io.WriteString(f, "__version__ = '1.0'\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	digest := func(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
	tag := digest([]byte("fixture-rag-1.0"))
	revision := digest([]byte(tag + "-" + c.Platform + "-" + c.Arch))
	base := strings.TrimSuffix(c.PolicyURL, "policy.json") + "RAG/" + tag + "/"
	rag := update.ComponentDescriptor{ID: "rag", Revision: revision, SHA256: digest(archive.Bytes()), Tag: tag, PythonABI: "cp311", BaseFingerprint: digest([]byte("fixture-base-cp311")), Platform: c.Platform, Arch: c.Arch, Packages: map[string]string{"fixture-rag": "1.0"},
		Filename: "rag.zip", URL: base + "rag.zip", CatalogURL: base + "catalog.json", SizeBytes: int64(archive.Len()), UnpackedBytes: int64(unpacked)}
	catalog, err := json.Marshal(map[string]any{"schemaVersion": 1, "platform": c.Platform, "arch": c.Arch, "components": map[string]update.ComponentDescriptor{"rag": rag}})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func inPlaceBundle(t *testing.T, root string, c update.Config, name, id string) string {
	t.Helper()
	app := filepath.Join(root, name+".app")
	write := func(rel string, raw []byte, mode os.FileMode) {
		t.Helper()
		file := filepath.Join(app, "Contents", rel)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, raw, mode); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.ReadFile("/bin/sleep")
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join("MacOS", name), exe, 0700)
	write("MacOS/LazyMindApp", exe, 0700)
	write("Info.plist", []byte(fmt.Sprintf(`<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>%s</string><key>CFBundleExecutable</key><string>%s</string><key>CFBundleShortVersionString</key><string>%s</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`, id, name, c.Version)), 0600)
	meta, err := json.Marshal(map[string]any{"updaterCacheDirName": "LazyMind-" + c.TrustDomain + "-" + c.Audience + "-" + c.Channel, "lazymind": map[string]any{"protocol": 2, "releaseId": c.ReleaseID, "version": c.Version, "mode": c.Mode, "platform": c.Platform, "arch": c.Arch, "signingIdentity": c.SigningIdentity}})
	if err != nil {
		t.Fatal(err)
	}
	write("Resources/app-update.yml", meta, 0600)
	write("Resources/release-witness", []byte(c.ReleaseID), 0600)
	write("Resources/runtime/config/python-components.json", inPlaceRAGCatalog(t, c), 0600)
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(app, "Contents", "Resources", "runtime", "bin", "lazymind-update-helper")
	if err = os.MkdirAll(filepath.Dir(helper), 0700); err != nil {
		t.Fatal(err)
	}
	runInPlaceFixture(t, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-tags", "lazymind_unsigned_test", "-ldflags", "-X main.embeddedConfig="+base64.StdEncoding.EncodeToString(raw), "-o", helper, "../update-helper")
	runInPlaceFixture(t, "/usr/bin/codesign", "--force", "--deep", "--sign", "-", app)
	return app
}
func runInPlaceFixture(t *testing.T, cmd string, args ...string) {
	t.Helper()
	if out, err := exec.CommandContext(t.Context(), cmd, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v %s", filepath.Base(cmd), err, out)
	}
}
