package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"lazymind/core/vocabulary"
)

func TestOpenAPIVocabularyCloudContracts(t *testing.T) {
	t.Setenv("LAZYMIND_VOCABULARY_ENABLED", "true")
	spec := cloudSpecForTest(t)
	for _, tc := range []struct{ method, path, response, request string }{
		{"get", "/vocabulary/cloud/connections", "VocabularyCloudConnectionsResponse", ""},
		{"put", "/vocabulary/cloud/connections/{provider}", "VocabularyCloudConnectionResponse", "VocabularyCloudConnectionRequest"},
		{"delete", "/vocabulary/cloud/connections/{provider}", "VocabularyCloudDisconnectResponse", ""},
		{"post", "/vocabulary/cloud/connections/{provider}/test", "VocabularyCloudWordbooksResponse", "VocabularyCloudTokenRequest"},
		{"post", "/vocabulary/cloud/connections/{provider}/wordbooks", "VocabularyCloudWordbookResponse", "VocabularyCloudWordbookRequest"},
		{"post", "/vocabulary/cloud/connections/{provider}/sync", "VocabularyCloudSyncResponse", ""},
		{"get", "/vocabulary/cloud/operations", "VocabularyCloudOperationsResponse", ""},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			operation := openAPIOperationForTest(t, spec, tc.method, apiPrefix+tc.path)
			if got := openAPIObjectResponseRefForTest(t, operation); got != "#/components/schemas/"+tc.response {
				t.Fatalf("response=%s", got)
			}
			if tc.request != "" {
				body := operation["requestBody"].(map[string]any)
				got := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["$ref"]
				if got != "#/components/schemas/"+tc.request || body["required"] != true {
					t.Fatal("untyped request", body)
				}
			}
			if strings.Contains(tc.path, "{provider}") {
				found := false
				for _, raw := range operation["parameters"].([]any) {
					p := raw.(map[string]any)
					if p["name"] == "provider" {
						found = true
						if p["in"] != "path" || p["required"] != true || !reflect.DeepEqual(p["schema"].(map[string]any)["enum"], []any{"eudic", "maimemo"}) {
							t.Fatal("invalid provider parameter", p)
						}
					}
				}
				if !found {
					t.Fatal("provider absent")
				}
			}
		})
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	for name, model := range map[string]any{"VocabularyCloudConnection": vocabulary.CloudConnection{}, "VocabularyCloudOperation": vocabulary.CloudOperation{}, "VocabularyCloudWordbook": vocabulary.CloudWordbook{}} {
		properties := schemas[name].(map[string]any)["properties"].(map[string]any)
		raw, _ := json.Marshal(model)
		var wire map[string]any
		json.Unmarshal(raw, &wire)
		if len(properties) != len(wire) {
			t.Fatalf("%s fields differ from wire: %v / %v", name, properties, wire)
		}
		for field := range wire {
			if properties[field] == nil {
				t.Errorf("%s missing %s", name, field)
			}
		}
	}
	for _, name := range []string{"VocabularyCloudConnectionRequest", "VocabularyCloudTokenRequest", "VocabularyCloudWordbookRequest"} {
		token := schemas[name].(map[string]any)["properties"].(map[string]any)["token"].(map[string]any)
		if token["writeOnly"] != true {
			t.Errorf("%s token is not writeOnly", name)
		}
	}
	// Follow response refs recursively so nested schemas cannot leak credentials.
	var check func(any)
	visited := map[string]bool{}
	check = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			if ref, ok := value["$ref"].(string); ok && !visited[ref] {
				visited[ref] = true
				check(schemas[strings.TrimPrefix(ref, "#/components/schemas/")])
			}
			if props, ok := value["properties"].(map[string]any); ok {
				for _, secret := range []string{"token", "token_cipher", "account_key", "owner_id"} {
					if props[secret] != nil {
						t.Errorf("response exposes %s", secret)
					}
				}
			}
			for _, child := range value {
				check(child)
			}
		case []any:
			for _, child := range value {
				check(child)
			}
		}
	}
	for name, schema := range schemas {
		if strings.HasPrefix(name, "VocabularyCloud") && strings.HasSuffix(name, "Response") {
			check(schema)
		}
	}
}
