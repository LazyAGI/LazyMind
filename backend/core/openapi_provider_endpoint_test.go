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
