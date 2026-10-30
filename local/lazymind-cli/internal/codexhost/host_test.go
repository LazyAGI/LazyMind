package codexhost

import (
	"context"
	"encoding/json"
	"lazymind/agentconnector/internal/workflowhost"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestDesktopReadyAcceptsChatWindowsAndExcludesAuxiliaryWindows(t *testing.T) {
	for _, test := range []struct {
		body  string
		ready bool
	}{{`[{"type":"page","url":"app://-/index.html?initialRoute=%2Flocal%2F01a16196-3096-7dd3-8c20-33cd9250503c"}]`, true}, {`[{"type":"page","url":"app://-/detached-window.html?initialRoute=%2Fdetached-window"}]`, true}, {`[{"type":"page","url":"app://-/index.html"}]`, true}, {`[{"type":"page","url":"app://-/index.html?initialRoute=%2Favatar-overlay"},{"type":"page","url":"app://-/index.html"}]`, true}, {`[{"type":"page","url":"app://-/index.html?initialRoute=%2Favatar-overlay"}]`, false}, {`[{"type":"page","url":"https://example.test"}]`, false}, {`[{"type":"page","url":"app://-/index.html"},{"type":"page","url":"app://-/index.html"}]`, true}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/json/list" {
				t.Error("unexpected discovery route")
			}
			w.Write([]byte(test.body))
		}))
		_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
		number, _ := strconv.Atoi(port)
		ready, err := DesktopReady(context.Background(), number)
		server.Close()
		if ready != test.ready || !test.ready && err == nil {
			t.Fatalf("ready=%v err=%v", ready, err)
		}
	}
}

func TestDesktopReadyDistinguishesBrokenListenerFromClosedPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
	}))
	port := server.Listener.Addr().(*net.TCPAddr).Port
	if ready, err := DesktopReady(context.Background(), port); ready || err == nil {
		t.Fatalf("broken listener was treated as a missing CDP flag: ready=%v err=%v", ready, err)
	}
	server.Close()
	if ready, err := DesktopReady(context.Background(), port); ready || err != nil {
		t.Fatalf("closed port was not recognized: ready=%v err=%v", ready, err)
	}
}
func TestStatusRejectsStaleHeartbeatAndRevokedPairing(t *testing.T) {
	home := t.TempDir()
	pair, err := workflowhost.EnsureForProvider(home, "codex", filepath.Join(home, "profile"), "account")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json")
	if err = Configure(home, filepath.Join(home, "node"), file); err != nil {
		t.Fatal(err)
	}
	status := Status{Connector: pair.ConnectorID, Ready: true, CheckedAt: time.Now().UnixMilli()}
	write := func() {
		data, _ := json.Marshal(status)
		if err := os.WriteFile(file+".codex-status.json", data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if !Ready(home, "account") {
		t.Fatal("fresh heartbeat was rejected")
	}
	status.CheckedAt = time.Now().Add(-time.Minute).UnixMilli()
	write()
	if Ready(home, "account") {
		t.Fatal("stale heartbeat was accepted")
	}
	status.CheckedAt = time.Now().UnixMilli()
	write()
	if err = workflowhost.Disable(home, pair.ConnectorID); err != nil {
		t.Fatal(err)
	}
	if Ready(home, "account") {
		t.Fatal("revoked pairing was accepted")
	}
}
