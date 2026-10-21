package main

func documentDirectorySchemas() map[string]any {
	arbitrary := map[string]any{"type": "object", "additionalProperties": true}
	schemas := map[string]any{
		"DirectoryLocalFile":      objReq([]string{"path", "namespace", "filename", "size", "mime_type", "content_base64"}, prop("path", strSchema()), prop("namespace", strSchema()), prop("filename", strSchema()), prop("size", int64Schema()), prop("mime_type", strSchema()), prop("content_base64", strSchema()), prop("error_code", strSchema())),
		"DocumentFolderInventory": objReq([]string{"id", "status", "source", "state", "directory_id", "batch_id"}, prop("id", strSchema()), prop("status", strSchema()), prop("source", arbitrary), prop("state", arbitrary), prop("directory_id", strSchema()), prop("batch_id", strSchema())),
		"DocumentFolderNode":      objReq([]string{"node_key", "parent_key", "path", "name", "kind", "listed"}, prop("node_key", strSchema()), prop("parent_key", strSchema()), prop("path", strSchema()), prop("name", strSchema()), prop("kind", strSchema()), prop("listed", boolSchema()), prop("candidate", arbitrary)),
		"DocumentFolderNodes":     objReq([]string{"nodes", "total", "next_offset"}, prop("nodes", array(refSchema("DocumentFolderNode"))), prop("total", int64Schema()), prop("next_offset", intSchema())),
		"DirectoryMail":           objReq([]string{"subject", "from", "to", "cc", "date", "mailbox", "body", "attachments", "truncated"}, prop("subject", strSchema()), prop("from", strSchema()), prop("to", strSchema()), prop("cc", strSchema()), prop("date", strSchema()), prop("mailbox", strSchema()), prop("body", strSchema()), prop("truncated", boolSchema()), prop("attachments", array(objReq([]string{"filename", "mime_type", "size"}, prop("filename", strSchema()), prop("mime_type", strSchema()), prop("size", int64Schema()))))),
		"DocumentDirectory": objReq([]string{"id", "name", "intent", "organization", "auto_add", "pinned", "version", "last_used_at"},
			prop("id", strSchema()), prop("name", strSchema()), prop("intent", strSchema()), prop("organization", strSchema()), prop("auto_add", boolSchema()), prop("pinned", boolSchema()), prop("version", int64Schema()), prop("last_used_at", dateTimeSchema()), prop("organized_at", dateTimeSchema()), prop("coverage", array(arbitrary))),
		"DocumentDirectoryGroup":        objReq([]string{"id", "name", "parent_id", "position"}, prop("id", strSchema()), prop("name", strSchema()), prop("parent_id", strSchema()), prop("position", intSchema())),
		"DocumentDirectoryEntry":        objReq([]string{"id", "resource_key", "source", "namespace", "resource_id", "title", "group_id", "pinned", "excluded", "read_state", "confirmed", "access_state"}, prop("id", strSchema()), prop("resource_key", strSchema()), prop("source", strSchema()), prop("namespace", strSchema()), prop("resource_id", strSchema()), prop("title", strSchema()), prop("group_id", strSchema()), prop("pinned", boolSchema()), prop("excluded", boolSchema()), prop("read_state", strSchema()), prop("confirmed", boolSchema()), prop("access_state", strSchema()), prop("content_state", strSchema()), prop("content_version", strSchema()), prop("read_at", dateTimeSchema()), prop("locator", arbitrary), prop("evidence", arbitrary)),
		"DocumentDirectoryDetail":       objReq([]string{"directory", "groups", "entries", "organizing"}, prop("directory", refSchema("DocumentDirectory")), prop("groups", array(refSchema("DocumentDirectoryGroup"))), prop("entries", array(refSchema("DocumentDirectoryEntry"))), prop("organizing", boolSchema()), prop("folder", refSchema("DocumentFolderInventory")), prop("total", int64Schema()), prop("group_counts", map[string]any{"type": "object", "additionalProperties": int64Schema()})),
		"DocumentDirectoryList":         objReq([]string{"directories"}, prop("directories", array(refSchema("DocumentDirectory")))),
		"DocumentDirectoryBatch":        objReq([]string{"id", "directory_id", "parent_id", "status", "acknowledged", "created_at"}, prop("id", strSchema()), prop("directory_id", strSchema()), prop("parent_id", strSchema()), prop("status", strSchema()), prop("acknowledged", boolSchema()), prop("result", arbitrary), prop("created_at", dateTimeSchema())),
		"DocumentDirectoryBatches":      objReq([]string{"batches"}, prop("batches", array(refSchema("DocumentDirectoryBatch")))),
		"DocumentDirectoryCommand":      objReq([]string{"operation"}, prop("operation", enumStringSchema("create", "update", "delete", "touch", "group", "delete_group", "entry", "entry_pin", "exclude", "restore", "organize", "reorganize", "retry", "cancel", "acknowledge")), prop("directory_id", strSchema()), prop("version", int64Schema()), prop("name", strSchema()), prop("intent", strSchema()), prop("organization", strSchema()), prop("auto_add", boolSchema()), prop("pinned", boolSchema()), prop("group_id", strSchema()), prop("parent_id", strSchema()), prop("entry_id", strSchema()), prop("batch_id", strSchema()), prop("instruction", strSchema()), prop("candidate", arbitrary)),
		"DocumentDirectoryActionResult": obj(prop("directory", refSchema("DocumentDirectory")), prop("group", refSchema("DocumentDirectoryGroup")), prop("entry", refSchema("DocumentDirectoryEntry")), prop("batch", refSchema("DocumentDirectoryBatch")), prop("evicted", strSchema())),
	}
	for _, name := range []string{"DirectoryLocalFile", "DocumentFolderNodes", "DirectoryMail", "DocumentDirectoryList", "DocumentDirectoryDetail", "DocumentDirectoryBatches", "DocumentDirectoryActionResult"} {
		schemas[name+"Response"] = objReq([]string{"code", "data"}, prop("code", intSchema()), prop("data", refSchema(name)))
	}
	return schemas
}
func documentDirectoryPaths() map[string]any {
	operation := func(id, summary, schema string, params []map[string]any, body map[string]any) map[string]any {
		value := op(summary, params, body, response(200, summary, refSchema(schema+"Response")))
		value["operationId"] = id
		value["tags"] = []string{"DocumentDirectories"}
		return value
	}
	return map[string]any{
		"/document-directories/{directory_id}/entries/{entry_id}/local-file": map[string]any{"get": operation("readDocumentDirectoryLocalFile", "Preview an owned local file without changing Agent evidence", "DirectoryLocalFile", queryParams(param("path", "directory_id", true, strSchema()), param("path", "entry_id", true, strSchema()), param("query", "metadata_only", false, boolSchema())), nil)},
		"/document-directories/{directory_id}/source-nodes":                  map[string]any{"get": operation("getDocumentFolderNodes", "Page original source tree children", "DocumentFolderNodes", queryParams(param("path", "directory_id", true, strSchema()), param("query", "parent_key", false, strSchema()), param("query", "offset", false, intSchema())), nil)},
		"/document-directories/{directory_id}/entries/{entry_id}/mail":       map[string]any{"get": operation("readDocumentDirectoryMail", "Read an owned email without changing directory evidence", "DirectoryMail", queryParams(param("path", "directory_id", true, strSchema()), param("path", "entry_id", true, strSchema())), nil)},
		"/document-directories":                map[string]any{"get": operation("listDocumentDirectories", "List up to 20 directories without refreshing use", "DocumentDirectoryList", nil, nil)},
		"/document-directories/{directory_id}": map[string]any{"get": operation("getDocumentDirectory", "Read last committed directory version", "DocumentDirectoryDetail", queryParams(param("path", "directory_id", true, strSchema()), param("query", "offset", false, intSchema()), param("query", "limit", false, intSchema()), param("query", "group_id", false, strSchema()), param("query", "query", false, strSchema()), param("query", "state", false, strSchema())), nil)},
		"/document-directories/batches":        map[string]any{"get": operation("listDocumentDirectoryBatches", "List durable organization results", "DocumentDirectoryBatches", nil, nil)},
		"/document-directories/actions":        map[string]any{"post": operation("executeDocumentDirectoryAction", "Apply an owned directory action; organizing directories reject edits", "DocumentDirectoryActionResult", nil, jsonBody(refSchema("DocumentDirectoryCommand"), true))},
	}
}
