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
		"CurrentMemoryDocument": map[string]any{
			"type": "object",
			"additionalProperties": map[string]any{
				"oneOf": []any{
					map[string]any{
						"type":     "string",
						"nullable": true,
					},
					array(strSchema()),
					refSchema("CurrentMemoryDocument"),
				},
			},
		},
		"CurrentMemoryLocalizedText": map[string]any{
			"type":                 "object",
			"additionalProperties": strSchema(),
		},
		"CurrentMemoryPresentationField": objReq(
			[]string{"path", "labels", "summary_role"},
			prop("path", strSchema()),
			prop("labels", refSchema("CurrentMemoryLocalizedText")),
			prop("summary_role", enumStringSchema("title", "subtitle", "description", "tag", "none")),
		),
		"CurrentMemoryPresentationSection": objReq(
			[]string{"path", "labels", "fields"},
			prop("path", strSchema()),
			prop("labels", refSchema("CurrentMemoryLocalizedText")),
			prop("fields", array(refSchema("CurrentMemoryPresentationField"))),
		),
		"CurrentMemoryPresentation": objReq(
			[]string{"fallbacks", "sections"},
			prop("fallbacks", map[string]any{
				"type":                 "object",
				"additionalProperties": refSchema("CurrentMemoryLocalizedText"),
			}),
			prop("sections", array(refSchema("CurrentMemoryPresentationSection"))),
		),
		"CurrentMemorySoulData": objReq(
			[]string{"document", "template_version", "presentation", "updated_at"},
			prop("document", refSchema("CurrentMemoryDocument")),
			prop("template_version", intSchema()),
			prop("presentation", refSchema("CurrentMemoryPresentation")),
			prop("updated_at", int64Schema()),
		),
		"CurrentMemorySoulResponse": objReq(
			[]string{"code", "message", "data"},
			prop("code", intSchema()),
			prop("message", strSchema()),
			prop("data", refSchema("CurrentMemorySoulData")),
		),
		"CurrentMemoryOperation": objReq(
			[]string{"op", "path"},
			prop("op", enumStringSchema("set", "clear", "add", "remove")),
			prop("path", strSchema()),
			prop("value", strSchema()),
		),
		"CurrentMemoryOperationsRequest": objReq(
			[]string{"operations"},
			prop("operations", array(refSchema("CurrentMemoryOperation"))),
		),
		"CurrentMemoryProfileData": objReq(
			[]string{"document", "template_version", "presentation", "updated_at"},
			prop("document", refSchema("CurrentMemoryDocument")),
			prop("template_version", intSchema()),
			prop("presentation", refSchema("CurrentMemoryPresentation")),
			prop("updated_at", int64Schema()),
		),
		"CurrentMemoryProfileResponse": objReq(
			[]string{"code", "message", "data"},
			prop("code", intSchema()),
			prop("message", strSchema()),
			prop("data", refSchema("CurrentMemoryProfileData")),
		),
		"CurrentMemoryAvatarData": objReq(
			[]string{"kind", "content_type", "size", "updated_at"},
			prop("kind", enumStringSchema("soul", "profile")),
			prop("content_type", enumStringSchema("image/png", "image/jpeg", "image/webp")),
			prop("size", int64Schema()),
			prop("updated_at", int64Schema()),
		),
		"CurrentMemoryAvatarResponse": objReq(
			[]string{"code", "message", "data"},
			prop("code", intSchema()),
			prop("message", strSchema()),
			prop("data", refSchema("CurrentMemoryAvatarData")),
		),
		"CurrentMemoryPreferenceItem": objReq(
			[]string{"name", "summary", "created_at", "updated_at"},
			prop("name", strSchema()),
			prop("summary", strSchema()),
			prop("created_at", dateTimeSchema()),
			prop("updated_at", dateTimeSchema()),
		),
		"CurrentMemoryPreferenceResidentIndexUsage": objReq(
			[]string{"used_items", "max_items", "over_limit"},
			prop("used_items", int64Schema()),
			prop("max_items", int64Schema()),
			prop("over_limit", boolSchema()),
		),
		"CurrentMemoryReferenceSource": objReq(
			[]string{"kind", "conversation_id"},
			prop("kind", enumStringSchema("memory_review", "chat_explicit")),
			prop("conversation_id", strSchema()),
		),
		"CurrentMemoryReference": objReq(
			[]string{"name", "summary", "created_at", "updated_at", "source", "application_scenarios", "preference_details", "reason"},
			prop("name", strSchema()),
			prop("summary", strSchema()),
			prop("created_at", dateTimeSchema()),
			prop("updated_at", dateTimeSchema()),
			prop("source", refSchema("CurrentMemoryReferenceSource")),
			prop("application_scenarios", strSchema()),
			prop("preference_details", strSchema()),
			prop("reason", strSchema()),
		),
		"CurrentMemoryPreferenceListData": objReq(
			[]string{"items", "total_size", "resident_index_usage", "etag", "updated_at"},
			prop("items", array(refSchema("CurrentMemoryPreferenceItem"))),
			prop("total_size", int64Schema()),
			prop("resident_index_usage", refSchema("CurrentMemoryPreferenceResidentIndexUsage")),
			prop("etag", strSchema()),
			prop("updated_at", int64Schema()),
		),
		"CurrentMemoryPreferenceListResponse": objReq(
			[]string{"code", "message", "data"},
			prop("code", intSchema()),
			prop("message", strSchema()),
			prop("data", refSchema("CurrentMemoryPreferenceListData")),
		),
		"CurrentMemoryPreferenceDetailData": objReq(
			[]string{"item", "reference_status", "reference"},
			prop("item", refSchema("CurrentMemoryPreferenceItem")),
			prop("reference_status", enumStringSchema("available", "missing")),
			prop("reference", nullableSchema(refSchema("CurrentMemoryReference"))),
		),
		"CurrentMemoryPreferenceDetailResponse": objReq(
			[]string{"code", "message", "data"},
			prop("code", intSchema()),
			prop("message", strSchema()),
			prop("data", refSchema("CurrentMemoryPreferenceDetailData")),
		),
		"CurrentMemoryPreferenceOrderRequest": objReq(
			[]string{"ordered_names", "expected_etag"},
			prop("ordered_names", array(strSchema())),
			prop("expected_etag", strSchema()),
		),
		"CurrentMemoryConflictData": obj(
			prop("current_etag", strSchema()),
		),
		"CurrentMemoryErrorResponse": objReq(
			[]string{"code", "message"},
			prop("code", intSchema()),
			prop("message", strSchema()),
			prop("data", refSchema("CurrentMemoryConflictData")),
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
			prop("created_by_data_source", boolSchema()),
			prop("source_type", strSchema()),
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
			prop("reparse_mode", strSchema()),
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
			prop("content_hash", strSchema()),
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
		"UploadFileResponse":       obj(prop("upload_file_id", strSchema()), prop("content_hash", strSchema()), prop("dataset_id", strSchema()), prop("filename", strSchema()), prop("stored_name", strSchema()), prop("stored_path", strSchema()), prop("relative_path", strSchema()), prop("document_pid", strSchema()), prop("document_tags", array(strSchema())), prop("file_size", int64Schema()), prop("content_type", strSchema()), prop("content_url", strSchema()), prop("download_url", strSchema()), prop("file_url", strSchema()), prop("status", strSchema()), prop("upload_scope", strSchema())),
		"UploadFilesResponse":      obj(prop("files", array(refSchema("UploadFileResponse")))),
		"CheckFileHashesRequest":   objReq([]string{"hashes"}, prop("hashes", array(strSchema()))),
		"CheckFileHashesResponse":  obj(prop("missing_hashes", array(strSchema()))),
		"InitUploadRequest":        objReq([]string{"filename"}, prop("document_pid", strSchema()), prop("relative_path", strSchema()), prop("filename", strSchema()), prop("file_size", int64Schema()), prop("content_type", strSchema()), prop("part_size", int64Schema()), prop("idempotency_key", strSchema())),
		"InitUploadResponse":       obj(prop("upload_id", strSchema()), prop("task_id", strSchema()), prop("document_id", strSchema()), prop("dataset_id", strSchema()), prop("stored_name", strSchema()), prop("upload_mode", strSchema()), prop("part_size", int64Schema()), prop("total_parts", intSchema()), prop("upload_state", strSchema()), prop("upload_scope", strSchema())),
		"UploadPartResponse":       obj(prop("upload_id", strSchema()), prop("part_number", intSchema()), prop("part_size", int64Schema()), prop("uploaded_parts", intSchema()), prop("total_parts", intSchema()), prop("upload_state", strSchema())),
		"CompleteUploadRequest":    obj(prop("auto_start", boolSchema()), prop("idempotency_key", strSchema())),
		"CompleteUploadResponse":   obj(prop("task_id", strSchema()), prop("upload_id", strSchema()), prop("document_id", strSchema()), prop("upload_file_id", strSchema()), prop("content_hash", strSchema()), prop("dataset_id", strSchema()), prop("stored_path", strSchema()), prop("parse_stored_path", strSchema()), prop("content_url", strSchema()), prop("download_url", strSchema()), prop("file_url", strSchema()), prop("file_size", int64Schema()), prop("convert_status", strSchema()), prop("convert_error", strSchema()), prop("upload_scope", strSchema())),
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
		"PromptRequest":                    objReq([]string{"display_name", "content"}, prop("display_name", strSchema()), prop("content", strSchema()), prop("category", strSchema())),
		"PromptPatchRequest":               obj(prop("display_name", strSchema()), prop("content", strSchema()), prop("category", strSchema())),
		"PromptCategoryRequest":            objReq([]string{"name"}, prop("name", strSchema())),
		"PromptCategory":                   objReq([]string{"id", "name"}, prop("id", strSchema()), prop("name", strSchema())),
		"PromptCategoryListResponse":       obj(prop("categories", array(refSchema("PromptCategory")))),
		"PromptPolishRequest":              objReq([]string{"content", "user_instruct"}, prop("content", strSchema()), prop("user_instruct", strSchema()), prop("allow_empty", boolSchema())),
		"PromptPolishResponse":             obj(prop("content", strSchema())),
		"PromptItem":                       obj(prop("name", strSchema()), prop("id", strSchema()), prop("content", strSchema()), prop("display_name", strSchema()), prop("category", strSchema()), prop("source", strSchema()), prop("is_favorite", boolSchema()), prop("usage_count", int64Schema()), prop("last_used_at", strSchema()), prop("created_at", strSchema()), prop("updated_at", strSchema())),
		"PromptFacets":                     obj(prop("scopes", obj()), prop("categories", obj()), prop("category_total", int64Schema())),
		"PromptListResponse":               obj(prop("prompts", array(refSchema("PromptItem"))), prop("custom_categories", array(refSchema("PromptCategory"))), prop("next_page_token", strSchema()), prop("total", int64Schema()), prop("facets", refSchema("PromptFacets"))),
		"PromptStateResponse":              obj(prop("id", strSchema()), prop("is_favorite", boolSchema()), prop("usage_count", int64Schema()), prop("last_used_at", strSchema())),
		"ToolMethod":                       obj(prop("name", strSchema()), prop("summary", strSchema())),
		"ToolGroup":                        obj(prop("name", strSchema()), prop("label", strSchema()), prop("description", strSchema()), prop("methods", array(refSchema("ToolMethod"))), prop("can_disable", boolSchema()), prop("active", boolSchema()), prop("disabled", boolSchema())),
		"ToolListResponse":                 obj(prop("tool_groups", array(refSchema("ToolGroup"))), prop("page", intSchema()), prop("page_size", intSchema()), prop("total", intSchema())),
		"ToolStateResponse":                obj(prop("name", strSchema()), prop("disabled", boolSchema())),
		"ConversationResumeRequest":        objReq([]string{"conversation_id"}, prop("conversation_id", strSchema()), prop("history_id", strSchema())),
		"ConversationStopRequest":          objReq([]string{"conversation_id"}, prop("conversation_id", strSchema()), prop("history_id", strSchema())),
		"ConversationSetHistoryRequest":    objReq([]string{"set_history_id", "deleted_history_id"}, prop("set_history_id", strSchema()), prop("deleted_history_id", strSchema())),
		"ConversationBatchDeleteRequest":   objReq([]string{"conversation_ids"}, prop("conversation_ids", array(strSchema()))),
		"ConversationBatchDeleteResponse":  obj(prop("deleted_count", intSchema()), prop("deleted_ids", array(strSchema()))),
		"ConversationArchiveFolderRequest": objReq([]string{"name"}, prop("name", strSchema())),
		"ConversationArchiveRequest":       obj(prop("folder_id", nullableSchema(strSchema()))),
		"ConversationArchiveFolder": objReq(
			[]string{"id", "name", "dialog_count", "task_count", "total_count", "created_at", "updated_at"},
			prop("id", strSchema()), prop("name", strSchema()),
			prop("dialog_count", int64Schema()), prop("task_count", int64Schema()), prop("total_count", int64Schema()),
			prop("created_at", dateTimeSchema()), prop("updated_at", dateTimeSchema()),
		),
		"ConversationArchiveFolderListResponse": objReq(
			[]string{"folders", "unfiled_dialog_count", "unfiled_task_count", "unfiled_total_count"},
			prop("folders", array(refSchema("ConversationArchiveFolder"))),
			prop("unfiled_dialog_count", int64Schema()), prop("unfiled_task_count", int64Schema()), prop("unfiled_total_count", int64Schema()),
		),
		"ConversationArchiveFolderCreateResponse": objReq([]string{"folder"}, prop("folder", refSchema("ConversationArchiveFolder"))),
		"ConversationArchiveFolderDeleteResponse": objReq([]string{"moved_count"}, prop("moved_count", int64Schema())),
		"ConversationRecoveryItem": objReq(
			[]string{"conversation_id", "display_name", "kind", "create_time", "update_time"},
			prop("conversation_id", strSchema()), prop("display_name", strSchema()),
			prop("kind", enumStringSchema("dialog", "task")), prop("folder_id", nullableSchema(strSchema())),
			prop("archive_folder_name", strSchema()), prop("create_time", dateTimeSchema()), prop("update_time", dateTimeSchema()),
			prop("archived_at", dateTimeSchema()), prop("deleted_at", dateTimeSchema()), prop("trash_expires_at", dateTimeSchema()),
		),
		"ConversationRecoveryListResponse": objReq(
			[]string{"items", "total", "page", "page_size"},
			prop("items", array(refSchema("ConversationRecoveryItem"))), prop("total", int64Schema()),
			prop("page", intSchema()), prop("page_size", intSchema()),
		),
		"RecoveryDeleteCountResponse": obj(prop("deleted_count", intSchema())),
		"WorkflowTrashItem": objReq(
			[]string{"id", "name", "deleted_at", "trash_expires_at", "updated_at"},
			prop("id", strSchema()), prop("name", strSchema()), prop("workflow_id", strSchema()),
			prop("published_workflow_ref", strSchema()), prop("published_status_before_trash", strSchema()),
			prop("deleted_at", dateTimeSchema()), prop("trash_expires_at", dateTimeSchema()), prop("updated_at", dateTimeSchema()),
		),
		"WorkflowTrashListData": objReq(
			[]string{"records", "total", "page", "page_size"}, prop("records", array(refSchema("WorkflowTrashItem"))),
			prop("total", int64Schema()), prop("page", intSchema()), prop("page_size", intSchema()),
		),
		"WorkflowTrashListResponse": objReq(
			[]string{"code", "message", "data"}, prop("code", intSchema()), prop("message", strSchema()), prop("data", refSchema("WorkflowTrashListData")),
		),
		"CoreEmptyResponse":      objReq([]string{"code", "message"}, prop("code", intSchema()), prop("message", strSchema())),
		"WorkflowTrashEmptyData": obj(prop("purged", intSchema())),
		"WorkflowTrashEmptyResponse": objReq(
			[]string{"code", "message", "data"}, prop("code", intSchema()), prop("message", strSchema()), prop("data", refSchema("WorkflowTrashEmptyData")),
		),
		"TaskCenterStepInfo": objReq(
			[]string{"step_id", "status"}, prop("step_id", strSchema()), prop("title", strSchema()),
			prop("status", strSchema()), prop("current_phase", strSchema()), prop("summary", strSchema()), prop("artifact", strSchema()),
		),
		"TaskCenterTaskResponse": objReq(
			[]string{"id", "user_id", "conversation_id", "conversation_state", "task_type", "status", "steps", "created_at", "updated_at"},
			prop("id", strSchema()), prop("user_id", strSchema()), prop("conversation_id", strSchema()),
			prop("conversation_state", enumStringSchema("active", "archived", "trash", "missing")), prop("conversation_title", strSchema()),
			prop("workflow_session_id", strSchema()), prop("task_type", strSchema()), prop("title", strSchema()), prop("status", strSchema()),
			prop("schedule_id", strSchema()), prop("schedule_name", strSchema()), prop("steps", array(refSchema("TaskCenterStepInfo"))),
			prop("progress", obj()), prop("created_at", dateTimeSchema()), prop("updated_at", dateTimeSchema()),
			prop("finished_at", dateTimeSchema()), prop("waiting_reason", strSchema()),
		),
		"TaskCenterStatusCounts": obj(
			prop("all", int64Schema()), prop("pending", int64Schema()), prop("waiting", int64Schema()),
			prop("waiting_inputs", int64Schema()), prop("running", int64Schema()), prop("succeeded", int64Schema()),
			prop("failed", int64Schema()), prop("canceled", int64Schema()),
		),
		"TaskCenterTaskListResponse": objReq(
			[]string{"items", "total", "page", "page_size"}, prop("items", array(refSchema("TaskCenterTaskResponse"))),
			prop("total", int64Schema()), prop("page", intSchema()), prop("page_size", intSchema()), prop("status_counts", refSchema("TaskCenterStatusCounts")),
		),
		"ConversationFeedbackRequest": objReq(
			[]string{"history_id", "type"},
			prop("history_id", strSchema()),
			prop("type", feedbackTypeSchema()),
			prop("reason", strSchema()),
			prop("expected_answer", strSchema()),
		),
		"ConversationSwitchStatusRequest":  objReq([]string{"status"}, prop("status", intSchema())),
		"ConversationSwitchStatusResponse": obj(prop("status", intSchema())),
		"ConversationChatStatusResponse":   obj(prop("is_generating", boolSchema())),
		"ConversationItem": obj(
			prop("name", strSchema()), prop("conversation_id", strSchema()), prop("display_name", strSchema()), prop("search_config", obj()), prop("user", strSchema()), prop("chat_times", int64Schema()), prop("total_feedback_like", int64Schema()), prop("total_feedback_unlike", int64Schema()), prop("create_time", strSchema()), prop("update_time", strSchema()), prop("pinned_at", nullableSchema(dateTimeSchema())), prop("is_pinned", boolSchema()), prop("models", array(strSchema())), prop("chat_executor", enumStringSchema("lazymind", "codex", "cursor", "workbuddy")), prop("thinking_depth", enumStringSchema("low", "medium", "high", "max")), prop("assistant", enumStringSchema("lazymind", "codex", "cursor", "workbuddy")), prop("project_key", strSchema()), prop("project_name", strSchema()),
		),
		"ConversationPinResponse": objReq(
			[]string{"conversation_id", "is_pinned"},
			prop("conversation_id", strSchema()), prop("is_pinned", boolSchema()), prop("pinned_at", nullableSchema(dateTimeSchema())),
		),
		"ExternalExecutionInvocation": obj(
			prop("total", intSchema()), prop("running", intSchema()), prop("succeeded", intSchema()),
			prop("failed", intSchema()), prop("interrupted", intSchema()), prop("tools", array(strSchema())),
		),
		"ExternalExecutionWorkflow": obj(
			prop("session_id", strSchema()), prop("workflow_id", strSchema()), prop("status", strSchema()),
			prop("current_step_id", strSchema()), prop("state_version", int64Schema()),
			prop("artifact_count", int64Schema()), prop("artifact_revision_count", int64Schema()),
		),
		"ExternalExecutionProjection": obj(
			prop("run_id", strSchema()), prop("history_id", strSchema()), prop("provider", strSchema()),
			prop("status", enumStringSchema("pending", "running", "completed", "failed", "stopped")),
			prop("host_id", strSchema()), prop("host_online", boolSchema()), prop("claim_count", intSchema()),
			prop("recovery_count", intSchema()), prop("event_count", int64Schema()),
			prop("invocation", refSchema("ExternalExecutionInvocation")),
			prop("workflows", array(refSchema("ExternalExecutionWorkflow"))),
			prop("artifact_count", int64Schema()), prop("artifact_revision_count", int64Schema()),
			prop("error_message", strSchema()), prop("claimed_at", dateTimeSchema()),
			prop("last_heartbeat_at", dateTimeSchema()), prop("completed_at", dateTimeSchema()),
			prop("created_at", dateTimeSchema()), prop("updated_at", dateTimeSchema()),
		),
		"ConversationHistoryItem": obj(
			prop("seq", intSchema()), prop("query", strSchema()), prop("result", strSchema()), prop("id", strSchema()), prop("feed_back", intSchema()), prop("sources", array(obj())), prop("input", array(obj())), prop("reasoning_content", strSchema()), prop("thinking_time_s", int64Schema()), prop("reason", strSchema()), prop("expected_answer", strSchema()), prop("create_time", strSchema()), prop("run_id", strSchema()), prop("run_status", strSchema()), prop("run_terminal", refSchema("RunTerminal")), prop("execution", refSchema("ExternalExecutionProjection")),
		),
		"ConversationDetailResponse":      obj(prop("conversation", refSchema("ConversationItem"))),
		"ConversationHistoryListResponse": obj(prop("conversation_id", strSchema()), prop("name", strSchema()), prop("history", array(refSchema("ConversationHistoryItem"))), prop("total_size", int64Schema()), prop("next_page_token", strSchema())),
		"ConversationTrailItem": obj(
			prop("history_id", strSchema()), prop("seq", intSchema()), prop("summary", strSchema()), prop("question", strSchema()), prop("depth", intSchema()), prop("parent_history_id", strSchema()), prop("source", strSchema()), prop("create_time", strSchema()),
		),
		"ConversationTrailListResponse": obj(prop("conversation_id", strSchema()), prop("name", strSchema()), prop("items", array(refSchema("ConversationTrailItem"))), prop("total_size", int64Schema()), prop("next_page_token", strSchema())),
		"ConversationListResponse":      obj(prop("conversations", array(refSchema("ConversationItem"))), prop("total_size", int64Schema()), prop("next_page_token", strSchema())),
		"EpisodeMemory": objReq(
			[]string{
				"id",
				"conversation_id",
				"source_kind",
				"episode_type",
				"summary",
				"occurred_at_ms",
				"recorded_at_ms",
				"hit_count",
			},
			prop("id", strSchema()),
			prop("conversation_id", strSchema()),
			prop("source_kind", enumStringSchema("chat_explicit", "memory_review")),
			prop("episode_type", enumStringSchema("decision", "progress", "result", "blocker", "event")),
			prop("summary", strSchema()),
			prop("occurred_at_ms", int64Schema()),
			prop("recorded_at_ms", int64Schema()),
			prop("hit_count", int64Schema()),
		),
		"EpisodeMemoryListData": objReq(
			[]string{"items", "total_size", "next_page_token"},
			prop("items", array(refSchema("EpisodeMemory"))),
			prop("total_size", int64Schema()),
			prop("next_page_token", strSchema()),
		),
		"EpisodeMemoryListResponse": objReq(
			[]string{"code", "message", "data"},
			prop("code", intSchema()),
			prop("message", strSchema()),
			prop("data", refSchema("EpisodeMemoryListData")),
		),
		"EpisodeMemoryDetailResponse": objReq(
			[]string{"code", "message", "data"},
			prop("code", intSchema()),
			prop("message", strSchema()),
			prop("data", refSchema("EpisodeMemory")),
		),
		"SetChatHistoryResponse": obj(prop("history_id", strSchema())),
		"RunTerminal": objReq(
			[]string{"status", "reason", "partial_output"},
			prop("status", enumStringSchema("completed", "interrupted", "failed", "cancelled")),
			prop("reason", enumStringSchema("normal", "awaiting_user_input", "model_incomplete", "model_failure", "runtime_failure", "user_cancelled")),
			prop("code", strSchema()),
			prop("partial_output", boolSchema()),
			prop("model_call_id", strSchema()),
			prop("diagnostic_id", strSchema()),
		),
		"ChatRuntimeEvent":            obj(prop("schema_version", intSchema()), prop("event_id", strSchema()), prop("run_id", strSchema()), prop("type", strSchema()), prop("data", obj())),
		"ChatChunkResponse":           obj(prop("conversation_id", strSchema()), prop("seq", intSchema()), prop("message", strSchema()), prop("delta", strSchema()), prop("delta_mode", enumStringSchema("append", "replace")), prop("history_id", strSchema()), prop("sources", array(obj())), prop("prompt_questions", array(strSchema())), prop("reasoning_content", strSchema()), prop("thinking_duration_s", int64Schema()), prop("runtime_event", refSchema("ChatRuntimeEvent")), prop("execution", refSchema("ExternalExecutionProjection"))),
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
			"get":  op("Dataset list", queryParams(param("query", "page_token", false, strSchema()), param("query", "page_size", false, intSchema()), param("query", "order_by", false, strSchema()), param("query", "keyword", false, strSchema()), param("query", "tags", false, array(strSchema())), param("query", "source", false, strSchema())), nil, response(200, "Dataset list", refSchema("ListDatasetsResponse"))),
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
		"/datasets/{dataset}/uploads:checkHashes":               map[string]any{"post": op("Check reusable file hashes", nil, jsonBody(refSchema("CheckFileHashesRequest"), true), response(200, "Missing file hashes", refSchema("CheckFileHashesResponse")))},
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
			"get":  op("Prompt list", queryParams(param("query", "page_size", false, intSchema()), param("query", "page_token", false, strSchema()), param("query", "keyword", false, strSchema()), param("query", "category", false, strSchema()), param("query", "scope", false, strSchema()), param("query", "sort", false, strSchema()), param("query", "locale", false, strSchema())), nil, response(200, "Prompt list", refSchema("PromptListResponse"))),
			"post": op("Create prompt", nil, jsonBody(refSchema("PromptRequest"), true), response(200, "Created prompt", refSchema("PromptItem"))),
		},
		"/prompt_categories": map[string]any{
			"get":  op("Prompt category list", nil, nil, response(200, "Prompt category list", refSchema("PromptCategoryListResponse"))),
			"post": op("Create prompt category", nil, jsonBody(refSchema("PromptCategoryRequest"), true), response(200, "Created prompt category", refSchema("PromptCategory"))),
		},
		"/prompt_categories/{name}": map[string]any{
			"delete": op("Delete prompt category", nil, nil, response(200, "Deleted successfully", refSchema("EmptyObject"))),
		},
		"/prompts:polish": map[string]any{"post": op("Polish prompt", nil, jsonBody(refSchema("PromptPolishRequest"), true), response(200, "Polished prompt", refSchema("PromptPolishResponse")))},
		"/prompts/{name}": map[string]any{
			"get":    op("Get prompt", nil, nil, response(200, "Prompt details", refSchema("PromptItem"))),
			"patch":  op("Update prompt", nil, jsonBody(refSchema("PromptPatchRequest"), true), response(200, "Updated prompt", refSchema("PromptItem"))),
			"delete": op("Delete prompt", nil, nil, response(200, "Deleted successfully", refSchema("EmptyObject"))),
		},
		"/prompts/{name}:favorite":                           map[string]any{"post": op("Favorite prompt", nil, nil, response(200, "Favorited successfully", refSchema("PromptStateResponse")))},
		"/prompts/{name}:unfavorite":                         map[string]any{"post": op("Unfavorite prompt", nil, nil, response(200, "Unfavorited successfully", refSchema("PromptStateResponse")))},
		"/prompts/{name}:use":                                map[string]any{"post": op("Record prompt usage", nil, nil, response(200, "Usage recorded", refSchema("PromptStateResponse")))},
		"/tools":                                             map[string]any{"get": op("Tool list", queryParams(param("query", "keyword", false, strSchema()), param("query", "page", false, intSchema()), param("query", "page_size", false, intSchema())), nil, response(200, "Tool list", refSchema("ToolListResponse")))},
		"/tools/{tool_name}:disable":                         map[string]any{"post": op("Disable tool", nil, nil, response(200, "Tool disabled", refSchema("ToolStateResponse")))},
		"/tools/{tool_name}:enable":                          map[string]any{"post": op("Enable tool", nil, nil, response(200, "Tool enabled", refSchema("ToolStateResponse")))},
		"/conversations:resumeChat":                          map[string]any{"post": sseOp("Resume conversation stream", jsonBody(refSchema("ConversationResumeRequest"), true), response(200, "SSE streaming response item is ChatChunkResponse wrapped by result", refSchema("ChatChunkResponse")))},
		"/conversations:stopChatGeneration":                  map[string]any{"post": op("Stop conversation generation", nil, jsonBody(refSchema("ConversationStopRequest"), true), response(200, "Stopped successfully", refSchema("EmptyObject")))},
		"/conversations/{conversation_id}:toolLimitDecision": map[string]any{"post": op("Choose whether to continue after the tool-round limit", nil, nil, response(200, "Decision forwarded", refSchema("EmptyObject")))},
		"/conversations/{conversation_id}:status":            map[string]any{"get": op("Get conversation status", nil, nil, response(200, "Conversation status", refSchema("ConversationChatStatusResponse")))},
		"/conversations/{name}": map[string]any{
			"get":    op("Get conversation", nil, nil, response(200, "Conversationtext", refSchema("ConversationItem"))),
			"delete": op("Delete conversation", nil, nil, response(200, "Deleted successfully", refSchema("EmptyObject"))),
		},
		"/conversations/{name}:detail": map[string]any{"get": op(
			"Get conversation metadata",
			[]map[string]any{param("path", "name", true, strSchema())},
			nil,
			response(200, "Conversation metadata (no chat history; use :history)", refSchema("ConversationDetailResponse")),
		)},
		"/conversations/{name}:history": map[string]any{"get": op(
			"List conversation history (paginated)",
			conversationHistoryListParams(),
			nil,
			response(200, "Chat history page ordered by seq descending; may include in-flight turns from Redis", refSchema("ConversationHistoryListResponse")),
		)},
		"/conversations/{name}:trail": map[string]any{"get": op(
			"List conversation trail metadata (paginated)",
			conversationTrailListParams(),
			nil,
			response(200, "Conversation navigation metadata ordered by seq ascending", refSchema("ConversationTrailListResponse")),
		)},
		"/conversation-archive-folders": map[string]any{
			"get": op("List conversation archive folders", nil, nil, response(200, "Archive folders", refSchema("ConversationArchiveFolderListResponse"))),
			"post": map[string]any{
				"summary": "Create a conversation archive folder", "requestBody": jsonBody(refSchema("ConversationArchiveFolderRequest"), true),
				"responses": map[string]any{"201": response(201, "Archive folder created", refSchema("ConversationArchiveFolderCreateResponse"))},
			},
		},
		"/conversation-archive-folders/{folder_id}": map[string]any{
			"patch": op(
				"Rename a conversation archive folder",
				queryParams(param("path", "folder_id", true, strSchema())),
				jsonBody(refSchema("ConversationArchiveFolderRequest"), true),
				response(200, "Archive folder renamed", refSchema("EmptyObject")),
			),
			"delete": op(
				"Delete a conversation archive folder",
				queryParams(
					param("path", "folder_id", true, strSchema()),
					param("query", "move_to_folder_id", false, strSchema()),
				),
				nil,
				response(200, "Archive folder deleted", refSchema("ConversationArchiveFolderDeleteResponse")),
			),
		},
		"/conversations:archived": map[string]any{"get": op(
			"List archived conversations", recoveryListParams(true), nil,
			response(200, "Archived conversation page", refSchema("ConversationRecoveryListResponse")),
		)},
		"/conversations:trash": map[string]any{
			"get":    op("List trashed conversations", recoveryListParams(false), nil, response(200, "Conversation trash page", refSchema("ConversationRecoveryListResponse"))),
			"delete": op("Empty conversation trash for one kind", queryParams(param("query", "kind", true, enumStringSchema("dialog", "task"))), nil, response(200, "Conversation trash emptied", refSchema("RecoveryDeleteCountResponse"))),
		},
		"/conversations/{conversation_id}:archive": map[string]any{"post": op(
			"Archive or move a conversation", queryParams(param("path", "conversation_id", true, strSchema())), jsonBody(refSchema("ConversationArchiveRequest"), true),
			response(200, "Conversation archived", refSchema("EmptyObject")),
		)},
		"/conversations/{conversation_id}:unarchive": map[string]any{"post": op(
			"Unarchive a conversation", queryParams(param("path", "conversation_id", true, strSchema())), nil, response(200, "Conversation unarchived", refSchema("EmptyObject")),
		)},
		"/conversations/{conversation_id}:pin": map[string]any{"post": op(
			"Pin a conversation", queryParams(param("path", "conversation_id", true, strSchema())), nil, response(200, "Conversation pinned", refSchema("ConversationPinResponse")),
		)},
		"/conversations/{conversation_id}:unpin": map[string]any{"post": op(
			"Unpin a conversation", queryParams(param("path", "conversation_id", true, strSchema())), nil, response(200, "Conversation unpinned", refSchema("ConversationPinResponse")),
		)},
		"/conversations/{conversation_id}:restore": map[string]any{"post": op(
			"Restore a trashed conversation", queryParams(param("path", "conversation_id", true, strSchema())), nil, response(200, "Conversation restored", refSchema("EmptyObject")),
		)},
		"/conversations/{conversation_id}:purge": map[string]any{"delete": op(
			"Permanently delete a trashed conversation", queryParams(param("path", "conversation_id", true, strSchema())), nil, response(200, "Conversation permanently deleted", refSchema("EmptyObject")),
		)},
		"/workflow-drafts:trash": map[string]any{
			"get":    op("List workflow draft trash", recoveryPageParams(), nil, response(200, "Workflow trash page", refSchema("WorkflowTrashListResponse"))),
			"delete": op("Empty workflow draft trash", nil, nil, response(200, "Workflow trash emptied", refSchema("WorkflowTrashEmptyResponse"))),
		},
		"/workflow-drafts/{draft_id}:restore": map[string]any{"post": op(
			"Restore a workflow draft", queryParams(param("path", "draft_id", true, strSchema())), nil, response(200, "Workflow restored", refSchema("CoreEmptyResponse")),
		)},
		"/workflow-drafts/{draft_id}:purge": map[string]any{"delete": op(
			"Permanently delete a workflow draft", queryParams(param("path", "draft_id", true, strSchema())), nil, response(200, "Workflow permanently deleted", refSchema("CoreEmptyResponse")),
		)},
		"/task-center/tasks": map[string]any{"get": op(
			"List task-center tasks", queryParams(
				param("query", "status", false, strSchema()), param("query", "task_type", false, strSchema()),
				param("query", "keyword", false, strSchema()), param("query", "page", false, intSchema()), param("query", "page_size", false, intSchema()),
			), nil, response(200, "Task-center task page", refSchema("TaskCenterTaskListResponse")),
		)},
		"/task-center/tasks/{task_id}": map[string]any{"get": op(
			"Get a task-center task", queryParams(param("path", "task_id", true, strSchema())), nil,
			response(200, "Task-center task", refSchema("TaskCenterTaskResponse")),
		)},
		"/task-center/schedules/{schedule_id}/tasks": map[string]any{"get": op(
			"List tasks for a schedule", queryParams(
				param("path", "schedule_id", true, strSchema()), param("query", "page", false, intSchema()), param("query", "page_size", false, intSchema()),
			), nil, response(200, "Schedule task page", refSchema("TaskCenterTaskListResponse")),
		)},
		"/conversations": map[string]any{"get": op("Conversation list", queryParams(param("query", "keyword", false, strSchema()), param("query", "assistant", false, enumStringSchema("lazymind", "codex", "cursor", "workbuddy")), param("query", "page_size", false, intSchema()), param("query", "page_token", false, strSchema())), nil, response(200, "Conversation list", refSchema("ConversationListResponse")))},
		"/memory/soul": map[string]any{
			"get": map[string]any{
				"summary": "Get current user's Soul memory",
				"responses": map[string]any{
					"200": response(200, "Current Soul document", refSchema("CurrentMemorySoulResponse")),
					"401": response(401, "Gateway user identity is missing", refSchema("CurrentMemoryErrorResponse")),
					"404": response(404, "Soul document is missing", refSchema("CurrentMemoryErrorResponse")),
					"500": response(500, "Stored Soul document is invalid or unavailable", refSchema("CurrentMemoryErrorResponse")),
				},
			},
			"patch": map[string]any{
				"summary":     "Apply operations to current user's Soul memory",
				"description": "Applies an atomic batch of set operations. Core migrates legacy content and retries an internal content compare-and-swap up to three times.",
				"requestBody": jsonBody(refSchema("CurrentMemoryOperationsRequest"), true),
				"responses": map[string]any{
					"200": response(200, "Updated Soul document", refSchema("CurrentMemorySoulResponse")),
					"400": response(400, "Invalid partial Soul document", refSchema("CurrentMemoryErrorResponse")),
					"401": response(401, "Gateway user identity is missing", refSchema("CurrentMemoryErrorResponse")),
					"404": response(404, "Soul document is missing", refSchema("CurrentMemoryErrorResponse")),
					"409": response(409, "Soul document kept changing during the update", refSchema("CurrentMemoryErrorResponse")),
					"500": response(500, "Stored Soul document is invalid or unavailable", refSchema("CurrentMemoryErrorResponse")),
				},
			},
		},
		"/memory/profile": map[string]any{
			"get": map[string]any{
				"summary": "Get current user's Profile memory",
				"responses": map[string]any{
					"200": response(200, "Current Profile document", refSchema("CurrentMemoryProfileResponse")),
					"401": response(401, "Gateway user identity is missing", refSchema("CurrentMemoryErrorResponse")),
					"404": response(404, "Profile document is missing", refSchema("CurrentMemoryErrorResponse")),
					"500": response(500, "Stored Profile document is invalid or unavailable", refSchema("CurrentMemoryErrorResponse")),
				},
			},
			"patch": map[string]any{
				"summary":     "Apply operations to current user's Profile memory",
				"description": "Applies an atomic batch. Scalar fields accept set/clear; list fields accept add/remove/clear.",
				"requestBody": jsonBody(refSchema("CurrentMemoryOperationsRequest"), true),
				"responses": map[string]any{
					"200": response(200, "Updated Profile document", refSchema("CurrentMemoryProfileResponse")),
					"400": response(400, "Invalid partial Profile document", refSchema("CurrentMemoryErrorResponse")),
					"401": response(401, "Gateway user identity is missing", refSchema("CurrentMemoryErrorResponse")),
					"404": response(404, "Profile document is missing", refSchema("CurrentMemoryErrorResponse")),
					"409": response(409, "Profile document kept changing during the update", refSchema("CurrentMemoryErrorResponse")),
					"500": response(500, "Stored Profile document is invalid or unavailable", refSchema("CurrentMemoryErrorResponse")),
				},
			},
		},
		"/memory/soul/avatar":    currentMemoryAvatarPath("Soul"),
		"/memory/profile/avatar": currentMemoryAvatarPath("Profile"),
		"/memory/preferences": map[string]any{
			"get": map[string]any{
				"summary": "List current user's Preference memory",
				"responses": map[string]any{
					"200": response(200, "Current ordered Preference items", refSchema("CurrentMemoryPreferenceListResponse")),
					"401": response(401, "Gateway user identity is missing", refSchema("CurrentMemoryErrorResponse")),
					"404": response(404, "Preference index is missing", refSchema("CurrentMemoryErrorResponse")),
					"500": response(500, "Stored Preference index is invalid or unavailable", refSchema("CurrentMemoryErrorResponse")),
				},
			},
		},
		"/memory/preferences:order": map[string]any{
			"put": map[string]any{
				"summary":     "Replace the order of current user's Preference items",
				"description": "ordered_names must be an exact, unnormalized permutation of every current Preference name. expected_etag must equal the list response etag.",
				"requestBody": jsonBody(refSchema("CurrentMemoryPreferenceOrderRequest"), true),
				"responses": map[string]any{
					"200": response(200, "Reordered Preference items", refSchema("CurrentMemoryPreferenceListResponse")),
					"400": response(400, "ordered_names is not an exact permutation", refSchema("CurrentMemoryErrorResponse")),
					"401": response(401, "Gateway user identity is missing", refSchema("CurrentMemoryErrorResponse")),
					"404": response(404, "Preference index is missing", refSchema("CurrentMemoryErrorResponse")),
					"409": response(409, "Preference etag conflict; data.current_etag contains the latest etag", refSchema("CurrentMemoryErrorResponse")),
					"500": response(500, "Stored Preference index is invalid or unavailable", refSchema("CurrentMemoryErrorResponse")),
				},
			},
		},
		"/memory/preferences/{name}": map[string]any{
			"get": map[string]any{
				"summary": "Get a current Preference item and its reference detail",
				"parameters": []map[string]any{
					param("path", "name", true, strSchema()),
				},
				"description": "A missing reference file is represented by HTTP 200 with reference_status=missing and reference=null.",
				"responses": map[string]any{
					"200": response(200, "Preference item detail", refSchema("CurrentMemoryPreferenceDetailResponse")),
					"401": response(401, "Gateway user identity is missing", refSchema("CurrentMemoryErrorResponse")),
					"404": response(404, "Preference item is missing", refSchema("CurrentMemoryErrorResponse")),
					"500": response(500, "Stored Preference or reference document is invalid", refSchema("CurrentMemoryErrorResponse")),
				},
			},
			"delete": map[string]any{
				"summary": "Delete a current Preference item",
				"parameters": []map[string]any{
					param("path", "name", true, strSchema()),
				},
				"description": "Atomically removes the item and its now-unreferenced reference file. Missing items, including other users' items, are idempotent success.",
				"responses": map[string]any{
					"204": map[string]any{"description": "Deleted or already absent"},
					"401": response(401, "Gateway user identity is missing", refSchema("CurrentMemoryErrorResponse")),
					"500": response(500, "Stored Preference index is invalid or unavailable", refSchema("CurrentMemoryErrorResponse")),
				},
			},
		},
		"/memory/episodes": map[string]any{
			"get": map[string]any{
				"summary": "List current user's Episode Memory",
				"parameters": queryParams(
					param("query", "page_size", false, intSchema()),
					param("query", "page_token", false, strSchema()),
				),
				"responses": map[string]any{
					"200": response(200, "Episode Memory page", refSchema("EpisodeMemoryListResponse")),
					"400": response(400, "Invalid page size or cursor", refSchema("ErrorResponse")),
					"401": response(401, "Gateway user identity is missing", refSchema("ErrorResponse")),
					"500": response(500, "Episode Memory list is unavailable", refSchema("ErrorResponse")),
				},
			},
		},
		"/memory/episodes/{episode_id}": map[string]any{
			"get": map[string]any{
				"summary": "Get an Episode Memory item",
				"parameters": []map[string]any{
					param("path", "episode_id", true, strSchema()),
				},
				"responses": map[string]any{
					"200": response(200, "Episode Memory item", refSchema("EpisodeMemoryDetailResponse")),
					"401": response(401, "Gateway user identity is missing", refSchema("ErrorResponse")),
					"404": response(404, "Episode Memory item does not exist for this user", refSchema("ErrorResponse")),
					"500": response(500, "Episode Memory detail is unavailable", refSchema("ErrorResponse")),
				},
			},
			"delete": map[string]any{
				"summary": "Delete an Episode Memory item",
				"parameters": []map[string]any{
					param("path", "episode_id", true, strSchema()),
				},
				"responses": map[string]any{
					"204": map[string]any{"description": "Deleted or already absent"},
					"401": response(401, "Gateway user identity is missing", refSchema("ErrorResponse")),
					"500": response(500, "Episode Memory deletion failed", refSchema("ErrorResponse")),
				},
			},
		},
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
func feedbackTypeSchema() map[string]any {
	return map[string]any{
		"description": "Feedback type. 0 or FEED_BACK_TYPE_UNSPECIFIED cancels feedback; 1 or FEED_BACK_TYPE_LIKE likes; 2 or FEED_BACK_TYPE_UNLIKE dislikes. Cancelling or changing away from unlike clears reason and expected_answer.",
		"oneOf": []any{
			map[string]any{
				"type": "integer",
				"enum": []any{0, 1, 2},
			},
			map[string]any{
				"type": "string",
				"enum": []any{
					"FEED_BACK_TYPE_UNSPECIFIED",
					"FEED_BACK_TYPE_LIKE",
					"FEED_BACK_TYPE_UNLIKE",
				},
			},
		},
	}
}
func refSchema(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func param(in, name string, required bool, schema map[string]any) map[string]any {
	return map[string]any{"in": in, "name": name, "required": required, "schema": schema}
}

func queryParams(params ...map[string]any) []map[string]any { return params }

func conversationHistoryListParams() []map[string]any {
	return []map[string]any{
		param("path", "name", true, map[string]any{
			"type":        "string",
			"description": "Conversation ID or resource name (e.g. conv-1 or conversations/conv-1; :history suffix is stripped)",
		}),
		param("query", "page_size", false, map[string]any{
			"type":        "integer",
			"description": "Page size (default 20, max 100)",
		}),
		param("query", "page_token", false, map[string]any{
			"type":        "string",
			"description": "Pagination offset token from a previous response next_page_token",
		}),
	}
}

func conversationTrailListParams() []map[string]any {
	return []map[string]any{
		param("path", "name", true, map[string]any{
			"type":        "string",
			"description": "Conversation ID or resource name (e.g. conv-1 or conversations/conv-1; :trail suffix is stripped)",
		}),
		param("query", "page_size", false, map[string]any{
			"type":        "integer",
			"description": "Page size (default 20, max 100)",
		}),
		param("query", "page_token", false, map[string]any{
			"type":        "string",
			"description": "Pagination offset token from a previous response next_page_token",
		}),
	}
}

func recoveryPageParams() []map[string]any {
	return queryParams(
		param("query", "keyword", false, strSchema()),
		param("query", "page", false, intSchema()),
		param("query", "page_size", false, intSchema()),
	)
}

func recoveryListParams(withFolder bool) []map[string]any {
	params := recoveryPageParams()
	params = append(params, param("query", "kind", true, enumStringSchema("dialog", "task")))
	if withFolder {
		params = append(params, param("query", "folder_id", false, strSchema()))
	}
	return params
}

func jsonBody(schema map[string]any, required bool) map[string]any {
	return map[string]any{"required": required, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
}

func nullableSchema(schema map[string]any) map[string]any {
	if reference, ok := schema["$ref"]; ok && len(schema) == 1 {
		return map[string]any{
			"allOf":    []any{map[string]any{"$ref": reference}},
			"nullable": true,
		}
	}
	cloned := make(map[string]any, len(schema)+1)
	for key, value := range schema {
		cloned[key] = value
	}
	cloned["nullable"] = true
	return cloned
}

func enumStringSchema(values ...string) map[string]any {
	schema := strSchema()
	items := make([]any, 0, len(values))
	for _, value := range values {
		items = append(items, value)
	}
	schema["enum"] = items
	return schema
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

func currentMemoryAvatarPath(label string) map[string]any {
	errorResponse := refSchema("CurrentMemoryErrorResponse")
	return map[string]any{
		"get": map[string]any{
			"summary": "Get current user's " + label + " avatar",
			"responses": map[string]any{
				"200": avatarBinaryResponse(label + " avatar"),
				"401": response(401, "Gateway user identity is missing", errorResponse),
				"404": response(404, label+" avatar is not configured", errorResponse),
				"500": response(500, "Stored avatar is invalid or unavailable", errorResponse),
			},
		},
		"put": map[string]any{
			"summary": "Upload current user's " + label + " avatar",
			"requestBody": map[string]any{
				"required": true,
				"content": map[string]any{
					"multipart/form-data": map[string]any{
						"schema": objReq(
							[]string{"file"},
							prop("file", map[string]any{"type": "string", "format": "binary"}),
						),
					},
				},
			},
			"responses": map[string]any{
				"200": response(200, "Uploaded avatar metadata", refSchema("CurrentMemoryAvatarResponse")),
				"400": response(400, "Avatar file is invalid or unsupported", errorResponse),
				"401": response(401, "Gateway user identity is missing", errorResponse),
				"413": response(413, "Avatar file exceeds 2 MiB", errorResponse),
				"500": response(500, "Avatar upload failed", errorResponse),
			},
		},
		"delete": map[string]any{
			"summary": "Delete current user's " + label + " avatar",
			"responses": map[string]any{
				"204": map[string]any{"description": "Deleted or already absent"},
				"401": response(401, "Gateway user identity is missing", errorResponse),
				"500": response(500, "Avatar deletion failed", errorResponse),
			},
		},
	}
}

func avatarBinaryResponse(description string) map[string]any {
	content := map[string]any{}
	for _, contentType := range []string{"image/png", "image/jpeg", "image/webp"} {
		content[contentType] = map[string]any{
			"schema": map[string]any{"type": "string", "format": "binary"},
		}
	}
	return map[string]any{"description": description, "content": content}
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
