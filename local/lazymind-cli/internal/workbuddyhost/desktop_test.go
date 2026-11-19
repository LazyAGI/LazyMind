package workbuddyhost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestDesktopStatusRejectsForeignAndAmbiguousEndpoints(t *testing.T) {
	for _, test := range []struct {
		body  string
		ok    bool
		issue string
	}{
		{`[{"type":"page","title":"WorkBuddy","url":"file:///app/renderer/index.html"}]`, true, ""},
		{`[{"type":"page","title":"Other"}]`, false, "control_window_unavailable"},
		{`[{"type":"page","title":"WorkBuddy","url":"file:///app/renderer/index.html"},{"type":"page","title":"WorkBuddy","url":"file:///app/renderer/index.html"}]`, false, "control_window_unavailable"},
		{`[{"type":"page","title":"WorkBuddy","url":"file:///C:/app/renderer/index.html?windowKind=main"},{"type":"page","title":"WorkBuddy","url":"file:///C:/app/renderer/index.html?windowAppId=settings"}]`, true, ""},
		{`[{"type":"page","title":"WorkBuddy","url":"https://example.com/renderer/index.html"}]`, false, "control_window_unavailable"},
		{`not json`, false, "control_port_conflict"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(test.body)) }))
		address, _ := url.Parse(server.URL)
		port, _ := strconv.Atoi(address.Port())
		ready, err := desktopStatus(context.Background(), port)
		if ready != test.ok || (err == nil) != test.ok {
			t.Fatalf("body=%s ready=%v error=%v", test.body, ready, err)
		}
		if SetupIssue(err) != test.issue {
			t.Fatalf("body=%s issue=%q want=%q", test.body, SetupIssue(err), test.issue)
		}
		if test.ok {
			if err := PrepareDesktop(context.Background(), port, false); err != nil {
				t.Fatal(err)
			}
		}
		if !test.ok {
			// An incompatible occupied endpoint must return before desktop
			// discovery or launch, even when the caller requests an MCP reload.
			if err := PrepareDesktop(context.Background(), port, true); SetupIssue(err) != test.issue {
				t.Fatalf("PrepareDesktop issue=%q want=%q", SetupIssue(err), test.issue)
			}
		}
		server.Close()
	}
}

func TestDesktopStatusRejectsHTTPFailuresAndRedirectsWithoutFollowingThem(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusFound} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			followed := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/json/list" {
					followed = true
				}
				w.Header().Set("Location", "/unexpected")
				w.WriteHeader(status)
			}))
			defer server.Close()
			address, _ := url.Parse(server.URL)
			port, _ := strconv.Atoi(address.Port())
			ready, err := desktopStatus(context.Background(), port)
			if ready || SetupIssue(err) != "control_port_conflict" || followed {
				t.Fatalf("ready=%v issue=%q followed=%v", ready, SetupIssue(err), followed)
			}
		})
	}
}
