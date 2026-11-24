package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

type handoffOwnerResult struct {
	Status, ReleaseID, AttemptID, DataProfile, Reason, Error string
	ExitedActor                                              *update.ProcessIdentity
}

// Reuse the actual CLI child. The process issuing each command stands in for
// Electron; kill/exit and PID start identity are observed by the OS.
func handoffOwnerCommand(ctx context.Context, input admissionChildInput, ownerMode string) handoffOwnerResult {
	raw, err := json.Marshal(input)
	if err != nil {
		return handoffOwnerResult{Error: err.Error()}
	}
	child := "TestRuntimeAdmissionEntryChild"
	if ownerMode != "" {
		child = "TestV1DepartingSourceOwner"
	}
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+child+"$")
	cmd.Env = append(os.Environ(), "LAZYMIND_TEST_V1_DEPART="+ownerMode, "LAZYMIND_UNIT_ADMISSION_CHILD=v1", "LAZYMIND_UNIT_ADMISSION_INPUT="+string(raw))
	out, runErr := cmd.Output()
	var result handoffOwnerResult
	if err := json.Unmarshal(out, &result); err != nil {
		return handoffOwnerResult{Error: fmt.Sprintf("CLI output: %v / %v: %q", runErr, err, out)}
	}
	return result
}

// This Electron stand-in owns both CLI calls and exits before the replacement
// owner starts. The final call must use the attempt returned by its own prepare.
func TestV1DepartingSourceOwner(t *testing.T) {
	mode := os.Getenv("LAZYMIND_TEST_V1_DEPART")
	if mode == "" {
		return
	}
	var request admissionChildInput
	requireOwnerFixture(t, json.Unmarshal([]byte(os.Getenv("LAZYMIND_UNIT_ADMISSION_INPUT")), &request))
	actor, err := update.CurrentProcessIdentity()
	requireOwnerFixture(t, err)
	result := handoffOwnerCommand(t.Context(), request, "")
	if mode == "dispatch-and-exit" && result.Status == "prepared" && result.AttemptID != "" {
		id := result.AttemptID
		for i, arg := range request.Args {
			if arg == "--prepare-handoff" {
				request.Args[i] = "--handoff"
			}
		}
		request.Args = append(request.Args, "--attempt-id", id)
		result = handoffOwnerCommand(t.Context(), request, "")
		result.AttemptID = id
	}
	result.ExitedActor = &actor
	requireOwnerFixture(t, json.NewEncoder(os.Stdout).Encode(result))
	os.Exit(0)
}

func requireOwnerFixture(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestV1PreparedHandoffOwnerTransitions(t *testing.T) {
	// The first approved v1 test covers the complete download and installed
	// target. Here only the OS signing provider is replaced; each case still
	// has a real signed policy, accepted ledger, source selection and CLI.
	helper := []byte("#!/bin/sh\n[ \"$1\" = verify-identity ] || exit 2\ncat \"$0.json\"\n")
	if runtime.GOOS == "windows" {
		dir := t.TempDir()
		source := filepath.Join(dir, "identity.go")
		binary := filepath.Join(dir, "identity.exe")
		code := `package main
import "os"
func main(){if len(os.Args)!=2 || os.Args[1]!="verify-identity"{os.Exit(2)};f,_:=os.Executable();b,e:=os.ReadFile(f+".json");if e!=nil{panic(e)};os.Stdout.Write(b)}`
		requireOwnerFixture(t, os.WriteFile(source, []byte(code), 0600))
		if out, err := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, source).CombinedOutput(); err != nil {
			t.Fatalf("%s %v", out, err)
		}
		var err error
		helper, err = os.ReadFile(binary)
		requireOwnerFixture(t, err)
	}
	for _, scenario := range []string{"dead can retry", "bad package can cancel", "revoked can cancel", "live owner", "unknown owner", "wrong scope", "dispatched", "direct dispatched", "unphased legacy", "prepared does not authorize target"} {
		t.Run(scenario, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			root := t.TempDir()
			oldRoot := resolveRuntimeRecoveryRoot
			resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
			t.Cleanup(func() { resolveRuntimeRecoveryRoot = oldRoot })
			installBootstrapFixtureRAG(t, f)
			profile := bootstrapPolicyLocalState(f.measure(t))
			catalogFile := filepath.Join(f.paths.ResourcesRoot, "config", "python-components.json")
			catalog, err := os.ReadFile(catalogFile)
			requireOwnerFixture(t, err)
			pkg := []byte("owner-transition package")
			manifest := []byte(`{"profile":"desktop","version":1}`)
			write := func(file string, raw []byte, mode os.FileMode) {
				t.Helper()
				requireOwnerFixture(t, os.MkdirAll(filepath.Dir(file), 0700))
				requireOwnerFixture(t, os.WriteFile(file, raw, mode))
			}
			pkgFile := filepath.Join(t.TempDir(), "package.zip")
			write(pkgFile, pkg, 0600)
			write(filepath.Join(f.paths.ResourcesRoot, "manifest.json"), manifest, 0600)
			pub, private, err := ed25519.GenerateKey(rand.Reader)
			requireOwnerFixture(t, err)
			platform := runtime.GOOS
			if platform == "linux" {
				platform = "darwin"
			}
			const origin = "https://updates.example.invalid"
			cfg := update.Config{TrustDomain: "owner-fixture", Audience: "official", Channel: "stable", Mode: "official", ReleaseID: "a", Version: "1.0.0", Platform: platform, Arch: runtime.GOARCH, SigningIdentity: "team", UpdaterProtocol: 1,
				PolicyURL: origin + "/policy.json", AllowedOrigins: []string{origin}, PublicKeys: map[string]string{"fixture": base64.StdEncoding.EncodeToString(pub)}}
			helperFile := filepath.Join(f.paths.ResourcesRoot, "bin", executableName("lazymind-update-helper"))
			write(helperFile, helper, 0700)
			bind := func(c update.Config) {
				raw, e := json.Marshal(map[string]any{"status": "enabled", "config": c})
				if e != nil {
					t.Fatal(e)
				}
				write(helperFile+".json", raw, 0600)
			}
			bind(cfg)
			t.Setenv(desktopAppVersionEnvVar, cfg.Version)
			artifact := func(name string, data []byte) update.Artifact {
				return update.Artifact{URL: origin + "/" + name, Size: int64(len(data)), SHA256: hexDigest(data)}
			}
			a := update.Release{ID: "a", Version: cfg.Version, Platform: cfg.Platform, Arch: cfg.Arch, SigningIdentity: cfg.SigningIdentity, UpdaterProtocol: 1, MinUpdaterProtocol: 1, MinimumSystemVersion: "1.0.0", Package: artifact("old", manifest), Feed: artifact("feed", []byte("feed")), Components: artifact("catalog", catalog)}
			b := a
			b.ID, b.Version, b.Package = "b", "1.1.0", artifact("package", pkg)
			edge := update.Edge{SourceID: a.ID, TargetID: b.ID, MigrationProfile: "same-v1", SourceDataProfiles: []string{profile.DataProfile}, TargetDataProfile: profile.DataProfile, RequiredComponents: profile.Components,
				Recovery: update.RecoveryContract{Version: 1, SourcePackageSHA256: a.Package.SHA256, SourceManifestSHA256: hexDigest(manifest), HelperProtocolMin: 1, HelperProtocolMax: 1, Direction: "target-to-source"}}
			now := time.Now().UTC()
			policy := update.Policy{SchemaVersion: 1, MinUpdaterProtocol: 1, Sequence: 1, TrustDomain: cfg.TrustDomain, Audience: cfg.Audience, Channel: cfg.Channel, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), Releases: []update.Release{a, b}, Edges: []update.Edge{edge}}
			accept := func() {
				t.Helper()
				raw, e := update.SignPublication(policy, cfg, "fixture", private, now)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = update.Check(t.Context(), raw, cfg, profile, root, false, now); e != nil {
					t.Fatal(e)
				}
			}
			accept()
			base := []string{"--components", catalogFile, "--sha256", b.Components.SHA256, "--size", strconv.Itoa(len(catalog)), "--package", pkgFile}
			request := func(owner string, args ...string) admissionChildInput {
				common := []string{"update-components-check", "--profile", "desktop", "--owner-token", owner, "--runtime-root", f.paths.RuntimeRoot, "--repo-root", f.cfg.RepoRoot, "--resources-root", f.paths.ResourcesRoot}
				return admissionChildInput{Root: root, Args: append(common, args...)}
			}
			attempt := func(owner, verb, id string) admissionChildInput {
				args := append([]string{verb, b.ID}, base...)
				if id != "" {
					args = append(args, "--attempt-id", id)
				}
				return request(owner, args...)
			}
			oldToken := f.cfg.OwnerToken
			call := func(r admissionChildInput) handoffOwnerResult { return handoffOwnerCommand(t.Context(), r, "") }
			preflight := call(request(oldToken, base[:len(base)-2]...))
			if preflight.Status != "compatible" || preflight.DataProfile != profile.DataProfile {
				t.Fatalf("existing source preflight: %+v", preflight)
			}
			exitOwner := scenario != "live owner"
			verb := "--prepare-handoff"
			mode := ""
			if exitOwner {
				mode = "exit"
			}
			if scenario == "dispatched" {
				mode = "dispatch-and-exit"
			}
			if scenario == "direct dispatched" || scenario == "unphased legacy" {
				verb = "--handoff"
			}
			finalized := scenario == "dispatched" || verb == "--handoff"
			before := handoffOwnerCommand(t.Context(), attempt(oldToken, verb, ""), mode)
			if exitOwner {
				if before.ExitedActor == nil {
					t.Fatalf("owner process did not report its observed identity: %+v", before)
				}
				alive, e := update.SameProcess(*before.ExitedActor)
				requireOwnerFixture(t, e)
				if alive {
					t.Fatal("previous Electron process is still alive")
				}
			}
			id := before.AttemptID
			if finalized {
				if before.Status != "compatible" {
					t.Fatalf("legacy direct commit failed: %+v", before)
				}
			} else if before.Status != "prepared" || before.ReleaseID != b.ID || before.DataProfile != profile.DataProfile || id == "" {
				t.Fatalf("source did not persist a verified prepared attempt: %+v", before)
			}
			if scenario == "prepared does not authorize target" {
				target := cfg
				target.ReleaseID, target.Version = b.ID, b.Version
				bind(target)
				t.Setenv(desktopAppVersionEnvVar, b.Version)
				write(filepath.Join(f.paths.StateDir, "rag-component-app-version"), []byte(cfg.Version+"\n"), 0600)
				if err := prepareDesktopPythonComponents(t.Context(), f.cfg, f.paths); err == nil && len(pythonComponentSites(f.paths)) != 0 {
					t.Fatal("manual target consumed preparation without dispatch authority")
				}
				return
			}
			filename := filepath.Join(f.paths.StateDir, "v1-component-handoff.json")
			if !finalized {
				raw, err := os.ReadFile(filename)
				requireOwnerFixture(t, err)
				var wire struct{ SourceActor *update.ProcessIdentity }
				requireOwnerFixture(t, json.Unmarshal(raw, &wire))
				if wire.SourceActor == nil {
					t.Fatal("source receipt has no authenticated process identity")
				}
				alive, err := update.SameProcess(*wire.SourceActor)
				requireOwnerFixture(t, err)
				if alive == exitOwner {
					t.Fatalf("invalid fixture: actual old owner alive=%v exitOwner=%v", alive, exitOwner)
				}
			}
			state, err := readRuntimeState(f.paths.StateFile)
			requireOwnerFixture(t, err)
			const newToken = "replacement-electron-owner"
			state.OwnerToken = newToken
			requireOwnerFixture(t, writeRuntimeState(f.paths.StateFile, state))
			if scenario == "unknown owner" || scenario == "unphased legacy" {
				raw, e := os.ReadFile(filename)
				if e != nil {
					t.Fatal(e)
				}
				var data map[string]json.RawMessage
				if e = json.Unmarshal(raw, &data); e != nil {
					t.Fatal(e)
				}
				legacy := map[string]bool{}
				for _, k := range []string{"SourceID", "SourceVersion", "TargetID", "TargetVersion", "Installation", "Runtime", "SourceOwner", "DataProfile", "PolicyURL", "EdgeDigest", "SelectionDigest", "markerChanges"} {
					legacy[strings.ToLower(k)] = true
				}
				removed := false
				for k := range data {
					if scenario == "unphased legacy" && !legacy[strings.ToLower(k)] || scenario == "unknown owner" && strings.EqualFold(k, "sourceActor") {
						delete(data, k)
						removed = true
					}
				}
				if !removed && scenario != "unphased legacy" {
					t.Fatal("fixture did not remove lifecycle evidence")
				}
				edited, e := json.Marshal(data)
				if e != nil {
					t.Fatal(e)
				}
				write(filename, edited, 0600)
			}
			if scenario == "dispatched" || scenario == "direct dispatched" {
				raw, e := os.ReadFile(filename)
				requireOwnerFixture(t, e)
				var wire struct{ Phase string }
				requireOwnerFixture(t, json.Unmarshal(raw, &wire))
				if wire.Phase == "" || wire.Phase == "prepared" {
					t.Fatal("direct handoff did not leave an irreversible dispatch fence")
				}
			}
			if scenario == "wrong scope" {
				other := t.TempDir()
				requireOwnerFixture(t, os.CopyFS(other, os.DirFS(f.paths.ResourcesRoot)))
				f.paths.ResourcesRoot = other
			}
			if scenario == "bad package can cancel" {
				write(pkgFile, []byte("corrupt"), 0600)
			}
			if scenario == "revoked can cancel" {
				policy.Sequence++
				policy.Revoked = []string{b.ID}
				accept()
			}

			discard := func(owner, token string) admissionChildInput {
				return request(owner, "--discard-handoff", b.ID, "--attempt-id", token)
			}
			snapshot := func() []byte {
				raw, e := os.ReadFile(filename)
				if os.IsNotExist(e) {
					return nil
				}
				requireOwnerFixture(t, e)
				return raw
			}
			readOnlyCall := func(req admissionChildInput) handoffOwnerResult {
				t.Helper()
				prior := snapshot()
				got := call(req)
				if !bytes.Equal(prior, snapshot()) {
					t.Fatalf("operation changed the receipt: %v", req.Args)
				}
				return got
			}
			blocked := func(req admissionChildInput) {
				t.Helper()
				got := readOnlyCall(req)
				if got.Status != "blocked" || got.Reason == "" {
					t.Fatalf("wanted an explicit rejection: %+v", got)
				}
			}
			inspect := request(newToken, "--inspect-handoff")
			switch scenario {
			case "dead can retry":
				prepared := readOnlyCall(inspect)
				if prepared.Status != "prepared" || prepared.AttemptID != id || prepared.ReleaseID != b.ID {
					t.Fatalf("could not recover prepared evidence: %+v", prepared)
				}
				rebound := call(attempt(newToken, "--prepare-handoff", id))
				if rebound.Status != "prepared" || rebound.AttemptID == id || rebound.AttemptID == "" {
					t.Fatalf("replacement owner did not acquire its own attempt: %+v", rebound)
				}
				blocked(attempt(oldToken, "--handoff", id))
				blocked(attempt(newToken, "--handoff", id))
				blocked(discard(newToken, id))
				if got := call(attempt(newToken, "--handoff", rebound.AttemptID)); got.Status != "compatible" {
					t.Fatalf("fresh owner could not commit: %+v", got)
				}
				var fence struct{ Phase string }
				requireOwnerFixture(t, json.Unmarshal(snapshot(), &fence))
				if fence.Phase == "" || fence.Phase == "prepared" {
					t.Fatal("final authorization returned without persisting the irreversible boundary")
				}
			case "bad package can cancel", "revoked can cancel":
				prepared := readOnlyCall(inspect)
				if prepared.Status != "prepared" || prepared.AttemptID != id {
					t.Fatalf("pre-dispatch cancellation became unavailable: %+v", prepared)
				}
				blocked(attempt(newToken, "--prepare-handoff", id))
				if got := call(discard(newToken, id)); got.Status != "compatible" {
					t.Fatalf("could not explicitly cancel pre-dispatch attempt: %+v", got)
				}
				if got := readOnlyCall(inspect); got.Status != "absent" {
					t.Fatalf("canceled attempt survived: %+v", got)
				}
				blocked(attempt(oldToken, "--handoff", id))
				blocked(attempt(newToken, "--handoff", id))
			default:
				blocked(inspect)
				blocked(attempt(newToken, "--prepare-handoff", id))
				blocked(attempt(newToken, "--handoff", id))
				blocked(discard(newToken, id))
				if scenario == "unphased legacy" {
					blocked(request(newToken, "--discard-handoff", b.ID))
				}
			}
		})
	}
}
