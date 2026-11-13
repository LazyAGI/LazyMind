package workflowtool

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func TestWorkflowPDFPublicResourceScopeAndReplay(t *testing.T) {
	content := []byte("%PDF-1.7\npinned bytes")
	hash := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	var calls atomic.Int32
	service, input := workflowToolFixture(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/api/workflow/external-tools/list" {
			return toolResponse(`{"tools":[{"name":"pdf_read_text","available":true,"input_schema":{"type":"object","additionalProperties":false,"properties":{"content_base64":{"type":"string"},"filename":{"type":"string"},"start_page":{"type":"integer"}},"required":["content_base64","start_page"]}}]}`), nil
		}
		calls.Add(1)
		var payload struct {
			ToolName  string         `json:"tool_name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.ToolName != "pdf_read_text" || payload.Arguments["resource_ref"] != nil || payload.Arguments["content_base64"] != base64.StdEncoding.EncodeToString(content) || payload.Arguments["filename"] != "paper.pdf" {
			t.Fatalf("adapter did not receive exact pinned bytes and filename: %+v", payload)
		}
		return toolResponse(`{"result":{"page_count":1,"pages":[{"page":1,"text":"bounded evidence"}]}}`), nil
	})
	if err := service.DB.AutoMigrate(&orm.WorkflowInputResource{}); err != nil {
		t.Fatal(err)
	}
	var revision orm.WorkflowRevision
	if err := service.DB.First(&revision, "id = ?", "pinned-revision").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	node := graph.Nodes["write"]
	node.Execution.Tools = []graphengine.ExecutionTool{{Name: "pdf_read_text", Source: "package"}}
	graph.Nodes["write"] = node
	graph.MaterialCardinalities = map[string]string{"brief": "list"}
	if err := service.DB.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ id, owner string }{{"pdf", "owner"}, {"unbound", "owner"}, {"foreign", "other"}} {
		resource := orm.WorkflowInputResource{ID: item.id, OwnerUserID: item.owner, Name: "paper.pdf", Revision: 1,
			Content: content, Size: int64(len(content)), ContentHash: hash, MimeType: "application/pdf"}
		if err := service.DB.Create(&resource).Error; err != nil {
			t.Fatal(err)
		}
	}
	witness := func(id string) map[string]any {
		return map[string]any{"source_type": "input_resource", "source_id": id, "source_revision": "1", "content_hash": hash}
	}
	setInputs := func(inputs map[string]any, frozen bool) {
		t.Helper()
		payload := map[string]any{"operation": "execute", "inputs": inputs}
		if frozen {
			payload["metadata"] = map[string]string{"workflow_control_inputs_frozen": "true"}
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.DB.Model(&orm.WorkflowOutbox{}).Where("attempt_id = ?", "attempt").Update("payload_json", json.RawMessage(raw)).Error; err != nil {
			t.Fatal(err)
		}
	}
	setInputs(map[string]any{"brief": witness("pdf")}, true)
	input.ToolName = "pdf_read_text"
	input.Arguments = map[string]any{"resource_ref": "resource:pdf", "start_page": 1, "filename": "caller-label.pdf"}

	t.Run("catalog keeps XOR and all other required fields", func(t *testing.T) {
		catalog, err := service.List(t.Context(), "owner", "run", "attempt", "lease")
		if err != nil || len(catalog) != 1 || !catalog[0].Available {
			t.Fatalf("PDF catalog unavailable: %+v %v", catalog, err)
		}
		raw, _ := json.Marshal(catalog[0].InputSchema)
		var schema jsonschema.Schema
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, args := range []map[string]any{
			{"resource_ref": "resource:pdf", "start_page": 1},
			{"content_base64": "inline-bytes", "start_page": 1},
		} {
			if err := resolved.Validate(args); err != nil {
				t.Fatalf("valid file input rejected: %v", err)
			}
		}
		for _, args := range []map[string]any{
			{"start_page": 1}, {"resource_ref": "resource:pdf"},
			{"resource_ref": "resource:pdf", "content_base64": "also supplied", "start_page": 1},
			{"resource_ref": "/etc/passwd", "start_page": 1},
			{"resource_ref": "resource:pdf", "start_page": 1, "path": "/etc/passwd"},
		} {
			if err := resolved.Validate(args); err == nil {
				t.Fatalf("invalid public schema accepted: %+v", args)
			}
		}
	})

	t.Run("pinned bytes and immutable receipt", func(t *testing.T) {
		first, err := service.Call(t.Context(), "owner", "run", "attempt", input)
		if err != nil || first.Error != nil {
			t.Fatalf("resource call failed: %+v %v", first, err)
		}
		replay, err := service.Call(t.Context(), "owner", "run", "attempt", input)
		if err != nil || replay.Error != nil || calls.Load() != 1 {
			t.Fatalf("resource replay repeated adapter: %+v %v calls=%d", replay, err, calls.Load())
		}
		if input.Arguments["content_base64"] != nil || input.Arguments["filename"] != "caller-label.pdf" {
			t.Fatal("public request was replaced by hydrated server bytes")
		}
		changed := input
		changed.Arguments = map[string]any{"resource_ref": "resource:unbound", "start_page": 1}
		_, err = service.Call(t.Context(), "owner", "run", "attempt", changed)
		requireControlError(t, err, "COMMAND_CONFLICT")
	})

	for index, test := range []struct {
		name, ref, code string
		inputs          map[string]any
	}{
		{"unbound resource", "unbound", "PERMISSION_DENIED", map[string]any{"brief": witness("pdf")}},
		{"foreign owner", "foreign", "PERMISSION_DENIED", map[string]any{"brief": witness("foreign")}},
		{"nested artifact JSON is not a binding", "unbound", "PERMISSION_DENIED", map[string]any{
			"summary": map[string]any{"source_type": "artifact", "source_id": "summary", "value": witness("unbound")},
		}},
		{"nested JSON list is not a binding", "unbound", "PERMISSION_DENIED", map[string]any{
			"summary": map[string]any{"source_type": "artifact", "value": []any{witness("unbound")}},
		}},
		{"changed pinned revision", "pdf", "INPUT_VERSION_CHANGED", map[string]any{"brief": map[string]any{
			"source_type": "input_resource", "source_id": "pdf", "source_revision": "2", "content_hash": hash,
		}}},
		{"changed pinned hash", "pdf", "INPUT_VERSION_CHANGED", map[string]any{"brief": map[string]any{
			"source_type": "input_resource", "source_id": "pdf", "source_revision": "1", "content_hash": "sha256:changed",
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			setInputs(test.inputs, true)
			call := input
			call.CommandID = fmt.Sprintf("denied-%d", index)
			call.Arguments = map[string]any{"resource_ref": "resource:" + test.ref, "start_page": 1}
			result, err := service.Call(t.Context(), "owner", "run", "attempt", call)
			if err != nil || result.Error == nil || result.Error.Code != test.code || calls.Load() != 1 {
				t.Fatalf("scope failure reached the adapter: %+v %v calls=%d", result, err, calls.Load())
			}
		})
	}

	t.Run("both frozen and typed durable lists bind exact resources", func(t *testing.T) {
		setInputs(map[string]any{"brief": []any{witness("pdf")}}, true)
		call := input
		call.CommandID = "frozen-list"
		if result, err := service.Call(t.Context(), "owner", "run", "attempt", call); err != nil || result.Error != nil {
			t.Fatalf("frozen list input failed: %+v %v", result, err)
		}
		binding := orm.WorkflowAttemptInputBinding{ID: "pdf-binding", SessionID: "run", AttemptID: "attempt",
			MaterialID: "brief", MaterialRevisionID: "input-binding", SourceType: "input_resource", SourceID: "pdf",
			SourceRevision: "1", ContentHash: hash}
		if err := service.DB.Create(&binding).Error; err != nil {
			t.Fatal(err)
		}
		setInputs(nil, false)
		call.CommandID = "durable-list"
		if result, err := service.Call(t.Context(), "owner", "run", "attempt", call); err != nil || result.Error != nil {
			t.Fatalf("typed durable list input failed: %+v %v", result, err)
		}
		if calls.Load() != 3 {
			t.Fatalf("expected only three authorized resource executions, got %d", calls.Load())
		}
	})

	t.Run("same-size source corruption cannot bypass the pinned hash", func(t *testing.T) {
		corrupt := append([]byte(nil), content...)
		corrupt[len(corrupt)-1] ^= 1
		if err := service.DB.Model(&orm.WorkflowInputResource{}).Where("id = ?", "pdf").Update("content", corrupt).Error; err != nil {
			t.Fatal(err)
		}
		call := input
		call.CommandID = "corrupt-file"
		result, err := service.Call(t.Context(), "owner", "run", "attempt", call)
		if err != nil || result.Error == nil || result.Error.Code != "INPUT_VERSION_CHANGED" || calls.Load() != 3 {
			t.Fatalf("corrupt resource reached adapter: %+v %v calls=%d", result, err, calls.Load())
		}
	})
}
