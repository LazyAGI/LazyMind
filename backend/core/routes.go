package main

import (
	"net/http"
	"strings"

	"lazymind/core/acl"
	"lazymind/core/agent"
	"lazymind/core/chat"
	"lazymind/core/datasource"
	"lazymind/core/doc"
	"lazymind/core/evalset"
	"lazymind/core/evolution"
	"lazymind/core/file"
	"lazymind/core/mcp"
	"lazymind/core/memory"
	"lazymind/core/modelprovider"
	"lazymind/core/plugin"
	"lazymind/core/preference"
	"lazymind/core/remotefs"
	"lazymind/core/resourcechange"
	"lazymind/core/resourcefs"
	"lazymind/core/resourceupdate"
	"lazymind/core/scheduler"
	skillv2handler "lazymind/core/skillv2/handler"
	"lazymind/core/subagent"
	"lazymind/core/taskcenter"
	"lazymind/core/userprefs"
	"lazymind/core/wordgroup"

	"github.com/gorilla/mux"
)

func handleAgentThreadAPI(r *mux.Router, method, path string, perms []string, h http.HandlerFunc) {
	handleAPI(r, method, path, perms, h).MatcherFunc(func(r *http.Request, _ *mux.RouteMatch) bool {
		path := strings.TrimPrefix(r.URL.EscapedPath(), "/api/core")
		const prefix = "/agent/threads/"
		rest := strings.TrimPrefix(path, prefix)
		if rest == path {
			return true
		}
		threadID, _, _ := strings.Cut(rest, "/")
		return !strings.Contains(threadID, ":")
	})
}

// registerAllRoutes text OpenAPI text（text Job），text handleAPI textPermissiontext（text extract_api_permissions.py text Kong RBAC）。
func registerAllRoutes(r *mux.Router) {
	// ----- Datasettext -----
	handleAPI(r, "GET", "/dataset/algos", []string{"document.read"}, doc.ListAlgos)
	handleAPI(r, "GET", "/dataset/tags", []string{"document.read"}, doc.AllDatasetTags)
	handleAPI(r, "GET", "/datasets", []string{"document.read"}, doc.ListDatasets)
	handleAPI(r, "POST", "/datasets", []string{"document.write"}, doc.CreateDataset)
	handleAPI(r, "GET", "/datasets/{dataset}", []string{"document.read"}, doc.GetDataset)
	handleAPI(r, "DELETE", "/datasets/{dataset}", []string{"document.write"}, doc.DeleteDataset)
	handleAPI(r, "PATCH", "/datasets/{dataset}", []string{"document.write"}, doc.UpdateDataset)
	handleAPI(r, "POST", "/datasets/{dataset}:setDefault", []string{"document.write"}, doc.SetDefault)
	handleAPI(r, "POST", "/datasets/{dataset}:unsetDefault", []string{"document.write"}, doc.UnsetDefault)
	handleAPI(r, "GET", "/data-sources/local-fs-chat-setting", []string{"document.read"}, datasource.GetLocalFSChatSetting)
	handleAPI(r, "PUT", "/data-sources/local-fs-chat-setting", []string{"document.write"}, datasource.SetLocalFSChatSetting)
	handleAPI(r, "GET", "/data-sources/database-connections", []string{"document.read"}, datasource.ListDatabaseConnections)
	handleAPI(r, "POST", "/data-sources/database-connections", []string{"document.write"}, datasource.CreateDatabaseConnection)
	handleAPI(r, "POST", "/data-sources/database-connections/{connection}:check", []string{"document.write"}, datasource.CheckDatabaseConnection)
	handleAPI(r, "GET", "/data-sources/database-connections/{connection}:secret", []string{"document.read"}, datasource.GetDatabaseConnectionSecret)
	handleAPI(r, "GET", "/data-sources/database-connections/{connection}", []string{"document.read"}, datasource.GetDatabaseConnection)
	handleAPI(r, "PATCH", "/data-sources/database-connections/{connection}", []string{"document.write"}, datasource.UpdateDatabaseConnection)
	handleAPI(r, "DELETE", "/data-sources/database-connections/{connection}", []string{"document.write"}, datasource.DeleteDatabaseConnection)

	// ----- Eval set metadata -----
	handleAPI(r, "GET", "/eval-sets", []string{"document.read"}, evalset.ListEvalSets)
	handleAPI(r, "POST", "/eval-sets", []string{"document.write"}, evalset.CreateEvalSet)
	handleAPI(r, "GET", "/eval-sets/datasets", []string{"document.read"}, evalset.ListDatasetOptions)
	handleAPI(r, "GET", "/eval-sets/question-types", []string{"document.read"}, evalset.ListQuestionTypeOptions)
	handleAPI(r, "GET", "/eval-set-import-templates/{file_type}", []string{"document.read"}, evalset.DownloadImportTemplate)
	handleAPI(r, "POST", "/eval-sets/imports:preview", []string{"document.write"}, evalset.PreviewEvalSetImport)
	handleAPI(r, "POST", "/eval-sets:import", []string{"document.write"}, evalset.CreateEvalSetByImport)
	handleAPI(r, "GET", "/eval-set-import-tasks/{task_id}", []string{"document.read"}, evalset.GetEvalSetImportTask)
	handleAPI(r, "GET", "/eval-sets/{eval_set_id}/question-types", []string{"document.read"}, evalset.ListEvalSetQuestionTypes)
	handleAPI(r, "GET", "/eval-sets/{eval_set_id}/items:invalidReferences", []string{"document.read"}, evalset.ListInvalidReferenceEvalSetItems)
	handleAPI(r, "GET", "/eval-sets/{eval_set_id}/items", []string{"document.read"}, evalset.ListEvalSetItems)
	handleAPI(r, "POST", "/eval-sets/{eval_set_id}/imports", []string{"document.write"}, evalset.AppendEvalSetImport)
	handleAPI(r, "POST", "/eval-sets/{eval_set_id}/items", []string{"document.write"}, evalset.CreateEvalSetItem)
	handleAPI(r, "POST", "/eval-sets/{eval_set_id}/items:batchDelete", []string{"document.write"}, evalset.BatchDeleteEvalSetItems)
	handleAPI(r, "PATCH", "/eval-sets/{eval_set_id}/items/{item_id}", []string{"document.write"}, evalset.UpdateEvalSetItem)
	handleAPI(r, "DELETE", "/eval-sets/{eval_set_id}/items/{item_id}", []string{"document.write"}, evalset.DeleteEvalSetItem)
	handleAPI(r, "GET", "/eval-sets/{eval_set_id}", []string{"document.read"}, evalset.GetEvalSet)
	handleAPI(r, "PATCH", "/eval-sets/{eval_set_id}", []string{"document.write"}, evalset.UpdateEvalSet)
	handleAPI(r, "DELETE", "/eval-sets/{eval_set_id}", []string{"document.write"}, evalset.DeleteEvalSet)

	// ----- DocumentService -----
	handleAPI(r, "GET", "/datasets/{dataset}/documents", []string{"document.read"}, doc.ListDocuments)
	handleAPI(r, "POST", "/datasets/{dataset}/documents", []string{"document.write"}, doc.CreateDocument)
	// :content/:download text {document} text，text /documents/xxx:content text {document} text。
	handleAPI(r, "GET", "/datasets/{dataset}/documents/{document}:content", []string{"document.read"}, doc.GetDocumentContent)
	handleAPI(r, "GET", "/datasets/{dataset}/documents/{document}:download", []string{"document.read"}, doc.DownloadDocument)
	handleAPI(r, "GET", "/datasets/{dataset}/documents/{document}", []string{"document.read"}, doc.GetDocument)
	handleAPI(r, "DELETE", "/datasets/{dataset}/documents/{document}", []string{"document.write"}, doc.DeleteDocument)
	handleAPI(r, "PATCH", "/datasets/{dataset}/documents/{document}", []string{"document.write"}, doc.UpdateDocument)
	handleAPI(r, "POST", "/datasets/{dataset}/documents:search", []string{"document.read"}, doc.SearchDocuments)
	handleAPI(r, "POST", "/datasets/{dataset}/documents:batchUpdateTags", []string{"document.write"}, doc.BatchUpdateDocumentTags)
	handleAPI(r, "POST", "/documents:listByDatasets", []string{"document.read"}, doc.ListDocumentsByDatasets)
	handleAPI(r, "POST", "/documents:search", []string{"document.read"}, doc.SearchAllDocuments)
	handleAPI(r, "POST", "/system-query/documents:aggregate", []string{"document.read"}, doc.AggregateDocuments)
	handleAPI(r, "POST", "/datasets/{dataset}:batchDelete", []string{"document.write"}, doc.BatchDeleteDocument)
	handleAPI(r, "GET", "/document/creators", []string{"document.read"}, doc.AllDocumentCreators)
	handleAPI(r, "GET", "/document/tags", []string{"document.read"}, doc.AllDocumentTags)
	// ----- text -----
	handleAPI(r, "GET", "/datasets/{dataset}/documents/{document}/segments", []string{"document.read"}, doc.ListSegments)
	handleAPI(r, "GET", "/datasets/{dataset}/documents/{document}/segments/{segment}", []string{"document.read"}, doc.GetSegment)
	handleAPI(r, "POST", "/datasets/{dataset}/documents/{document}/segments:search", []string{"document.read"}, doc.SearchSegments)

	// ----- DatasetMembertext -----
	handleAPI(r, "GET", "/datasets/{dataset}/members", []string{"document.read"}, doc.ListDatasetMembers)
	handleAPI(r, "GET", "/datasets/{dataset}/members/{user_id}", []string{"document.read"}, doc.GetDatasetMember)
	handleAPI(r, "DELETE", "/datasets/{dataset}/members/{user_id}", []string{"document.write"}, doc.DeleteDatasetMember)
	handleAPI(r, "PATCH", "/datasets/{dataset}/members/{user_id}", []string{"document.write"}, doc.UpdateDatasetMember)
	handleAPI(r, "DELETE", "/datasets/{dataset}/members/groups/{group_id}", []string{"document.write"}, doc.DeleteDatasetGroupMember)
	handleAPI(r, "PATCH", "/datasets/{dataset}/members/groups/{group_id}", []string{"document.write"}, doc.UpdateDatasetGroupMember)
	handleAPI(r, "POST", "/datasets/{dataset}/members:search", []string{"document.read"}, doc.SearchDatasetMember)
	handleAPI(r, "POST", "/datasets/{dataset}:batchAddMember", []string{"document.write"}, doc.BatchAddDatasetMember)

	// ----- Tasktext（text Task，text Job） -----
	handleAPI(r, "GET", "/datasets/{dataset}/tasks", []string{"document.read"}, doc.ListTasks)
	handleAPI(r, "POST", "/datasets/{dataset}/tasks", []string{"document.write"}, doc.CreateTask)
	handleAPI(r, "POST", "/datasets/{dataset}/tasks:search", []string{"document.read"}, doc.SearchTasks)
	handleAPI(r, "POST", "/datasets/{dataset}/uploads", []string{"document.write"}, doc.UploadFile)
	handleAPI(r, "POST", "/temp/uploads", []string{"document.write"}, doc.UploadTempFile)
	handleAPI(r, "POST", "/temp/uploads:initUpload", []string{"document.write"}, doc.InitTempUpload)
	handleAPI(r, "PUT", "/temp/uploads/{upload_id}/parts/{part_number}", []string{"document.write"}, doc.UploadTempPart)
	handleAPI(r, "POST", "/temp/uploads/{upload_id}:complete", []string{"document.write"}, doc.CompleteTempUpload)
	handleAPI(r, "POST", "/temp/uploads/{upload_id}:abort", []string{"document.write"}, doc.AbortTempUpload)
	handleAPI(r, "GET", "/datasets/{dataset}/uploads/{upload_file_id}:content", []string{"document.read"}, doc.GetUploadedFileContent)
	handleAPI(r, "GET", "/datasets/{dataset}/uploads/{upload_file_id}:download", []string{"document.read"}, doc.DownloadUploadedFile)
	handleAPI(r, "POST", "/datasets/{dataset}/tasks:batchUpload", []string{"document.write"}, doc.BatchUploadTasks)
	handleAPI(r, "GET", "/datasets/{dataset}/tasks/{task}", []string{"document.read"}, doc.GetTask)
	handleAPI(r, "DELETE", "/datasets/{dataset}/tasks/{task}", []string{"document.write"}, doc.DeleteTask)
	handleAPI(r, "POST", "/datasets/{dataset}/tasks:start", []string{"document.write"}, doc.StartTask)
	handleAPI(r, "POST", "/datasets/{dataset}/tasks/{task}:resume", []string{"document.write"}, doc.ResumeTask)
	handleAPI(r, "POST", "/datasets/{dataset}/tasks/{task}:suspend", []string{"document.write"}, doc.SuspendTask)
	handleAPI(r, "POST", "/datasets/{dataset}/uploads:initUpload", []string{"document.write"}, doc.InitUpload)
	handleAPI(r, "PUT", "/datasets/{dataset}/uploads/{upload_id}/parts/{part_number}", []string{"document.write"}, doc.UploadPart)
	handleAPI(r, "POST", "/datasets/{dataset}/uploads/{upload_id}:complete", []string{"document.write"}, doc.CompleteUpload)
	handleAPI(r, "POST", "/datasets/{dataset}/uploads/{upload_id}:abort", []string{"document.write"}, doc.AbortUpload)
	// text URL：text，text :file text。
	handleAPI(r, "GET", "/static-files/{path:.*}", nil, doc.GetSignedStaticFile)
	handleAPI(r, "POST", "/static-files:sign", []string{"document.read"}, doc.SignStaticFiles)

	// ----- RAG text（text） -----
	handleAPI(r, "POST", "/upload_files", []string{"document.write"}, file.UploadFiles)
	handleAPI(r, "POST", "/add_files_to_group", []string{"document.write"}, file.AddFilesToGroup)
	handleAPI(r, "GET", "/list_files", []string{"document.read"}, file.ListFiles)
	handleAPI(r, "GET", "/list_files_in_group", []string{"document.read"}, file.ListFilesInGroup)
	handleAPI(r, "GET", "/list_kb_groups", []string{"document.read"}, file.ListKBGroups)

	// ----- text -----
	handleAPI(r, "POST", "/chat", []string{"qa.write"}, chat.Chat)
	handleAPI(r, "GET", "/tools", []string{"qa.read"}, chat.ListTools)
	handleAPI(r, "POST", "/tools/{tool_name}:disable", []string{"qa.read"}, chat.DisableTool)
	handleAPI(r, "POST", "/tools/{tool_name}:enable", []string{"qa.read"}, chat.EnableTool)

	// ----- MCP servers -----
	handleAPI(r, "GET", "/mcp_servers", []string{"qa.read"}, mcp.List)
	handleAPI(r, "POST", "/mcp_servers", []string{"qa.write"}, mcp.Create)
	handleAPI(r, "GET", "/mcp_servers/{id}", []string{"qa.read"}, mcp.Get)
	handleAPI(r, "PATCH", "/mcp_servers/{id}", []string{"qa.write"}, mcp.Update)
	handleAPI(r, "DELETE", "/mcp_servers/{id}", []string{"qa.write"}, mcp.Delete)
	handleAPI(r, "POST", "/mcp_servers/{id}:check", []string{"qa.write"}, mcp.Check)
	handleAPI(r, "POST", "/mcp_servers/{id}:discover", []string{"qa.write"}, mcp.Discover)
	handleAPI(r, "PUT", "/mcp_servers/{id}/tools", []string{"qa.write"}, mcp.UpdateTools)

	// ----- Agent thread stream -----
	handleAPI(r, "GET", "/agent/threads", []string{"qa.read"}, agent.ListThreads)
	handleAPI(r, "POST", "/agent/threads", []string{"qa.write"}, agent.CreateThread)
	handleAgentThreadAPI(r, "GET", "/agent/threads/{thread_id}/events:stream", []string{"qa.read"}, agent.StreamThreadEvents)
	handleAgentThreadAPI(r, "GET", "/agent/threads/{thread_id}/event-trace:stream", []string{"qa.read"}, agent.StreamThreadEventTrace)
	handleAgentThreadAPI(r, "GET", "/agent/threads/{thread_id}/steps", []string{"qa.read"}, agent.ListThreadSteps)
	handleAgentThreadAPI(r, "GET", "/agent/threads/{thread_id}/gates", []string{"qa.read"}, agent.ListThreadGates)
	handleAgentThreadAPI(r, "GET", "/agent/threads/{thread_id}/gates/{step}/versions/{version}:download", []string{"qa.read"}, agent.DownloadThreadGate)
	handleAgentThreadAPI(r, "GET", "/agent/threads/{thread_id}/gates/{step}/versions/{version}", []string{"qa.read"}, agent.GetThreadGateContent)
	handleAgentThreadAPI(r, "GET", "/agent/threads/{thread_id}/gates/eval/versions/{version}/bad-cases", []string{"qa.read"}, agent.GetThreadEvalGateBadCases)
	handleAgentThreadAPI(r, "GET", "/agent/threads/{thread_id}/gates/abtest/versions/{version}/case-details", []string{"qa.read"}, agent.GetThreadABTestGateCaseDetails)
	handleAgentThreadAPI(r, "GET", "/agent/threads/{thread_id}/results/traces:compare", []string{"qa.read"}, agent.CompareThreadTraces)
	handleAgentThreadAPI(r, "GET", "/agent/threads/{thread_id}/results/traces/{trace_id}", []string{"qa.read"}, agent.GetThreadTraceDetail)
	handleAgentThreadAPI(r, "GET", "/agent/threads/{thread_id}", []string{"qa.read"}, agent.GetThread)
	handleAgentThreadAPI(r, "DELETE", "/agent/threads/{thread_id}", []string{"qa.write"}, agent.DeleteThread)
	handleAgentThreadAPI(r, "GET", "/agent/threads/{thread_id}/messages", []string{"qa.read"}, agent.GetThreadMessages)
	handleAgentThreadAPI(r, "POST", "/agent/threads/{thread_id}/messages", []string{"qa.write"}, agent.StreamThreadMessages)
	handleAgentThreadAPI(r, "POST", "/agent/threads/{thread_id}/start", []string{"qa.write"}, agent.StartThread)
	handleAgentThreadAPI(r, "POST", "/agent/threads/{thread_id}/pause", []string{"qa.write"}, agent.PauseThread)
	handleAgentThreadAPI(r, "POST", "/agent/threads/{thread_id}/cancel", []string{"qa.write"}, agent.CancelThread)
	handleAgentThreadAPI(r, "POST", "/agent/threads/{thread_id}/retry", []string{"qa.write"}, agent.RetryThread)
	handleAgentThreadAPI(r, "POST", "/agent/threads/{thread_id}/continue", []string{"qa.write"}, agent.ContinueThread)
	handleAPI(r, "GET", "/agent/candidates", []string{"qa.read"}, agent.ListCandidates)
	handleAPI(r, "GET", "/agent/candidates/{candidate_id:.*}", []string{"qa.read"}, agent.GetCandidate)
	handleAPI(r, "GET", "/agent/router/status", []string{"qa.read"}, agent.GetRouterStatus)
	handleAPI(r, "GET", "/agent/router/algorithms", []string{"qa.read"}, agent.ListRouterAlgorithms)
	handleAPI(r, "POST", "/agent/router/algorithms", []string{"qa.write"}, agent.RegisterRouterAlgorithm)
	handleAPI(r, "POST", "/agent/router/algorithms/{algorithm_id}:action", []string{"qa.write"}, agent.PostRouterAlgorithmAction)
	handleAPI(r, "GET", "/agent/router/ab-strategy", []string{"qa.read"}, agent.GetRouterABStrategy)
	handleAPI(r, "PUT", "/agent/router/ab-strategy", []string{"qa.write"}, agent.PutRouterABStrategy)

	// ----- Conversation -----
	handleAPI(r, "POST", "/conversations:chat", []string{"qa.write"}, chat.ChatConversations)
	handleAPI(r, "POST", "/conversations:resumeChat", []string{"qa.write"}, chat.ResumeChat)
	handleAPI(r, "POST", "/conversations:stopChatGeneration", []string{"qa.write"}, chat.StopChatGeneration)
	handleAPI(r, "POST", "/conversations/{conversation_id}:stop", []string{"qa.write"}, chat.StopChatGeneration)
	handleAPI(r, "GET", "/conversations/{conversation_id}:status", []string{"qa.read"}, chat.GetChatStatus)

	// ----- SubAgent (Task Center) -----
	handleAPI(r, "GET", "/conversations/{conversation_id}/tasks", []string{"qa.read"}, subagent.ListConversationTasks)
	handleAPI(r, "GET", "/conversations/{conversation_id}/events", []string{"qa.read"}, chat.StreamConvEvents)
	handleAPI(r, "GET", "/tasks/{task_id}:stream", []string{"qa.read"}, subagent.StreamTask)
	handleAPI(r, "GET", "/tasks/{task_id}/artifacts", []string{"qa.read"}, subagent.GetTaskArtifacts)
	handleAPI(r, "GET", "/tasks/{task_id}", []string{"qa.read"}, subagent.GetTaskDetail)
	// Internal endpoint for algorithm service auto polling; no request-level RBAC.
	handleAPI(r, "GET", "/internal/subagent/tasks/{task_id}", nil, subagent.InternalGetTaskStatus)
	handleAPI(r, "GET", "/internal/subagent/tasks/{task_id}/events", nil, subagent.InternalGetTaskEvents)

	// ----- Plugin Info -----
	handleAPI(r, "GET", "/plugins", []string{"qa.read"}, plugin.ListPlugins)
	handleAPI(r, "GET", "/plugins/{plugin_id}", []string{"qa.read"}, plugin.GetPluginInfo)

	// ----- Plugin Drafts (user-created plugin authoring) -----
	handleAPI(r, "GET", "/plugin-drafts", []string{"qa.read"}, plugin.ListPluginDrafts)
	handleAPI(r, "POST", "/plugin-drafts", []string{"qa.write"}, plugin.CreatePluginDraft)
	handleAPI(r, "POST", "/plugin-drafts:polish-info", []string{"qa.write"}, plugin.PolishPluginDraftInfo)
	handleAPI(r, "GET", "/plugin-drafts/{draft_id}", []string{"qa.read"}, plugin.GetPluginDraft)
	handleAPI(r, "POST", "/plugin-drafts/{draft_id}:save", []string{"qa.write"}, plugin.SavePluginDraft)
	handleAPI(r, "POST", "/plugin-drafts/{draft_id}:ai-generate", []string{"qa.write"}, plugin.AIGeneratePluginDraft)
	handleAPI(r, "POST", "/plugin-drafts/{draft_id}:ai-repair", []string{"qa.write"}, plugin.AIRepairPluginDraft)
	handleAPI(r, "DELETE", "/plugin-drafts/{draft_id}", []string{"qa.write"}, plugin.DeletePluginDraft)

	// ----- Task Center -----
	handleAPI(r, "GET", "/task-center/tasks", []string{"qa.read"}, taskcenter.ListTasks)
	handleAPI(r, "POST", "/task-center/tasks", []string{"qa.write"}, taskcenter.AddTaskHandler)
	handleAPI(r, "GET", "/task-center/tasks/{task_id}", []string{"qa.read"}, taskcenter.GetTaskByID)
	handleAPI(r, "POST", "/task-center/tasks/{task_id}:cancel", []string{"qa.write"}, taskcenter.CancelTaskByID)
	handleAPI(r, "POST", "/task-center/tasks/{task_id}:remove", []string{"qa.write"}, taskcenter.RemoveTaskHandler)
	handleAPI(r, "GET", "/task-center/schedules/{schedule_id}/tasks", []string{"qa.read"}, taskcenter.ListScheduleTasks)

	// ----- Schedules -----
	handleAPI(r, "GET", "/schedules", []string{"qa.read"}, scheduler.ListSchedulesHandler)
	handleAPI(r, "POST", "/schedules", []string{"qa.write"}, scheduler.CreateScheduleHandler)
	handleAPI(r, "PUT", "/schedules/{schedule_id}", []string{"qa.write"}, scheduler.UpdateScheduleHandler)
	handleAPI(r, "POST", "/schedules/{schedule_id}:cancel", []string{"qa.write"}, scheduler.CancelScheduleHandler)
	handleAPI(r, "POST", "/schedules/{schedule_id}:enable", []string{"qa.write"}, scheduler.EnableScheduleHandler)
	handleAPI(r, "POST", "/schedules/{schedule_id}:run-now", []string{"qa.write"}, scheduler.RunNowHandler)

	// ----- User Chat Settings (global plugin/subagent defaults) -----
	handleAPI(r, "GET", "/user/chat-settings", []string{"qa.read"}, chat.GetChatSettings)
	handleAPI(r, "PATCH", "/user/chat-settings", []string{"qa.write"}, chat.PatchChatSettings)
	handleAPI(r, "GET", "/user/ui-preferences", []string{"qa.read"}, userprefs.GetUIPreferences)
	handleAPI(r, "PATCH", "/user/ui-preferences", []string{"qa.write"}, userprefs.PatchUIPreferences)
	handleAPI(r, "PATCH", "/conversations/{conversation_id}/plugin-settings", []string{"qa.write"}, chat.PatchConversationPluginSettings)

	// ----- Plugin Sessions -----
	handleAPI(r, "GET", "/conversations/{conversation_id}/plugin-sessions", []string{"qa.read"}, plugin.ListConversationSessions)
	handleAPI(r, "GET", "/conversations/{conversation_id}/plugin-sessions:active", []string{"qa.read"}, plugin.GetActiveConversationSession)
	handleAPI(r, "GET", "/conversations/{conversation_id}/plugin-sessions:latest", []string{"qa.read"}, plugin.GetLatestConversationSession)
	handleAPI(r, "GET", "/plugin-sessions/{session_id}", []string{"qa.read"}, plugin.GetSessionDetail)
	handleAPI(r, "GET", "/plugin-sessions/{session_id}/slots", []string{"qa.read"}, plugin.GetSessionSlots)
	handleAPI(r, "GET", "/plugin-sessions/{session_id}/steps", []string{"qa.read"}, plugin.GetSessionSteps)
	handleAPI(r, "GET", "/plugin-sessions/{session_id}/state-graph", []string{"qa.read"}, plugin.GetStateGraph)
	handleAPI(r, "PATCH", "/plugin-sessions/{session_id}/slots/{slot_id}", []string{"qa.write"}, plugin.PatchSessionSlot)
	handleAPI(r, "POST", "/plugin-sessions/{session_id}:sync-search-config", []string{"qa.write"}, plugin.SyncSessionSearchConfig)
	// Phase 3: slot item management.
	// Stable list_index-based routes (preferred).
	handleAPI(r, "DELETE", "/plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}", []string{"qa.write"}, plugin.DeleteSlotItemByIndex)
	handleAPI(r, "PATCH", "/plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}", []string{"qa.write"}, plugin.PatchSlotItemByIndex)
	handleAPI(r, "GET", "/plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/versions", []string{"qa.read"}, plugin.GetSlotItemVersionsByIndex)
	handleAPI(r, "POST", "/plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/rollback", []string{"qa.write"}, plugin.RollbackSlotItemByIndex)
	handleAPI(r, "PATCH", "/plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/caption", []string{"qa.write"}, plugin.PatchSlotCaptionByIndex)
	// Order management
	handleAPI(r, "PATCH", "/plugin-sessions/{session_id}/slots/{slot_id}/order", []string{"qa.write"}, plugin.ReorderSlotItems)
	handleAPI(r, "GET", "/plugin-sessions/{session_id}/slots/{slot_id}/order", []string{"qa.read"}, plugin.GetSlotOrderHandler)
	// Phase 4: caption editing and manual item creation
	handleAPI(r, "POST", "/plugin-sessions/{session_id}/slots/{slot_id}/items", []string{"qa.write"}, plugin.CreateSlotItem)
	handleAPI(r, "POST", "/plugin-sessions/{session_id}/artifacts", []string{"qa.write"}, plugin.SaveArtifactByKey)
	// Dismiss and restore plugin sessions.
	handleAPI(r, "POST", "/plugin-sessions/{session_id}:dismiss", []string{"qa.write"}, plugin.DismissSessionHandler)
	handleAPI(r, "POST", "/plugin-sessions/{session_id}:restore", []string{"qa.write"}, plugin.RestoreSessionHandler)
	// List dismissed sessions for a conversation (used by restore UI).
	handleAPI(r, "GET", "/conversations/{conversation_id}/dismissed-plugin-sessions", []string{"qa.read"}, plugin.ListDismissedSessionsHandler)
	handleAPI(r, "GET", "/evolution/tasks", []string{"qa.read"}, resourceupdate.ListTasks)
	handleAPI(r, "GET", "/evolution/tasks/{task_id}", []string{"qa.read"}, resourceupdate.GetTask)
	handleAPI(r, "GET", "/skill-review:summary", []string{"qa.read"}, resourceupdate.GetSkillReviewSummary)
	handleAPI(r, "POST", "/skill-review:run", []string{"qa.write"}, resourceupdate.RunSkillReview)
	handleAPI(r, "GET", "/skill-review/tasks", []string{"qa.read"}, resourceupdate.ListSkillReviewTasks)
	handleAPI(r, "GET", "/skill-review-results", []string{"qa.read"}, resourceupdate.ListSkillReviewResults)
	handleAPI(r, "GET", "/skill-review-results/{review_result_id}", []string{"qa.read"}, resourceupdate.GetSkillReviewResult)
	handleAPI(r, "POST", "/skill-review-results/{review_result_id}:accept", []string{"qa.read"}, resourceupdate.AcceptSkillReviewResult)
	handleAPI(r, "POST", "/skill-review-results/{review_result_id}:reject", []string{"qa.read"}, resourceupdate.RejectSkillReviewResult)
	handleAPI(r, "GET", "/memory-review-results", []string{"qa.read"}, resourceupdate.ListMemoryReviewResults)
	handleAPI(r, "GET", "/memory-review-results/{review_result_id}", []string{"qa.read"}, resourceupdate.GetMemoryReviewResult)
	handleAPI(r, "POST", "/memory-review-results/{review_result_id}:accept", []string{"qa.read"}, resourceupdate.AcceptMemoryReviewResult)
	handleAPI(r, "POST", "/memory-review-results/{review_result_id}:reject", []string{"qa.read"}, resourceupdate.RejectMemoryReviewResult)
	handleAPI(r, "GET", "/resource-versions", []string{"qa.read"}, resourcechange.ListVersions)
	handleAPI(r, "GET", "/resource-versions/{version_id}", []string{"qa.read"}, resourcechange.GetVersion)
	handleAPI(r, "GET", "/personalization-items", []string{"qa.read"}, evolution.ListManagedStates)
	handleAPI(r, "GET", "/personalization-setting", []string{"qa.read"}, evolution.GetPersonalizationSetting)
	handleAPI(r, "PUT", "/personalization-setting", []string{"qa.write"}, evolution.SetPersonalizationSetting)
	handleAPI(r, "GET", "/skills", []string{"qa.read"}, skillv2handler.List)
	handleAPI(r, "GET", "/skills:trash", []string{"qa.read"}, skillv2handler.ListTrash)
	handleAPI(r, "DELETE", "/skills:trash", []string{"qa.write"}, skillv2handler.EmptyTrash)
	handleAPI(r, "POST", "/skill_organize", []string{"qa.write"}, skillv2handler.SubmitSkillOrganize)
	handleAPI(r, "GET", "/skills/tags", []string{"qa.read"}, skillv2handler.ListTags)
	handleAPI(r, "GET", "/skills/categories", []string{"qa.read"}, skillv2handler.ListCategories)
	handleAPI(r, "POST", "/skills", []string{"qa.write"}, skillv2handler.Create)
	handleAPI(r, "POST", "/builtin-skills/{builtin_skill_uid}:enable", []string{"qa.write"}, skillv2handler.EnableBuiltinSkill)
	handleAPI(r, "GET", "/skills/{skill_id}:shares", []string{"qa.read"}, skillv2handler.ListShareTargets)
	handleAPI(r, "GET", "/skill-shares/incoming", []string{"qa.read"}, skillv2handler.IncomingShares)
	handleAPI(r, "GET", "/skill-shares/outgoing", []string{"qa.read"}, skillv2handler.OutgoingShares)
	handleAPI(r, "GET", "/skill-shares/{share_item_id}", []string{"qa.read"}, skillv2handler.GetShareItem)
	handleAPI(r, "POST", "/skill-shares/{share_item_id}:accept", []string{"qa.write"}, skillv2handler.AcceptShare)
	handleAPI(r, "POST", "/skill-shares/{share_item_id}:reject", []string{"qa.write"}, skillv2handler.RejectShare)
	handleAPI(r, "GET", "/skills/{skill_id}:draft-preview", []string{"qa.read"}, skillv2handler.DraftPreview)
	handleAPI(r, "GET", "/skills/{skill_id}/tree", []string{"qa.read"}, skillv2handler.Tree)
	handleAPI(r, "GET", "/skills/{skill_id}/file", []string{"qa.read"}, skillv2handler.File)
	handleAPI(r, "GET", "/skills/{skill_id}/fs/list", []string{"qa.read"}, skillv2handler.FSList)
	handleAPI(r, "GET", "/skills/{skill_id}/fs/info", []string{"qa.read"}, skillv2handler.FSInfo)
	handleAPI(r, "GET", "/skills/{skill_id}/fs/exists", []string{"qa.read"}, skillv2handler.FSExists)
	handleAPI(r, "GET", "/skills/{skill_id}/fs/content", []string{"qa.read"}, skillv2handler.FSContent)
	handleAPI(r, "GET", "/skills/{skill_id}/fs/download", []string{"qa.read"}, skillv2handler.FSDownload)
	handleAPI(r, "GET", "/skills/{skill_id}/draft/exists", []string{"qa.read"}, skillv2handler.DraftExists)
	handleAPI(r, "GET", "/skills/{skill_id}/draft/status", []string{"qa.read"}, skillv2handler.DraftStatus)
	handleAPI(r, "PUT", "/skills/{skill_id}/draft/fs/text", []string{"qa.write"}, skillv2handler.DraftWriteText)
	handleAPI(r, "PUT", "/skills/{skill_id}/draft/fs/upload", []string{"qa.write"}, skillv2handler.DraftUpload)
	handleAPI(r, "POST", "/skills/{skill_id}/draft/fs/dir", []string{"qa.write"}, skillv2handler.DraftMkdir)
	handleAPI(r, "DELETE", "/skills/{skill_id}/draft/fs/path", []string{"qa.write"}, skillv2handler.DraftDeletePath)
	handleAPI(r, "POST", "/skills/{skill_id}/draft/fs/move", []string{"qa.write"}, skillv2handler.DraftMove)
	handleAPI(r, "POST", "/skills/{skill_id}/draft-review/{review_id}/actions", []string{"qa.write"}, skillv2handler.DraftReviewAction)
	handleAPI(r, "POST", "/skills/{skill_id}/draft-review/{review_id}:undo", []string{"qa.write"}, skillv2handler.DraftReviewUndo)
	handleAPI(r, "POST", "/skills/{skill_id}/draft-review/{review_id}:commit", []string{"qa.write"}, skillv2handler.DraftReviewCommit)
	handleAPI(r, "POST", "/skills/{skill_id}/commit", []string{"qa.write"}, skillv2handler.Commit)
	handleAPI(r, "GET", "/skills/{skill_id}/revisions", []string{"qa.read"}, skillv2handler.ListRevisions)
	handleAPI(r, "GET", "/skills/{skill_id}/revisions/{revision_id}/tree", []string{"qa.read"}, skillv2handler.GetRevisionTree)
	handleAPI(r, "GET", "/skills/{skill_id}/revisions/{revision_id}/file", []string{"qa.read"}, skillv2handler.ReadRevisionFile)
	handleAPI(r, "GET", "/skills/{skill_id}/revisions/{revision_id}", []string{"qa.read"}, skillv2handler.GetRevision)
	handleAPI(r, "POST", "/skills/{skill_id}/rollback/preview", []string{"qa.read"}, skillv2handler.RollbackPreview)
	handleAPI(r, "POST", "/skills/{skill_id}/rollback", []string{"qa.write"}, skillv2handler.Rollback)
	handleAPI(r, "DELETE", "/skills/{skill_id}/revisions/{revision_id}", []string{"qa.write"}, skillv2handler.DeleteRevision)
	handleAPI(r, "GET", "/skills/{skill_id}", []string{"qa.read"}, skillv2handler.Get)
	handleAPI(r, "PATCH", "/skills/{skill_id}", []string{"qa.write"}, skillv2handler.Patch)
	handleAPI(r, "POST", "/skills/{skill_id}:trash", []string{"qa.write"}, skillv2handler.Trash)
	handleAPI(r, "POST", "/skills/{skill_id}:restore", []string{"qa.write"}, skillv2handler.Restore)
	handleAPI(r, "DELETE", "/skills/{skill_id}:purge", []string{"qa.write"}, skillv2handler.Purge)
	handleAPI(r, "DELETE", "/skills/{skill_id}", []string{"qa.write"}, skillv2handler.Delete)
	handleAPI(r, "POST", "/skills/{skill_id}:generate", []string{"qa.write"}, skillv2handler.Generate)
	handleAPI(r, "POST", "/skills/{skill_id}:confirm", []string{"qa.write"}, skillv2handler.Confirm)
	handleAPI(r, "POST", "/skills/{skill_id}:discard", []string{"qa.write"}, skillv2handler.Discard)
	handleAPI(r, "POST", "/skills/{skill_id}:share", []string{"qa.write"}, skillv2handler.Share)
	handleAPI(r, "POST", "/skill-diff/tree", []string{"qa.read"}, skillv2handler.DiffTree)
	handleAPI(r, "POST", "/skill-diff/file", []string{"qa.read"}, skillv2handler.DiffFile)
	handleAPI(r, "GET", "/skill-market", []string{"qa.read"}, skillv2handler.MarketList)
	handleAPI(r, "GET", "/skill-market/{market_item_id}", []string{"qa.read"}, skillv2handler.MarketGet)
	handleAPI(r, "POST", "/skill-market:install", []string{"qa.write"}, skillv2handler.MarketInstall)
	handleAPI(r, "POST", "/skill-market/{market_item_id}:install", []string{"qa.write"}, skillv2handler.MarketInstall)
	handleAPI(r, "POST", "/admin/skill-market", []string{"qa.write"}, skillv2handler.MarketPublish)
	handleAPI(r, "PATCH", "/admin/skill-market/{market_item_id}", []string{"qa.write"}, skillv2handler.MarketEdit)
	handleAPI(r, "POST", "/admin/skill-market/{market_item_id}:offline", []string{"qa.write"}, skillv2handler.MarketUnpublish)
	handleAPI(r, "POST", "/skill-market/admin/items", []string{"qa.write"}, skillv2handler.MarketPublish)
	handleAPI(r, "PATCH", "/skill-market/admin/items/{market_item_id}", []string{"qa.write"}, skillv2handler.MarketEdit)
	handleAPI(r, "POST", "/skill-market/admin/items/{market_item_id}:unpublish", []string{"qa.write"}, skillv2handler.MarketUnpublish)
	handleAPI(r, "PUT", "/memory", []string{"qa.write"}, memory.Upsert)
	handleAPI(r, "GET", "/memory:draft-preview", []string{"qa.read"}, memory.DraftPreview)
	handleAPI(r, "POST", "/memory:generate", []string{"qa.write"}, memory.Generate)
	handleAPI(r, "POST", "/memory:confirm", []string{"qa.write"}, memory.Confirm)
	handleAPI(r, "POST", "/memory:discard", []string{"qa.write"}, memory.Discard)
	handleAPI(r, "PUT", "/user-preference", []string{"qa.write"}, preference.Upsert)
	handleAPI(r, "GET", "/user-preference:draft-preview", []string{"qa.read"}, preference.DraftPreview)
	handleAPI(r, "POST", "/user-preference:generate", []string{"qa.write"}, preference.Generate)
	handleAPI(r, "POST", "/user-preference:confirm", []string{"qa.write"}, preference.Confirm)
	handleAPI(r, "POST", "/user-preference:discard", []string{"qa.write"}, preference.Discard)
	handleAPI(r, "GET", "/personal-resource/{resource_type}:file", []string{"qa.read"}, resourcefs.GetFile)
	handleAPI(r, "PUT", "/personal-resource/{resource_type}:file", []string{"qa.write"}, resourcefs.WriteDraft)
	handleAPI(r, "PUT", "/personal-resource/{resource_type}:draft", []string{"qa.write"}, resourcefs.WriteDraft)
	handleAPI(r, "GET", "/personal-resource/{resource_type}:draft-preview", []string{"qa.read"}, resourcefs.DraftPreview)
	handleAPI(r, "POST", "/personal-resource/{resource_type}/draft-review/{review_id}/actions", []string{"qa.write"}, resourcefs.ReviewAction)
	handleAPI(r, "POST", "/personal-resource/{resource_type}/draft-review/{review_id}:undo", []string{"qa.write"}, resourcefs.ReviewUndo)
	handleAPI(r, "POST", "/personal-resource/{resource_type}:commit", []string{"qa.write"}, resourcefs.CommitDraft)
	handleAPI(r, "POST", "/personal-resource/{resource_type}:discard", []string{"qa.write"}, resourcefs.DiscardDraft)
	handleAPI(r, "GET", "/personal-resource/{resource_type}/revisions", []string{"qa.read"}, resourcefs.ListRevisions)
	handleAPI(r, "GET", "/personal-resource/{resource_type}/revisions/{revision_id}", []string{"qa.read"}, resourcefs.GetRevision)
	handleAPI(r, "POST", "/personal-resource/{resource_type}:rollback", []string{"qa.write"}, resourcefs.Rollback)

	handleAPI(r, "GET", "/conversations/{name}:detail", []string{"qa.read"}, chat.GetConversationDetail)
	handleAPI(r, "GET", "/conversations/{name}:history", []string{"qa.read"}, chat.GetConversationHistory)
	handleAPI(r, "GET", "/conversations/{name}", []string{"qa.read"}, chat.GetConversation)
	handleAPI(r, "DELETE", "/conversations/{name}", []string{"qa.write"}, chat.DeleteConversation)
	handleAPI(r, "POST", "/conversations:batchDelete", []string{"qa.write"}, chat.BatchDeleteConversations)
	handleAPI(r, "GET", "/conversations", []string{"qa.read"}, chat.ListConversations)
	handleAPI(r, "POST", "/conversations:setChatHistory", []string{"qa.write"}, chat.SetChatHistory)
	handleAPI(r, "POST", "/conversations:feedBackChatHistory", []string{"qa.write"}, chat.FeedBackChatHistory)
	handleAPI(r, "PATCH", "/conversations/{name}:ask-answers", []string{"qa.write"}, chat.SaveAskAnswers)

	handleAPI(r, "GET", "/conversation:switchStatus", []string{"qa.read"}, chat.GetMultiAnswersSwitchStatus)
	handleAPI(r, "POST", "/conversation:switchStatus", []string{"qa.write"}, chat.SetMultiAnswersSwitchStatus)
	handleAPI(r, "POST", "/conversation:export", []string{"qa.read"}, chat.ExportConversations)
	handleAPI(r, "GET", "/conversation:export/files/{file_id}", []string{"qa.read"}, chat.DownloadExportConversationFile)

	// ----- Word group -----
	handleAPI(r, "POST", "/word_group:checkExists", []string{"document.read"}, wordgroup.CheckWordsExist)
	handleAPI(r, "POST", "/word_group:update", []string{"document.write"}, wordgroup.UpdateWordGroup)
	handleAPI(r, "POST", "/word_group:search", []string{"document.read"}, wordgroup.SearchWordGroups)
	handleAPI(r, "GET", "/word_group", []string{"document.read"}, wordgroup.ListWordGroups)
	handleAPI(r, "GET", "/word_group/{group_id}", []string{"document.read"}, wordgroup.GetWordGroup)
	handleAPI(r, "DELETE", "/word_group/{group_id}", []string{"document.write"}, wordgroup.DeleteWordGroup)
	handleAPI(r, "POST", "/word_group:batchDelete", []string{"document.write"}, wordgroup.BatchDeleteWordGroups)
	handleAPI(r, "POST", "/word_group:merge", []string{"document.write"}, wordgroup.MergeWordGroups)
	handleAPI(r, "POST", "/word_group", []string{"document.write"}, wordgroup.CreateWordGroup)

	handleAPI(r, "GET", "/word_group_conflict", []string{"document.read"}, wordgroup.ListWordGroupConflicts)
	handleAPI(r, "POST", "/word_group_conflict:addToGroup", []string{"document.write"}, wordgroup.AddWordGroupConflictToGroups)
	handleAPI(r, "POST", "/word_group_conflict:createGroup", []string{"document.write"}, wordgroup.CreateWordGroupFromConflict)
	handleAPI(r, "DELETE", "/word_group_conflict/{id}", []string{"document.write"}, wordgroup.DeleteWordGroupConflict)
	handleAPI(r, "POST", "/word_group_conflict:mergeAndAddWord", []string{"document.write"}, wordgroup.MergeWordGroupsAndAddWord)
	// Internal endpoint for algorithm service. Uses user_id in payload, no request auth headers.
	handleAPI(r, "POST", "/inner/word_group:apply", nil, wordgroup.ApplyWordGroupAction)

	// ----- Model provider -----
	handleAPI(r, "GET", "/model_providers/features", []string{"model.read"}, modelprovider.GetModelFeatures)
	handleAPI(r, "GET", "/model_providers", []string{"model.read"}, modelprovider.ListUserProviders)
	handleAPI(r, "GET", "/model_providers:with_groups", []string{"model.read"}, modelprovider.ListUserProvidersWithGroups)
	handleAPI(r, "POST", "/model_providers/{model_provider_id}/groups/{group_id}:check", []string{"model.write"}, modelprovider.CheckGroup)
	handleAPI(r, "GET", "/model_providers/models", []string{"model.read"}, modelprovider.ListUserModelsByModelType)
	handleAPI(r, "GET", "/model_providers/models/ready", []string{"model.read"}, modelprovider.GetModelReady)
	handleAPI(r, "GET", "/model_providers/selected_models", []string{"model.read"}, modelprovider.GetSelectedModels)
	handleAPI(r, "PUT", "/model_providers/selected_models", []string{"model.write"}, modelprovider.SetSelectedModels)
	handleAPI(r, "PUT", "/model_providers/selected_models/share", []string{"model.write"}, modelprovider.SetSharedModel)
	handleAPI(r, "GET", "/model_providers/provider_groups", []string{"model.read"}, modelprovider.ListUserProviderGroupsByCategory)
	handleAPI(r, "GET", "/model_providers/verified", []string{"model.read"}, modelprovider.GetVerifiedProvider)
	handleAPI(r, "GET", "/model_providers/selected_providers", []string{"model.read"}, modelprovider.GetSelectedProviders)
	handleAPI(r, "PUT", "/model_providers/selected_providers", []string{"model.write"}, modelprovider.SetSelectedProvider)
	handleAPI(r, "PUT", "/model_providers/selected_providers/share", []string{"model.write"}, modelprovider.SetSharedProvider)
	handleAPI(r, "GET", "/model_providers/{model_provider_id}/groups", []string{"model.read"}, modelprovider.ListGroups)
	handleAPI(r, "POST", "/model_providers/{model_provider_id}/groups", []string{"model.write"}, modelprovider.CreateGroup)
	handleAPI(r, "PATCH", "/model_providers/{model_provider_id}/groups/{group_id}", []string{"model.write"}, modelprovider.UpdateGroup)
	handleAPI(r, "DELETE", "/model_providers/{model_provider_id}/groups/{group_id}", []string{"model.write"}, modelprovider.DeleteGroup)
	handleAPI(r, "GET", "/model_providers/{model_provider_id}/groups/{group_id}/models", []string{"model.read"}, modelprovider.ListGroupModels)
	handleAPI(r, "POST", "/model_providers/{model_provider_id}/groups/{group_id}/models", []string{"model.write"}, modelprovider.AddGroupModel)
	handleAPI(r, "DELETE", "/model_providers/{model_provider_id}/groups/{group_id}/models/{model_id}", []string{"model.write"}, modelprovider.DeleteGroupModel)
	handleAPI(r, "POST", "/model_providers/{model_provider_id}/groups/{group_id}/keys", []string{"model.write"}, modelprovider.AddKey)
	handleAPI(r, "DELETE", "/model_providers/{model_provider_id}/groups/{group_id}/keys", []string{"model.write"}, modelprovider.RemoveKey)

	// ----- Prompttext -----
	handleAPI(r, "POST", "/prompts", []string{"document.write"}, chat.CreatePrompt)
	handleAPI(r, "POST", "/prompts:polish", []string{"qa.read"}, chat.PolishPrompt)
	// :setDefault/:unsetDefault text {name} text，text :action text。
	handleAPI(r, "POST", "/prompts/{name}:setDefault", []string{"document.write"}, chat.SetDefaultPrompt)
	handleAPI(r, "POST", "/prompts/{name}:unsetDefault", []string{"document.write"}, chat.UnsetDefaultPrompt)
	handleAPI(r, "PATCH", "/prompts/{name}", []string{"document.write"}, chat.UpdatePrompt)
	handleAPI(r, "DELETE", "/prompts/{name}", []string{"document.write"}, chat.DeletePrompt)
	handleAPI(r, "GET", "/prompts/{name}", []string{"document.read"}, chat.GetPrompt)
	handleAPI(r, "GET", "/prompts", []string{"document.read"}, chat.ListPrompts)

	// Algorithm service callbacks: no request-level RBAC, protected by internal service token at infra level.
	handleAPI(r, "POST", "/skill/create", nil, skillv2handler.InternalCreate)
	handleAPI(r, "GET", "/remote-fs/list", nil, remotefs.List)
	handleAPI(r, "GET", "/remote-fs/info", nil, remotefs.Info)
	handleAPI(r, "GET", "/remote-fs/exists", nil, remotefs.Exists)
	handleAPI(r, "GET", "/remote-fs/content", nil, remotefs.Content)
	handleAPI(r, "PUT", "/remote-fs/content", nil, remotefs.Content)
	handleAPI(r, "POST", "/remote-fs/dir", nil, remotefs.Dir)
	handleAPI(r, "DELETE", "/remote-fs/path", nil, remotefs.Delete)
	handleAPI(r, "POST", "/remote-fs/copy", nil, remotefs.Copy)
	handleAPI(r, "POST", "/remote-fs/move", nil, remotefs.Move)
	handleAPI(r, "POST", "/remote-fs/trash", nil, remotefs.Trash)

	// ----- ACL（Knowledge basetextPermission） -----
	handleAPI(r, "GET", "/kb/list", []string{"document.read"}, acl.ListKB)
	handleAPI(r, "POST", "/kb/permission/batch", []string{"document.read"}, acl.PermissionBatch)
	handleAPI(r, "GET", "/kb/{kb_id}/permission", []string{"document.read"}, acl.GetPermission)
	handleAPI(r, "GET", "/kb/{kb_id}/can", []string{"document.read"}, acl.CanHandler)
	handleAPI(r, "GET", "/kb/{kb_id}/acl", []string{"document.read"}, acl.ListACL)
	handleAPI(r, "POST", "/kb/{kb_id}/acl", []string{"document.write"}, acl.AddACL)
	handleAPI(r, "POST", "/kb/{kb_id}/acl/batch", []string{"document.write"}, acl.BatchAddACL)
	handleAPI(r, "PUT", "/kb/{kb_id}/acl/{acl_id}", []string{"document.write"}, acl.UpdateACL)
	handleAPI(r, "DELETE", "/kb/{kb_id}/acl/{acl_id}", []string{"document.write"}, acl.DeleteACL)
	handleAPI(r, "GET", "/kb/{kb_id}/authorization", []string{"document.read"}, acl.GetKBAuthorization)
	handleAPI(r, "POST", "/kb/{kb_id}/authorization", []string{"document.write"}, acl.SetKBAuthorization)
	handleAPI(r, "GET", "/kb/grant-principals", []string{"document.read"}, acl.ListGrantPrincipals)
}
