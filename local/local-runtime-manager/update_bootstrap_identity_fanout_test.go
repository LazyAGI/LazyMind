package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
	"gopkg.in/yaml.v3"
)

const fanoutChildRoot = "LAZYMIND_TEST_FANOUT_RECOVERY_ROOT"

// Copies of this test executable stand in for the packaged programs. Only
// the outer supervisor and the OS signature provider are fixtures; the shell
// trampoline, individual internal CLIs and all of their admission checks run.
func init() {
	root := os.Getenv(fanoutChildRoot)
	if root == "" || len(os.Args) < 2 {
		return
	}
	var err error
	switch os.Args[1] {
	case "--config":
		err = serveFanoutCompose(root, os.Args[2])
	case "verify-identity":
		executable, e := os.Executable()
		if e != nil {
			err = e
			break
		}
		helper, e := os.ReadFile(executable)
		if e != nil {
			err = e
			break
		}
		app, e := os.ReadFile(os.Getenv("LAZYMIND_TEST_FANOUT_APP"))
		if e != nil {
			err = e
			break
		}
		subject := hexDigest(app) + "/" + hexDigest(helper)
		if _, e := os.Stat(filepath.Join(root, "defer-provider-output")); e == nil {
			// Keep the output pipe open after the original helper exits. Windows
			// can then replace the executable while its old result is in transit.
			cmd := exec.Command(os.Getenv("LAZYMIND_TEST_FANOUT_MANAGER"), "fanout-provider-output", subject, strconv.Itoa(os.Getpid()))
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			if err = cmd.Start(); err == nil {
				err = cmd.Process.Release()
			}
		} else {
			err = fanoutProviderOutput(subject, strconv.Itoa(os.Getpid()))
		}
	case "fanout-provider-output":
		err = fanoutProviderOutput(os.Args[2], os.Args[3])
	case "up":
		resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		cli := NewCLI(io.Discard, os.Stderr, &ExecRunner{}, os.Args[0])
		cli.newManager = func(r CommandRunner, exe string) *RuntimeManager {
			m := NewRuntimeManager(r, exe)
			m.processScanner = func(RuntimePaths) ([]LocalProcessRecord, error) { return nil, nil }
			m.relocatePythonVenvs = func(RuntimeConfig, RuntimePaths) error { return nil }
			// No product services beyond the admission boundary are installed.
			m.probeAPI = func(int, time.Duration) bool {
				if _, err := os.Stat(filepath.Join(root, "compose-started")); os.IsNotExist(err) {
					return false
				}
				<-ctx.Done()
				return false
			}
			return m
		}
		err = cli.Run(ctx, os.Args[1:])
		cancel()
	case "internal", "shell":
		resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
		deadline, e := time.ParseDuration(os.Getenv("LAZYMIND_TEST_FANOUT_TIMEOUT"))
		if e != nil {
			err = e
			break
		}
		// The service owns its deadline inside the Windows shell trampoline.
		if os.Args[1] == "shell" {
			deadline = 2 * time.Minute
		}
		bounded, cancel := context.WithTimeout(context.Background(), deadline)
		var ctx context.Context = bounded
		if os.Args[1] == "internal" {
			fmt.Println("entering CLI")
			ctx = &fanoutCallerContext{Context: bounded}
		}
		cli := NewCLI(os.Stdout, os.Stderr, &ExecRunner{}, os.Args[0])
		err = cli.Run(ctx, os.Args[1:])
		if os.Args[1] == "internal" && errors.Is(bounded.Err(), context.DeadlineExceeded) {
			fmt.Println("caller deadline expired")
		}
		cancel()
	default:
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func fanoutProviderOutput(subject, pid string) error {
	res, err := http.Get(os.Getenv("LAZYMIND_TEST_FANOUT_PROVIDER") + "/" + subject + "?helper=" + pid)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New("signature rejected")
	}
	_, err = io.Copy(os.Stdout, res.Body)
	return err
}

// NewRuntimeConfig does not take a context. This signal comes from the real
// CLI once it starts using its caller's cancellation/deadline, after config
// resolution. It is only an observation and cannot grant admission.
type fanoutCallerContext struct {
	context.Context
	observed sync.Once
}

func (c *fanoutCallerContext) Done() <-chan struct{} {
	c.observed.Do(func() { fmt.Println("CLI is observing cancellation") })
	return c.Context.Done()
}

type fanoutJob struct {
	Service  string
	Deadline time.Duration
	Env      []string
}

// The process-compose stand-in dispatches only the named generated service.
// Jobs and output are private to this test installation and carry no proof
// accepted by the runtime. Windows uses the actual manager shell trampoline.
func serveFanoutCompose(root, config string) error {
	raw, err := os.ReadFile(config)
	if err != nil {
		return err
	}
	var plan processComposeConfig
	if err = yaml.Unmarshal(raw, &plan); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(root, "compose-started"), nil, 0600); err != nil {
		return err
	}
	for until := time.Now().Add(2 * time.Minute); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(root, "stop-compose")); err == nil {
			return nil
		}
		files, err := filepath.Glob(filepath.Join(root, "*.job"))
		if err != nil {
			return err
		}
		for _, file := range files {
			jobData, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			if err = os.Remove(file); err != nil {
				return err
			}
			var job fanoutJob
			if err = json.Unmarshal(jobData, &job); err != nil {
				return err
			}
			p, ok := plan.Processes[job.Service]
			if !ok || !strings.HasSuffix(p.Command, "internal "+job.Service+"-run") {
				return fmt.Errorf("missing service %s", job.Service)
			}
			// process-compose v1.116.0 passes the whole generated command to its shell.
			cmd := exec.Command("bash", "-c", p.Command)
			if plan.Shell != nil {
				cmd = exec.Command(plan.Shell.Command, plan.Shell.Argument, p.Command)
			}
			cmd.Dir = p.WorkingDir
			cmd.Env = append(os.Environ(), p.Environment...)
			cmd.Env = append(cmd.Env, "LAZYMIND_TEST_FANOUT_TIMEOUT="+job.Deadline.String())
			cmd.Env = append(cmd.Env, job.Env...)
			go runFanoutCommand(file, cmd)
		}
	}
	return errors.New("test supervisor expired")
}

func runFanoutCommand(file string, cmd *exec.Cmd) {
	out, err := os.Create(file + ".out")
	if err != nil {
		return
	}
	cmd.Stderr, cmd.Stdout = out, out
	if err = cmd.Run(); err != nil {
		fmt.Fprintln(out, err)
	}
	_ = out.Close()
	_ = os.Rename(file+".out", file+".done")
}

type fanoutBatch struct {
	started, release chan struct{}
	first, released  sync.Once
	calls            atomic.Int32
	firstHelperPID   atomic.Int64
	reject           bool
	denyOtherCalls   atomic.Bool
}

func (b *fanoutBatch) finish() { b.released.Do(func() { close(b.release) }) }

// The HTTP endpoint is private to the test signature provider. Production
// never receives its address or protocol as part of any startup credential.
type fanoutFixture struct {
	*updateStateFixture
	root, appDir, application, helper, executable string
	record                                        map[string]any
	batch                                         atomic.Pointer[fanoutBatch]
	jobCount                                      int
	stopOwner                                     func()
}

func (f *fanoutFixture) hold(t *testing.T, reject bool) *fanoutBatch {
	t.Helper()
	b := &fanoutBatch{started: make(chan struct{}), release: make(chan struct{}), reject: reject}
	f.batch.Store(b)
	t.Cleanup(func() { b.finish(); f.batch.CompareAndSwap(b, nil) })
	return b
}

func (b *fanoutBatch) waitForProvider(t *testing.T) {
	t.Helper()
	select {
	case <-b.started:
	case <-time.After(10 * time.Second):
		t.Fatal("service never entered the full identity provider")
	}
}

func (b *fanoutBatch) expectCalls(t *testing.T, count int32) {
	t.Helper()
	if got := b.calls.Load(); got != count {
		t.Errorf("full verify-identity invocations: got %d, want %d", got, count)
	}
}

func newFanoutFixture(t *testing.T) *fanoutFixture {
	t.Helper()
	t.Setenv(desktopAppVersionEnvVar, "1.1.0")
	f := &fanoutFixture{updateStateFixture: actualUpdateFormatFixture(t, 2), root: t.TempDir()}
	f.appDir = filepath.Join(t.TempDir(), "LazyMind")
	resources := filepath.Join(f.appDir, "resources", "runtime")
	f.application = filepath.Join(f.appDir, "LazyMind.exe")
	if runtime.GOOS != "windows" {
		f.appDir += ".app"
		resources = filepath.Join(f.appDir, "Contents", "Resources", "runtime")
		f.application = filepath.Join(f.appDir, "Contents", "MacOS", "LazyMind")
	}
	var err error
	f.cfg, f.paths, err = newRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: "desktop", RepoRoot: f.cfg.RepoRoot, ResourcesRoot: resources, RuntimeRoot: f.paths.RuntimeRoot, OwnerToken: f.cfg.OwnerToken}, true)
	requireOwnerFixture(t, err)
	f.cfg.UpdateStorage.Version = 2
	state := defaultRuntimeState(f.cfg, f.cfg.ProcessComposePort, f.paths.RunDirTokenFile)
	state.OverallStatus, state.ProcessCompose.APIPort = "stopped", 0
	requireOwnerFixture(t, writeRuntimeState(f.paths.StateFile, state))
	platform, arch := runtime.GOOS, runtime.GOARCH
	if platform == "linux" {
		platform = "darwin"
	} // exercised as a Mac policy subject in Linux functional CI
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	requireOwnerFixture(t, err)
	cfg := update.Config{TrustDomain: "fixture-domain", Audience: "official", Mode: "official", Channel: "stable", ReleaseID: "target", Version: "1.1.0", Platform: platform, Arch: arch, SigningIdentity: "fixture", UpdaterProtocol: 2, PolicyURL: "https://updates.example.invalid/policy", AllowedOrigins: []string{"https://updates.example.invalid"}, PublicKeys: map[string]string{"fixture": base64.StdEncoding.EncodeToString(pub)}}
	scope, err := runtimeUpdateScope(f.cfg, f.paths)
	requireOwnerFixture(t, err)
	actor, err := update.CurrentProcessIdentity()
	requireOwnerFixture(t, err)
	measured := bootstrapPolicyLocalState(f.measure(t))
	storage := cloneUpdateStorage(f.cfg.UpdateStorage)
	storage.VectorPath, storage.VectorEngine, storage.LayoutSource = f.cfg.ModeProfile.VectorStore.DBPath, "milvus-lite", "accepted-source-runtime"
	tx := strings.Repeat("1", 32)
	f.record = map[string]any{"version": 2, "transactionId": tx, "phase": "activated", "sourceReleaseId": "source", "sourceVersion": "1.0.0", "targetReleaseId": cfg.ReleaseID, "targetVersion": cfg.Version,
		"trustDomain": cfg.TrustDomain, "audience": cfg.Audience, "channel": cfg.Channel, "signingIdentity": cfg.SigningIdentity, "platform": cfg.Platform, "arch": cfg.Arch, "runtimeRoot": f.paths.RuntimeRoot, "installationRoot": resources, "scope": scope,
		"sourceDataProfile": measured.DataProfile, "targetDataProfile": measured.DataProfile, "sourceStorage": storage, "sourceHadRAG": false, "componentMode": "absent", "expectedPeers": bootstrapPeerNames(f.cfg), "owner": f.cfg.OwnerToken, "sourceActor": actor, "handoffActor": actor, "targetActor": actor,
		"activation": map[string]any{"transactionId": tx, "releaseId": cfg.ReleaseID, "componentMode": "absent", "components": measured.Components, "verified": true}}
	for _, name := range []string{"package", "components"} {
		data := []byte("fixture " + name)
		file := filepath.Join(f.root, name)
		requireOwnerFixture(t, os.WriteFile(file, data, 0600))
		f.record[name+"Path"], f.record[name+"SHA256"], f.record[name+"Size"] = file, hexDigest(data), len(data)
	}
	now := time.Now().UTC()
	verified, err := update.Check(t.Context(), signBootstrapPolicy(t, key, bootstrapPolicyPayload(cfg, f.record, 42, now)), cfg, measured, f.root, false, now)
	requireOwnerFixture(t, err)
	if verified.Status != "up_to_date" {
		t.Fatalf("signed target policy: %+v", verified)
	}
	f.record["policyDigest"], f.record["policySequence"] = verified.PolicyDigest, verified.PolicySequence
	saveBootstrapRecord(t, f.root, f.record)
	config, err := json.Marshal(map[string]any{"status": "enabled", "config": cfg})
	requireOwnerFixture(t, err)
	f.helper = filepath.Join(resources, "bin", executableName("lazymind-update-helper"))
	f.executable = filepath.Join(resources, "bin", executableName("local-runtime-manager"))
	self, err := os.Executable()
	requireOwnerFixture(t, err)
	image, err := os.ReadFile(self)
	requireOwnerFixture(t, err)
	for _, exe := range []string{f.helper, f.executable, f.paths.ProcessComposeBin} {
		requireOwnerFixture(t, os.MkdirAll(filepath.Dir(exe), 0700))
		requireOwnerFixture(t, os.WriteFile(exe, image, 0700))
	}
	app := []byte("original packaged application")
	requireOwnerFixture(t, os.MkdirAll(filepath.Dir(f.application), 0700))
	requireOwnerFixture(t, os.WriteFile(f.application, app, 0700))
	validHash := "/" + hexDigest(app) + "/" + hexDigest(image)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b := f.batch.Load(); b != nil {
			call := b.calls.Add(1)
			b.first.Do(func() {
				pid, _ := strconv.ParseInt(r.URL.Query().Get("helper"), 10, 64)
				b.firstHelperPID.Store(pid)
				close(b.started)
			})
			select {
			case <-b.release:
			case <-r.Context().Done():
				return
			}
			if b.reject || (b.denyOtherCalls.Load() && call > 1) {
				http.Error(w, "invalid signature", http.StatusForbidden)
				return
			}
		}
		if r.URL.Path != validHash {
			http.Error(w, "application or helper changed", http.StatusForbidden)
			return
		}
		_, _ = w.Write(config)
	}))
	t.Cleanup(server.Close)
	t.Setenv(fanoutChildRoot, f.root)
	t.Setenv("LAZYMIND_TEST_FANOUT_APP", f.application)
	t.Setenv("LAZYMIND_TEST_FANOUT_PROVIDER", server.URL)
	t.Setenv("LAZYMIND_TEST_FANOUT_MANAGER", f.executable)
	f.startSupervisor(t)
	return f
}

func (f *fanoutFixture) startSupervisor(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	cmd := exec.CommandContext(ctx, f.executable, "up", "--profile", "desktop", "--repo-root", f.cfg.RepoRoot, "--resources-root", f.paths.ResourcesRoot, "--runtime-root", f.paths.RuntimeRoot, "--owner-token", f.cfg.OwnerToken, "--update-bootstrap", f.record["transactionId"].(string), "--bootstrap-owner", f.cfg.OwnerToken)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	requireOwnerFixture(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var once sync.Once
	f.stopOwner = func() {
		once.Do(func() {
			_ = os.WriteFile(filepath.Join(f.root, "stop-compose"), nil, 0600)
			for until := time.Now().Add(3 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
				if _, err := os.Stat(f.paths.ProcessComposePIDFile); os.IsNotExist(err) {
					break
				}
			}
			cancel()
			<-done
		})
	}
	t.Cleanup(f.stopOwner)
	for until := time.Now().Add(20 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		select {
		case err := <-done:
			done <- err
			t.Fatalf("sealed parent exited: %v %s", err, stderr.String())
		default:
		}
		if _, err := os.Stat(filepath.Join(f.root, "compose-started")); err == nil {
			return
		}
	}
	t.Fatal("sealed parent never dispatched process-compose")
}

type fanoutChild struct {
	service string
	file    string
}

func (f *fanoutFixture) service(t *testing.T, name string, timeout time.Duration, extra ...string) fanoutChild {
	t.Helper()
	f.jobCount++
	file := filepath.Join(f.root, fmt.Sprintf("%d.job", f.jobCount))
	raw, err := json.Marshal(fanoutJob{name, timeout, extra})
	requireOwnerFixture(t, err)
	requireOwnerFixture(t, os.WriteFile(file+".new", raw, 0600))
	requireOwnerFixture(t, os.Rename(file+".new", file))
	return waitFanoutEntry(t, name, file)
}

func waitFanoutEntry(t *testing.T, name, file string) fanoutChild {
	t.Helper()
	for until := time.Now().Add(12 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		for _, path := range []string{file + ".out", file + ".done"} {
			if raw, _ := os.ReadFile(path); bytes.Contains(raw, []byte("entering CLI")) {
				return fanoutChild{name, file}
			}
		}
	}
	out, _ := os.ReadFile(file + ".done")
	t.Fatalf("%s never entered CLI: %s", name, out)
	return fanoutChild{}
}

func (p fanoutChild) result(t *testing.T) string {
	t.Helper()
	for until := time.Now().Add(12 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		raw, err := os.ReadFile(p.file + ".done")
		if os.IsNotExist(err) {
			continue
		}
		requireOwnerFixture(t, err)
		return string(raw)
	}
	t.Fatalf("%s did not finish", p.service)
	return ""
}
func (p fanoutChild) expect(t *testing.T, fragments ...string) {
	t.Helper()
	got := p.result(t)
	if len(fragments) == 0 {
		fragments = []string{"desktop " + p.service + " binary not found:"}
	}
	for _, want := range fragments {
		if strings.Contains(got, want) {
			return
		}
	}
	t.Fatalf("%s: got %q, expected one of %v", p.service, got, fragments)
}

func (p fanoutChild) atAdmission(t *testing.T, mayReject bool) {
	t.Helper()
	for until := time.Now().Add(10 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		if done, err := os.ReadFile(p.file + ".done"); err == nil {
			if !mayReject {
				t.Fatalf("%s completed before provider release: %s", p.service, done)
			}
			return
		}
		if out, _ := os.ReadFile(p.file + ".out"); bytes.Contains(out, []byte("CLI is observing cancellation")) {
			return
		}
	}
	t.Fatalf("%s never observed its caller's context or returned", p.service)
}

// A separate provider request or early refusal can be observed directly.
// A correctly joined waiter emits neither; hold the original verification
// briefly after observing its live CLI context to allow local IPC to settle.
func (b *fanoutBatch) finishWith(t *testing.T, other fanoutChild, mayReject bool) {
	t.Helper()
	other.atAdmission(t, mayReject)
	for until := time.Now().Add(500 * time.Millisecond); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		if b.calls.Load() > 1 {
			break
		}
		if _, err := os.Stat(other.file + ".done"); err == nil {
			break
		}
	}
	b.finish()
}

func TestUpdateBootstrapIdentityFanout(t *testing.T) {
	f := newFanoutFixture(t)
	const normal = 10 * time.Second
	t.Run("two real services share only a live verification", func(t *testing.T) {
		b := f.hold(t, false)
		core := f.service(t, coreProcessName, normal)
		b.waitForProvider(t)
		peers := []fanoutChild{core, f.service(t, scanControlPlaneProcessName, normal)}
		if os.Getenv("LAZYMIND_TEST_FANOUT_SCALE") == "14" {
			for i := 2; i < 14; i++ {
				peers = append(peers, f.service(t, []string{coreProcessName, scanControlPlaneProcessName}[i%2], 30*time.Second))
			}
		}
		b.finishWith(t, peers[len(peers)-1], false)
		for _, peer := range peers {
			peer.expect(t)
		}
		t.Logf("%d admitted independent processes; full provider calls = %d", len(peers), b.calls.Load())
		b.expectCalls(t, 1)
	})
	t.Run("all waiters receive signature failure", func(t *testing.T) {
		b := f.hold(t, true)
		core := f.service(t, coreProcessName, normal)
		b.waitForProvider(t)
		scan := f.service(t, scanControlPlaneProcessName, normal)
		b.finishWith(t, scan, false)
		core.expect(t, "native_install_unavailable")
		scan.expect(t, "native_install_unavailable")
		b.expectCalls(t, 1)
	})
	for _, shortFirst := range []bool{false, true} {
		name := "later waiter times out"
		if shortFirst {
			name = "first waiter times out"
		}
		t.Run(name, func(t *testing.T) {
			b := f.hold(t, false)
			firstTimeout, secondTimeout := normal, 3*time.Second
			if shortFirst {
				firstTimeout, secondTimeout = secondTimeout, firstTimeout
			}
			first := f.service(t, coreProcessName, firstTimeout)
			b.waitForProvider(t)
			second := f.service(t, scanControlPlaneProcessName, secondTimeout)
			second.atAdmission(t, false)
			short, long := second, first
			if shortFirst {
				short, long = first, second
			}
			// Both callers have reached cancellable admission with the initial
			// verification still held. Observe the short caller actually leave,
			// then deliver the sole underlying result to the surviving caller.
			short.atAdmission(t, false)
			reason := short.result(t)
			if !strings.Contains(reason, "caller deadline expired") || strings.Contains(reason, " binary not found:") {
				t.Fatalf("short caller must depart on its own deadline before service entry: %s", reason)
			}
			long.atAdmission(t, false)
			b.finish()
			long.expect(t)
			b.expectCalls(t, 1)
		})
	}
	t.Run("each service checks current transaction and permission", func(t *testing.T) {
		b := f.hold(t, false)
		core := f.service(t, coreProcessName, normal)
		b.waitForProvider(t)
		f.service(t, scanControlPlaneProcessName, normal, "LAZYMIND_UPDATE_BOOTSTRAP_OWNER=foreign-owner").expect(t, "update_bootstrap_invalid", "update_transaction_conflict")
		f.service(t, scanControlPlaneProcessName, normal, "LAZYMIND_UPDATE_BOOTSTRAP_TRANSACTION="+strings.Repeat("2", 32)).expect(t, "update_bootstrap_invalid", "update_incomplete")
		// With the original owner/tx, revoke admission while a valid identity
		// is still in flight. The independent locked permission check must win.
		f.record["phase"] = "repair"
		saveBootstrapRecord(t, f.root, f.record)
		t.Cleanup(func() { f.record["phase"] = "activated"; saveBootstrapRecord(t, f.root, f.record) })
		b.finish()
		core.expect(t, "update_incomplete", "update_bootstrap_invalid", "update_transaction_conflict")
	})
	t.Run("another target release cannot receive startup permission", func(t *testing.T) {
		b := f.hold(t, false)
		core := f.service(t, coreProcessName, normal)
		b.waitForProvider(t)
		f.record["targetReleaseId"] = "other-release"
		t.Cleanup(func() { f.record["targetReleaseId"] = "target"; saveBootstrapRecord(t, f.root, f.record) })
		saveBootstrapRecord(t, f.root, f.record)
		b.finish()
		core.expect(t, "update_incomplete", "update_transaction_conflict", "update_bootstrap_invalid")
	})
	t.Run("every service refuses a mismatched target version with the same release ID", func(t *testing.T) {
		b := f.hold(t, false)
		core := f.service(t, coreProcessName, normal)
		b.waitForProvider(t)
		f.record["targetVersion"] = "1.2.0"
		t.Cleanup(func() { f.record["targetVersion"] = "1.1.0"; saveBootstrapRecord(t, f.root, f.record) })
		saveBootstrapRecord(t, f.root, f.record)
		scan := f.service(t, scanControlPlaneProcessName, normal)
		b.finishWith(t, scan, true)
		for _, peer := range []fanoutChild{core, scan} {
			t.Run(peer.service, func(t *testing.T) {
				peer.expect(t, "native_install_unavailable", "update_transaction_conflict", "update_incomplete", "update_bootstrap_invalid")
			})
		}
		f.service(t, coreProcessName, normal).expect(t, "native_install_unavailable", "update_transaction_conflict", "update_incomplete", "update_bootstrap_invalid")
	})
	t.Run("independent process cannot borrow a supervisor wave", func(t *testing.T) {
		b := f.hold(t, false)
		b.denyOtherCalls.Store(true)
		core := f.service(t, coreProcessName, normal)
		b.waitForProvider(t)
		raw, err := os.ReadFile(f.paths.GeneratedConfig)
		requireOwnerFixture(t, err)
		var plan processComposeConfig
		requireOwnerFixture(t, yaml.Unmarshal(raw, &plan))
		peer := plan.Processes[scanControlPlaneProcessName]
		cmd := exec.CommandContext(t.Context(), f.executable, "internal", "scan-control-plane-run")
		cmd.Dir = peer.WorkingDir
		cmd.Env = append(os.Environ(), peer.Environment...)
		cmd.Env = append(cmd.Env, "LAZYMIND_TEST_FANOUT_TIMEOUT=10s")
		file := filepath.Join(t.TempDir(), "independent")
		go runFanoutCommand(file, cmd)
		direct := waitFanoutEntry(t, scanControlPlaneProcessName, file)
		b.finishWith(t, direct, true)
		core.expect(t)
		direct.expect(t, "native_install_unavailable", "update_transaction_conflict", "update_incomplete", "update_bootstrap_invalid")
	})
	t.Run("same path and version with replaced application", func(t *testing.T) {
		b := f.hold(t, false)
		old := f.service(t, coreProcessName, normal)
		b.waitForProvider(t)
		if runtime.GOOS == "windows" {
			backup := filepath.Join(t.TempDir(), "original.exe")
			requireOwnerFixture(t, os.Rename(f.application, backup))
			requireOwnerFixture(t, os.WriteFile(f.application, []byte("changed unsigned application"), 0700))
			t.Cleanup(func() {
				requireOwnerFixture(t, os.Remove(f.application))
				requireOwnerFixture(t, os.Rename(backup, f.application))
			})
		} else {
			next, backup := f.appDir+".new", f.appDir+".previous"
			requireOwnerFixture(t, os.CopyFS(next, os.DirFS(f.appDir)))
			rel, err := filepath.Rel(f.appDir, f.application)
			requireOwnerFixture(t, err)
			requireOwnerFixture(t, os.WriteFile(filepath.Join(next, rel), []byte("changed unsigned application"), 0700))
			requireOwnerFixture(t, os.Rename(f.appDir, backup))
			requireOwnerFixture(t, os.Rename(next, f.appDir))
			t.Cleanup(func() {
				requireOwnerFixture(t, os.Rename(f.appDir, next))
				requireOwnerFixture(t, os.Rename(backup, f.appDir))
			})
		}
		fresh := f.service(t, scanControlPlaneProcessName, normal)
		b.finishWith(t, fresh, true)
		fresh.expect(t, "native_install_unavailable", "update_transaction_conflict", "update_incomplete")
		// Finishing the old request against a changed installation may also
		// be refused; its result must never authorize the replacement.
		old.expect(t, "desktop core binary not found:", "native_install_unavailable", "update_transaction_conflict", "update_incomplete")
	})
	t.Run("replaced executable helper cannot use an outstanding old verification", func(t *testing.T) {
		// A distinct build of the same test provider is executable on each OS.
		// Its bytes are outside the originally trusted test identity.
		replacement := filepath.Join(t.TempDir(), executableName("replacement-helper"))
		build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-c", "-ldflags=-buildid=fanout_alternate_identity", "-o", replacement, ".")
		build.Env = coreFixtureBuildEnv
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build replacement: %v %s", err, out)
		}
		check := exec.Command(replacement, "verify-identity")
		out, err := check.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "signature rejected") {
			t.Fatalf("replacement must execute and reach the two-object signature check: %v %s", err, out)
		}
		mode := filepath.Join(f.root, "defer-provider-output")
		requireOwnerFixture(t, os.WriteFile(mode, nil, 0600))
		t.Cleanup(func() { requireOwnerFixture(t, os.Remove(mode)) })
		b := f.hold(t, false)
		old := f.service(t, coreProcessName, normal)
		b.waitForProvider(t)
		pid := int(b.firstHelperPID.Load())
		if pid <= 0 {
			t.Fatal("provider did not report its original helper process")
		}
		for until := time.Now().Add(5 * time.Second); processAlive(pid) && time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		}
		if processAlive(pid) {
			t.Fatal("original helper image is still mapped")
		}
		old.atAdmission(t, false)
		backup := f.helper + ".original"
		requireOwnerFixture(t, os.Rename(f.helper, backup))
		requireOwnerFixture(t, os.Rename(replacement, f.helper))
		t.Cleanup(func() {
			requireOwnerFixture(t, os.Remove(f.helper))
			requireOwnerFixture(t, os.Rename(backup, f.helper))
		})
		fresh := f.service(t, scanControlPlaneProcessName, normal)
		b.finishWith(t, fresh, true)
		fresh.expect(t, "native_install_unavailable", "update_transaction_conflict", "update_incomplete")
		old.expect(t, "desktop core binary not found:", "native_install_unavailable", "update_transaction_conflict", "update_incomplete")
	})
	t.Run("exited service and later request verify again", func(t *testing.T) {
		b := f.hold(t, false)
		first := f.service(t, coreProcessName, normal)
		b.waitForProvider(t)
		b.finish()
		first.expect(t)
		b.expectCalls(t, 1)
		b = f.hold(t, true)
		restarted := f.service(t, coreProcessName, normal)
		b.waitForProvider(t)
		b.finish()
		restarted.expect(t, "native_install_unavailable")
		b.expectCalls(t, 1)
		// Stop and reap the actual parent and supervisor processes. The next
		// parent opens the same installation and durable activated transaction.
		f.batch.Store(nil)
		f.stopOwner()
		requireOwnerFixture(t, os.Remove(filepath.Join(f.root, "compose-started")))
		requireOwnerFixture(t, os.Remove(filepath.Join(f.root, "stop-compose")))
		f.startSupervisor(t)
		b = f.hold(t, true)
		relaunched := f.service(t, scanControlPlaneProcessName, normal)
		b.waitForProvider(t)
		b.finish()
		relaunched.expect(t, "native_install_unavailable")
		b.expectCalls(t, 1)
	})
}
