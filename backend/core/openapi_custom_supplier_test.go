package main

import "testing"

func TestOpenAPICustomSupplierContract(t *testing.T) {
	spec := cloudSpecForTest(t)
	for _, endpoint := range []struct{ method, path string }{
		{"post", "/model_providers"},
		{"patch", "/model_providers/{model_provider_id}"},
		{"delete", "/model_providers/{model_provider_id}"},
	} {
		op := openAPIOperationForTest(t, spec, endpoint.method, apiPrefix+endpoint.path)
		if endpoint.method != "delete" && op["requestBody"].(map[string]any)["required"] != true {
			t.Fatal("supplier writes require a JSON body")
		}
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	create := schemas["createCustomModelProviderOpenAPIRequest"].(map[string]any)["properties"].(map[string]any)
	if len(create) != 3 || create["name"] == nil || create["source_provider_id"] == nil || create["base_url"] == nil {
		t.Fatal("supplier creation must separate display name, protocol source and endpoint")
	}
	provider := schemas["userModelProviderOpenAPIItem"].(map[string]any)["properties"].(map[string]any)
	if provider["is_custom"] == nil || provider["protocol_name"] == nil {
		t.Fatal("supplier list must expose custom identity and technical protocol")
	}
	chatProvider := schemas["chatModelProviderOpenAPIItem"].(map[string]any)["properties"].(map[string]any)
	if chatProvider["is_custom"].(map[string]any)["type"] != "boolean" {
		t.Fatal("chat supplier list must expose custom identity for generic branding")
	}

}
