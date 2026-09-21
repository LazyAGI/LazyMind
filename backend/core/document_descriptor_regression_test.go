package main

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestDocumentDescriptorNestedCarrierPolicy(t *testing.T) {
	cases := []struct{ name, value, artifact, schema, representation, errorCode string }{
		{"ordinary", `{"data":{"data":"# ordinary status"}}`, "", "", "", ""},
		{"declared", `{"data":{"schema":"text/markdown","data":"nested prose"}}`, `"nested prose"`, "text/markdown", "markdown", ""},
		{"conflict", `{"schema":"text/markdown","data":{"schema_name":"application/vnd.lazymind.writer+json","data":{"document_id":"nested","blocks":[]}}}`, "", "", "", "DOCUMENT_INVALID"},
		{"ir", `{"data":{"schema_name":"application/vnd.lazymind.writer+json","data":{"document_id":"nested","blocks":[]}}}`, `{"document_id":"nested","blocks":[]}`, descriptorIRSchema, "ir", ""},
	}
	for _, tc := range cases {
		for _, endpoint := range descriptorRoutes {
			t.Run(tc.name+endpoint, func(t *testing.T) {
				f := newDescriptorFixture(t)
				f.seed(t, "descriptor-artifact", "unknown-slot", "json", tc.value)
				response := descriptorMarkdown
				if tc.representation == "ir" {
					response = descriptorIR
				}
				spy := descriptorAlgorithm(t, 200, response)
				requireDescriptorReadOnly(t, f)
				record := descriptorRecords(t, f.read(t.Context(), endpoint, "descriptor-owner"))[0]
				requireDescriptorValue(t, record, tc.value)
				requireDescriptorInspectInput(t, spy, tc.artifact, tc.schema)
				if tc.errorCode != "" {
					requireDescriptorError(t, record, tc.errorCode, false)
				} else if tc.representation != "" {
					requireDescriptor(t, record, tc.representation, true)
				} else if record["document"] != nil || record["document_error"] != nil {
					t.Errorf("ordinary nested JSON projected: %#v", record)
				}
			})
		}
	}
}

func TestDocumentDescriptorLiveConsumerBlocksSave(t *testing.T) {
	for _, kind := range []string{"direct", "transitive", "route"} {
		for _, endpoint := range descriptorRoutes {
			t.Run(kind+endpoint, func(t *testing.T) {
				f := newDescriptorFixture(t)
				if err := f.db.AutoMigrate(&orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{}); err != nil {
					t.Fatal(err)
				}
				f.seed(t, "descriptor-artifact", "unknown-slot", "text/markdown", `{"text":"bound document"}`)
				now := time.Now().UTC()
				consumer := orm.WorkflowSessionStep{ID: "live", SessionID: "descriptor-session", StepID: "consume", TaskID: "live-task", Attempt: 1, Status: "running", Validity: "effective", CreatedAt: now, UpdatedAt: now}
				if err := f.db.Create(&consumer).Error; err != nil {
					t.Fatal(err)
				}
				revisionID := "descriptor-artifact"
				if kind == "transitive" {
					for _, row := range []any{&orm.WorkflowSessionStep{ID: "terminal", SessionID: "descriptor-session", StepID: "terminal", TaskID: "terminal-task", Attempt: 1, Status: "succeeded", Validity: "effective", CreatedAt: now, UpdatedAt: now}, &orm.WorkflowSlotRevision{ID: "terminal-output", SessionID: "descriptor-session", SlotID: "descendant", Slot: "descendant", StepID: "terminal", Attempt: 1, Revision: 1, Selected: true, Validity: "effective", ProducerAttemptID: "terminal", ContentSnapshot: json.RawMessage(`42`), CreatedAt: now}, &orm.WorkflowAttemptInputBinding{ID: "terminal-binding", SessionID: "descriptor-session", AttemptID: "terminal", MaterialID: "unknown-slot", MaterialRevisionID: revisionID, SourceType: "artifact", CreatedAt: now}} {
						if err := f.db.Create(row).Error; err != nil {
							t.Fatal(err)
						}
					}
					revisionID = "terminal-output"
				}
				if kind == "route" {
					if err := f.db.Create(&orm.WorkflowRouteDecision{ID: "route", SessionID: "descriptor-session", FromStepID: "source", ActivatedJSON: json.RawMessage(`["consume"]`), PrunedJSON: json.RawMessage(`[]`), BypassedJSON: json.RawMessage(`[]`), WitnessJSON: json.RawMessage(`[{"revision_id":"descriptor-artifact","material_id":"unknown-slot"}]`), Validity: "effective", CreatedAt: now}).Error; err != nil {
						t.Fatal(err)
					}
				} else if err := f.db.Create(&orm.WorkflowAttemptInputBinding{ID: "live-binding", SessionID: "descriptor-session", AttemptID: "live", MaterialID: "input", MaterialRevisionID: revisionID, SourceType: "artifact", CreatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
				descriptorAlgorithm(t, 200, descriptorMarkdown)
				graphSnapshot := func() string {
					var attempts []orm.WorkflowSessionStep
					var decisions []orm.WorkflowRouteDecision
					if err := f.db.Order("id").Find(&attempts).Error; err != nil {
						t.Fatal(err)
					}
					if err := f.db.Order("id").Find(&decisions).Error; err != nil {
						t.Fatal(err)
					}
					return descriptorSnapshot(t, f) + mustDescriptorJSON(t, []any{attempts, decisions})
				}
				for _, blocked := range []bool{true, false} {
					if !blocked {
						if err := f.db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "live").Update("status", "succeeded").Error; err != nil {
							t.Fatal(err)
						}
					}
					before := graphSnapshot()
					records := descriptorRecords(t, f.read(t.Context(), endpoint, "descriptor-owner"))
					found := false
					for _, record := range records {
						if record["artifact_id"] == "descriptor-artifact" {
							found = true
							requireDescriptor(t, record, "markdown", !blocked, true)
						}
					}
					if !found {
						t.Fatal("source artifact missing")
					}
					if graphSnapshot() != before {
						t.Error("read-only capability check mutated graph/state/value")
					}
				}
			})
		}
	}
}

func TestDocumentDescriptorEmptySessionAndGeneratedNullable(t *testing.T) {
	f := newDescriptorFixture(t)
	if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("dismissed", true).Error; err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range descriptorRoutes[2:4] {
		w := f.read(t.Context(), endpoint, "descriptor-owner")
		if w.Code != 200 {
			t.Fatalf("status=%d", w.Code)
		}
		var body struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if session, exists := body.Data["session"]; !exists || session != nil {
			t.Errorf("empty session=%#v", body.Data)
		}
	}
	raw, err := os.ReadFile("../../frontend/src/api/generated/core-client/api.ts")
	if err != nil {
		t.Fatal(err)
	}
	contract := regexp.MustCompile(`(?s)export interface WorkflowSessionReadData\s*\{([^}]*)\}`).FindSubmatch(raw)
	if len(contract) != 2 || !regexp.MustCompile(`'session':\s*SessionDTO\s*\|\s*null\s*;`).Match(contract[1]) {
		t.Fatal("generated WorkflowSessionReadData must require session: SessionDTO | null")
	}
}
