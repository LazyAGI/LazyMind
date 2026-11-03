package main

import "testing"

func TestOpenAPIProviderRequestAndSavedKeyActions(t *testing.T) {
	spec := cloudSpecForTest(t)
	for _, endpoint := range []struct{ method, path string }{
		{"get", "/model_providers/{model_provider_id}/request-config"},
		{"put", "/model_providers/{model_provider_id}/request-config"},
		{"put", "/model_providers/{model_provider_id}/groups/{group_id}/keys/{key_id}"},
		{"post", "/model_providers/{model_provider_id}/groups/{group_id}/keys/{key_id}/reveal"},
	} {
		openAPIOperationForTest(t, spec, endpoint.method, apiPrefix+endpoint.path)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	config := schemaPropertiesForTest(t, schemas, "providerRequestConfigOpenAPIResponse")
	if config["default_protocol"] == nil || config["protocols"] == nil {
		t.Fatal("request configuration lacks typed protocol choices")
	}
	protocol := schemaPropertiesForTest(t, schemas, "providerRequestProtocolOpenAPIItem")
	for _, field := range []string{"supported", "provider_supported", "runtime_supported"} {
		if protocol[field] == nil {
			t.Fatalf("request protocol lacks separate availability field %s", field)
		}
	}
	key := schemaPropertiesForTest(t, schemas, "replaceModelProviderKeyOpenAPIResponse")
	if key["key_id"] == nil || key["enabled"] == nil || key["api_key"] != nil {
		t.Fatal("replacement must return new opaque ID and enabled flag, never plaintext")
	}
}
