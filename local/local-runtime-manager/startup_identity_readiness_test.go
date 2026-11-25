package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

const readinessTestRole = "LAZYMIND_TEST_IDENTITY_READINESS_ROLE"
const readinessTestName = "^TestStartupIdentityReadinessWithAcceptedConnection$"

// Pause one real socket after the kernel accepts it but before serve hands it
// to an admission handler. The client still performs normal OS peer checks.
type readinessListener struct {
	net.Listener
	remaining int
	accepted  chan struct{}
	resume    chan struct{}
}

func (l *readinessListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.remaining--
		if l.remaining == 0 {
			close(l.accepted)
			<-l.resume
		}
	}
	return c, err
}

func TestStartupIdentityReadinessWithAcceptedConnection(t *testing.T) {
	role := os.Getenv(readinessTestRole)
	if role == "" {
		f := newFanoutFixture(t)
		cmd := exec.CommandContext(t.Context(), f.executable, "-test.run="+readinessTestName, "-test.v")
		cmd.Env = append(os.Environ(), readinessTestRole+"=server")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("installed manager: %v\n%s", err, out)
		}
		return
	}
	root := os.Getenv(fanoutChildRoot)
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	record, err := desktopupdate.ReadNativeRecord(root)
	requireOwnerFixture(t, err)
	paths := RuntimePaths{ResourcesRoot: record.InstallationRoot, RuntimeRoot: record.RuntimeRoot}
	if role != "server" {
		cfg, err := readServiceStartupIdentity(t.Context(), paths, record)
		if role == "enrolled" && (err != nil || cfg.ReleaseID != record.TargetReleaseID) {
			t.Fatalf("enrolled caller lost original verification: %q, %v", cfg.ReleaseID, err)
		}
		if role == "late" && desktopupdate.ErrorCode(err) != "native_install_unavailable" {
			t.Fatalf("late caller must use its independently rejecting verifier, got %q, %v", cfg.ReleaseID, err)
		}
		return
	}
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprintf("already-verifying=%t", active), func(t *testing.T) {
			exerciseReadyConnection(t, paths, record, active)
		})
	}
}

func exerciseReadyConnection(t *testing.T, paths RuntimePaths, record *desktopupdate.NativeRecord, active bool) {
	t.Helper()
	origin := os.Getenv("LAZYMIND_TEST_FANOUT_PROVIDER")
	started, release := make(chan struct{}), make(chan struct{})
	var sharedCalls atomic.Int32
	releaseVerification := sync.OnceFunc(func() { close(release) })
	pass := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sharedCalls.Add(1) == 1 {
			close(started)
		}
		if active {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		http.Redirect(w, r, origin+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	t.Cleanup(pass.Close)
	t.Cleanup(releaseVerification)
	t.Setenv("LAZYMIND_TEST_FANOUT_PROVIDER", pass.URL)
	cfg := RuntimeConfig{Profile: "desktop", OwnerToken: record.Owner, UpdateBootstrap: &RuntimeUpdateBootstrap{Owner: record.Owner, TransactionID: record.TransactionID, ReleaseID: record.TargetReleaseID}}
	server := newStartupIdentityServer(t.Context(), cfg, paths)
	if server == nil {
		t.Fatal("installed manager cannot open verifier")
	}
	t.Cleanup(server.cancel)
	lateConnection := 1
	if active {
		lateConnection = 2
	}
	gate := &readinessListener{Listener: server.listener, remaining: lateConnection, accepted: make(chan struct{}), resume: make(chan struct{})}
	unblock := sync.OnceFunc(func() { close(gate.resume) })
	t.Cleanup(unblock)
	server.listener = gate
	server.serve(os.Getpid())

	retried := make(chan struct{}, 1)
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case retried <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(deny.Close)
	caller := func(role string) <-chan string {
		done := make(chan string, 1)
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run="+readinessTestName)
		cmd.Env = append(os.Environ(), startupIdentityEnv+"="+server.locator, readinessTestRole+"="+role, "LAZYMIND_TEST_FANOUT_PROVIDER="+deny.URL)
		go func() {
			raw, err := cmd.CombinedOutput()
			if err != nil {
				done <- fmt.Sprintf("%v %s", err, raw)
			} else {
				done <- ""
			}
		}()
		return done
	}
	wait := func(ch <-chan struct{}, reason string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(8 * time.Second):
			t.Fatal(reason)
		}
	}
	var callers []<-chan string
	if active {
		callers = append(callers, caller("enrolled"))
		wait(started, "enrolled service did not reach the full signature provider")
	}
	callers = append(callers, caller("late"))
	wait(gate.accepted, "late service did not open its peer connection")
	server.stopAccepting()
	unblock()
	select {
	case <-retried:
	case <-time.After(3 * time.Second):
		t.Error("preaccepted connection did not switch to its independent verifier at readiness")
	}
	releaseVerification()
	for _, done := range callers {
		select {
		case failure := <-done:
			if failure != "" {
				t.Error(failure)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("service did not finish")
		}
	}
	want := int32(lateConnection - 1)
	if got := sharedCalls.Load(); got != want {
		t.Errorf("shared provider called %d times, want %d already enrolled", got, want)
	}
}
