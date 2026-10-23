package main

import "testing"

func TestOpenAPICustomSupplierLogoContract(t *testing.T) {
	spec := cloudSpecForTest(t)
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{
		"createCustomModelProviderOpenAPIRequest",
		"createCustomModelProviderOpenAPIResponse",
		"updateCustomModelProviderOpenAPIRequest",
		"customModelProviderMutationOpenAPIResponse",
		"userModelProviderOpenAPIItem",
		"listModelProviderGroupModelsOpenAPIItem",
		"selectedModelOpenAPIItem",
		"chatModelListOpenAPIItem",
		"chatModelSelectionOpenAPI",
		"chatModelProviderOpenAPIItem",
	} {
		schema := schemas[name].(map[string]any)
		field, ok := schema["properties"].(map[string]any)["logo_data_url"].(map[string]any)
		if !ok || field["type"] != "string" {
			t.Fatalf("%s must expose the optional custom supplier PNG logo", name)
		}
		if required, ok := schema["required"].([]any); ok {
			for _, key := range required {
				if key == "logo_data_url" {
					t.Fatalf("%s must accept existing models without a logo", name)
				}
			}
		}
	}
	for _, name := range []string{"addModelProviderGroupModelOpenAPIRequest", "updateModelProviderGroupModelOpenAPIRequest"} {
		properties := schemas[name].(map[string]any)["properties"].(map[string]any)
		if _, exists := properties["logo_data_url"]; exists {
			t.Fatalf("%s must inherit supplier artwork rather than expose model-level logo editing", name)
		}
	}
}
