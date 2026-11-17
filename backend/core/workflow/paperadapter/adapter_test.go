package paperadapter

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/mux"
	"io"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflow/graphengine"
	"lazymind/core/workflowtool"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func toolResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func workflowToolFixture(t *testing.T, transport transportFunc) (*workflowtool.Service, workflowtool.CallInput) {
	t.Helper()
	db := orm.OpenTestDB(t).DB
	if err := db.AutoMigrate(&orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowOutbox{},
		&orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}, &orm.WorkflowCommand{},
		&orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{}, &orm.WorkflowInputBinding{}, &orm.WorkflowSlotRevision{}); err != nil {
		t.Fatal(err)
	}
	now, expires := time.Now().UTC(), time.Now().UTC().Add(time.Hour)
	graph := graphengine.CompiledStateGraph{Nodes: map[string]graphengine.CompiledNode{"write_outline": {
		ID: "write_outline", Prompt: "Generate report", Execution: &graphengine.StepExecution{
			Version: "1",
			Skills:  []string{"scenario/external.md"}, Tools: []graphengine.ExecutionTool{{Name: "paper_prepare_inputs", Source: "package"}},
		},
	}}}
	for _, row := range []any{
		&orm.WorkflowRevision{ID: "pinned-revision", WorkflowResourceID: "resource", RevisionNo: 1, CompiledGraph: graph.JSON(), TreeHash: strings.Repeat("a", 64), CreatedAt: now},
		&orm.WorkflowSession{ID: "run", ConversationID: "chat", WorkflowRevisionID: "pinned-revision", ControllerHost: "external-agent", ControlProtocol: controlpolicy.Protocol, CreateUserID: "owner", Status: "active"},
		&orm.WorkflowSessionStep{ID: "attempt", TaskID: "attempt", SessionID: "run", StepID: "write_outline", ExecutorHost: "external-agent", Status: "running", Validity: "effective", LeaseToken: "lease", LeaseExpiresAt: &expires},
		&orm.WorkflowOutbox{ID: "outbox", AttemptID: "attempt", SessionID: "run", Status: "pending", PayloadJSON: json.RawMessage(`{"operation":"execute"}`)},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []struct{ revision, path, content string }{
		{"pinned-revision", "scenario/external.md", "Pinned external instructions"},
		{"pinned-revision", "workflow.yaml", "id: portable\nslots: []\n"},
		{"pinned-revision", "scripts/tools.py", "# pinned tool binding\n"},
		{"new-head", "scripts/tools.py", "# must not execute head\n"},
	} {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(file.content)))
		if err := db.Create(&orm.WorkflowBlob{Hash: hash, Content: []byte(file.content), Size: int64(len(file.content))}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowRevisionEntry{RevisionID: file.revision, Path: file.path, EntryType: "file", BlobHash: &hash}).Error; err != nil {
			t.Fatal(err)
		}
	}
	bindingHash := fmt.Sprintf("%x", sha256.Sum256([]byte(Binding)))
	if err := db.Create(&orm.WorkflowBlob{Hash: bindingHash, Content: []byte(Binding), Size: int64(len(Binding))}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevisionEntry{RevisionID: "pinned-revision", Path: "scripts/agent_tools.py", EntryType: "file", BlobHash: &bindingHash}).Error; err != nil {
		t.Fatal(err)
	}
	return &workflowtool.Service{DB: db, Endpoint: "http://workflow-tools.invalid", HTTPClient: &http.Client{Transport: transport}}, workflowtool.CallInput{
		ExecutionHandle: "lease", ToolName: "paper_prepare_inputs", CommandID: "call-1", Arguments: map[string]any{"file": "artifact-ref"},
	}
}

func requireControlError(t *testing.T, err error, code string) {
	t.Helper()
	var controlled *controlstore.Error
	if !errors.As(err, &controlled) || controlled.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestPaperInternalLivenessIsBoundToCurrentLease(t *testing.T) {
	service, _ := workflowToolFixture(t, func(*http.Request) (*http.Response, error) { return toolResponse(`{}`), nil })
	var revision orm.WorkflowRevision
	if err := service.DB.First(&revision, "id = ?", "pinned-revision").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	graph.Nodes["write_outline"].Execution.OutputValidator = graphengine.PaperAgentOutputValidator
	graph.Nodes["write_outline"].Execution.Tools = []graphengine.ExecutionTool{{Name: "paper_prepare_inputs", Source: "package"}}
	service.Adapters = map[string]workflowtool.ToolAdapter{graphengine.PaperAgentOutputValidator: &Adapter{DB: service.DB, ReadKnowledge: service.ReadSelectedKnowledge}}
	if err := service.DB.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-test")
	call := func(token, lease string, extra string) int {
		payload, _ := json.Marshal(map[string]any{"tool_name": "execution_status", "arguments": map[string]any{}, "lease_hash": lease})
		request := httptest.NewRequest(http.MethodPost, "/internal/workflow-attempts/attempt/paper-data", strings.NewReader(string(payload)+extra))
		request.Header.Set("X-LazyMind-Internal-Token", token)
		request = mux.SetURLVars(request, map[string]string{"attempt_id": "attempt"})
		response := httptest.NewRecorder()
		(&Adapter{DB: service.DB, ReadKnowledge: service.ReadSelectedKnowledge}).Data(response, request)
		return response.Code
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("lease")))
	if got := call("internal-test", hash, ""); got != http.StatusOK {
		t.Fatalf("active execution rejected: %d", got)
	}
	if call("bad", hash, "") != http.StatusUnauthorized {
		t.Fatal("internal identity bypass")
	}
	if call("internal-test", "different", "") != http.StatusConflict {
		t.Fatal("reclaimed lease survived")
	}
	if call("internal-test", hash, " {}") != http.StatusUnprocessableEntity {
		t.Fatal("trailing JSON accepted")
	}
	if err := service.DB.Model(&orm.WorkflowSession{}).Where("id = ?", "run").Update("status", "stopped").Error; err != nil {
		t.Fatal(err)
	}
	if call("internal-test", hash, "") != http.StatusConflict {
		t.Fatal("stopped execution reported active")
	}
}

func TestPaperToolBudgetIsPinnedAtomicAndIdempotent(t *testing.T) {
	var executed atomic.Int32
	service, input := workflowToolFixture(t, func(r *http.Request) (*http.Response, error) {
		executed.Add(1)
		return toolResponse(`{"result":{"actual":true}}`), nil
	})
	var revision orm.WorkflowRevision
	if err := service.DB.First(&revision, "id = ?", "pinned-revision").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	graph.Nodes["write_outline"].Execution.OutputValidator = graphengine.PaperAgentOutputValidator
	graph.Nodes["write_outline"].Execution.Tools = []graphengine.ExecutionTool{{Name: "paper_prepare_inputs", Source: "package"}}
	service.Adapters = map[string]workflowtool.ToolAdapter{graphengine.PaperAgentOutputValidator: &Adapter{DB: service.DB, ReadKnowledge: service.ReadSelectedKnowledge}}
	graph.Runtime.ExecutionLimits = map[string]graphengine.ExecutionLimit{"write_outline": {Rounds: 32, Timeout: 1200, ToolCalls: map[string]int{"paper_prepare_inputs": 2}}}
	if err := service.DB.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	input.ToolName = "paper_prepare_inputs"
	contract, err := executor.AuthorizeExternalExecution(t.Context(), service.DB, "owner", "run", "attempt", "lease")
	if err != nil {
		t.Fatal(err)
	}
	if contract.Execution.Limits == nil || contract.Execution.Limits.ToolCalls["paper_prepare_inputs"] != 2 {
		t.Fatal("worker contract lost pinned limits")
	}
	first, err := service.Call(t.Context(), "owner", "run", "attempt", input)
	if err != nil || first.Error != nil {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	replay, err := service.Call(t.Context(), "owner", "run", "attempt", input)
	if err != nil || replay.Error != nil || executed.Load() != 1 {
		t.Fatalf("replay spent quota or executed: %+v %v %d", replay, err, executed.Load())
	}
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			call := input
			call.CommandID = fmt.Sprintf("concurrent-%d", i)
			_, err := service.Call(t.Context(), "owner", "run", "attempt", call)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else {
			requireControlError(t, err, "TOOL_BUDGET_EXHAUSTED")
		}
	}
	if accepted != 1 || executed.Load() != 2 {
		t.Fatalf("concurrent budget overrun: accepted=%d actual=%d", accepted, executed.Load())
	}
	_, err = service.Call(t.Context(), "owner", "run", "attempt", input)
	if err != nil || executed.Load() != 2 {
		t.Fatalf("budget exhausted blocked existing receipt: %v", err)
	}
}

func TestPaperToolChecksContractNameBindingAndPrivateContext(t *testing.T) {
	for _, change := range []string{"valid", "unknown-name", "wrong-contract", "wrong-binding", "forged-context"} {
		t.Run(change, func(t *testing.T) {
			service, input := workflowToolFixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid tool executed"); return nil, nil })
			var revision orm.WorkflowRevision
			service.DB.First(&revision, "id = ?", "pinned-revision")
			var graph graphengine.CompiledStateGraph
			if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
				t.Fatal(err)
			}
			graph.Nodes["write_outline"].Execution.OutputValidator = graphengine.PaperAgentOutputValidator
			if err := service.DB.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
				t.Fatal(err)
			}
			contract, err := executor.AuthorizeExternalExecution(t.Context(), service.DB, "owner", "run", "attempt", "lease")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "unknown-name":
				input.ToolName = "paper_unregistered"
			case "wrong-contract":
				contract.Execution.OutputValidator = "ppt-images-v1"
			case "wrong-binding":
				if err := service.DB.Model(&orm.WorkflowBlob{}).Where("hash = ?", fmt.Sprintf("%x", sha256.Sum256([]byte(Binding)))).Update("content", []byte("forged binding")).Error; err != nil {
					t.Fatal(err)
				}
			case "forged-context":
				input.Arguments["__paper_context"] = map[string]any{"owner": "other"}
			}
			value, err := (&Adapter{DB: service.DB}).Prepare(t.Context(), contract, input)
			if change == "valid" {
				if err != nil || value["revision"] != contract.WorkflowRevision || value["attempt_id"] != "attempt" {
					t.Fatalf("trusted context missing: %v %v", value, err)
				}
			} else if err == nil {
				t.Fatal("untrusted invocation accepted")
			}
		})
	}
}

func TestTrustedContextDoesNotEnterBusinessArgumentsOrReplayHash(t *testing.T) {
	executed := 0
	service, input := workflowToolFixture(t, func(r *http.Request) (*http.Response, error) {
		executed++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["trusted_context"].(map[string]any)["attempt_id"] != "attempt" {
			t.Fatal("missing private context")
		}
		if _, found := body["arguments"].(map[string]any)["__paper_context"]; found {
			t.Fatal("private context leaked into business arguments")
		}
		return toolResponse(`{"result":{"actual":true}}`), nil
	})
	var revision orm.WorkflowRevision
	service.DB.First(&revision, "id = ?", "pinned-revision")
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	graph.Nodes["write_outline"].Execution.OutputValidator = graphengine.PaperAgentOutputValidator
	graph.Runtime.ExecutionLimits = map[string]graphengine.ExecutionLimit{"write_outline": {Rounds: 32}}
	if err := service.DB.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	service.Adapters = map[string]workflowtool.ToolAdapter{graphengine.PaperAgentOutputValidator: &Adapter{DB: service.DB}}
	if _, err := service.Call(t.Context(), "owner", "run", "attempt", input); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Model(&orm.WorkflowOutbox{}).Where("attempt_id = ?", "attempt").Update("payload_json", json.RawMessage(`{"user_request":"a changed service context"}`)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Call(t.Context(), "owner", "run", "attempt", input); err != nil {
		t.Fatal(err)
	}
	if executed != 1 {
		t.Fatal("context-only change re-executed the business operation")
	}
}

func TestPaperHostSourceRegistrationKeepsPinnedBindingCompatibility(t *testing.T) {
	builtin, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "workflows", "paper-writing", "scripts", "agent_tools.py"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, binding, tool string
		allowed             bool
	}{
		{"current registration", string(builtin), "paper_import_sources", true},
		{"legacy preparation", legacyBinding, "paper_prepare_inputs", true},
		{"legacy cannot add registration", legacyBinding, "paper_import_sources", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, input := workflowToolFixture(t, func(*http.Request) (*http.Response, error) {
				t.Fatal("binding validation unexpectedly executed a provider")
				return nil, nil
			})
			var revision orm.WorkflowRevision
			if err := service.DB.First(&revision, "id = ?", "pinned-revision").Error; err != nil {
				t.Fatal(err)
			}
			var graph graphengine.CompiledStateGraph
			if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
				t.Fatal(err)
			}
			node := graph.Nodes["write_outline"]
			node.Execution.OutputValidator = graphengine.PaperAgentOutputValidator
			node.Execution.Tools = []graphengine.ExecutionTool{{Name: tc.tool, Source: "package"}}
			graph.Nodes["write_outline"] = node
			if err := service.DB.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
				t.Fatal(err)
			}
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(tc.binding)))
			if err := service.DB.Where("hash = ?", hash).FirstOrCreate(&orm.WorkflowBlob{
				Hash: hash, Content: []byte(tc.binding), Size: int64(len(tc.binding))}).Error; err != nil {
				t.Fatal(err)
			}
			if err := service.DB.Model(&orm.WorkflowRevisionEntry{}).Where("revision_id = ? AND path = ?",
				"pinned-revision", "scripts/agent_tools.py").Update("blob_hash", hash).Error; err != nil {
				t.Fatal(err)
			}
			contract, err := executor.AuthorizeExternalExecution(t.Context(), service.DB, "owner", "run", "attempt", "lease")
			if err != nil {
				t.Fatal(err)
			}
			input.ToolName = tc.tool
			actual, err := (&Adapter{DB: service.DB}).Prepare(t.Context(), contract, input)
			if tc.allowed {
				if err != nil {
					t.Fatal(err)
				}
				names, ok := actual["available_tools"].([]string)
				if !ok || len(names) != 1 || names[0] != tc.tool {
					t.Fatalf("wrong pinned tools: %#v", actual["available_tools"])
				}
			} else if err == nil {
				t.Fatal("legacy binding gained an undeclared import")
			}
		})
	}
}
