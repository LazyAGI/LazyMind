package assistantbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/executorpolicy"
	"lazymind/agentconnector/internal/mcpbridge"
)

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
		r := httptest.NewRequest(method, "/v1/session", strings.NewReader(`{"server_url":"http://localhost:8090","access_token":"compose-token","refresh_token":"compose-refresh"}`))
		r.Header.Set("Origin", "http://localhost:8090")
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
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
		if r.URL.Path == "/v1/shutdown" {
			stopped.Store(true)
		}
		writeJSON(w, 200, map[string]any{"version": "v8", "running": true})
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

func TestStartPreservesIncompatibleLegacyBridge(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_HOME", root)
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	t.Setenv(connectorconfig.EnvironmentKey, "")
	var mutations atomic.Int32
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations.Add(1)
		}
		writeJSON(w, 200, map[string]any{"version": "v8", "platform": runtime.GOOS, "executable": "/legacy/lazymind"})
	}))
	defer legacy.Close()
	_, err := Start(context.Background(), strings.TrimPrefix(legacy.URL, "http://"))
	if err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("expected clear compatibility error, got %v", err)
	}
	if mutations.Load() != 0 {
		t.Fatal("modified existing legacy bridge")
	}
	if _, err := Health(context.Background(), strings.TrimPrefix(legacy.URL, "http://")); err != nil {
		t.Fatal(err)
	}
}
