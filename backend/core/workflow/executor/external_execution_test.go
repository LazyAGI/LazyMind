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
	policy := &graphengine.ExternalStepExecution{Version: "1", ValidationOwner: "external-agent", Prompt: "Generate and validate with the external agent",
		Skills: []string{"scenario/external.md"}, Constraints: []string{"Check rendered output"},
		Acceptance: []string{"One page allowed"}, RequiredOutputs: []string{"result"},
		Tools: []graphengine.ExternalTool{{Name: "inspect_output", Source: "package"}, {Name: "builtin:image_generator", Source: "global"}}}
	graph := graphengine.CompiledStateGraph{Nodes: map[string]graphengine.CompiledNode{"write": {
		ID: "write", Prompt: "Use the native generator", ToolsOnly: true, TerminalToolsOnly: true,
		LegacyTools: []string{"native_generator"}, TerminalTools: []string{"native_generator"},
		Acceptance: []string{"At least two pages"}, Outputs: []string{"result", "native_only"}, RequiredOutputs: []string{"native_only"}, ExternalExecution: policy,
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
		{"pinned", "scenario/external.md", "pinned-skill", "Pinned instructions"},
		{"new-head", "scenario/external.md", "new-skill", "New head must not replace pinned instructions"},
		{"pinned", "workflow.yaml", "manifest", "slots:\n  - {id: result, cardinality: single}\n  - {id: native_only, cardinality: single}\n"},
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

func TestSharedExecutionLoadsIdenticalBusinessContractForBothExecutors(t *testing.T) {
	db := externalExecutionDB(t)
	compiled := graphengine.Compile(`
id: shared
runtime:
  execution:
    version: '1'
    skills: [scenario/external.md]
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
	if err != nil || external.Execution == nil || external.ExternalExecution == nil || len(external.Execution.SkillFiles) != 1 || external.Execution.SkillFiles[0].Content != "Pinned instructions" {
		t.Fatalf("external shared context: %+v %v", external, err)
	}
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt").Update("executor_host", "lazymind").Error; err != nil {
		t.Fatal(err)
	}
	native, err := loader.LoadAttemptContext(t.Context(), "attempt")
	if err != nil || native.Execution == nil || native.ExternalExecution != nil || !native.ToolsOnly || external.ToolsOnly || len(external.LegacyTools) != 0 {
		t.Fatalf("native adapter/shared context: %+v %v", native, err)
	}
	if native.Prompt != external.Prompt || !reflect.DeepEqual(native.Execution, external.Execution) || !reflect.DeepEqual(native.Acceptance, external.Acceptance) || !reflect.DeepEqual(native.RequiredOutputs, external.RequiredOutputs) {
		t.Fatal("executors received different business rules, Skills or outputs")
	}
	if err := db.Where("revision_id = ? AND path = ?", "pinned", "scenario/external.md").Delete(&orm.WorkflowRevisionEntry{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := loader.LoadAttemptContext(t.Context(), "attempt"); err == nil {
		t.Fatal("native execution silently skipped a missing shared Skill")
	}
}

func TestExternalExecutionLoadsPinnedSkillsAndNativeKeepsOriginalContract(t *testing.T) {
	db := externalExecutionDB(t)
	loader := DBContextLoader{DB: db}
	value, err := loader.LoadAttemptContext(t.Context(), "attempt")
	if err != nil {
		t.Fatal(err)
	}
	if value.ExternalExecution == nil || len(value.ExternalExecution.SkillFiles) != 1 || value.ExternalExecution.SkillFiles[0].Content != "Pinned instructions" || !strings.HasPrefix(value.ExternalExecution.SkillFiles[0].SHA256, "sha256:") {
		t.Fatalf("skill snapshot missing: %+v", value.ExternalExecution)
	}
	if value.Prompt != value.ExternalExecution.Prompt || value.ToolsOnly || value.TerminalToolsOnly || len(value.LegacyTools) != 0 || len(value.TerminalTools) != 0 || value.RequiredOutputs[0] != "result" || value.Acceptance[0] != "One page allowed" || value.Objective != "User objective" {
		t.Fatalf("external contract retained native execution instructions: %+v", value)
	}
	if value.ExternalExecution.TaskSpecification != "Use the native generator" {
		t.Fatal("fixed business specification was lost; it must remain separate from the active external prompt")
	}
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt").Update("executor_host", "lazymind").Error; err != nil {
		t.Fatal(err)
	}
	native, err := loader.LoadAttemptContext(t.Context(), "attempt")
	if err != nil || native.ExternalExecution != nil || !native.ToolsOnly || native.Prompt != "Use the native generator" || native.RequiredOutputs[0] != "native_only" || native.Acceptance[0] != "At least two pages" {
		t.Fatalf("native contract changed: %+v %v", native, err)
	}
}

func TestExternalToolAuthorizationUsesPinnedAllowlistAndFences(t *testing.T) {
	db := externalExecutionDB(t)
	value, tool, err := AuthorizeExternalToolCall(t.Context(), db, "owner", "run", "attempt", "valid-handle", "inspect_output")
	if err != nil || tool.Source != "package" || value.Metadata != nil || value.ExternalExecution == nil {
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

func TestExternalSkillMissingFailsClosedWithoutAffectingNative(t *testing.T) {
	db := externalExecutionDB(t)
	if err := db.Where("revision_id = ? AND path = ?", "pinned", "scenario/external.md").Delete(&orm.WorkflowRevisionEntry{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (DBContextLoader{DB: db}).LoadAttemptContext(t.Context(), "attempt"); err == nil {
		t.Fatal("external execution accepted missing pinned skill")
	}
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt").Update("executor_host", "lazymind").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (DBContextLoader{DB: db}).LoadAttemptContext(t.Context(), "attempt"); err != nil {
		t.Fatal("native execution depended on an external skill", err)
	}
}
