package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/gorilla/mux"
)

func TestOpenAPIModelsExposeCloudAsAReadOnlySelectionSource(t *testing.T) {
	schemas := cloudSelectionSchemasForTest(t)

	listItem := schemaPropertiesForTest(t, schemas, "listModelProviderGroupModelsOpenAPIItem")
	assertCloudSelectionPropertyForTest(t, listItem, "source", []any{"own", "cloud"})
	for _, property := range []string{"provider_id", "availability", "read_only", "capabilities"} {
		if _, ok := listItem[property]; !ok {
			t.Errorf("selectable model item omitted %q", property)
		}
	}

	selected := schemaPropertiesForTest(t, schemas, "selectedModelOpenAPIItem")
	assertCloudSelectionPropertyForTest(t, selected, "source", []any{"own", "cloud"})
	for _, property := range []string{"provider_id", "availability", "unavailable_reason"} {
		if _, ok := selected[property]; !ok {
			t.Errorf("selected model item omitted %q", property)
		}
	}

	request := schemaPropertiesForTest(t, schemas, "setSelectedModelOpenAPIItem")
	assertCloudSelectionPropertyForTest(t, request, "source", []any{"own", "cloud"})
}

func TestOpenAPIChatModelSelectionDiscriminatesCloudFromLocalIDs(t *testing.T) {
	schemas := cloudSelectionSchemasForTest(t)
	for _, schemaName := range []string{
		"chatModelListOpenAPIItem", "chatModelProviderOpenAPIItem", "chatModelSelectionOpenAPI",
	} {
		properties := schemaPropertiesForTest(t, schemas, schemaName)
		assertCloudSelectionPropertyForTest(t, properties, "source", []any{"own", "shared", "cloud"})
	}
	request := schemaPropertiesForTest(t, schemas, "patchConversationModelOpenAPIRequest")
	assertCloudSelectionPropertyForTest(t, request, "source", []any{"own", "shared", "cloud"})
}

func cloudSelectionSchemasForTest(t *testing.T) map[string]any {
	t.Helper()
	router := mux.NewRouter()
	registerCoreRoutes(router)
	raw, err := buildOpenAPISpecFromRouter(router)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	components, _ := spec["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	if schemas == nil {
		t.Fatal("OpenAPI schemas missing")
	}
	return schemas
}

func assertCloudSelectionPropertyForTest(t *testing.T, properties map[string]any, name string, want []any) {
	t.Helper()
	property, ok := properties[name].(map[string]any)
	if !ok {
		t.Fatalf("property %q missing", name)
	}
	if got, ok := property["enum"].([]any); !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("property %q enum=%#v want=%#v", name, property["enum"], want)
	}
}
