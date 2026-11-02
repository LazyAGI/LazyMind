package main

import "testing"

func TestOpenAPIProviderEndpointContract(t *testing.T) {
	spec := cloudSpecForTest(t)
	op := openAPIOperationForTest(t, spec, "put", apiPrefix+"/model_providers/{model_provider_id}/endpoint")
	if op["requestBody"].(map[string]any)["required"] != true {
		t.Fatal("supplier endpoint update must require its JSON body")
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	request := schemas["updateModelProviderEndpointOpenAPIRequest"].(map[string]any)
	properties := request["properties"].(map[string]any)
	if properties["base_url"].(map[string]any)["type"] != "string" {
		t.Fatal("supplier endpoint must be a string")
	}
	if len(properties) != 1 {
		t.Fatal("endpoint update must not accept connection credentials")
	}
	if got := openAPIObjectResponseRefForTest(t, op); got != "#/components/schemas/updateModelProviderEndpointOpenAPIResponse" {
		t.Fatalf("endpoint response schema = %s", got)
	}
}

func TestOpenAPISupplierActiveKeyContract(t *testing.T) {
	spec := cloudSpecForTest(t)
	op := openAPIOperationForTest(t, spec, "put", apiPrefix+"/model_providers/{model_provider_id}/keys/active")
	if op["requestBody"].(map[string]any)["required"] != true {
		t.Fatal("active key update must require its JSON body")
	}
	if got := openAPIObjectResponseRefForTest(t, op); got != "#/components/schemas/setActiveModelProviderKeyOpenAPIResponse" {
		t.Fatalf("active key response schema = %s", got)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	request := schemas["setActiveModelProviderKeyOpenAPIRequest"].(map[string]any)["properties"].(map[string]any)
	if len(request) != 2 || request["group_id"] == nil || request["key_id"] == nil {
		t.Fatal("active key update must use opaque connection/key IDs")
	}
	group := schemas["listModelProviderGroupsOpenAPIItem"].(map[string]any)["properties"].(map[string]any)
	if group["api_key"] != nil || group["keys"] == nil {
		t.Fatal("connection list must describe masked keys without plaintext credentials")
	}
	key := schemas["modelProviderGroupKeyOpenAPIItem"].(map[string]any)["properties"].(map[string]any)
	if key["enabled"].(map[string]any)["type"] != "boolean" {
		t.Fatal("saved key must expose its enabled state")
	}
}
