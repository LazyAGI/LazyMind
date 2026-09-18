package cloudresource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/cloudclient"
)

// Public Handler signatures can be asserted without adding a nonfunctional
// production stub merely to make these tests compile in the Red phase.
type desktopTreeHandler interface {
	Tree(http.ResponseWriter, *http.Request)
}
type desktopContentHandler interface {
	Content(http.ResponseWriter, *http.Request)
}
type desktopMetadataHandler interface {
	Get(http.ResponseWriter, *http.Request)
}
type desktopReadTransport func(*http.Request) (*http.Response, error)

func (f desktopReadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func desktopReadFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "resource-content", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func callDesktopRead(t *testing.T, h Handler, operation string, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	switch operation {
	case "tree":
		target, ok := any(&h).(desktopTreeHandler)
		if !ok {
			t.Fatal("Cloud resource Tree handler has not been implemented")
		}
		target.Tree(w, r)
	case "content":
		target, ok := any(&h).(desktopContentHandler)
		if !ok {
			t.Fatal("Cloud resource Content handler has not been implemented")
		}
		target.Content(w, r)
	case "detail":
		target, ok := any(&h).(desktopMetadataHandler)
		if !ok {
			t.Fatal("Cloud resource Get handler has not been implemented")
		}
		target.Get(w, r)
	}
	return w
}

func TestDesktopReadCloudContentUsesCoreSessionAndNoInstall(t *testing.T) {
	for _, kind := range []string{"skill", "workflow"} {
		for _, operation := range []string{"detail", "tree", "content"} {
			t.Run(kind+"/"+operation, func(t *testing.T) {
				tree, content := desktopReadFixture(t, "tree"), desktopReadFixture(t, "content")
				id, hash := tree["resource_id"].(string), tree["content_hash"].(string)
				tree["resource_type"] = kind
				entrypath := "SKILL.md"
				if kind == "workflow" {
					entrypath = "workflow.yaml"
					tree["entrypoint"] = entrypath
					tree["files"].([]any)[0].(map[string]any)["path"] = entrypath
					digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x000\n", entrypath, int(content["size"].(float64)), content["sha256"])))
					hash = hex.EncodeToString(digest[:])
					tree["content_hash"] = hash
					content["content_hash"] = hash
					content["path"] = entrypath
				}
				requested := []string{}
				client, err := cloudclient.New("https://cloud.example", &http.Client{Transport: desktopReadTransport(func(r *http.Request) (*http.Response, error) {
					requested = append(requested, r.URL.Path)
					if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer core-cloud-token-fixture" {
						t.Errorf("unexpected upstream method or credential")
					}
					var payload any
					switch r.URL.Path {
					case "/v1/account/me":
						payload = map[string]any{"id": "account-a", "username": "fixture", "roles": []string{"user"}, "status": "active", "rbac_version": 1, "policy_revision": 1}
					case "/v1/resources/" + id:
						payload = map[string]any{"resource_id": id, "resource_type": kind, "client_resource_key": kind + ":fixture", "resource_name": "Cloud fixture", "content_hash": hash, "content_size": content["size"], "format_schema": "lazymind.resource-manifest/v2", "updated_at": "2026-09-07T00:00:00Z"}
					case "/v1/resources/" + id + "/tree":
						payload = tree
					case "/v1/resources/" + id + "/content":
						if r.URL.Query().Get("path") != entrypath || r.Header.Get("If-Match") != `"`+hash+`"` {
							t.Error("path or If-Match was not forwarded")
						}
						payload = content
					default:
						t.Errorf("unexpected upstream path: %s", r.URL.Path)
						payload = map[string]any{}
					}
					body, err := json.Marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Etag": {`"` + hash + `"`}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
				})})
				if err != nil {
					t.Fatal(err)
				}
				adapter, bindings := &fakeAdapter{}, &fakeBindings{}
				h := Handler{Service: &Service{Session: fakeSession{token: "core-cloud-token-fixture"}, Cloud: client, Bindings: bindings}, ResourceType: kind, Adapter: adapter}
				r := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/cloud/"+kind+"s/"+id+"/"+operation+"?path="+entrypath, nil), map[string]string{"resource_id": id})
				r.Header.Set("X-User-Id", "local-owner")
				r.Header.Set("Authorization", "Bearer renderer-local-token-fixture")
				r.Header.Set("If-Match", `"`+hash+`"`)
				w := callDesktopRead(t, h, operation, r)
				if w.Code != 200 {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
				var response struct {
					Data map[string]any `json:"data"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response.Data == nil {
					t.Fatal("missing Core response envelope")
				}
				if operation == "content" && response.Data["content"] != content["content"] {
					t.Fatal("Cloud-only document content was not returned")
				}
				if len(requested) == 0 {
					t.Fatal("read never contacted Cloud")
				}
				if adapter.importCalls != 0 || len(bindings.rows) != 0 {
					t.Fatal("viewing installed or bound a local resource")
				}
				if strings.Contains(w.Body.String(), "token-fixture") {
					t.Fatal("Core returned credentials")
				}
			})
		}
	}
}

func TestDesktopCloudReadSessionErrorHasDedicatedCode(t *testing.T) {
	for _, operation := range []string{"detail", "tree", "content"} {
		t.Run(operation, func(t *testing.T) {
			cloud := &fakeCloud{}
			h := Handler{Service: &Service{Session: fakeSession{err: errors.New("fixture-refresh-secret-canary")}, Cloud: cloud, Bindings: &fakeBindings{}}, ResourceType: "skill", Adapter: &fakeAdapter{}}
			r := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/cloud/skills/id/content?path=SKILL.md", nil), map[string]string{"resource_id": "fixture-id"})
			r.Header.Set("X-User-Id", "local-owner")
			w := callDesktopRead(t, h, operation, r)
			var payload map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["code"] != float64(2002920) {
				t.Fatalf("Cloud authentication must be distinguishable from local 401: %s", w.Body.String())
			}
			if cloud.accountCalls != 0 || strings.Contains(w.Body.String(), "secret-canary") {
				t.Fatal("failed session accessed Cloud or leaked details")
			}
		})
	}
}

func TestDesktopCloudReadStillRequiresLocalIdentity(t *testing.T) {
	for _, operation := range []string{"detail", "tree", "content"} {
		t.Run(operation, func(t *testing.T) {
			cloud := &fakeCloud{}
			h := Handler{Service: &Service{Session: fakeSession{token: "fixture"}, Cloud: cloud, Bindings: &fakeBindings{}}, ResourceType: "skill", Adapter: &fakeAdapter{}}
			r := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/cloud/skills/id/tree", nil), map[string]string{"resource_id": "fixture-id"})
			w := callDesktopRead(t, h, operation, r)
			if w.Code != 401 || cloud.accountCalls != 0 {
				t.Fatalf("local identity not required: status=%d Cloud calls=%d", w.Code, cloud.accountCalls)
			}
		})
	}
}

func TestDesktopCloudTreeRejectsUntrustedResponses(t *testing.T) {
	for _, scenario := range []string{"type_mismatch", "unknown_sensitive_field", "invalid_path", "oversized_body", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			tree := desktopReadFixture(t, "tree")
			id, hash := tree["resource_id"].(string), tree["content_hash"].(string)
			switch scenario {
			case "type_mismatch":
				tree["resource_type"] = "workflow"
			case "unknown_sensitive_field":
				tree["access_token"] = "upstream-secret-canary"
			case "invalid_path":
				tree["files"].([]any)[0].(map[string]any)["path"] = "../outside.md"
			}
			client, err := cloudclient.New("https://cloud.example", &http.Client{Transport: desktopReadTransport(func(r *http.Request) (*http.Response, error) {
				if scenario == "cancelled" {
					if r.Context().Err() == nil {
						t.Error("Core lost the cancelled request context")
					}
					return nil, context.Canceled
				}
				var payload any = tree
				if r.URL.Path == "/v1/account/me" {
					payload = map[string]any{"id": "account-a", "username": "fixture", "roles": []string{"user"}, "status": "active", "rbac_version": 1, "policy_revision": 1}
				}
				if r.URL.Path == "/v1/resources/"+id {
					payload = map[string]any{"resource_id": id, "resource_type": "skill", "client_resource_key": "skill:fixture", "resource_name": "fixture", "content_hash": hash, "content_size": 10, "format_schema": "lazymind.resource-manifest/v2", "updated_at": "2026-09-07T00:00:00Z"}
				}
				body, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "oversized_body" && strings.HasSuffix(r.URL.Path, "/tree") {
					body = append([]byte(strings.Repeat(" ", 17<<20)), body...)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Etag": {`"` + hash + `"`}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			h := Handler{Service: &Service{Session: fakeSession{token: "fixture"}, Cloud: client, Bindings: &fakeBindings{}}, ResourceType: "skill", Adapter: &fakeAdapter{}}
			r := mux.SetURLVars(httptest.NewRequest("GET", "/cloud/skills/"+id+"/tree", nil), map[string]string{"resource_id": id})
			r.Header.Set("X-User-Id", "local-owner")
			if scenario == "cancelled" {
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
			}
			w := callDesktopRead(t, h, "tree", r)
			if w.Code == 200 || strings.Contains(w.Body.String(), "upstream-secret-canary") {
				t.Fatalf("untrusted upstream tree accepted or echoed: %s", w.Body.String())
			}
		})
	}
}
