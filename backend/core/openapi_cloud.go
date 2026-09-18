package main

import (
	"strconv"

	"lazymind/core/cloudclient"
)

// Keep the published Cloud DTO names stable across client regeneration.
func desktopCloudSchemas() map[string]any {
	resourceType := enumStringSchema("skill", "workflow")
	format := enumStringSchema("lazymind.resource-manifest/v2")
	filePath := map[string]any{"type": "string", "description": "Canonical relative file path; no absolute paths, dot segments, repeated separators, backslashes, drive prefixes or control characters."}
	catalogProperties := []map[string]any{
		prop("catalog_key", strSchema()), prop("version", int64Schema()), prop("category", enumStringSchema("industry", "evaluation")),
		prop("name", strSchema()), prop("description", strSchema()), prop("icon", strSchema()), prop("domain", strSchema()),
		prop("tags", array(strSchema())), prop("online_access_url", strSchema()), prop("data_source", strSchema()),
		prop("published_at", strSchema()), prop("updated_at", strSchema()),
	}
	catalogRequired := []string{"catalog_key", "version", "category", "name", "description", "icon", "domain", "tags", "online_access_url", "data_source", "published_at", "updated_at"}
	schemas := map[string]any{
		"ProviderConnectionCreateRequest": objReq([]string{"provider"}, prop("provider", strSchema())),
		"ProviderConnectionError":         objReq([]string{"error_code", "message"}, prop("error_code", strSchema()), prop("message", strSchema())),
		"CloudResourceListItem": objReq([]string{"resource_id", "resource_type", "resource_name", "content_size", "format_schema", "updated_at", "presence_status", "local_exists"},
			prop("resource_id", strSchema()), prop("resource_type", resourceType), prop("resource_name", strSchema()), prop("content_size", int64Schema()),
			prop("format_schema", format), prop("updated_at", strSchema()),
			prop("presence_status", enumStringSchema("present_current", "download_required", "local_missing", "cloud_updated", "local_modified", "diverged", "incompatible")),
			prop("local_exists", boolSchema()), prop("local_resource_id", strSchema()), prop("local_resource_ref", strSchema())),
		"CloudResourcePage": objReq([]string{"items"}, prop("items", array(refSchema("CloudResourceListItem"))), prop("next_cursor", strSchema())),
		"CloudResourceMetadata": objReq([]string{"resource_id", "resource_type", "client_resource_key", "resource_name", "content_hash", "content_size", "format_schema", "updated_at"},
			prop("resource_id", strSchema()), prop("resource_type", resourceType), prop("client_resource_key", strSchema()), prop("resource_name", strSchema()),
			prop("content_hash", strSchema()), prop("content_size", int64Schema()), prop("format_schema", format), prop("updated_at", strSchema())),
		"CloudResourceTree": objReq([]string{"resource_id", "resource_type", "content_hash", "entrypoint", "files"},
			prop("resource_id", strSchema()), prop("resource_type", resourceType), prop("content_hash", strSchema()), prop("entrypoint", filePath),
			prop("files", array(objReq([]string{"path", "size", "sha256"}, prop("path", filePath), prop("size", int64Schema()), prop("sha256", strSchema()), prop("executable", boolSchema()))))),
		"CloudResourceContent": objReq([]string{"path", "content_hash", "sha256", "size", "mime", "binary", "content", "preview_status"},
			prop("path", filePath), prop("content_hash", strSchema()), prop("sha256", strSchema()), prop("size", int64Schema()), prop("mime", strSchema()), prop("binary", boolSchema()),
			prop("content", map[string]any{"type": "string", "description": "UTF-8 text up to 2 MiB; empty for binary and too_large states."}),
			prop("preview_status", enumStringSchema("ready", "binary", "too_large"))),
		"CloudResourceDownloadResult": objReq([]string{"resource_id", "local_resource_id", "already_present"},
			prop("resource_id", strSchema()), prop("local_resource_id", strSchema()), prop("local_resource_ref", strSchema()), prop("already_present", boolSchema())),
		"CloudResourceUploadResult": objReq([]string{"status"}, prop("status", enumStringSchema("upload_not_required", "upload_first", "upload_update_available", "cloud_updated", "diverged", "incompatible")), prop("resource_id", strSchema())),
		"CloudKnowledgeCatalogItem": objReq(catalogRequired, catalogProperties...),
		"CloudKnowledgeCatalogDetail": objReq(append(catalogRequired, "package_url", "package_revision", "source_adapter", "adapter_options", "sample_questions"),
			append(catalogProperties, prop("package_url", strSchema()), prop("package_revision", strSchema()), prop("source_adapter", strSchema()),
				prop("adapter_options", map[string]any{"type": "object", "additionalProperties": true}), prop("sample_questions", array(strSchema())))...),
		"CloudKnowledgeCatalogPage": objReq([]string{"items", "catalog_revision"}, prop("items", array(refSchema("CloudKnowledgeCatalogItem"))), prop("catalog_revision", int64Schema()), prop("next_cursor", strSchema())),
	}
	// Provider handlers return these public wire DTOs directly, without ReplyOK's envelope.
	builder := newSchemaBuilder()
	builder.schemaFromSource(schemaSource{Type: cloudclient.ProviderConnectionSession{}})
	builder.schemaFromSource(schemaSource{Type: cloudclient.ProviderConnectionPage{}})
	for name, schema := range builder.components {
		schemas[name] = schema
	}
	for _, name := range []string{"CloudResourcePage", "CloudResourceMetadata", "CloudResourceTree", "CloudResourceContent", "CloudResourceDownloadResult", "CloudResourceUploadResult", "CloudKnowledgeCatalogPage", "CloudKnowledgeCatalogDetail"} {
		schemas[name+"Response"] = objReq([]string{"code", "message", "data"}, prop("code", intSchema()), prop("message", strSchema()), prop("data", refSchema(name)))
	}
	for name, schema := range cloudAccountSchemas() {
		schemas[name] = schema
	}
	return schemas
}

func desktopCloudPaths() map[string]any {
	providerOperation := func(summary string, params []map[string]any, body map[string]any, status int, schema string, plainUnavailable bool) map[string]any {
		success := map[string]any{"description": summary}
		if schema != "" {
			success = response(status, summary, refSchema(schema))
		}
		unavailable := response(503, "Provider connection unavailable", refSchema("ProviderConnectionError"))
		if plainUnavailable {
			unavailable["content"].(map[string]any)["text/plain"] = map[string]any{"schema": strSchema()}
		}
		value := op(summary, params, body, success)
		value["responses"] = map[string]any{
			strconv.Itoa(status): success,
			"404":                response(404, "Provider connection or session not found", refSchema("ProviderConnectionError")),
			"503":                unavailable,
		}
		return value
	}
	sessionID := queryParams(param("path", "session_id", true, strSchema()))
	connectionID := queryParams(param("path", "auth_connection_id", true, strSchema()))
	create := providerOperation("Create a provider authorization session", nil, jsonBody(refSchema("ProviderConnectionCreateRequest"), true), 201, "ProviderConnectionSession", false)
	create["responses"].(map[string]any)["422"] = map[string]any{"description": "Invalid request or service unavailable", "content": map[string]any{"text/plain": map[string]any{"schema": strSchema()}}}
	paths := map[string]any{
		"/provider-connections/sessions": map[string]any{"post": create},
		"/provider-connections/sessions/{session_id}": map[string]any{
			"get":    providerOperation("Get provider authorization session", sessionID, nil, 200, "ProviderConnectionSession", true),
			"delete": providerOperation("Cancel provider authorization session", sessionID, nil, 204, "", true),
		},
		"/provider-connections":                                  map[string]any{"get": providerOperation("List provider connections", nil, nil, 200, "ProviderConnectionPage", false)},
		"/provider-connections/{auth_connection_id}:reauthorize": map[string]any{"post": providerOperation("Reauthorize provider connection", connectionID, nil, 201, "ProviderConnectionSession", true)},
		"/provider-connections/{auth_connection_id}":             map[string]any{"delete": providerOperation("Revoke provider connection", connectionID, nil, 204, "", true)},
	}
	cloudOperation := func(summary string, params []map[string]any, schema string, desktopClient bool) map[string]any {
		value := op(summary, params, nil, response(200, summary, refSchema(schema+"Response")))
		if desktopClient {
			value["tags"] = []string{"DesktopCloud"}
		}
		for _, status := range []int{400, 401, 403, 404, 409, 412, 422, 429, 502, 503} {
			value["responses"].(map[string]any)[strconv.Itoa(status)] = response(status, "Cloud request failed", refSchema("ErrorResponse"))
		}
		return value
	}
	for _, kind := range []string{"skills", "workflows"} {
		base := "/cloud/" + kind
		resourceID := queryParams(param("path", "resource_id", true, strSchema()))
		paths[base] = map[string]any{"get": cloudOperation("List Cloud "+kind,
			queryParams(param("query", "cursor", false, strSchema()), param("query", "page_size", false, map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20})), "CloudResourcePage", false)}
		paths[base+"/{resource_id}"] = map[string]any{"get": cloudOperation("Get Cloud resource metadata", resourceID, "CloudResourceMetadata", true)}
		paths[base+"/{resource_id}/tree"] = map[string]any{"get": cloudOperation("Get Cloud resource directory", resourceID, "CloudResourceTree", true)}
		content := cloudOperation("Read Cloud resource file", append(resourceID,
			param("query", "path", true, strSchema()), param("header", "If-Match", true, map[string]any{"type": "string", "description": "Quoted content hash from the resource ETag; required to pin the resource version."})), "CloudResourceContent", true)
		content["responses"].(map[string]any)["428"] = response(428, "A valid If-Match header is required", refSchema("ErrorResponse"))
		paths[base+"/{resource_id}/content"] = map[string]any{"get": content}
		paths[base+"/{resource_id}:download"] = map[string]any{"post": cloudOperation("Download Cloud resource", resourceID, "CloudResourceDownloadResult", false)}
	}
	paths["/cloud/skills/{skill_id}:upload"] = map[string]any{"post": cloudOperation("Upload local Skill to Cloud", queryParams(param("path", "skill_id", true, strSchema())), "CloudResourceUploadResult", false)}
	paths["/cloud/knowledge-market"] = map[string]any{"get": cloudOperation("List Cloud knowledge catalog", queryParams(
		param("query", "cursor", false, map[string]any{"type": "string", "maxLength": 2048}),
		param("query", "page_size", false, map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 100}),
		param("query", "category", false, enumStringSchema("industry", "evaluation")),
		param("query", "domain", false, map[string]any{"type": "string", "maxLength": 64}),
		param("query", "q", false, map[string]any{"type": "string", "maxLength": 200})), "CloudKnowledgeCatalogPage", true)}
	paths["/cloud/knowledge-market/items/{catalog_key}"] = map[string]any{"get": cloudOperation("Get Cloud knowledge catalog item", queryParams(param("path", "catalog_key", true, strSchema())), "CloudKnowledgeCatalogDetail", true)}
	for path, operations := range cloudAccountPaths() {
		paths[path] = operations
	}
	return paths
}
