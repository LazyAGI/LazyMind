package assistantbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/executorpolicy"
	"lazymind/agentconnector/internal/mcpbridge"
)

func TestListenerAbsentRecognizesClosedLoopbackPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	address := listener.Addr().String()
	if listenerAbsent(address) {
		t.Fatal("an active loopback listener was reported absent")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if !listenerAbsent(address) {
		t.Fatal("a refused connection to the closed listener was not recognized")
	}
}

func TestSharedBridgeDoesNotInheritDesktopProcessOwnership(t *testing.T) {
	for _, key := range []string{"LAZYMIND_RUNTIME_ROOT", "LAZYMIND_RUNTIME_OWNER_TOKEN", "LAZYMIND_DESKTOP_OWNER_PID", "LAZYMIND_AGENT_RUN_ID", "LAZYMIND_WORKFLOW_RUN_ID"} {
		t.Setenv(key, "desktop-owned")
	}
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "desktop")
	t.Setenv("PATH", "/preserved/path")
	for _, isolated := range []bool{false, true} {
		env := map[string]string{}
		for _, entry := range bridgeEnvironment("/shared/bridge", isolated) {
			key, value, _ := strings.Cut(entry, "=")
			env[key] = value
		}
		for _, key := range []string{"LAZYMIND_RUNTIME_ROOT", "LAZYMIND_RUNTIME_OWNER_TOKEN", "LAZYMIND_DESKTOP_OWNER_PID", "LAZYMIND_AGENT_RUN_ID", "LAZYMIND_WORKFLOW_RUN_ID"} {
			if _, ok := env[key]; ok {
				t.Fatalf("bridge inherited Desktop ownership: %s", key)
			}
		}
		if env["LAZYMIND_RUNTIME_PROFILE"] != "bridge" || env["LAZYMIND_HOME"] != "/shared/bridge" || env["PATH"] != "/preserved/path" {
			t.Fatal("bridge lost its own identity or executable search path")
		}
	}
}

func scopedTestServer(t *testing.T, home string) *Server {
	t.Helper()
	store, err := credentials.NewStore(home, "")
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := mcpbridge.New(store)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := executorpolicy.New(home)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New("127.0.0.1:0", bridge, store, policy)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func sharedTestServer(t *testing.T) (*Server, *sharedBridge) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", root)
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	t.Setenv("LAZYMIND_BRIDGE_ISOLATED", "")
	t.Setenv(connectorconfig.EnvironmentKey, "")
	if _, err := connectorconfig.EnvironmentHome(root, "desktop"); err != nil {
		t.Fatal(err)
	}
	s := scopedTestServer(t, root)
	b, err := newSharedBridge(s)
	if err != nil {
		t.Fatal(err)
	}
	s.shared = b
	return s, b
}

func lifecycleRequest(s *Server, b *sharedBridge, action string, client ClientOptions) *httptest.ResponseRecorder {
	body, _ := json.Marshal(client)
	r := httptest.NewRequest(http.MethodPost, "/v1/clients/"+action, bytes.NewReader(body))
	r.Header.Set(managementHeader, b.token)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	return w
}

func TestSharedBridgeReleasePreservesOtherDeployment(t *testing.T) {
	s, b := sharedTestServer(t)
	var stopped atomic.Bool
	s.stop = func() { stopped.Store(true) }
	for _, id := range []string{"desktop", "compose"} {
		w := lifecycleRequest(s, b, "acquire", ClientOptions{ID: id, Home: b.root})
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	w := lifecycleRequest(s, b, "release", ClientOptions{ID: "desktop", Home: b.root})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"running":true`) || stopped.Load() {
		t.Fatalf("stopped borrowed service: %s", w.Body.String())
	}
	if _, ok := b.clients["compose"]; !ok {
		t.Fatal("lost Compose lease")
	}
	w = lifecycleRequest(s, b, "release", ClientOptions{ID: "unregistered", Home: b.root})
	if w.Code != 200 || len(b.clients) != 1 {
		t.Fatal("unknown owner changed leases")
	}
	w = lifecycleRequest(s, b, "release", ClientOptions{ID: "compose", Home: b.root})
	if w.Code != 200 || !b.stopping {
		t.Fatalf("last owner was not released: %s", w.Body.String())
	}
}

func TestLifecycleRequiresControlTokenAndRejectsBrowserShutdown(t *testing.T) {
	s, b := sharedTestServer(t)
	for _, path := range []string{"/v1/clients/release", "/v1/shutdown"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		r.Header.Set("Origin", "http://localhost:8090")
		r.Header.Set(managementHeader, b.token)
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		if w.Code != 403 && w.Code != 409 {
			t.Fatalf("browser shutdown status=%d", w.Code)
		}
	}
}

func TestBrowserSessionsRouteToIndependentStores(t *testing.T) {
	s, b := sharedTestServer(t)
	if err := s.store.Save(credentials.Credentials{ServerURL: "http://127.0.0.1:8091", AccessToken: "desktop-token", RefreshToken: "desktop-refresh", LocalRuntimeID: "desktop"}); err != nil {
		t.Fatal(err)
	}
	identity, _ := connectorconfig.OriginIdentity("http://localhost:8090")
	home, err := connectorconfig.EnvironmentHome(b.root, identity)
	if err != nil {
		t.Fatal(err)
	}
	child := scopedTestServer(t, home)
	child.isolated = true
	childHTTP := httptest.NewServer(child.routes())
	defer childHTTP.Close()
	if err := connectorconfig.WriteBridgeEndpoint(home, connectorconfig.BridgeEndpoint{URL: childHTTP.URL, Broker: "http://127.0.0.1:19091"}); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		requestBody := `{"server_url":"http://localhost:8090","access_token":"compose-token","refresh_token":"compose-refresh"}`
		if method == http.MethodDelete {
			requestBody = `{"expected_session":` + requestBody + `}`
		}
		r := httptest.NewRequest(method, "/v1/session", strings.NewReader(requestBody))
		r.Header.Set("Origin", "http://localhost:8090")
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
		}
		origins := w.Header().Values("Access-Control-Allow-Origin")
		if len(origins) != 1 || origins[0] != "http://localhost:8090" {
			t.Fatalf("%s: browser needs exactly one allowed origin, got %q", method, origins)
		}
		body, _ := os.ReadFile(filepath.Join(b.root, "credentials.json"))
		if !bytes.Contains(body, []byte("desktop-token")) {
			t.Fatal("Compose overwrote or cleared Desktop login")
		}
		if method == http.MethodPost {
			body, _ := os.ReadFile(filepath.Join(home, "credentials.json"))
			if !bytes.Contains(body, []byte("compose-token")) {
				t.Fatal("Compose session missing")
			}
		}
	}
	// Direct requests cannot bypass the environment router to overwrite a sibling.
	r := httptest.NewRequest(http.MethodPost, "/v1/session", strings.NewReader(`{"server_url":"http://localhost:8091","access_token":"wrong","refresh_token":"wrong"}`))
	r.Header.Set("Origin", "http://localhost:8091")
	w := httptest.NewRecorder()
	child.routes().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-environment request=%d", w.Code)
	}
}

func TestComposeOriginDoesNotUseSavedDesktopAddress(t *testing.T) {
	s, b := sharedTestServer(t)
	origin := "http://localhost:8090"
	if err := s.store.Save(credentials.Credentials{ServerURL: origin, AccessToken: "test-access", RefreshToken: "test-refresh", LocalRuntimeID: "old-desktop"}); err != nil {
		t.Fatal(err)
	}
	identity, err := connectorconfig.OriginIdentity(origin)
	if err != nil {
		t.Fatal(err)
	}
	compose, err := connectorconfig.EnvironmentHome(b.root, identity)
	if err != nil {
		t.Fatal(err)
	}
	b.clients["compose"] = ClientOptions{ID: "compose", Home: compose, ServerURL: origin}
	home, err := b.browserHome(identity)
	if err != nil || home != compose {
		t.Fatalf("registered Compose origin routed to %q, want %q: %v", home, compose, err)
	}
	delete(b.clients, "compose")
	home, err = b.browserHome(identity)
	if err != nil || home != compose {
		t.Fatalf("historical Desktop address claimed an unregistered browser: home=%q err=%v", home, err)
	}
}

func TestIsolatedDesktopAcceptsOnlyItsRegisteredOrigin(t *testing.T) {
	root := t.TempDir()
	const desktopOrigin = "http://localhost:8091"
	const composeOrigin = "http://localhost:8090"
	composeIdentity, err := connectorconfig.OriginIdentity(composeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connectorconfig.EnvironmentHome(root, composeIdentity); err != nil {
		t.Fatal(err)
	}
	desktop, err := connectorconfig.EnvironmentHome(root, "desktop")
	if err != nil {
		t.Fatal(err)
	}
	b := &sharedBridge{root: root, clients: map[string]ClientOptions{
		"desktop": {ID: "desktop", Home: desktop, OwnerPID: os.Getpid(), ServerURL: desktopOrigin},
		"compose": {ID: "compose", Home: root, ServerURL: composeOrigin},
	}}
	if err := b.saveClients(); err != nil {
		t.Fatal(err)
	}
	s := scopedTestServer(t, desktop)
	s.isolated = true
	for origin, want := range map[string]int{
		desktopOrigin: http.StatusOK,
		composeOrigin: http.StatusForbidden,
	} {
		request := httptest.NewRequest(http.MethodGet, "/v1/executors", nil)
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		s.routes().ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("origin %s: got HTTP %d, want %d", origin, response.Code, want)
		}
	}
}

func TestBridgeIdentityRequiresProductProtocolPlatformAndEnvironment(t *testing.T) {
	good := map[string]any{"service": bridgeProduct, "version": bridgeProtocol, "platform": runtime.GOOS, "home": "/expected", "isolated": false}
	if !compatibleBridge(good, "/expected", false) {
		t.Fatal("compatible service rejected")
	}
	for key, value := range map[string]any{"service": "other", "version": "v8", "platform": "different", "home": "/other", "isolated": true} {
		copy := map[string]any{}
		for k, v := range good {
			copy[k] = v
		}
		copy[key] = value
		if compatibleBridge(copy, "/expected", false) {
			t.Fatalf("accepted wrong %s", key)
		}
	}
}

func TestReleaseDoesNotStopUnmanagedLegacyBridge(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", root)
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	t.Setenv(connectorconfig.EnvironmentKey, "")
	var stopped atomic.Bool
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			stopped.Store(true)
		}
		writeJSON(w, 200, map[string]any{"version": "v9", "platform": runtime.GOOS, "executable": "/legacy/lazymind"})
	}))
	defer legacy.Close()
	if err := connectorconfig.WriteBridgeEndpoint(root, connectorconfig.BridgeEndpoint{URL: legacy.URL, Broker: legacy.URL}); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseClient(context.Background(), ClientOptions{}); err == nil {
		t.Fatal("legacy ownership was not reported")
	}
	if stopped.Load() {
		t.Fatal("stopped legacy service")
	}
}

func TestReleaseDrainsPreviousScopedBridgeWithoutStoppingOtherClient(t *testing.T) {
	s, b := sharedTestServer(t)
	var stopped atomic.Bool
	s.stop = func() { stopped.Store(true) }
	t.Setenv("LAZYMIND_HOME", b.root)
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	t.Setenv(connectorconfig.EnvironmentKey, "")
	for _, id := range []string{"desktop", "compose"} {
		w := lifecycleRequest(s, b, "acquire", ClientOptions{ID: id, Home: b.root})
		if w.Code != http.StatusOK {
			t.Fatal(w.Body.String())
		}
	}
	routes := s.routes()
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			writeJSON(w, http.StatusOK, map[string]any{
				"version": "v9", "service": bridgeProduct, "platform": runtime.GOOS,
				"home": b.root, "isolated": false,
			})
			return
		}
		routes.ServeHTTP(w, r)
	}))
	defer broker.Close()
	if err := connectorconfig.WriteBridgeEndpoint(b.root, connectorconfig.BridgeEndpoint{URL: broker.URL, Broker: broker.URL}); err != nil {
		t.Fatal(err)
	}
	if err := releaseForUpgrade(t.Context(), broker.Client(), strings.TrimPrefix(broker.URL, "http://"), b.token,
		ClientOptions{ID: "desktop", Home: b.root}, b.root); err == nil {
		t.Fatal("upgrade should preserve a broker still leased by Compose")
	}
	if err := ReleaseClient(t.Context(), ClientOptions{ID: "compose"}); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	_, desktopRemains := b.clients["desktop"]
	shared := len(b.clients) == 1 && !b.stopping
	b.mu.Unlock()
	if !desktopRemains || !shared {
		t.Fatal("releasing Compose must keep the Desktop client active")
	}
	if err := releaseForUpgrade(t.Context(), broker.Client(), strings.TrimPrefix(broker.URL, "http://"), b.token,
		ClientOptions{ID: "desktop", Home: b.root}, b.root); err != nil {
		t.Fatalf("release the sole remaining client for upgrade: %v", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.clients) != 0 || !b.stopping {
		t.Fatal("verified broker with no other clients refused shutdown")
	}
}

func TestStartBridgeOwnership(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		version  string
		scoped   bool
		verified bool
	}{
		{name: "verified v9 replaces itself", version: "v9", verified: true},
		{name: "unverified v8", version: "v8"},
		{name: "unverified v9", version: "v9"},
		{name: "unverified scoped v9", version: "v9", scoped: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("LAZYMIND_HOME", root)
			t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
			t.Setenv(connectorconfig.EnvironmentKey, "")
			if test.verified {
				checkVerifiedBridgeReplacement(t, self, root)
				return
			}
			status := map[string]any{
				"ok": true, "pid": os.Getpid(), "version": test.version, "platform": runtime.GOOS, "executable": self,
			}
			if test.scoped {
				status["service"], status["home"], status["isolated"] = bridgeProduct, root, false
			}
			var mutations atomic.Int32
			legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					mutations.Add(1)
				}
				writeJSON(w, 200, status)
			}))
			defer legacy.Close()
			address := strings.TrimPrefix(legacy.URL, "http://")
			_, err := Start(context.Background(), address)
			if err == nil || !strings.Contains(err.Error(), "cannot verify ownership for this environment") {
				t.Fatalf("expected ownership error, got %v", err)
			}
			if mutations.Load() != 0 {
				t.Fatal("modified existing legacy bridge")
			}
			if _, err := Health(context.Background(), address); err != nil {
				t.Fatal(err)
			}
		})
	}
}

const bridgeStartupTestProcess = "LAZYMIND_TEST_ASSISTANT_BRIDGE_PROCESS"

func TestMain(m *testing.M) {
	mode := os.Getenv(bridgeStartupTestProcess)
	if mode != "" && len(os.Args) == 5 && os.Args[1] == "assistant" && os.Args[2] == "serve" && os.Args[3] == "--listen" {
		if err := serveBridgeStartupTest(mode, os.Args[4]); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func serveBridgeStartupTest(mode, address string) error {
	if mode == "legacy" {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		// Reproduce the old protocol only. OS process identity, logfile ownership,
		// the HTTP round trip and process exit are real in this test.
		mux := http.NewServeMux()
		server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: time.Second}
		stopped := make(chan struct{})
		mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": true, "pid": os.Getpid(), "version": "v9", "platform": runtime.GOOS, "executable": executable,
			})
		})
		mux.HandleFunc("POST /v1/shutdown", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
			go func() {
				defer close(stopped)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = server.Shutdown(ctx)
			}()
		})
		err = server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			<-stopped
			return nil
		}
		return err
	}
	home := os.Getenv("LAZYMIND_HOME")
	store, err := credentials.NewStore(home, "")
	if err != nil {
		return err
	}
	mcp, err := mcpbridge.New(store)
	if err != nil {
		return err
	}
	policy, err := executorpolicy.New(home)
	if err != nil {
		return err
	}
	server, err := New(address, mcp, store, policy)
	if err != nil {
		return err
	}
	return server.Serve(context.Background())
}

func checkVerifiedBridgeReplacement(t *testing.T, self, root string) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		t.Skip("legacy process verification requires macOS, Linux or Windows")
	}
	t.Setenv(bridgeStartupTestProcess, "current")
	t.Setenv("LAZYMIND_DESKTOP_OWNER_PID", "")
	t.Setenv("LAZYMIND_BRIDGE_ISOLATED", "")
	t.Setenv("LAZYMIND_HOST_HOME", root)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	logPath := filepath.Join(root, "logs", "assistant-bridge.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		t.Fatal(err)
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	legacy := exec.Command(self, "assistant", "serve", "--listen", address)
	legacy.Env = append(os.Environ(), bridgeStartupTestProcess+"=legacy")
	legacy.Stdout, legacy.Stderr = log, log
	err = legacy.Start()
	_ = log.Close()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- legacy.Wait(); close(done) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = ReleaseClient(ctx, ClientOptions{})
		_ = legacy.Process.Kill()
		<-done
	})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	for {
		if status, err := Health(ctx, address); err == nil && status["pid"] == float64(legacy.Process.Pid) {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("legacy test Bridge exited during startup: %v", err)
		case <-ctx.Done():
			t.Fatal("legacy test Bridge never became healthy")
		case <-time.After(25 * time.Millisecond):
		}
	}
	status, err := Start(ctx, address)
	if err != nil {
		t.Fatalf("replace verified v9: %v", err)
	}
	if !compatibleBridge(status, root, false) || status["pid"] == float64(legacy.Process.Pid) {
		t.Fatalf("start did not launch a compatible replacement: %v", status)
	}
	if err := <-done; err != nil {
		t.Fatalf("verified v9 did not shut down gracefully: %v", err)
	}
}
