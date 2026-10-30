package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"lazymind/core/common/orm"
)

// All document actions share the same read projection. Exercise the complete
// model/state/route matrix once, including each route's distinct filtering.
func TestDocumentCapabilities(t *testing.T) {
	for _, withModel := range []bool{false, true} {
		for _, state := range []string{"editable", "live", "historical", "stale", "deleted", "dismissed", "unknown session"} {
			for index, endpoint := range descriptorRoutes {
				t.Run(fmt.Sprintf("model=%v/%s%s", withModel, state, endpoint), func(t *testing.T) {
					f := newPortableFixture(t, "markdown", withModel)
					forbiddenCalls := documentCapabilitiesAlgorithm(t)
					var err error
					switch state {
					case "live":
						seedRewriteConsumer(t, rewriteFixture{descriptorFixture: f.descriptorFixture}, "running")
					case "historical":
						err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("selected", false).Error
					case "stale", "deleted":
						err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("validity", state).Error
					case "dismissed":
						err = f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("dismissed", true).Error
					case "unknown session":
						err = f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("status", "unknown-state").Error
					}
					if err != nil {
						t.Fatal(err)
					}
					// Includes session/revision/value/event, dependency graph, native
					// artifacts and workspace files from the action-specific suites.
					before := portableState(t, f)
					w := f.read(t.Context(), endpoint, "descriptor-owner")
					if state == "dismissed" && index < 2 {
						if w.Code != http.StatusNotFound {
							t.Errorf("dismissed status=%d", w.Code)
						}
					} else {
						records := descriptorOptionalRecords(t, w)
						filtered := (state == "dismissed" && (index == 2 || index == 3)) || (state == "historical" && index == 4) || (state == "unknown session" && index == 2)
						if filtered {
							if len(records) != 0 {
								t.Errorf("filter changed=%#v", records)
							}
						} else {
							found := false
							for _, record := range records {
								if record["artifact_id"] != "descriptor-artifact" {
									continue
								}
								found = true
								doc, ok := record["document"].(map[string]any)
								if !ok {
									t.Fatalf("document missing=%#v", record)
								}
								want := []any{}
								if state == "editable" {
									want = append(want, "save")
									if withModel {
										want = append(want, "rewrite_selection")
									}
									want = append(want, "convert_document", "numbering", "cross_reference", "publish_document")
								} else if state == "live" {
									want = []any{"convert_document", "numbering", "cross_reference"}
								}
								if !reflect.DeepEqual(doc["capabilities"], want) || doc["editable"] != (state == "editable") {
									t.Errorf("capabilities/state=%#v want=%#v", doc, want)
								}
							}
							if !found {
								t.Error("source document missing")
							}
						}
					}
					if calls := forbiddenCalls.Load(); calls != 0 {
						t.Errorf("capability read invoked %d Action/Provider requests", calls)
					}
					if portableState(t, f) != before {
						t.Error("capability read changed Core state, dependency graph or workspace files")
					}
				})
			}
		}
	}
}

// Capability reads may inspect a document and check model availability, but may
// not invoke any action or provider, regardless of the action reference.
func documentCapabilitiesAlgorithm(t *testing.T) *atomic.Int32 {
	t.Helper()
	var forbiddenCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveDynamicLLMRole(w, r) {
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/document:inspect" {
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Error(err)
				return
			}
			_, _ = io.WriteString(w, descriptorMarkdown)
			return
		}
		forbiddenCalls.Add(1)
		t.Errorf("capability read made unexpected Algorithm/Provider request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	return &forbiddenCalls
}
