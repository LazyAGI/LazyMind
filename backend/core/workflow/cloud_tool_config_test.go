package workflow

import (
	"reflect"
	"testing"
)

func TestWorkflowNodeToolConfigDropsPreviousCloudCredentials(t *testing.T) {
	base := map[string]any{
		"feishu": "old-feishu-token", "notion": []string{"old-notion-token"},
		"tavily": "search-token", "custom": "custom-token",
	}
	got, err := workflowNodeToolConfig(t.Context(), nil, "user-1", base, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"tavily": "search-token", "custom": "custom-token"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stale cloud credentials survived: got %v, want %v", got, want)
	}
	if base["feishu"] != "old-feishu-token" {
		t.Fatal("mutated the stored base configuration")
	}
}
