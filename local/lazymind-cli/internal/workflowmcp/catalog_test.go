package workflowmcp

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWorkflowListMCPReturnsOnlyRoutingMetadata(t *testing.T) {
	expected := map[string]any{
		"resource_id": "resource-1", "workflow_ref": "ref-1", "workflow_id": "ppt-workflow",
		"name": "演示文稿", "description": "Create a presentation", "when_to_use": "When a slide deck is requested",
		"source_type": "builtin", "revision_id": "revision-1", "revision_no": float64(3),
		"tree_hash": "tree-1", "graph_hash": "graph-1", "graph_schema_version": "1", "contains_scripts": true,
	}
	for _, empty := range []bool{false, true} {
		name := "populated"
		if empty {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			pkg := map[string]any{}
			for key, value := range expected {
				pkg[key] = value
			}
			// Core's catalogue contains implementation data. Discovery must not
			// expand it, including future fields that contain equivalent content.
			pkg["compiled_graph"] = map[string]any{"nodes": strings.Repeat("implementation-marker", 10000)}
			pkg["files"] = map[string]any{"runtime/script.py": "source-marker"}
			pkg["future_implementation"] = "future-marker"
			packages := []map[string]any{pkg}
			if empty {
				packages = []map[string]any{}
			}
			client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/core/workflow-runtime/v1/workflows" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"workflows": packages, "future_implementation": "outer-marker"})
			})
			result := inputToolCall(t, inputToolSession(t, client), "workflow.list", map[string]any{})
			if result.IsError {
				t.Fatal("workflow discovery failed")
			}
			for _, representation := range []any{result.Content, result.StructuredContent} {
				encoded, err := json.Marshal(representation)
				if err != nil {
					t.Fatal(err)
				}
				for _, leaked := range []string{"compiled_graph", "files", "future_implementation", "implementation-marker", "source-marker", "future-marker", "outer-marker"} {
					if strings.Contains(string(encoded), leaked) {
						t.Fatalf("catalogue exposed implementation field/content %s", leaked)
					}
				}
				if len(encoded) > 2000 {
					t.Fatalf("catalogue response unexpectedly large: %d bytes", len(encoded))
				}
			}
			structured, _ := json.Marshal(result.StructuredContent)
			if len(result.Content) != 1 {
				t.Fatal("missing catalogue text fallback")
			}
			fallback, ok := result.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatal("catalogue fallback is not text")
			}
			for _, encoded := range [][]byte{structured, []byte(fallback.Text)} {
				var decoded struct {
					Workflows []map[string]any `json:"workflows"`
				}
				if err := json.Unmarshal(encoded, &decoded); err != nil {
					t.Fatal(err)
				}
				if empty {
					if decoded.Workflows == nil || len(decoded.Workflows) != 0 {
						t.Fatal("empty catalogue must remain an empty list")
					}
				} else if len(decoded.Workflows) != 1 || !reflect.DeepEqual(decoded.Workflows[0], expected) {
					t.Fatal("catalogue lost routing or pinned revision metadata")
				}
			}
		})
	}
}
