package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestBootstrapCompleteUsesCanonicalNativeAcknowledgement(t *testing.T) {
	for _, active := range []int{0, 1} {
		t.Run(strconv.Itoa(active), func(t *testing.T) {
			f := actualUpdateFormatFixture(t, 2)
			root := t.TempDir()
			old := resolveRuntimeRecoveryRoot
			resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
			t.Cleanup(func() { resolveRuntimeRecoveryRoot = old })
			tx := strings.Repeat("e", 32)
			peers := bootstrapPeerNames(f.cfg)
			var released atomic.Int64
			for _, peer := range peers {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request struct{ Action, TransactionID, Owner string }
					if json.NewDecoder(r.Body).Decode(&request) != nil || request.TransactionID != tx || request.Owner != f.cfg.OwnerToken {
						t.Error("invalid peer identity request")
						w.WriteHeader(400)
						return
					}
					sealed := request.Action != "bootstrap-complete"
					if !sealed {
						released.Add(1)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"active": active, "bootstrap": map[string]any{"transactionId": tx, "owner": f.cfg.OwnerToken, "sealed": sealed}})
				}))
				t.Cleanup(server.Close)
				port := server.Listener.Addr().(*net.TCPAddr).Port
				if peer == "core" {
					f.cfg.LocalProxy.CoreHostPort = port
				} else {
					f.cfg.LocalProxy.ScanHostPort = port
				}
			}
			// This replaces only the installed signature provider. The legal Mac
			// response also works on Linux CI; no native host fixture is required.
			config := desktopupdate.Config{TrustDomain: "fixture", Audience: "official", Channel: "stable", Mode: "official", ReleaseID: "target", Version: "1.1.0", Platform: "darwin", Arch: "arm64", SigningIdentity: "FIXTURE_TEAM", UpdaterProtocol: 2, PolicyURL: "https://updates.example.invalid/policy.json", AllowedOrigins: []string{"https://updates.example.invalid"}, PublicKeys: map[string]string{"fixture": base64.StdEncoding.EncodeToString(make([]byte, 32))}}
			raw, _ := json.Marshal(map[string]any{"status": "enabled", "config": config})
			helper := filepath.Join(f.paths.ResourcesRoot, "bin", executableName("lazymind-update-helper"))
			if err := os.MkdirAll(filepath.Dir(helper), 0700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "provider.go")
			if err := os.WriteFile(source, []byte("package main\nimport \"os\"\nfunc main(){os.Stdout.Write([]byte("+strconv.Quote(string(raw))+"))}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", helper, source).CombinedOutput(); err != nil {
				t.Fatalf("provider build: %v %s", err, output)
			}
			profile := f.measure(t).DataProfile
			scope, err := runtimeUpdateScope(f.cfg, f.paths)
			if err != nil {
				t.Fatal(err)
			}
			record := desktopupdate.NativeRecord{Version: 2, TransactionID: tx, Phase: "activated", SourceReleaseID: "source", SourceVersion: "1.0.0", TargetReleaseID: "target", TargetVersion: "1.1.0", TrustDomain: config.TrustDomain, Audience: config.Audience, Channel: config.Channel, SigningIdentity: config.SigningIdentity, Platform: config.Platform, Arch: config.Arch, RuntimeRoot: f.paths.RuntimeRoot, InstallationRoot: f.paths.ResourcesRoot, Scope: scope, SourceDataProfile: profile, TargetDataProfile: profile, SourceStorage: desktopupdate.NativeStorage{Version: 2, Databases: cloneUpdateStorage(f.cfg.UpdateStorage).Databases, SegmentStore: "SQLiteStore", VectorEngine: "milvus-lite", VectorPath: f.cfg.ModeProfile.VectorStore.DBPath}, ComponentMode: "absent", ExpectedPeers: peers, PackagePath: filepath.Join(root, "package.zip"), PackageSHA256: strings.Repeat("a", 64), PackageSize: 1, ComponentsPath: filepath.Join(root, "components.json"), ComponentsSHA256: strings.Repeat("b", 64), ComponentsSize: 1, PolicyDigest: strings.Repeat("c", 64), PolicySequence: 1, Owner: f.cfg.OwnerToken}
			for _, path := range []string{record.PackagePath, record.ComponentsPath} {
				if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			encoded, _ := json.Marshal(record)
			var fixture map[string]any
			_ = json.Unmarshal(encoded, &fixture)
			saveBootstrapRecord(t, root, fixture)
			state := defaultRuntimeState(f.cfg, f.cfg.ProcessComposePort, f.paths.RunDirTokenFile)
			state.OverallStatus = "ready"
			state.Config.UpdateBootstrap = &RuntimeUpdateBootstrap{TransactionID: tx, Owner: f.cfg.OwnerToken, ReleaseID: "target", DataProfile: profile, Storage: cloneUpdateStorage(f.cfg.UpdateStorage), ExpectedPeers: peers}
			if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			cli := NewCLI(&out, &out, &fakeRunner{t: t}, "fixture-manager")
			err = cli.Run(t.Context(), []string{"update-bootstrap", "--action", "complete", "--transaction-id", tx, "--profile", "desktop", "--owner-token", f.cfg.OwnerToken, "--repo-root", f.cfg.RepoRoot, "--resources-root", f.paths.ResourcesRoot, "--runtime-root", f.paths.RuntimeRoot})
			var result struct{ Status, Reason string }
			if err != nil || json.Unmarshal(out.Bytes(), &result) != nil {
				t.Fatalf("complete entry: %v %s", err, out.String())
			}
			remaining, readErr := desktopupdate.ReadNativeRecord(root)
			if active == 0 {
				if result.Status != "completed" || readErr != nil || remaining != nil || released.Load() != int64(len(peers)) {
					t.Fatalf("valid real CLI completion rejected: %+v remaining=%v read=%v released=%d", result, remaining != nil, readErr, released.Load())
				}
				committed, err := readRuntimeState(f.paths.StateFile)
				if err != nil || committed.Config.UpdateBootstrap != nil || committed.Config.NativeCommittedTx != tx {
					t.Fatalf("completion receipt missing: %v", err)
				}
			} else if result.Status != "blocked" || result.Reason != "update_startup_failed" || remaining == nil || released.Load() != 0 {
				t.Fatalf("active peer was accepted: %+v remaining=%v released=%d", result, remaining != nil, released.Load())
			}
		})
	}
}
