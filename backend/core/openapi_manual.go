package main

func manualOpenAPISpec() map[string]any {
	return map[string]any{
		"components": map[string]any{
			"schemas": manualSchemas(),
		},
		"paths": manualPaths(),
	}
}

func manualSchemas() map[string]any {
	return map[string]any{
		"EmptyObject": obj(),
		"ErrorResponse": obj(
			prop("code", intSchema()),
			prop("message", strSchema()),
		),
		"Algo": obj(
			prop("algo_id", strSchema()),
			prop("description", strSchema()),
			prop("display_name", strSchema()),
		),
		"ParserConfig": obj(
			prop("name", strSchema()),
			prop("params", obj()),
			prop("type", strSchema()),
		),
		"Dataset": obj(
			prop("name", strSchema()),
			prop("dataset_id", strSchema()),
			prop("display_name", strSchema()),
			prop("desc", strSchema()),
			prop("cover_image", strSchema()),
			prop("state", strSchema()),
			prop("is_empty", boolSchema()),
			prop("document_count", int64Schema()),
			prop("document_size", int64Schema()),
			prop("segment_count", int64Schema()),
			prop("token_count", int64Schema()),
			prop("parsers", array(refSchema("ParserConfig"))),
			prop("algo", refSchema("Algo")),
			prop("creator", strSchema()),
			prop("is_owner", boolSchema()),
			prop("create_time", dateTimeSchema()),
			prop("update_time", dateTimeSchema()),
			prop("acl", array(strSchema())),
			prop("share_type", strSchema()),
			prop("type", strSchema()),
			prop("tags", array(strSchema())),
			prop("default_dataset", boolSchema()),
		),
		"ListAlgosResponse":      obj(prop("algos", array(refSchema("Algo")))),
		"AllDatasetTagsResponse": obj(prop("tags", array(strSchema()))),
		"ListDatasetsResponse": obj(
			prop("datasets", array(refSchema("Dataset"))),
			prop("total_size", intSchema()),
			prop("next_page_token", strSchema()),
		),
		"SetDefaultDatasetRequest":   objReq([]string{"name"}, prop("name", strSchema())),
		"UnsetDefaultDatasetRequest": objReq([]string{"name"}, prop("name", strSchema())),
		"DocumentTableColumn": obj(
			prop("id", intSchema()),
			prop("display_name", strSchema()),
			prop("type", strSchema()),
			prop("desc", strSchema()),
			prop("sample", strSchema()),
			prop("source_column", strSchema()),
			prop("index_type", strSchema()),
		),
		"Doc": obj(
			prop("name", strSchema()),
			prop("document_id", strSchema()),
			prop("display_name", strSchema()),
			prop("document_size", int64Schema()),
			prop("dataset_id", strSchema()),
			prop("dataset_display", strSchema()),
			prop("p_id", strSchema()),
			prop("creator", strSchema()),
			prop("uri", strSchema()),
			prop("file_url", strSchema()),
			prop("download_file_url", strSchema()),
			prop("columns", array(refSchema("DocumentTableColumn"))),
			prop("create_time", strSchema()),
			prop("update_time", strSchema()),
			prop("tags", array(strSchema())),
			prop("file_id", strSchema()),
			prop("data_source_type", strSchema()),
			prop("file_system_path", strSchema()),
			prop("type", strSchema()),
			prop("convert_file_uri", strSchema()),
			prop("rel_path", strSchema()),
			prop("document_stage", strSchema()),
			prop("pdf_convert_result", strSchema()),
			prop("child_document_count", int64Schema()),
			prop("child_folder_count", int64Schema()),
			prop("recursive_document_count", int64Schema()),
			prop("recursive_folder_count", int64Schema()),
			prop("recursive_file_size", int64Schema()),
			prop("children", array(refSchema("Doc"))),
		),
		"ListDocumentsResponse": obj(
			prop("documents", array(refSchema("Doc"))),
			prop("total_size", intSchema()),
			prop("next_page_token", strSchema()),
		),
		"SearchDocumentsRequest": obj(
			prop("parent", strSchema()),
			prop("p_id", strSchema()),
			prop("dir_path", strSchema()),
			prop("order_by", strSchema()),
			prop("page_token", strSchema()),
			prop("page_size", intSchema()),
			prop("keyword", strSchema()),
			prop("recursive", boolSchema()),
		),
		"BatchDeleteDocumentRequest": objReq([]string{"parent", "names"}, prop("parent", strSchema()), prop("names", array(strSchema()))),
		"UserInfo":                   obj(prop("id", strSchema()), prop("name", strSchema())),
		"DocumentCreatorsResponse":   obj(prop("creators", array(refSchema("UserInfo")))),
		"DocumentTagsResponse":       obj(prop("tags", array(strSchema()))),
		"DatasetRole":                obj(prop("role", strSchema()), prop("display_name", strSchema())),
		"DatasetMember": obj(
			prop("name", strSchema()),
			prop("dataset_id", strSchema()),
			prop("user_id", strSchema()),
			prop("user", strSchema()),
			prop("group", strSchema()),
			prop("role", refSchema("DatasetRole")),
			prop("create_time", strSchema()),
			prop("group_id", strSchema()),
		),
		"ListDatasetMembersResponse": obj(
			prop("dataset_members", array(refSchema("DatasetMember"))),
			prop("next_page_token", strSchema()),
		),
		"SearchDatasetMemberRequest": obj(
			prop("parent", strSchema()),
			prop("name_prefix", strSchema()),
			prop("is_all", boolSchema()),
			prop("page_token", strSchema()),
			prop("page_size", intSchema()),
		),
		"BatchAddDatasetMemberRequest": obj(
			prop("parent", strSchema()),
			prop("user_name_list", array(strSchema())),
			prop("group_name_list", array(strSchema())),
			prop("user_id_list", array(strSchema())),
			prop("group_id_list", array(strSchema())),
			prop("role", obj(prop("role", strSchema()))),
		),
		"BatchAddDatasetMemberResponse": obj(prop("dataset_members", array(refSchema("DatasetMember")))),
		"UpdateDatasetMemberRequest": obj(
			prop("dataset_member", refSchema("DatasetMember")),
			prop("update_mask", obj(prop("paths", array(strSchema())))),
		),
		"TaskFile": obj(
			prop("display_name", strSchema()),
			prop("stored_name", strSchema()),
			prop("stored_path", strSchema()),
			prop("parse_stored_path", strSchema()),
			prop("file_size", int64Schema()),
			prop("relative_path", strSchema()),
			prop("content_type", strSchema()),
		),
		"TaskDocumentInfo": obj(
			prop("document_id", strSchema()),
			prop("display_name", strSchema()),
			prop("document_state", strSchema()),
			prop("document_size", int64Schema()),
		),
		"TaskInfo": obj(
			prop("total_document_size", int64Schema()),
			prop("total_document_count", int64Schema()),
			prop("succeed_document_size", int64Schema()),
			prop("succeed_document_count", int64Schema()),
			prop("succeed_token_count", int64Schema()),
			prop("failed_document_size", int64Schema()),
			prop("failed_document_count", int64Schema()),
			prop("filtered_document_count", int64Schema()),
		),
		"TaskPayload": obj(
			prop("data_source_type", strSchema()),
			prop("task_type", strSchema()),
			prop("document_pid", strSchema()),
			prop("display_name", strSchema()),
			prop("document_id", strSchema()),
			prop("document_ids", array(strSchema())),
			prop("files", array(refSchema("TaskFile"))),
			prop("reparse_groups", array(strSchema())),
			prop("document_tags", array(strSchema())),
			prop("target_dataset_id", strSchema()),
			prop("target_path", strSchema()),
			prop("target_pid", strSchema()),
		),
		"CreateTaskItem": obj(
			prop("task", refSchema("TaskPayload")),
			prop("task_id", strSchema()),
			prop("cross_dataset", boolSchema()),
			prop("upload_file_id", strSchema()),
		),
		"CreateTaskRequest": objReq([]string{"items"}, prop("parent", strSchema()), prop("items", array(refSchema("CreateTaskItem")))),
		"TaskResponse": obj(
			prop("name", strSchema()),
			prop("task_id", strSchema()),
			prop("document_id", strSchema()),
			prop("data_source_type", strSchema()),
			prop("task_state", strSchema()),
			prop("creator", strSchema()),
			prop("err_msg", strSchema()),
			prop("task_info", refSchema("TaskInfo")),
			prop("document_info", array(refSchema("TaskDocumentInfo"))),
			prop("files", array(refSchema("TaskFile"))),
			prop("create_time", strSchema()),
			prop("start_time", strSchema()),
			prop("finish_time", strSchema()),
			prop("display_name", strSchema()),
			prop("task_type", strSchema()),
			prop("target_dataset_id", strSchema()),
			prop("target_pid", strSchema()),
			prop("parse_stored_path", strSchema()),
			prop("pdf_convert_result", strSchema()),
			prop("convert_required", boolSchema()),
			prop("convert_status", strSchema()),
			prop("convert_error", strSchema()),
		),
		"CreateTasksResponse":      obj(prop("tasks", array(refSchema("TaskResponse")))),
		"StartTaskRequest":         objReq([]string{"task_ids"}, prop("task_ids", array(strSchema())), prop("start_mode", strSchema())),
		"StartTaskResult":          obj(prop("task_id", strSchema()), prop("document_id", strSchema()), prop("display_name", strSchema()), prop("status", strSchema()), prop("submit_status", strSchema()), prop("message", strSchema())),
		"StartTasksResponse":       obj(prop("tasks", array(refSchema("StartTaskResult"))), prop("requested_count", intSchema()), prop("started_count", intSchema()), prop("failed_count", intSchema())),
		"SearchTasksRequest":       objReq([]string{"task_ids"}, prop("task_ids", array(strSchema())), prop("task_state", strSchema())),
		"SuspendJobRequest":        obj(prop("task_id", strSchema())),
		"ResumeTaskRequest":        obj(prop("task_id", strSchema())),
		"UploadFileResponse":       obj(prop("upload_file_id", strSchema()), prop("dataset_id", strSchema()), prop("filename", strSchema()), prop("stored_name", strSchema()), prop("stored_path", strSchema()), prop("relative_path", strSchema()), prop("document_pid", strSchema()), prop("document_tags", array(strSchema())), prop("file_size", int64Schema()), prop("content_type", strSchema()), prop("content_url", strSchema()), prop("download_url", strSchema()), prop("file_url", strSchema()), prop("status", strSchema()), prop("upload_scope", strSchema())),
		"UploadFilesResponse":      obj(prop("files", array(refSchema("UploadFileResponse")))),
		"InitUploadRequest":        objReq([]string{"filename"}, prop("document_pid", strSchema()), prop("relative_path", strSchema()), prop("filename", strSchema()), prop("file_size", int64Schema()), prop("content_type", strSchema()), prop("part_size", int64Schema()), prop("idempotency_key", strSchema())),
		"InitUploadResponse":       obj(prop("upload_id", strSchema()), prop("task_id", strSchema()), prop("document_id", strSchema()), prop("dataset_id", strSchema()), prop("stored_name", strSchema()), prop("upload_mode", strSchema()), prop("part_size", int64Schema()), prop("total_parts", intSchema()), prop("upload_state", strSchema()), prop("upload_scope", strSchema())),
		"UploadPartResponse":       obj(prop("upload_id", strSchema()), prop("part_number", intSchema()), prop("part_size", int64Schema()), prop("uploaded_parts", intSchema()), prop("total_parts", intSchema()), prop("upload_state", strSchema())),
		"CompleteUploadRequest":    obj(prop("auto_start", boolSchema()), prop("idempotency_key", strSchema())),
		"CompleteUploadResponse":   obj(prop("task_id", strSchema()), prop("upload_id", strSchema()), prop("document_id", strSchema()), prop("upload_file_id", strSchema()), prop("dataset_id", strSchema()), prop("stored_path", strSchema()), prop("parse_stored_path", strSchema()), prop("content_url", strSchema()), prop("download_url", strSchema()), prop("file_url", strSchema()), prop("file_size", int64Schema()), prop("convert_status", strSchema()), prop("convert_error", strSchema()), prop("upload_scope", strSchema())),
		"AbortUploadRequest":       obj(prop("reason", strSchema())),
		"AbortUploadResponse":      obj(prop("upload_id", strSchema()), prop("upload_state", strSchema())),
		"BatchUploadTasksResponse": obj(prop("tasks", array(refSchema("TaskResponse")))),
		"TransferBinding": obj(
			prop("source_document_id", strSchema()),
			prop("target_document_id", strSchema()),
			prop("source_lazy_doc_id", strSchema()),
			prop("target_lazy_doc_id", strSchema()),
			prop("display_name", strSchema()),
			prop("stored_path", strSchema()),
			prop("mode", strSchema()),
			prop("status", strSchema()),
			prop("error_message", strSchema()),
		),
		"ListTasksResponse":                obj(prop("tasks", array(refSchema("TaskResponse"))), prop("total_size", intSchema()), prop("next_page_token", strSchema())),
		"PromptRequest":                    objReq([]string{"display_name", "content"}, prop("display_name", strSchema()), prop("content", strSchema())),
		"PromptPatchRequest":               obj(prop("display_name", strSchema()), prop("content", strSchema())),
		"PromptItem":                       obj(prop("name", strSchema()), prop("id", strSchema()), prop("content", strSchema()), prop("display_name", strSchema()), prop("is_default", boolSchema())),
		"PromptListResponse":               obj(prop("prompts", array(refSchema("PromptItem"))), prop("next_page_token", strSchema()), prop("total", int64Schema())),
		"ConversationResumeRequest":        objReq([]string{"conversation_id"}, prop("conversation_id", strSchema()), prop("history_id", strSchema())),
		"ConversationStopRequest":          objReq([]string{"conversation_id"}, prop("conversation_id", strSchema()), prop("history_id", strSchema())),
		"ConversationSetHistoryRequest":    objReq([]string{"set_history_id", "deleted_history_id"}, prop("set_history_id", strSchema()), prop("deleted_history_id", strSchema())),
		"ConversationBatchDeleteRequest":   objReq([]string{"conversation_ids"}, prop("conversation_ids", array(strSchema()))),
		"ConversationBatchDeleteResponse":  obj(prop("deleted_count", intSchema()), prop("deleted_ids", array(strSchema()))),
		"ConversationFeedbackRequest":      objReq([]string{"history_id", "type"}, prop("history_id", strSchema()), prop("type", intSchema()), prop("reason", strSchema()), prop("expected_answer", strSchema())),
		"ConversationSwitchStatusRequest":  objReq([]string{"status"}, prop("status", intSchema())),
		"ConversationSwitchStatusResponse": obj(prop("status", intSchema())),
		"ConversationChatStatusResponse":   obj(prop("is_generating", boolSchema())),
		"ConversationItem": obj(
			prop("name", strSchema()), prop("conversation_id", strSchema()), prop("display_name", strSchema()), prop("search_config", obj()), prop("user", strSchema()), prop("chat_times", int64Schema()), prop("total_feedback_like", int64Schema()), prop("total_feedback_unlike", int64Schema()), prop("create_time", strSchema()), prop("update_time", strSchema()), prop("models", array(strSchema())),
		),
		"ConversationHistoryItem": obj(
			prop("seq", intSchema()), prop("query", strSchema()), prop("result", strSchema()), prop("id", strSchema()), prop("feed_back", intSchema()), prop("sources", array(obj())), prop("input", array(obj())), prop("reasoning_content", strSchema()), prop("reason", strSchema()), prop("expected_answer", strSchema()), prop("create_time", strSchema()),
		),
		"ConversationDetailResponse":  obj(prop("conversation", refSchema("ConversationItem")), prop("history", array(refSchema("ConversationHistoryItem")))),
		"ConversationListResponse":    obj(prop("conversations", array(refSchema("ConversationItem"))), prop("total_size", int64Schema()), prop("next_page_token", strSchema())),
		"SetChatHistoryResponse":      obj(prop("history_id", strSchema())),
		"ChatChunkResponse":           obj(prop("conversation_id", strSchema()), prop("seq", intSchema()), prop("message", strSchema()), prop("delta", strSchema()), prop("finish_reason", strSchema()), prop("history_id", strSchema()), prop("sources", array(obj())), prop("prompt_questions", array(strSchema())), prop("reasoning_content", strSchema()), prop("thinking_duration_s", int64Schema())),
		"ACLApiResponse":              obj(prop("code", intSchema()), prop("message", strSchema()), prop("data", obj())),
		"AddACLRequest":               objReq([]string{"grantee_type", "grantee_id", "permission"}, prop("grantee_type", strSchema()), prop("grantee_id", strSchema()), prop("permission", strSchema()), prop("expires_at", dateTimeSchema())),
		"UpdateACLRequest":            objReq([]string{"permission"}, prop("permission", strSchema()), prop("expires_at", dateTimeSchema())),
		"BatchAddACLItem":             objReq([]string{"grantee_type", "grantee_id", "permission"}, prop("grantee_type", strSchema()), prop("grantee_id", strSchema()), prop("permission", strSchema())),
		"BatchAddACLRequest":          objReq([]string{"items"}, prop("items", array(refSchema("BatchAddACLItem")))),
		"PermissionBatchRequest":      objReq([]string{"kb_ids"}, prop("kb_ids", array(strSchema()))),
		"ACLListItem":                 obj(prop("id", int64Schema()), prop("grantee_type", strSchema()), prop("grantee_id", strSchema()), prop("permission", strSchema()), prop("created_at", dateTimeSchema())),
		"ACLListData":                 obj(prop("list", array(refSchema("ACLListItem")))),
		"AddACLData":                  obj(prop("acl_id", int64Schema())),
		"BatchAddACLData":             obj(prop("count", intSchema()), prop("invalid_count", intSchema()), prop("failed_count", intSchema())),
		"PermissionResult":            obj(prop("permissions", array(strSchema())), prop("source", strSchema())),
		"PermissionBatchItem":         obj(prop("kb_id", strSchema()), prop("permissions", array(strSchema()))),
		"CanResult":                   obj(prop("allowed", boolSchema())),
		"KBListRow":                   obj(prop("id", strSchema()), prop("name", strSchema()), prop("visibility", strSchema()), prop("permissions", array(strSchema()))),
		"KBListResult":                obj(prop("total", int64Schema()), prop("list", array(refSchema("KBListRow")))),
		"AuthorizationSubjectGrant":   obj(prop("grantee_type", strSchema()), prop("grantee_id", strSchema()), prop("permissions", array(strSchema()))),
		"GetKBAuthorizationResponse":  obj(prop("kb_id", strSchema()), prop("grants", array(refSchema("AuthorizationSubjectGrant")))),
		"SetKBAuthorizationRequest":   obj(prop("grants", array(refSchema("AuthorizationSubjectGrant")))),
		"SetKBAuthorizationData":      obj(prop("kb_id", strSchema()), prop("subject_count", intSchema()), prop("acl_rows", intSchema())),
		"GrantPrincipal":              obj(prop("grantee_type", strSchema()), prop("grantee_id", strSchema()), prop("name", strSchema())),
		"ListGrantPrincipalsResponse": obj(prop("users", array(refSchema("GrantPrincipal"))), prop("groups", array(refSchema("GrantPrincipal")))),
	}
}

func manualPaths() map[string]any {
	return map[string]any{
		"/dataset/algos": map[string]any{"get": op("Dataset algorithm list", nil, nil, response(200, "Algorithm list", refSchema("ListAlgosResponse")))},
		"/dataset/tags":  map[string]any{"get": op("Dataset tags", queryParams(param("name", "order_by", false, strSchema()), param("query", "keyword", false, strSchema())), nil, response(200, "Dataset tags", refSchema("AllDatasetTagsResponse")))},
		"/datasets": map[string]any{
			"get":  op("Dataset list", queryParams(param("query", "page_token", false, strSchema()), param("query", "page_size", false, intSchema()), param("query", "order_by", false, strSchema()), param("query", "keyword", false, strSchema()), param("query", "tags", false, array(strSchema()))), nil, response(200, "Dataset list", refSchema("ListDatasetsResponse"))),
			"post": op("Create dataset", queryParams(param("query", "dataset_id", false, strSchema())), jsonBody(refSchema("Dataset"), false), response(200, "Created dataset", refSchema("Dataset"))),
		},
		"/datasets/{dataset}": map[string]any{
			"get":    op("Get dataset", nil, nil, response(200, "Dataset details", refSchema("Dataset"))),
			"delete": op("Delete dataset", nil, nil, response(200, "Deleted successfully", refSchema("EmptyObject"))),
			"patch":  op("Update dataset", nil, jsonBody(refSchema("Dataset"), false), response(200, "Updated dataset", refSchema("Dataset"))),
		},
		"/datasets/{dataset}:setDefault":   map[string]any{"post": op("Set as default dataset", nil, jsonBody(refSchema("SetDefaultDatasetRequest"), true), response(200, "Set successfully", refSchema("EmptyObject")))},
		"/datasets/{dataset}:unsetDefault": map[string]any{"post": op("Unset default dataset", nil, jsonBody(refSchema("UnsetDefaultDatasetRequest"), true), response(200, "Unset successfully", refSchema("EmptyObject")))},
		"/datasets/{dataset}/documents": map[string]any{
			"get":  op("Document list", queryParams(param("query", "page_token", false, strSchema()), param("query", "page_size", false, intSchema())), nil, response(200, "Document list", refSchema("ListDocumentsResponse"))),
			"post": op("Create document", queryParams(param("query", "document_id", false, strSchema())), jsonBody(refSchema("Doc"), false), response(200, "Created document", refSchema("Doc"))),
		},
		"/datasets/{dataset}/documents/{document}": map[string]any{
			"get":    op("Get document", nil, nil, response(200, "Document details", refSchema("Doc"))),
			"delete": op("Delete document", nil, nil, response(200, "Deleted successfully", refSchema("EmptyObject"))),
			"patch":  op("Update document", nil, jsonBody(refSchema("Doc"), false), response(200, "Updated document", refSchema("Doc"))),
		},
		"/datasets/{dataset}/documents/{document}:content":  map[string]any{"get": op("Preview document content", nil, nil, map[string]any{"description": "Document binary content", "content": map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}})},
		"/datasets/{dataset}/documents/{document}:download": map[string]any{"get": op("Download document", nil, nil, map[string]any{"description": "Document download content", "content": map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}})},
		"/datasets/{dataset}/documents:search":              map[string]any{"post": op("Search documents", nil, jsonBody(refSchema("SearchDocumentsRequest"), false), response(200, "Document search results", refSchema("ListDocumentsResponse")))},
		"/documents:search":                                 map[string]any{"post": op("textSearch documents", nil, jsonBody(refSchema("SearchDocumentsRequest"), false), response(200, "textDocument search results", refSchema("ListDocumentsResponse")))},
		"/datasets/{dataset}:batchDelete":                   map[string]any{"post": op("BatchDelete document", nil, jsonBody(refSchema("BatchDeleteDocumentRequest"), true), response(200, "Deleted successfully", refSchema("EmptyObject")))},
		"/document/creators":                                map[string]any{"get": op("Document creator list", nil, nil, response(200, "Creator list", refSchema("DocumentCreatorsResponse")))},
		"/document/tags":                                    map[string]any{"get": op("Document tag list", nil, nil, response(200, "Document tag list", refSchema("DocumentTagsResponse")))},
		"/datasets/{dataset}/members":                       map[string]any{"get": op("Dataset member list", nil, nil, response(200, "Member list", refSchema("ListDatasetMembersResponse")))},
		"/datasets/{dataset}/members/{user_id}": map[string]any{
			"get":    op("textUser ID Get datasetMember", nil, nil, response(200, "Member details", refSchema("DatasetMember"))),
			"delete": op("textUser ID Delete datasetMember", nil, nil, response(200, "Deleted successfully", refSchema("EmptyObject"))),
			"patch":  op("textUser ID Update datasetMember", nil, jsonBody(refSchema("UpdateDatasetMemberRequest"), true), response(200, "Updated member", refSchema("DatasetMember"))),
		},
		"/datasets/{dataset}/members/groups/{group_id}": map[string]any{
			"delete": op("Delete dataset group member", nil, nil, response(200, "Deleted successfully", refSchema("EmptyObject"))),
			"patch":  op("Update dataset group member", nil, jsonBody(refSchema("UpdateDatasetMemberRequest"), true), response(200, "Updated member", refSchema("DatasetMember"))),
		},
		"/datasets/{dataset}/members:search": map[string]any{"post": op("Search dataset members", queryParams(param("query", "name_prefix", false, strSchema())), jsonBody(refSchema("SearchDatasetMemberRequest"), false), response(200, "Member search results", refSchema("ListDatasetMembersResponse")))},
		"/datasets/{dataset}:batchAddMember": map[string]any{"post": op("Batch add dataset members", nil, jsonBody(refSchema("BatchAddDatasetMemberRequest"), true), response(200, "Added members", refSchema("BatchAddDatasetMemberResponse")))},
		"/datasets/{dataset}/tasks": map[string]any{
			"get":  op("Task list", queryParams(param("query", "page_token", false, strSchema()), param("query", "page_size", false, intSchema()), param("query", "task_state", false, strSchema()), param("query", "task_type", false, strSchema()), param("query", "document_id", false, strSchema()), param("query", "document_pid", false, strSchema())), nil, response(200, "Task list", refSchema("ListTasksResponse"))),
			"post": op("Create task", nil, jsonBody(refSchema("CreateTaskRequest"), true), response(200, "Created task", refSchema("CreateTasksResponse"))),
		},
		"/datasets/{dataset}/tasks:search":                      map[string]any{"post": op("Search tasks by task ID", nil, jsonBody(refSchema("SearchTasksRequest"), true), response(200, "Task search results", refSchema("ListTasksResponse")))},
		"/datasets/{dataset}/uploads":                           map[string]any{"post": multipartOp("Upload file", []map[string]any{param("formData", "relative_path", false, strSchema()), param("formData", "document_pid", false, strSchema()), param("formData", "document_tags", false, strSchema())}, response(200, "Upload filetext", refSchema("UploadFilesResponse")))},
		"/datasets/{dataset}/uploads/{upload_file_id}:content":  map[string]any{"get": op("PreviewtextUpload file", nil, nil, map[string]any{"description": "textUpload filetext", "content": map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}})},
		"/datasets/{dataset}/uploads/{upload_file_id}:download": map[string]any{"get": op("DownloadtextUpload file", nil, nil, map[string]any{"description": "textUpload fileDownloadtext", "content": map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}})},
		"/datasets/{dataset}/tasks:batchUpload":                 map[string]any{"post": multipartOp("BatchUploadtextCreate task", []map[string]any{param("formData", "relative_path", false, strSchema()), param("formData", "document_pid", false, strSchema()), param("formData", "document_tags", false, strSchema())}, response(200, "CreatetextTask list", refSchema("BatchUploadTasksResponse")))},
		"/datasets/{dataset}/tasks/{task}": map[string]any{
			"get":    op("Get task", nil, nil, response(200, "Task details", refSchema("TaskResponse"))),
			"delete": op("Delete task", nil, nil, response(200, "Deleted successfully", refSchema("EmptyObject"))),
		},
		"/datasets/{dataset}/tasks:start":                             map[string]any{"post": op("Start task", nil, jsonBody(refSchema("StartTaskRequest"), true), response(200, "Start result", refSchema("StartTasksResponse")))},
		"/datasets/{dataset}/tasks/{task}:resume":                     map[string]any{"post": op("Resume task", nil, jsonBody(refSchema("ResumeTaskRequest"), false), response(200, "Resume result", refSchema("StartTasksResponse")))},
		"/datasets/{dataset}/tasks/{task}:suspend":                    map[string]any{"post": op("Suspend task", nil, jsonBody(refSchema("SuspendJobRequest"), true), response(200, "Suspended successfully", refSchema("EmptyObject")))},
		"/datasets/{dataset}/uploads:initUpload":                      map[string]any{"post": op("Initialize dataset upload", nil, jsonBody(refSchema("InitUploadRequest"), true), response(200, "Upload initialization result", refSchema("InitUploadResponse")))},
		"/datasets/{dataset}/uploads/{upload_id}/parts/{part_number}": map[string]any{"put": binaryOp("Upload part", response(200, "Part upload result", refSchema("UploadPartResponse")))},
		"/datasets/{dataset}/uploads/{upload_id}:complete":            map[string]any{"post": op("Complete upload", nil, jsonBody(refSchema("CompleteUploadRequest"), false), response(200, "Complete uploadtext", refSchema("CompleteUploadResponse")))},
		"/datasets/{dataset}/uploads/{upload_id}:abort":               map[string]any{"post": op("Abort upload", nil, jsonBody(refSchema("AbortUploadRequest"), false), response(200, "Abort uploadtext", refSchema("AbortUploadResponse")))},
		"/temp/uploads":                                 map[string]any{"post": multipartOp("Upload temp file", nil, response(200, "Upload filetext", refSchema("UploadFilesResponse")))},
		"/temp/uploads:initUpload":                      map[string]any{"post": op("Initialize temp multipart upload", nil, jsonBody(refSchema("InitUploadRequest"), true), response(200, "Upload initialization result", refSchema("InitUploadResponse")))},
		"/temp/uploads/{upload_id}/parts/{part_number}": map[string]any{"put": binaryOp("Upload temp part", response(200, "Part upload result", refSchema("UploadPartResponse")))},
		"/temp/uploads/{upload_id}:complete":            map[string]any{"post": op("Complete temp upload", nil, jsonBody(refSchema("CompleteUploadRequest"), false), response(200, "Complete uploadtext", refSchema("CompleteUploadResponse")))},
		"/temp/uploads/{upload_id}:abort":               map[string]any{"post": op("Abort temp upload", nil, jsonBody(refSchema("AbortUploadRequest"), false), response(200, "Abort uploadtext", refSchema("AbortUploadResponse")))},
		"/prompts": map[string]any{
			"get":  op("Prompt list", queryParams(param("query", "page_size", false, intSchema()), param("query", "page_token", false, strSchema())), nil, response(200, "Prompt list", refSchema("PromptListResponse"))),
			"post": op("Create prompt", nil, jsonBody(refSchema("PromptRequest"), true), response(200, "Created prompt", refSchema("PromptItem"))),
		},
		"/prompts/{name}": map[string]any{
			"get":    op("Get prompt", nil, nil, response(200, "Prompt details", refSchema("PromptItem"))),
			"patch":  op("Update prompt", nil, jsonBody(refSchema("PromptPatchRequest"), true), response(200, "Updated prompt", refSchema("PromptItem"))),
			"delete": op("Delete prompt", nil, nil, response(200, "Deleted successfully", refSchema("EmptyObject"))),
		},
		"/prompts/{name}:setDefault":              map[string]any{"post": op("Set as default prompt", nil, nil, response(200, "Set successfully", refSchema("EmptyObject")))},
		"/prompts/{name}:unsetDefault":            map[string]any{"post": op("Unset default prompt", nil, nil, response(200, "Unset successfully", refSchema("EmptyObject")))},
		"/conversations:resumeChat":               map[string]any{"post": sseOp("Resume conversation stream", jsonBody(refSchema("ConversationResumeRequest"), true), response(200, "SSE streaming response item is ChatChunkResponse wrapped by result", refSchema("ChatChunkResponse")))},
		"/conversations:stopChatGeneration":       map[string]any{"post": op("Stop conversation generation", nil, jsonBody(refSchema("ConversationStopRequest"), true), response(200, "Stopped successfully", refSchema("EmptyObject")))},
		"/conversations/{conversation_id}:status": map[string]any{"get": op("Get conversation status", nil, nil, response(200, "Conversation status", refSchema("ConversationChatStatusResponse")))},
		"/conversations/{name}": map[string]any{
			"get":    op("Get conversation", nil, nil, response(200, "Conversationtext", refSchema("ConversationItem"))),
			"delete": op("Delete conversation", nil, nil, response(200, "Deleted successfully", refSchema("EmptyObject"))),
		},
		"/conversations/{name}:detail":       map[string]any{"get": op("Get conversationtext", nil, nil, response(200, "Conversation details and history", refSchema("ConversationDetailResponse")))},
		"/conversations":                     map[string]any{"get": op("Conversation list", queryParams(param("query", "keyword", false, strSchema()), param("query", "page_size", false, intSchema()), param("query", "page_token", false, strSchema())), nil, response(200, "Conversation list", refSchema("ConversationListResponse")))},
		"/conversations:setChatHistory":      map[string]any{"post": op("Set conversation history", nil, jsonBody(refSchema("ConversationSetHistoryRequest"), true), response(200, "Set result", refSchema("SetChatHistoryResponse")))},
		"/conversations:batchDelete":         map[string]any{"post": op("Batch delete conversations", nil, jsonBody(refSchema("ConversationBatchDeleteRequest"), true), response(200, "Batch deleted conversations", refSchema("ConversationBatchDeleteResponse")))},
		"/conversations:feedBackChatHistory": map[string]any{"post": op("Feedback conversation history", nil, jsonBody(refSchema("ConversationFeedbackRequest"), true), response(200, "Feedback succeeded", refSchema("EmptyObject")))},
		"/conversation:switchStatus": map[string]any{
			"get":  op("Get multi-answer switch status", nil, nil, response(200, "Multi-answer switch status", refSchema("ConversationSwitchStatusResponse"))),
			"post": op("SetMulti-answer switch status", nil, jsonBody(refSchema("ConversationSwitchStatusRequest"), true), response(200, "SettextMulti-answer switch status", refSchema("ConversationSwitchStatusResponse"))),
		},
		"/kb/list":               map[string]any{"get": op("Knowledge base list", queryParams(param("query", "permission", false, strSchema()), param("query", "keyword", false, strSchema()), param("query", "page", false, intSchema()), param("query", "page_size", false, intSchema())), nil, aclResponse(refSchema("KBListResult")))},
		"/kb/permission/batch":   map[string]any{"post": op("Batch query knowledge base permissions", nil, jsonBody(refSchema("PermissionBatchRequest"), true), aclResponse(array(refSchema("PermissionBatchItem"))))},
		"/kb/{kb_id}/permission": map[string]any{"get": op("Query knowledge base permissions", nil, nil, aclResponse(refSchema("PermissionResult")))},
		"/kb/{kb_id}/can":        map[string]any{"get": op("Check knowledge base operation permission", queryParams(param("query", "action", true, strSchema())), nil, aclResponse(refSchema("CanResult")))},
		"/kb/{kb_id}/acl": map[string]any{
			"get":  op("ACL list", queryParams(param("query", "grantee_type", false, strSchema())), nil, aclResponse(refSchema("ACLListData"))),
			"post": op("Add ACL", nil, jsonBody(refSchema("AddACLRequest"), true), aclResponse(refSchema("AddACLData"))),
		},
		"/kb/{kb_id}/acl/batch": map[string]any{"post": op("BatchAdd ACL", nil, jsonBody(refSchema("BatchAddACLRequest"), true), aclResponse(refSchema("BatchAddACLData")))},
		"/kb/{kb_id}/acl/{acl_id}": map[string]any{
			"put":    op("Update ACL", nil, jsonBody(refSchema("UpdateACLRequest"), true), aclResponse(refSchema("EmptyObject"))),
			"delete": op("Delete ACL", nil, nil, aclResponse(refSchema("EmptyObject"))),
		},
		"/kb/{kb_id}/authorization": map[string]any{
			"get":  op("Get knowledge base authorization", nil, nil, aclResponse(refSchema("GetKBAuthorizationResponse"))),
			"post": op("Set knowledge base authorization", nil, jsonBody(refSchema("SetKBAuthorizationRequest"), true), aclResponse(refSchema("SetKBAuthorizationData"))),
		},
		"/kb/grant-principals": map[string]any{"get": op("Get grantable principals", nil, nil, aclResponse(refSchema("ListGrantPrincipalsResponse")))},
	}
}

func obj(props ...map[string]any) map[string]any {
	m := map[string]any{"type": "object", "properties": map[string]any{}}
	p := m["properties"].(map[string]any)
	for _, item := range props {
		for k, v := range item {
			p[k] = v
		}
	}
	return m
}

func objReq(required []string, props ...map[string]any) map[string]any {
	m := obj(props...)
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func prop(name string, schema map[string]any) map[string]any { return map[string]any{name: schema} }
func strSchema() map[string]any                              { return map[string]any{"type": "string"} }
func boolSchema() map[string]any                             { return map[string]any{"type": "boolean"} }
func intSchema() map[string]any                              { return map[string]any{"type": "integer"} }
func int64Schema() map[string]any                            { return map[string]any{"type": "integer", "format": "int64"} }
func dateTimeSchema() map[string]any                         { return map[string]any{"type": "string", "format": "date-time"} }
func array(item map[string]any) map[string]any               { return map[string]any{"type": "array", "items": item} }
func refSchema(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func param(in, name string, required bool, schema map[string]any) map[string]any {
	return map[string]any{"in": in, "name": name, "required": required, "schema": schema}
}

func queryParams(params ...map[string]any) []map[string]any { return params }

func jsonBody(schema map[string]any, required bool) map[string]any {
	return map[string]any{"required": required, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
}

func multipartOp(summary string, formParams []map[string]any, resp map[string]any) map[string]any {
	props := map[string]any{"files": map[string]any{"type": "array", "items": map[string]any{"type": "string", "format": "binary"}}}
	for _, p := range formParams {
		name := p["name"].(string)
		props[name] = p["schema"]
	}
	return map[string]any{
		"summary":     summary,
		"requestBody": map[string]any{"required": true, "content": map[string]any{"multipart/form-data": map[string]any{"schema": map[string]any{"type": "object", "properties": props}}}},
		"responses":   map[string]any{"200": resp},
	}
}

func binaryOp(summary string, resp map[string]any) map[string]any {
	return map[string]any{
		"summary":     summary,
		"requestBody": map[string]any{"required": true, "content": map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}},
		"responses":   map[string]any{"200": resp},
	}
}

func sseOp(summary string, body map[string]any, resp map[string]any) map[string]any {
	return map[string]any{
		"summary":     summary,
		"requestBody": body,
		"responses":   map[string]any{"200": map[string]any{"description": resp["description"], "content": map[string]any{"text/event-stream": map[string]any{"schema": resp["content"].(map[string]any)["application/json"].(map[string]any)["schema"]}}}},
	}
}

func response(status int, desc string, schema map[string]any) map[string]any {
	return map[string]any{"description": desc, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
}

func aclResponse(schema map[string]any) map[string]any {
	return map[string]any{
		"description": "OK",
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{
					"allOf": []any{
						refSchema("ACLApiResponse"),
						map[string]any{"type": "object", "properties": map[string]any{"data": schema}},
					},
				},
			},
		},
	}
}

func op(summary string, params []map[string]any, body map[string]any, resp map[string]any) map[string]any {
	m := map[string]any{"summary": summary, "responses": map[string]any{"200": resp}}
	if len(params) > 0 {
		m["parameters"] = params
	}
	if body != nil {
		m["requestBody"] = body
	}
	return m
}
