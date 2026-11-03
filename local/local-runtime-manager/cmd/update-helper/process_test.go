package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// This test-only child calls the actual command entrypoint. Only the OS config
// directory lookup, fixture TLS trust and compiled build value are supplied by the harness; no
// production argument or environment variable can replace either authority.
func TestUpdateHelperSubprocess(t *testing.T) {
	if os.Getenv("LAZYMIND_UPDATE_UNIT_CHILD") != "1" {
		return
	}
	if encoded := os.Getenv("LAZYMIND_UPDATE_UNIT_TLS_CERT"); encoded != "" {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			os.Exit(91)
		}
		cert, err := x509.ParseCertificate(raw)
		if err != nil {
			os.Exit(92)
		}
		roots := x509.NewCertPool()
		roots.AddCert(cert)
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		http.DefaultTransport = transport
	}
	embeddedConfig = os.Getenv("LAZYMIND_UPDATE_UNIT_CONFIG")
	resolveRecoveryBase = func() (string, error) { return os.Getenv("LAZYMIND_UPDATE_UNIT_ROOT"), nil }
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("LAZYMIND_UPDATE_UNIT_ARGS")), &args); err != nil {
		os.Exit(90)
	}
	os.Args = append([]string{"update-helper"}, args...)
	main()
	os.Exit(0)
}

type helperProcessFixture struct {
	tlsCertificate []byte
	config         desktopupdate.Config
	private        ed25519.PrivateKey
	root           string
	now            time.Time
}

func newHelperProcessFixture(t *testing.T) *helperProcessFixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &helperProcessFixture{config: desktopupdate.Config{TrustDomain: "process-fixture", Audience: "official", Channel: "stable", Mode: "official", ReleaseID: "current", Version: "1.0.0", Platform: "darwin", Arch: "arm64", SigningIdentity: "FIXTURE_TEAM", UpdaterProtocol: 1, PolicyURL: "https://updates.example.invalid/policy.json", AllowedOrigins: []string{"https://updates.example.invalid", "https://mirror.example.invalid"}, PublicKeys: map[string]string{"key": base64.StdEncoding.EncodeToString(pub)}}, private: priv, root: t.TempDir(), now: time.Now().UTC()}
}

func (f *helperProcessFixture) request(sequence int) []byte {
	artifact := map[string]any{"url": "https://updates.example.invalid/releases/current/app.zip", "sha256": strings.Repeat("a", 64), "size": 1024}
	policy := map[string]any{"schemaVersion": 1, "minUpdaterProtocol": 1, "trustDomain": f.config.TrustDomain, "audience": f.config.Audience, "channel": f.config.Channel, "policySequence": sequence, "issuedAt": f.now.Add(-time.Minute).Format(time.RFC3339), "expiresAt": f.now.Add(time.Hour).Format(time.RFC3339), "releases": []any{map[string]any{"releaseId": "current", "version": "1.0.0", "platform": "darwin", "arch": "arm64", "minimumSystemVersion": "12.0.0", "minUpdaterProtocol": 1, "updaterProtocol": 1, "signingIdentity": "FIXTURE_TEAM", "package": artifact, "feed": artifact, "components": artifact}}, "edges": []any{}, "revoked": []string{}}
	payload, _ := json.Marshal(policy)
	envelope := map[string]string{"keyId": "key", "payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(f.private, payload))}
	raw, _ := json.Marshal(map[string]any{"envelope": envelope, "state": map[string]any{"systemVersion": "14.0.0", "dataProfile": "known-v1", "components": map[string]string{}}})
	return raw
}

func (f *helperProcessFixture) command(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	config, _ := json.Marshal(f.config)
	encodedArgs, _ := json.Marshal(args)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUpdateHelperSubprocess$")
	cmd.Env = append(os.Environ(), "LAZYMIND_UPDATE_UNIT_CHILD=1", "LAZYMIND_UPDATE_UNIT_CONFIG="+base64.StdEncoding.EncodeToString(config), "LAZYMIND_UPDATE_UNIT_ROOT="+f.root, "LAZYMIND_UPDATE_UNIT_ARGS="+string(encodedArgs), "LAZYMIND_DESKTOP_UPDATE_CONFIG=untrusted-runtime-override")
	cmd.Env = append(cmd.Env, "LAZYMIND_UPDATE_UNIT_TLS_CERT="+base64.StdEncoding.EncodeToString(f.tlsCertificate))
	return cmd
}

func (f *helperProcessFixture) check(t *testing.T, sequence int) (desktopupdate.Decision, error) {
	t.Helper()
	cmd := f.command(t, "check")
	cmd.Stdin = bytes.NewReader(f.request(sequence))
	raw, err := cmd.Output()
	if err != nil {
		return desktopupdate.Decision{}, err
	}
	var decision desktopupdate.Decision
	err = json.Unmarshal(raw, &decision)
	return decision, err
}

func TestActualEntrypointPersistsAcrossExitAndMirrorChange(t *testing.T) {
	f := newHelperProcessFixture(t)
	if d, err := f.check(t, 42); err != nil || d.Status != "up_to_date" || d.PolicySequence != 42 {
		t.Fatalf("actual entrypoint: %+v err=%v", d, err)
	}
	if _, err := f.check(t, 41); err == nil {
		t.Fatal("new helper process accepted replay")
	}
	f.config.PolicyURL = "https://mirror.example.invalid/policy.json"
	if _, err := f.check(t, 41); err == nil {
		t.Fatal("mirror switch opened a new empty trust partition")
	}
	if d, err := f.check(t, 42); err != nil || d.PolicySequence != 42 {
		t.Fatalf("mirror lost existing trusted policy: %+v err=%v", d, err)
	}
	if err := f.command(t, "check", "--trust-root", "/tmp/alternate").Run(); err == nil {
		t.Fatal("actual main accepted runtime root override")
	}
}

func TestSeparateHelpersCompeteWithoutLosingHighestSequence(t *testing.T) {
	f := newHelperProcessFixture(t)
	if _, err := f.check(t, 42); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	highestResult := make(chan error, 1)
	// Both helpers are started before either receives the request. All writable
	// state is under one isolated root, so per-process mutexes are insufficient.
	var releases []func()
	for _, seq := range []int{43, 44} {
		sequence := seq // Preserve per-process input with the module's Go 1.21 loop semantics.
		cmd := f.command(t, "check")
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		releases = append(releases, func() {
			go func() {
				defer wg.Done()
				_, writeErr := stdin.Write(f.request(sequence))
				_ = stdin.Close()
				err := cmd.Wait()
				if sequence == 44 {
					if err == nil {
						err = writeErr
					}
					if err == nil {
						var d desktopupdate.Decision
						err = json.Unmarshal(stdout.Bytes(), &d)
						if err == nil && d.PolicySequence != 44 {
							err = errors.New("wrong accepted sequence")
						}
					}
					highestResult <- err
				}
			}()
		})
	}
	for _, release := range releases {
		release()
	}
	wg.Wait()
	if err := <-highestResult; err != nil {
		t.Fatalf("highest process failed: %v", err)
	}
	if _, err := f.check(t, 43); err == nil {
		t.Fatal("concurrent processes lost highest sequence")
	}
	if d, err := f.check(t, 44); err != nil || d.PolicySequence != 44 {
		t.Fatalf("highest sequence not persisted: %+v err=%v", d, err)
	}
}
