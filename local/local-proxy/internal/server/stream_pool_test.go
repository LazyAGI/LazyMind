package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lazyagi/lazymind/local_proxy/internal/config"
)

func TestFullStreamPoolDoesNotBlockOrdinaryRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(": connected\n\n"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	proxy := httptest.NewServer(NewHandler(config.Config{
		Timeouts: config.TimeoutConfig{Connect: time.Second, Read: time.Second, Write: time.Second},
		Routes:   []config.RouteConfig{{Name: "test", Prefix: "/api", Upstream: upstream.URL, Public: true, Enabled: true}},
	}))
	defer proxy.Close()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	var streams []*http.Response
	defer func() {
		for _, stream := range streams {
			stream.Body.Close()
		}
	}()
	for i := 0; i < 32; i++ {
		req, _ := http.NewRequest("GET", proxy.URL+"/api/stream", nil)
		req.Header.Set("Accept", "text/event-stream")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		streams = append(streams, resp)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", proxy.URL+"/api/health", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("ordinary request starved behind SSE: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	req, _ = http.NewRequestWithContext(ctx, "GET", proxy.URL+"/api/stream", nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("overflow stream queued instead of failing promptly: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 || resp.Header.Get("Retry-After") == "" {
		t.Fatal("missing stream admission limit")
	}
}

func TestOnlyRealtimeUpgradeDelegatesAuthenticationToCore(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/realtime/connect" || r.Header.Get("X-User-Id") != "" || r.Header.Get("X-User-Role") != "" {
			t.Error("unexpected path or untrusted identity forwarded")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer auth.Close()
	proxy := httptest.NewServer(NewHandler(config.Config{
		Auth:   config.AuthConfig{AuthServiceURL: auth.URL},
		Routes: []config.RouteConfig{{Name: "core", Prefix: "/api/core", Upstream: upstream.URL, StripPath: true, Enabled: true}},
	}))
	defer proxy.Close()
	for _, test := range []struct {
		path, method, upgrade string
		status                int
	}{
		{"/api/core/realtime/connect", "GET", "websocket", 200},
		{"/api/core/realtime/connect", "GET", "", 401},
		{"/api/core/realtime/connect", "POST", "websocket", 401},
		{"/api/core/conversations/c1/events", "GET", "websocket", 401},
	} {
		req, _ := http.NewRequest(test.method, proxy.URL+test.path, nil)
		req.Header.Set("Upgrade", test.upgrade)
		req.Header.Set("Connection", "upgrade")
		req.Header.Set("X-User-Id", "forged")
		req.Header.Set("X-User-Role", "admin")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != test.status {
			t.Fatalf("%+v: got %d", test, resp.StatusCode)
		}
	}
}
