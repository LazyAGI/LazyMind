package assistantbridge

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestWorkBuddyPresentationUsesOnlyPairedBindingsAndCreationOrder(t *testing.T) {
	var connector string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("presentation mutated Core: %s", r.Method)
		}
		if r.URL.Path == "/api/core/workflow-sessions" {
			if r.URL.Query().Get("page_token") == "next" {
				_, _ = w.Write([]byte(`{"sessions":[{"session_id":"new","created_at":"2026-10-19T02:00:00Z"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"sessions":[
				{"session_id":"old","created_at":"2026-10-19T01:00:00Z","updated_at":"2026-10-20T01:00:00Z"},
				{"session_id":"foreign","created_at":"2026-10-19T04:00:00Z"},
				{"session_id":"failed","created_at":"2026-10-19T03:00:00Z"},
				{"session_id":"other-provider","created_at":"2026-10-19T05:00:00Z"},
				{"session_id":"other-session","created_at":"2026-10-19T06:00:00Z"}],"next_page_token":"next"}`))
			}
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/core/workflow-sessions/"), "/control")
		binding := map[string]any{"bound": true, "provider": "workbuddy", "connector_id": connector, "driver_session_id": "native-1"}
		switch id {
		case "foreign":
			binding["connector_id"] = "another-connector"
		case "failed":
			binding["bound"] = false
		case "other-provider":
			binding["provider"] = "codex"
		case "other-session":
			binding["driver_session_id"] = "native-2"
		case "old", "new":
		default:
			t.Errorf("unexpected run %s", id)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"control": map[string]any{"binding": binding}})
	}))
	defer upstream.Close()
	home := t.TempDir()
	server := newTestServer(t, home)
	if err := server.store.Save(credentials.Credentials{ServerURL: upstream.URL, AccessToken: "access", RefreshToken: "refresh", SavedAt: float64(time.Now().Unix()), ExpiresIn: 3600}); err != nil {
		t.Fatal(err)
	}
	account, err := server.store.AccountScope()
	if err != nil {
		t.Fatal(err)
	}
	pair, err := workflowhost.EnsureForProvider(home, "workbuddy", filepath.Join(home, "profile"), account)
	if err != nil {
		t.Fatal(err)
	}
	connector = pair.ConnectorID
	for _, test := range []struct {
		session, token, run string
		code                int
	}{
		{"native-1", pair.Token, "new", 200},
		{"unbound", pair.Token, "", 200},
		{"native-1", "invalid", "", 403},
		{"../bad", pair.Token, "", 422},
	} {
		r := httptest.NewRequest("GET", "/v1/workbuddy/presentation?session_id="+test.session, nil)
		r.Header.Set("Authorization", "Bearer "+test.token)
		r.Header.Set("X-LazyMind-Connector-Id", pair.ConnectorID)
		w := httptest.NewRecorder()
		server.routes().ServeHTTP(w, r)
		if w.Code != test.code {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		if w.Code == 200 {
			var result map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result["run_id"] != test.run || result["session_id"] != test.session {
				t.Fatalf("wrong target: %v", result)
			}
			if len(result) != 2 {
				t.Fatal("presentation exposed unnecessary data")
			}
		}
	}
}

func TestWorkBuddyPresentationSkipsOnlyMissingOrLegacyRuns(t *testing.T) {
	for _, test := range []struct {
		name, code string
		status     int
		skip       bool
	}{
		{"legacy protocol", "CONTROL_PROTOCOL_REQUIRED", http.StatusConflict, true},
		{"deleted run", "WORKFLOW_NOT_FOUND", http.StatusNotFound, true},
		{"permission denied", "PERMISSION_DENIED", http.StatusForbidden, false},
		{"other conflict", "STATE_VERSION_CONFLICT", http.StatusConflict, false},
		{"service unavailable", "WORKFLOW_CONTROL_FAILED", http.StatusServiceUnavailable, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var connector string
			var foundBinding bool
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/core/workflow-sessions":
					if r.URL.Query().Get("page_token") == "next" {
						_, _ = w.Write([]byte(`{"sessions":[{"session_id":"current","created_at":"2026-10-19T02:00:00Z"}]}`))
					} else {
						// Lists are sorted by update time: a recently updated old
						// run can precede the current WorkBuddy run.
						_, _ = w.Write([]byte(`{"sessions":[{"session_id":"historical","created_at":"2026-10-18T01:00:00Z"}],"next_page_token":"next"}`))
					}
				case "/api/core/workflow-sessions/historical/control":
					w.WriteHeader(test.status)
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]string{
						"code": test.code, "message": "historical run control unavailable",
					}})
				case "/api/core/workflow-sessions/current/control":
					foundBinding = true
					_ = json.NewEncoder(w).Encode(map[string]any{"control": map[string]any{"binding": map[string]any{
						"bound": true, "provider": "workbuddy", "connector_id": connector, "driver_session_id": "native-1",
					}}})
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer upstream.Close()
			home := t.TempDir()
			server := newTestServer(t, home)
			if err := server.store.Save(credentials.Credentials{
				ServerURL: upstream.URL, AccessToken: "access", RefreshToken: "refresh",
				SavedAt: float64(time.Now().Unix()), ExpiresIn: 3600,
			}); err != nil {
				t.Fatal(err)
			}
			account, err := server.store.AccountScope()
			if err != nil {
				t.Fatal(err)
			}
			pair, err := workflowhost.EnsureForProvider(home, "workbuddy", filepath.Join(home, "profile"), account)
			if err != nil {
				t.Fatal(err)
			}
			connector = pair.ConnectorID
			r := httptest.NewRequest("GET", "/v1/workbuddy/presentation?session_id=native-1", nil)
			r.Header.Set("Authorization", "Bearer "+pair.Token)
			r.Header.Set("X-LazyMind-Connector-Id", pair.ConnectorID)
			w := httptest.NewRecorder()
			server.routes().ServeHTTP(w, r)
			wantStatus := test.status
			if test.skip {
				wantStatus = http.StatusOK
			}
			if w.Code != wantStatus {
				t.Fatalf("status %d, want %d: %s", w.Code, wantStatus, w.Body.String())
			}
			if foundBinding != test.skip {
				t.Fatalf("continued binding lookup=%v, want %v", foundBinding, test.skip)
			}
			var result map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if test.skip && result["run_id"] != "current" {
				t.Fatalf("lost current binding: %v", result)
			}
			if !test.skip && result["code"] != test.code {
				t.Fatalf("lost upstream error: %v", result)
			}
		})
	}
}

func TestWorkBuddyFrameUsesCurrentFrontend(t *testing.T) {
	server := newTestServer(t, t.TempDir())
	t.Setenv("LAZYMIND_WEB_URL", "")
	for _, test := range []struct{ server, override, want string }{
		{"http://127.0.0.1:18090", "", "http://127.0.0.1:18090"},
		{"http://127.0.0.1:28090", "", "http://127.0.0.1:28090"},
		{"http://127.0.0.1:28090", "https://panel.example.test/lazymind", "https://panel.example.test/lazymind"},
		{"http://127.0.0.1:28090", `https://panel.example.test/a"b<c`, `https://panel.example.test/a"b<c`},
	} {
		if err := server.store.Save(credentials.Credentials{
			ServerURL: test.server, AccessToken: "private-access", RefreshToken: "private-refresh",
		}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LAZYMIND_WEB_URL", test.override)
		// A caller cannot override the trusted frontend through URL parameters.
		r := httptest.NewRequest("GET", "/v1/workbuddy/frame?frontend=https://untrusted.example", nil)
		w := httptest.NewRecorder()
		server.routes().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		want := `<meta name="lazymind-workflow-frontend" content="` + html.EscapeString(test.want) + `">`
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("missing frontend configuration %s", want)
		}
		for _, forbidden := range []string{"private-access", "private-refresh", "__LAZYMIND_FRONTEND_URL__", "untrusted.example", "localhost:8090"} {
			if strings.Contains(w.Body.String(), forbidden) {
				t.Fatalf("unexpected frame content: %s", forbidden)
			}
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("runtime frontend configuration must not be cached")
		}
	}
}

func TestWorkBuddyFrameRejectsInvalidFrontend(t *testing.T) {
	server := newTestServer(t, t.TempDir())
	for _, frontend := range []string{
		"file:///tmp/panel", "/relative", "https://user:password@panel.example.test",
		"https://panel.example.test?token=secret", "https://panel.example.test#fragment",
	} {
		t.Setenv("LAZYMIND_WEB_URL", frontend)
		w := httptest.NewRecorder()
		server.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/workbuddy/frame", nil))
		if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), frontend) {
			t.Fatalf("invalid frontend was not rejected safely: %d %s", w.Code, w.Body.String())
		}
	}
}
