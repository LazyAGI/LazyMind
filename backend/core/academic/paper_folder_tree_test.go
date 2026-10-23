package academic

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestPaperFolderHierarchyValidation(t *testing.T) {
	for _, key := range []string{"folders", "collections"} {
		for _, tc := range []struct {
			name, nodes string
			valid       bool
		}{
			{"legacy", `[{"id":"a","name":"Topic"}]`, true},
			{"tree", `[{"id":"a","name":"Topic","parentId":null},{"id":"b","name":"Topic","parentId":"a"}]`, true},
			{"different parents", `[{"id":"a","name":"A"},{"id":"b","name":"B"},{"id":"c","name":"Topic","parentId":"a"},{"id":"d","name":"Topic","parentId":"b"}]`, true},
			{"missing parent", `[{"id":"a","name":"Topic","parentId":"missing"}]`, false},
			{"self parent", `[{"id":"a","name":"Topic","parentId":"a"}]`, false},
			{"cycle", `[{"id":"a","name":"A","parentId":"b"},{"id":"b","name":"B","parentId":"a"}]`, false},
			{"non string parent", `[{"id":"a","name":"Topic","parentId":42}]`, false},
		} {
			t.Run(key+"/"+tc.name, func(t *testing.T) {
				var graph map[string]json.RawMessage
				json.Unmarshal([]byte(emptyPaperLibrary), &graph)
				graph[key] = json.RawMessage(tc.nodes)
				raw, _ := json.Marshal(graph)
				_, err := validatePaperLibrary(raw)
				if (err == nil) != tc.valid {
					t.Fatalf("valid=%v error=%v", tc.valid, err)
				}
			})
		}
	}
}

func TestPaperFolderHierarchyDepth(t *testing.T) {
	for _, depth := range []int{32, 33} {
		var graph map[string]any
		json.Unmarshal([]byte(emptyPaperLibrary), &graph)
		folders := []map[string]any{}
		for i := 0; i < depth; i++ {
			folder := map[string]any{"id": fmt.Sprint(i), "name": "Topic"}
			if i > 0 {
				folder["parentId"] = fmt.Sprint(i - 1)
			}
			folders = append(folders, folder)
		}
		graph["folders"] = folders
		raw, _ := json.Marshal(graph)
		_, err := validatePaperLibrary(raw)
		if (err == nil) != (depth <= 32) {
			t.Fatalf("depth %d: %v", depth, err)
		}
	}
}
