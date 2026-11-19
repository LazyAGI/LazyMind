package executor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

func externalExecutionDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := executorComponentDB(t, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowOutbox{},
		&orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}, &orm.WorkflowSlotRevision{})
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	policy := &graphengine.StepExecution{Version: "1",
		Skills: []string{"scenario/shared.md"}, Constraints: []string{"Check rendered output"},
		Tools: []graphengine.ExecutionTool{{Name: "inspect_output", Source: "package"}, {Name: "builtin:image_generator", Source: "global"}}}
	graph := graphengine.CompiledStateGraph{Nodes: map[string]graphengine.CompiledNode{"write": {
		ID: "write", Prompt: "Generate and validate the result", ToolsOnly: true, TerminalToolsOnly: true,
		LegacyTools: []string{"native_generator"}, TerminalTools: []string{"native_generator"},
		Acceptance: []string{"Preserve source facts"}, Outputs: []string{"result"}, RequiredOutputs: []string{"result"}, Execution: policy,
	}}}
	for _, model := range []any{
		&orm.WorkflowRevision{ID: "pinned", WorkflowResourceID: "resource", RevisionNo: 1, CompiledGraph: graph.JSON(), CreatedAt: now},
		&orm.WorkflowSession{ID: "run", ConversationID: "chat", WorkflowRevisionID: "pinned", CreateUserID: "owner", ControllerHost: "external-agent", ControlProtocol: controlpolicy.Protocol, Status: "active"},
		&orm.WorkflowSessionStep{ID: "attempt", TaskID: "attempt", SessionID: "run", StepID: "write", ExecutorHost: "external-agent", Status: "running", Validity: "effective", LeaseToken: "valid-handle", LeaseExpiresAt: &expires},
		&orm.WorkflowOutbox{ID: "outbox", AttemptID: "attempt", SessionID: "run", Status: "pending", PayloadJSON: json.RawMessage(`{"objective":"User objective","operation":"execute"}`)},
	} {
		if err := db.Create(model).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []struct{ revision, path, hash, content string }{
		{"pinned", "scenario/shared.md", "pinned-skill", "Pinned instructions"},
		{"new-head", "scenario/shared.md", "new-skill", "New head must not replace pinned instructions"},
		{"pinned", "workflow.yaml", "manifest", "slots:\n  - {id: result, cardinality: single}\n"},
	} {
		file.hash = fmt.Sprintf("%x", sha256.Sum256([]byte(file.content)))
		if err := db.Create(&orm.WorkflowBlob{Hash: file.hash, Content: []byte(file.content), Size: int64(len(file.content))}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowRevisionEntry{RevisionID: file.revision, Path: file.path, EntryType: "file", BlobHash: &file.hash}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestExternalSkillCorruptionFailsClosed(t *testing.T) {
	db := externalExecutionDB(t)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("Pinned instructions")))
	if err := db.Model(&orm.WorkflowBlob{}).Where("hash = ?", hash).Update("content", []byte("Corrupted instructions")).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (DBContextLoader{DB: db}).LoadAttemptContext(t.Context(), "attempt"); err == nil {
		t.Fatal("skill content no longer matching its pinned hash was accepted")
	}
}

func TestSharedContractCompactsOnlyExactPromptObjectives(t *testing.T) {
	for _, test := range []struct {
		name, prompt, objective, wireObjective, reference string
	}{
		{"same", "Pinned prompt", "Pinned prompt", "", "#/prompt"},
		{"refinement", "Pinned prompt", "Pinned prompt\n\nRuntime objective:\nKeep all 4 pages", "Keep all 4 pages", "#/prompt"},
		{"empty-refinement", "Pinned prompt", "Pinned prompt\n\nRuntime objective:\n", "Pinned prompt\n\nRuntime objective:\n", ""},
		{"unrelated", "Pinned prompt", "Different objective", "Different objective", ""},
		{"templated", "Read {{user_input}}", "Read the source", "Read the source", ""},
		{"empty", "Pinned prompt", "", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := AttemptContext{Prompt: test.prompt, Objective: test.objective,
				Execution: &ExecutionContract{StepExecution: graphengine.StepExecution{Version: "1"}}}
			encoded, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(encoded, &wire); err != nil {
				t.Fatal(err)
			}
			objective, _ := wire["objective"].(string)
			reference, _ := wire["objective_ref"].(string)
			if objective != test.wireObjective || reference != test.reference {
				t.Fatalf("objective/ref changed: %q %q", objective, reference)
			}
			var restored AttemptContext
			if err := json.Unmarshal(encoded, &restored); err != nil || restored.Objective != original.Objective || restored.Prompt != original.Prompt {
				t.Fatalf("objective failed to round trip: %q %v", restored.Objective, err)
			}
			again, err := json.Marshal(restored)
			if err != nil || string(again) != string(encoded) {
				t.Fatalf("repeated serialization changed the shared contract: %v", err)
			}
		})
	}
}

func TestSharedExecutionLoadsIdenticalBusinessContractForBothExecutors(t *testing.T) {
	db := externalExecutionDB(t)
	compiled := graphengine.Compile(`
id: shared
runtime:
  execution:
    version: '1'
    skills: [scenario/shared.md]
    constraints: [Keep exact requested scope]
    steps:
      write:
        tools: [{name: inspect_output, source: package}]
slots: [{id: result, type: file}]
steps: [{id: write}]
`, `
transitions:
  __start__: [{to: write}]
  write: [{to: __end__}]
steps:
  write:
    prompt: Create the requested file with the user's exact content.
    acceptance_criteria: [The actual file preserves the requested content]
    tools: [native_generator]
    terminal_tools: [native_generator]
    tools_only: true
    outputs: [{material: result, required: true}]
`, "", graphengine.ProfilePublish)
	if !compiled.Valid {
		t.Fatalf("shared compile: %+v", compiled.Diagnostics)
	}
	if err := db.Model(&orm.WorkflowRevision{}).Where("id = ?", "pinned").Update("compiled_graph", compiled.Graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	loader := DBContextLoader{DB: db}
	external, err := loader.LoadAttemptContext(t.Context(), "attempt")
	if err != nil || external.Execution == nil || len(external.Execution.SkillFiles) != 1 || external.Execution.SkillFiles[0].Content != "Pinned instructions" {
		t.Fatalf("external shared context: %+v %v", external, err)
	}
	// Frozen contexts are persisted too. A later native load must hydrate the
	// objective while preserving the same shared contract for native execution.
	external.Objective = external.Prompt + "\n\nRuntime objective:\nKeep all four pages"
	frozen, err := json.Marshal(external)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowOutbox{}).Where("attempt_id = ?", "attempt").Update("payload_json", frozen).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt").Update("executor_host", "lazymind").Error; err != nil {
		t.Fatal(err)
	}
	native, err := loader.LoadAttemptContext(t.Context(), "attempt")
	if err != nil || native.Execution == nil || !native.ToolsOnly || external.ToolsOnly || len(external.LegacyTools) != 0 {
		t.Fatalf("native adapter/shared context: %+v %v", native, err)
	}
	if native.Prompt != external.Prompt || native.Objective != external.Objective || !reflect.DeepEqual(native.Execution, external.Execution) || !reflect.DeepEqual(native.Acceptance, external.Acceptance) || !reflect.DeepEqual(native.RequiredOutputs, external.RequiredOutputs) {
		t.Fatal("executors received different business rules, Skills or outputs")
	}
	encoded, err := json.Marshal(external)
	if err != nil {
		t.Fatal(err)
	}
	for _, instruction := range []string{"Pinned instructions", external.Prompt, "Keep exact requested scope"} {
		if strings.Count(string(encoded), instruction) != 1 {
			t.Fatalf("shared instruction must appear once on the wire: %q", instruction)
		}
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if _, exists := wire["external_execution"]; exists {
		t.Fatal("shared contract emitted a separate external execution profile")
	}
	var restored AttemptContext
	if err := json.Unmarshal(encoded, &restored); err != nil || !reflect.DeepEqual(restored.Execution, external.Execution) {
		t.Fatalf("shared contract failed to round trip: %v", err)
	}
	if !reflect.DeepEqual(restored.Acceptance, external.Acceptance) || !reflect.DeepEqual(restored.RequiredOutputs, external.RequiredOutputs) {
		t.Fatal("shared wire contract lost top-level acceptance criteria or required outputs")
	}
	nativeJSON, err := json.Marshal(native)
	if err != nil || strings.Contains(string(nativeJSON), "external_execution") || !strings.Contains(string(nativeJSON), "objective_ref") || strings.Count(string(nativeJSON), native.Prompt) != 1 || strings.Count(string(nativeJSON), "Pinned instructions") != 1 {
		t.Fatalf("native wire contract changed: %v", err)
	}
	if err := db.Where("revision_id = ? AND path = ?", "pinned", "scenario/shared.md").Delete(&orm.WorkflowRevisionEntry{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := loader.LoadAttemptContext(t.Context(), "attempt"); err == nil {
		t.Fatal("native execution silently skipped a missing shared Skill")
	}
}

func TestExternalToolAuthorizationUsesPinnedAllowlistAndFences(t *testing.T) {
	db := externalExecutionDB(t)
	value, tool, err := AuthorizeExternalToolCall(t.Context(), db, "owner", "run", "attempt", "valid-handle", "inspect_output")
	if err != nil || tool.Source != "package" || value.Metadata != nil || value.Execution == nil {
		t.Fatalf("authorized tool: %+v %+v %v", value, tool, err)
	}
	for _, test := range []struct {
		name, owner, handle, tool string
		ctx                       context.Context
	}{
		{"owner", "other", "valid-handle", "inspect_output", t.Context()},
		{"lease", "owner", "stale-handle", "inspect_output", t.Context()},
		{"undeclared", "owner", "valid-handle", "native_generator", t.Context()},
		{"conversation", "owner", "valid-handle", "inspect_output", workflowstore.WithConversationScope(t.Context(), "other-chat")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := AuthorizeExternalToolCall(test.ctx, db, test.owner, "run", "attempt", test.handle, test.tool); err == nil {
				t.Fatal("unauthorized tool call accepted")
			}
		})
	}
	for _, state := range []string{"stopped", "active"} {
		if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "run").Update("status", state).Error; err != nil {
			t.Fatal(err)
		}
		if state == "active" {
			if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt").Update("validity", "stale").Error; err != nil {
				t.Fatal(err)
			}
		}
		if _, err := AuthorizeExternalExecution(t.Context(), db, "owner", "run", "attempt", "valid-handle"); err == nil {
			t.Fatalf("%s/stale execution retained tool authority", state)
		}
	}
}

func TestSharedSkillMissingFailsClosedForBothExecutors(t *testing.T) {
	db := externalExecutionDB(t)
	if err := db.Where("revision_id = ? AND path = ?", "pinned", "scenario/shared.md").Delete(&orm.WorkflowRevisionEntry{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (DBContextLoader{DB: db}).LoadAttemptContext(t.Context(), "attempt"); err == nil {
		t.Fatal("external execution accepted missing pinned skill")
	}
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt").Update("executor_host", "lazymind").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (DBContextLoader{DB: db}).LoadAttemptContext(t.Context(), "attempt"); err == nil {
		t.Fatal("native execution accepted missing pinned skill")
	}
}
