package server

import "testing"

func TestSyncEndpointDocumentsImplementedContract(t *testing.T) {
	t.Parallel()

	op := openAPIOperation(t, "/api/scan/sources/{source_id}/sync", "post")
	if op["operationId"] != "triggerSourceSync" {
		t.Fatalf("unexpected operationId: %+v", op["operationId"])
	}
	responses := op["responses"].(map[string]any)
	if _, ok := responses["200"]; !ok {
		t.Fatalf("sync operation must document implemented 200 contract: %+v", responses)
	}
	if _, ok := responses["501"]; ok {
		t.Fatalf("sync operation must not document stale 501 contract: %+v", responses)
	}
	schemas := OpenAPISpec()["components"].(map[string]any)["schemas"].(map[string]any)
	assertSchemaNotRequired(t, schemas, "TriggerSourceSyncRequest", "request_id")
}

func TestOpenAPIDocumentsSeparateTargetTreeAndSourceTreeContracts(t *testing.T) {
	t.Parallel()

	schemas := OpenAPISpec()["components"].(map[string]any)["schemas"].(map[string]any)
	targetProps := schemaProperties(t, schemas, "BindingTargetChildrenRequest")
	sourceProps := schemaProperties(t, schemas, "SourceTreeChildrenRequest")

	for _, field := range []string{"connector_type", "target_type", "target_ref", "node_ref"} {
		if _, ok := targetProps[field]; !ok {
			t.Fatalf("target tree request missing source-target field %s", field)
		}
	}
	for _, field := range []string{"binding_id", "tree_key", "parent_key", "state_filter", "use_cache"} {
		if _, ok := sourceProps[field]; !ok {
			t.Fatalf("source tree request missing indexed-tree field %s", field)
		}
	}
	for _, field := range []string{"connector_type", "target_type", "target_ref", "node_ref", "agent_id", "auth_connection_id"} {
		if _, ok := sourceProps[field]; ok {
			t.Fatalf("source tree request must not expose target tree field %s", field)
		}
	}
	for _, field := range []string{"binding_id", "tree_key", "parent_key", "state_filter"} {
		if _, ok := targetProps[field]; ok {
			t.Fatalf("target tree request must not expose indexed-tree field %s", field)
		}
	}
}

func TestOpenAPITreeNodeDocumentsBindingTargetFields(t *testing.T) {
	t.Parallel()

	schemas := OpenAPISpec()["components"].(map[string]any)["schemas"].(map[string]any)
	props := schemaProperties(t, schemas, "TreeNode")
	for _, field := range []string{"target_type", "target_ref", "tree_key", "object_key"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("TreeNode must document %s", field)
		}
	}
}

func TestOpenAPICreateSourceHasNoTopLevelTargetFields(t *testing.T) {
	t.Parallel()

	schemas := OpenAPISpec()["components"].(map[string]any)["schemas"].(map[string]any)
	createProps := schemaProperties(t, schemas, "CreateSourceRequest")
	bindingProps := schemaProperties(t, schemas, "SourceBindingRequest")

	assertSchemaRequired(t, schemas, "SourceBindingRequest", "connector_type", "target_type", "target_ref", "sync_mode")

	for _, field := range []string{"connector_type", "target_type", "target_ref", "agent_id", "auth_connection_id", "root" + "_path"} {
		if _, ok := createProps[field]; ok {
			t.Fatalf("CreateSourceRequest must not expose top-level binding field %s", field)
		}
	}
	for _, field := range []string{"connector_type", "target_type", "target_ref", "agent_id", "auth_connection_id"} {
		if _, ok := bindingProps[field]; !ok {
			t.Fatalf("SourceBindingRequest must expose binding field %s", field)
		}
	}
	if _, ok := bindingProps["schedule_policy"]; !ok {
		t.Fatalf("SourceBindingRequest must expose schedule_policy")
	}
}

func openAPIOperation(t *testing.T, path, method string) map[string]any {
	t.Helper()

	paths := OpenAPISpec()["paths"].(map[string]any)
	pathItem, ok := paths[path].(map[string]any)
	if !ok {
		t.Fatalf("missing path %s", path)
	}
	op, ok := pathItem[method].(map[string]any)
	if !ok {
		t.Fatalf("missing operation %s %s", method, path)
	}
	return op
}

func schemaProperties(t *testing.T, schemas map[string]any, name string) map[string]any {
	t.Helper()

	schema, ok := schemas[name].(map[string]any)
	if !ok {
		t.Fatalf("missing schema %s", name)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema %s has no properties: %+v", name, schema)
	}
	return properties
}
