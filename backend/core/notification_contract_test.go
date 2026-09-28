package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNotificationOpenAPIReferencesAndNativeContract(t *testing.T) {
	raw, err := json.Marshal(manualOpenAPISpec())
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if ref, ok := node["$ref"].(string); ok && strings.HasPrefix(ref, "#/components/schemas/") {
				if schemas[strings.TrimPrefix(ref, "#/components/schemas/")] == nil {
					t.Errorf("unresolved schema %s", ref)
				}
			}
			for _, child := range node {
				walk(child)
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	for name := range notificationSchemas() {
		walk(schemas[name])
	}
	paths := spec["paths"].(map[string]any)
	for path := range notificationPaths() {
		if paths[path] == nil {
			t.Fatalf("missing public notification path %s", path)
		}
		walk(paths[path])
	}
	properties := schemas["TaskNotification"].(map[string]any)["properties"].(map[string]any)
	for _, key := range []string{"notification_id", "title", "body", "app_name", "execution_id", "navigation"} {
		if properties[key] == nil {
			t.Errorf("missing native-client field %s", key)
		}
	}
	errorSchema := schemas["NotificationError"].(map[string]any)
	errorJSON, _ := json.Marshal(errorSchema)
	for _, key := range []string{"reason", "request_id", "running_task_ids"} {
		if !strings.Contains(string(errorJSON), key) {
			t.Errorf("missing safe error field %s", key)
		}
	}
	claim := paths["/task-center/notification-events/{notification_id}:claim"].(map[string]any)["post"]
	encoded, _ := json.Marshal(claim)
	if !strings.Contains(string(encoded), "X-LazyMind-Internal-Token") {
		t.Fatal("service identity missing from claim contract")
	}
	batch := paths["/automation-groups:batch-create"].(map[string]any)["post"]
	encoded, _ = json.Marshal(batch)
	for _, value := range []string{"AutomationGroupBatchCreateRequest", "AutomationGroupBatchCreateResponse"} {
		if !strings.Contains(string(encoded), value) {
			t.Fatalf("batch schedule notification contract missing %s", value)
		}
	}
	batchRequest, _ := json.Marshal(schemas["AutomationGroupBatchCreateRequest"])
	if !strings.Contains(string(batchRequest), "ScheduleNotificationUpdate") {
		t.Fatal("batch task request does not expose its notification update")
	}
}
