package main

import "testing"

func TestOpenAPIModelSettingsAndAdvancedOptions(t *testing.T) {
	spec := cloudSpecForTest(t)
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"addModelProviderGroupModelOpenAPIRequest", "addModelProviderGroupModelOpenAPIResponse", "updateModelProviderGroupModelOpenAPIRequest", "listModelProviderGroupModelsOpenAPIItem", "selectedModelOpenAPIItem"} {
		if schemaPropertiesForTest(t, schemas, name)["model_settings"] == nil {
			t.Fatalf("%s missing settings", name)
		}
	}
	advanced := schemaPropertiesForTest(t, schemas, "ProviderRequestAdvanced")["extra_body"].(map[string]any)
	if advanced["additionalProperties"] != true {
		t.Fatal("extra body must allow JSON scalar and nested values")
	}
	batch := schemaPropertiesForTest(t, schemas, "EmbeddingOptions")["batch_size"].(map[string]any)
	if batch["nullable"] != true {
		t.Fatal("batch size must allow null to reset default")
	}
	probe := schemaPropertiesForTest(t, schemas, "ModelProbeResponse")
	if probe["embedding_dimensions"] == nil {
		t.Fatal("probe lacks measured dimensions")
	}
}
