package workflowtool

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func toolResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func workflowToolFixture(t *testing.T, transport transportFunc) (*Service, CallInput) {
	t.Helper()
	db := orm.OpenTestDB(t).DB
	if err := db.AutoMigrate(&orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowOutbox{},
		&orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}, &orm.WorkflowCommand{},
		&orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{}, &orm.WorkflowInputBinding{}, &orm.WorkflowSlotRevision{}); err != nil {
		t.Fatal(err)
	}
	now, expires := time.Now().UTC(), time.Now().UTC().Add(time.Hour)
	graph := graphengine.CompiledStateGraph{Nodes: map[string]graphengine.CompiledNode{"write": {
		ID: "write", Prompt: "native", ExternalExecution: &graphengine.ExternalStepExecution{
			Version: "1", ValidationOwner: "external-agent", Prompt: "Generate externally",
			Skills: []string{"scenario/external.md"}, Tools: []graphengine.ExternalTool{{Name: "inspect_output", Source: "package"}},
		},
	}}}
	for _, row := range []any{
		&orm.WorkflowRevision{ID: "pinned-revision", WorkflowResourceID: "resource", RevisionNo: 1, CompiledGraph: graph.JSON(), TreeHash: strings.Repeat("a", 64), CreatedAt: now},
		&orm.WorkflowSession{ID: "run", ConversationID: "chat", WorkflowRevisionID: "pinned-revision", ControllerHost: "external-agent", ControlProtocol: controlpolicy.Protocol, CreateUserID: "owner", Status: "active"},
		&orm.WorkflowSessionStep{ID: "attempt", TaskID: "attempt", SessionID: "run", StepID: "write", ExecutorHost: "external-agent", Status: "running", Validity: "effective", LeaseToken: "lease", LeaseExpiresAt: &expires},
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
	return &Service{DB: db, Endpoint: "http://workflow-tools.invalid", HTTPClient: &http.Client{Transport: transport}}, CallInput{
		ExecutionHandle: "lease", ToolName: "inspect_output", CommandID: "call-1", Arguments: map[string]any{"file": "artifact-ref"},
	}
}

func requireControlError(t *testing.T, err error, code string) {
	t.Helper()
	var controlled *controlstore.Error
	if !errors.As(err, &controlled) || controlled.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestWorkflowToolPinnedPackageAndIdempotentReceipt(t *testing.T) {
	var calls atomic.Int32
	service, input := workflowToolFixture(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.URL.Path != "/api/workflow/external-tools/call" {
			t.Fatalf("wrong tool endpoint: %s", request.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["revision_id"] != "pinned-revision" || payload["step_id"] != "write" || payload["tool_name"] != "inspect_output" || payload["llm_config"] != nil {
			t.Fatalf("tool request lost pinned execution scope: %+v", payload)
		}
		files := payload["package"].(map[string]any)["files"].(map[string]any)
		content, err := base64.StdEncoding.DecodeString(files["scripts/tools.py"].(string))
		if err != nil || string(content) != "# pinned tool binding\n" {
			t.Fatal("tool request used mutable HEAD")
		}
		return toolResponse(`{"result":{"pages":1}}`), nil
	})
	first, err := service.Call(t.Context(), "owner", "run", "attempt", input)
	if err != nil || first.Error != nil {
		t.Fatalf("call failed: %+v %v", first, err)
	}
	replay, err := service.Call(t.Context(), "owner", "run", "attempt", input)
	if err != nil || replay.Error != nil || calls.Load() != 1 {
		t.Fatalf("receipt replay repeated tool: %+v %v calls=%d", replay, err, calls.Load())
	}
	input.Arguments = map[string]any{"file": "different-ref"}
	_, err = service.Call(t.Context(), "owner", "run", "attempt", input)
	requireControlError(t, err, "COMMAND_CONFLICT")
	if calls.Load() != 1 {
		t.Fatal("changed arguments executed under an existing command")
	}
}

func TestWorkflowToolAdmissionPreventsUnauthorizedCalls(t *testing.T) {
	var calls atomic.Int32
	service, input := workflowToolFixture(t, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return toolResponse(`{"result":"unexpected"}`), nil
	})
	for _, test := range []struct {
		name, owner, handle, tool, source string
		ctx                               context.Context
	}{
		{"owner", "other", "lease", "inspect_output", "", t.Context()},
		{"lease", "owner", "old-lease", "inspect_output", "", t.Context()},
		{"tool", "owner", "lease", "native_generator", "", t.Context()},
		{"source", "owner", "lease", "inspect_output", "global", t.Context()},
		{"conversation", "owner", "lease", "inspect_output", "", workflowstore.WithConversationScope(t.Context(), "other-chat")},
	} {
		t.Run(test.name, func(t *testing.T) {
			call := input
			call.ExecutionHandle, call.ToolName, call.Source = test.handle, test.tool, test.source
			if _, err := service.Call(test.ctx, test.owner, "run", "attempt", call); err == nil {
				t.Fatal("unauthorized call accepted")
			}
		})
	}
	var receipts int64
	if err := service.DB.Model(&orm.WorkflowCommand{}).Count(&receipts).Error; err != nil || receipts != 0 || calls.Load() != 0 {
		t.Fatalf("admission had side effects: receipts=%d calls=%d err=%v", receipts, calls.Load(), err)
	}
}

func TestWorkflowToolListShowsOnlyDeclaredTools(t *testing.T) {
	service, _ := workflowToolFixture(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/workflow/external-tools/list" {
			t.Fatal("list attempted execution")
		}
		return toolResponse(`{"tools":[{"name":"inspect_output","description":"Inspect only","input_schema":{"type":"object"},"available":true},{"name":"hidden_internal_generator"}]}`), nil
	})
	list, err := service.List(t.Context(), "owner", "run", "attempt", "lease")
	if err != nil || len(list) != 1 || list[0].Name != "inspect_output" || list[0].Source != "package" || !list[0].Available {
		t.Fatalf("list exposed undeclared tools: %+v %v", list, err)
	}
}

func TestWorkflowToolListRetainsUnavailableAdapter(t *testing.T) {
	service, _ := workflowToolFixture(t, func(*http.Request) (*http.Response, error) {
		return toolResponse(`{"tools":[{"name":"inspect_output","available":false,"reason":"Install the rendering dependency"}]}`), nil
	})
	list, err := service.List(t.Context(), "owner", "run", "attempt", "lease")
	if err != nil || len(list) != 1 || list[0].Available || list[0].Reason == "" {
		t.Fatalf("unavailable adapter was advertised ready: %+v %v", list, err)
	}
}

func TestWorkflowToolStopOrReclaimDuringHTTPFencesDelivery(t *testing.T) {
	for _, action := range []string{"stop", "reclaim"} {
		t.Run(action, func(t *testing.T) {
			var service *Service
			var calls atomic.Int32
			transport := func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				if action == "stop" {
					if err := service.DB.Model(&orm.WorkflowSession{}).Where("id = ?", "run").Update("status", "stopped").Error; err != nil {
						t.Fatal(err)
					}
				} else if err := service.DB.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt").Update("lease_token", "replacement-lease").Error; err != nil {
					t.Fatal(err)
				}
				return toolResponse(`{"result":"must not be delivered"}`), nil
			}
			var input CallInput
			service, input = workflowToolFixture(t, transport)
			result, err := service.Call(t.Context(), "owner", "run", "attempt", input)
			if err != nil || result.Error == nil || result.Error.Code != "EXECUTION_FENCED" || result.Result != nil {
				t.Fatalf("late tool result delivered: %+v %v", result, err)
			}
			if _, err := service.Call(t.Context(), "owner", "run", "attempt", input); err == nil || calls.Load() != 1 {
				t.Fatal("stale worker replay bypassed current authorization")
			}
		})
	}
}

func TestWorkflowToolConcurrentDuplicateDoesNotReexecute(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	service, input := workflowToolFixture(t, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		<-release
		return toolResponse(`{"result":"done"}`), nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := service.Call(t.Context(), "owner", "run", "attempt", input)
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("first call failed before HTTP: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("tool call never started")
	}
	_, err := service.Call(t.Context(), "owner", "run", "attempt", input)
	var pending *ToolError
	if !errors.As(err, &pending) || pending.Code != "TOOL_CALL_IN_PROGRESS" || calls.Load() != 1 {
		close(release)
		t.Fatalf("duplicate reexecuted tool: %v calls=%d", err, calls.Load())
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowToolRejectsCorruptPinnedPackage(t *testing.T) {
	var calls atomic.Int32
	service, input := workflowToolFixture(t, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return toolResponse(`{"result":"unexpected"}`), nil
	})
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("# pinned tool binding\n")))
	if err := service.DB.Model(&orm.WorkflowBlob{}).Where("hash = ?", hash).Update("content", []byte("changed body")).Error; err != nil {
		t.Fatal(err)
	}
	result, err := service.Call(t.Context(), "owner", "run", "attempt", input)
	if err != nil || result.Error == nil || calls.Load() != 0 {
		t.Fatalf("corrupt package was sent to tool execution: %+v %v", result, err)
	}
}
