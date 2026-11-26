package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// Run the actual v2 client commands on a private TLS publication. These bytes
// exercise discovery, transfer and cache integrity; native installation is a
// separate smoke against real signed NSIS/macOS packages.
func TestPublishedV2ClientDiscoversAndCachesB(t *testing.T) {
	for _, platform := range []string{"windows", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			f := newHelperProcessFixture(t)
			f.config.UpdaterProtocol, f.config.Platform = 2, platform
			if platform == "windows" {
				f.config.Arch = "amd64"
			}
			files := map[string][]byte{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if raw, ok := files[r.URL.Path]; ok {
					w.Write(raw)
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			f.config.PolicyURL, f.config.AllowedOrigins = server.URL+"/policy.json", []string{server.URL}
			f.tlsCertificate = server.Certificate().Raw
			artifact := func(id, kind string) update.Artifact {
				path := "/releases/" + id + "/" + kind
				raw := []byte(id + " " + kind + " TLS transfer fixture")
				files[path] = raw
				sum := sha256.Sum256(raw)
				return update.Artifact{URL: server.URL + path, Size: int64(len(raw)), SHA256: hex.EncodeToString(sum[:])}
			}
			release := func(id, version string) update.Release {
				ext := ".zip"
				if platform == "windows" {
					ext = ".exe"
				}
				pkg := artifact(id, "package"+ext)
				pkg.UnpackedBytes = 4096
				return update.Release{ID: id, Version: version, Platform: platform, Arch: f.config.Arch,
					MinimumSystemVersion: "1.0.0", MinUpdaterProtocol: 2, UpdaterProtocol: 2, SigningIdentity: f.config.SigningIdentity,
					Package: pkg, Feed: artifact(id, "feed.json"), Components: artifact(id, "components.json")}
			}
			a, b := release(f.config.ReleaseID, f.config.Version), release("next-"+platform, "1.1.0")
			profile := "format-v2-" + strings.Repeat("a", 64)
			class := update.ComponentSourceClass{Tag: strings.Repeat("a", 64), BaseFingerprint: strings.Repeat("b", 64),
				PythonABI: "cp311", Packages: map[string]string{"fixture": "1"}}
			policy := update.Policy{SchemaVersion: 2, MinUpdaterProtocol: 2, Sequence: 2,
				TrustDomain: f.config.TrustDomain, Audience: f.config.Audience, Channel: f.config.Channel,
				IssuedAt: f.now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: f.now.Add(time.Hour).Format(time.RFC3339),
				Releases: []update.Release{a, b}, Edges: []update.Edge{{SourceID: a.ID, TargetID: b.ID, MigrationProfile: "format-v2",
					SourceDataProfiles: []string{profile}, TargetDataProfile: profile,
					ComponentTransitions: map[string]update.ComponentTransition{"rag": {SourceClasses: []update.ComponentSourceClass{class}, TargetComponentsSHA256: b.Components.SHA256}}}}}
			envelope, err := update.SignPublication(policy, f.config, "key", f.private, f.now)
			if err != nil {
				t.Fatal(err)
			}
			files["/policy.json"] = envelope
			state := update.LocalState{SystemVersion: "99.0.0", DataProfile: profile, Components: map[string]string{}}
			input, _ := json.Marshal(map[string]any{"state": state})
			check := f.command(t, "discover")
			check.Stdin = bytes.NewReader(input)
			out, err := check.Output()
			var decision update.Decision
			if err != nil || json.Unmarshal(out, &decision) != nil || decision.Status != "direct" || decision.Next == nil || decision.Next.ID != b.ID || decision.Next.Package != b.Package {
				t.Fatalf("A's live discover did not offer the signed B: %v %s", err, out)
			}
			cmd := f.command(t, "download")
			in, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			go func() { _ = json.NewEncoder(in).Encode(map[string]any{"state": state, "releaseId": b.ID}) }()
			out, err = cmd.Output()
			lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
			var received update.DownloadReceipt
			if err != nil || len(lines) < 2 || json.Unmarshal(lines[len(lines)-1], &received) != nil || received.Status != "downloaded" || received.ReleaseID != b.ID || received.PolicyDigest != decision.PolicyDigest {
				t.Fatalf("A did not commit the downloaded B: %v %s", err, out)
			}
			for file, expected := range map[string]update.Artifact{received.PackagePath: b.Package, received.ComponentsPath: b.Components} {
				raw, err := os.ReadFile(file)
				relative, _ := filepath.Rel(f.root, file)
				if err != nil || !filepath.IsLocal(relative) || int64(len(raw)) != expected.Size || fmt.Sprintf("%x", sha256.Sum256(raw)) != expected.SHA256 {
					t.Fatalf("wrong verified cache file %s: %v", file, err)
				}
			}
		})
	}
}
