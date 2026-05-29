package main

import (
	"lazymind/core/acl"
	"lazymind/core/agent"
	"lazymind/core/chat"
	"lazymind/core/doc"
	"lazymind/core/evolution"
	"lazymind/core/file"
	"lazymind/core/memory"
	"lazymind/core/modelprovider"
	"lazymind/core/preference"
	"lazymind/core/skill"
	"lazymind/core/wordgroup"

	"github.com/gorilla/mux"
)

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
	handleAPI(r, "POST", "/datasets/{dataset}:setDefault", []string{"document.read"}, doc.SetDefault)
	handleAPI(r, "POST", "/datasets/{dataset}:unsetDefault", []string{"document.read"}, doc.UnsetDefault)

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
	handleAPI(r, "POST", "/documents:search", []string{"document.read"}, doc.SearchAllDocuments)
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
	handleAPI(r, "POST", "/chat", []string{"qa.read"}, chat.Chat)

	// ----- Agent thread stream -----
	handleAPI(r, "GET", "/agent/threads", []string{"qa.read"}, agent.ListThreads)
	handleAPI(r, "POST", "/agent/threads", []string{"qa.read"}, agent.CreateThread)
	handleAPI(r, "GET", "/agent/threads/{thread_id}:events", []string{"qa.read"}, agent.StreamThreadEvents)
	handleAPI(r, "GET", "/agent/threads/{thread_id}", []string{"qa.read"}, agent.GetThread)
	handleAPI(r, "GET", "/agent/threads/{thread_id}/history", []string{"qa.read"}, agent.GetThreadHistory)
	handleAPI(r, "DELETE", "/agent/threads/{thread_id}:history", []string{"qa.read"}, agent.DeleteThreadHistory)
	handleAPI(r, "GET", "/agent/threads/{thread_id}/rounds", []string{"qa.read"}, agent.ListThreadRounds)
	handleAPI(r, "GET", "/agent/threads/{thread_id}/records", []string{"qa.read"}, agent.ListThreadRecords)
	handleAPI(r, "GET", "/agent/threads/{thread_id}/results/datasets", []string{"qa.read"}, agent.GetThreadResultDatasets)
	handleAPI(r, "GET", "/agent/threads/{thread_id}/results/eval-reports", []string{"qa.read"}, agent.GetThreadResultEvalReports)
	handleAPI(r, "GET", "/agent/threads/{thread_id}/results/analysis-reports", []string{"qa.read"}, agent.GetThreadResultAnalysisReports)
	handleAPI(r, "GET", "/agent/threads/{thread_id}/results/diffs", []string{"qa.read"}, agent.GetThreadResultDiffs)
	handleAPI(r, "GET", "/agent/threads/{thread_id}/results/abtests", []string{"qa.read"}, agent.GetThreadResultAbtests)
	handleAPI(r, "POST", "/agent/threads/{thread_id}:messages", []string{"qa.read"}, agent.StreamThreadMessages)
	handleAPI(r, "POST", "/agent/threads/{thread_id}:start", []string{"qa.read"}, agent.StartThread)
	handleAPI(r, "POST", "/agent/threads/{thread_id}:pause", []string{"qa.read"}, agent.PauseThread)
	handleAPI(r, "POST", "/agent/threads/{thread_id}:cancel", []string{"qa.read"}, agent.CancelThread)
	handleAPI(r, "POST", "/agent/threads/{thread_id}:retry", []string{"qa.read"}, agent.RetryThread)
	handleAPI(r, "GET", "/agent/reports/{report_id}:content", []string{"qa.read"}, agent.GetReportContent)
	handleAPI(r, "GET", "/agent/diffs/{apply_id}/{filename:.*}", []string{"qa.read"}, agent.GetDiffContent)
	handleAPI(r, "POST", "/agent/files:content", []string{"qa.read"}, agent.GetAgentFileContent)

	// ----- Conversationtext -----
	handleAPI(r, "POST", "/conversations:chat", []string{"qa.read"}, chat.ChatConversations)
	handleAPI(r, "POST", "/conversations:resumeChat", []string{"qa.read"}, chat.ResumeChat)
	handleAPI(r, "POST", "/conversations:stopChatGeneration", []string{"qa.read"}, chat.StopChatGeneration)
	handleAPI(r, "GET", "/conversations/{conversation_id}:status", []string{"qa.read"}, chat.GetChatStatus)
	handleAPI(r, "GET", "/evolution/suggestions", []string{"qa.read"}, evolution.ListSuggestions)
	handleAPI(r, "GET", "/evolution/suggestions/{id}", []string{"qa.read"}, evolution.GetSuggestion)
	handleAPI(r, "POST", "/evolution/suggestions/{id}:approve", []string{"qa.read"}, evolution.ApproveSuggestion)
	handleAPI(r, "POST", "/evolution/suggestions/{id}:reject", []string{"qa.read"}, evolution.RejectSuggestion)
	handleAPI(r, "POST", "/evolution/suggestions:batchApprove", []string{"qa.read"}, evolution.BatchApproveSuggestions)
	handleAPI(r, "POST", "/evolution/suggestions:batchReject", []string{"qa.read"}, evolution.BatchRejectSuggestions)
	handleAPI(r, "GET", "/personalization-items", []string{"qa.read"}, evolution.ListManagedStates)
	handleAPI(r, "GET", "/personalization-setting", []string{"qa.read"}, evolution.GetPersonalizationSetting)
	handleAPI(r, "PUT", "/personalization-setting", []string{"qa.read"}, evolution.SetPersonalizationSetting)
	handleAPI(r, "GET", "/skills", []string{"qa.read"}, skill.List)
	handleAPI(r, "POST", "/skills", []string{"qa.read"}, skill.CreateManaged)
	handleAPI(r, "GET", "/skills/{skill_id}:shares", []string{"qa.read"}, skill.ListSkillShareTargets)
	handleAPI(r, "GET", "/skill-shares/incoming", []string{"qa.read"}, skill.IncomingShares)
	handleAPI(r, "GET", "/skill-shares/outgoing", []string{"qa.read"}, skill.OutgoingShares)
	handleAPI(r, "GET", "/skill-shares/{share_item_id}", []string{"qa.read"}, skill.GetShareItem)
	handleAPI(r, "POST", "/skill-shares/{share_item_id}:accept", []string{"qa.read"}, skill.AcceptShare)
	handleAPI(r, "POST", "/skill-shares/{share_item_id}:reject", []string{"qa.read"}, skill.RejectShare)
	handleAPI(r, "GET", "/skills/{skill_id}:draft-preview", []string{"qa.read"}, skill.DraftPreview)
	handleAPI(r, "GET", "/skills/{skill_id}", []string{"qa.read"}, skill.Get)
	handleAPI(r, "PATCH", "/skills/{skill_id}", []string{"qa.read"}, skill.UpdateManaged)
	handleAPI(r, "DELETE", "/skills/{skill_id}", []string{"qa.read"}, skill.DeleteManaged)
	handleAPI(r, "POST", "/skills/{skill_id}:generate", []string{"qa.read"}, skill.Generate)
	handleAPI(r, "POST", "/skills/{skill_id}:confirm", []string{"qa.read"}, skill.Confirm)
	handleAPI(r, "POST", "/skills/{skill_id}:discard", []string{"qa.read"}, skill.Discard)
	handleAPI(r, "POST", "/skills/{skill_id}:share", []string{"qa.read"}, skill.Share)
	handleAPI(r, "PUT", "/memory", []string{"qa.read"}, memory.Upsert)
	handleAPI(r, "GET", "/memory:draft-preview", []string{"qa.read"}, memory.DraftPreview)
	handleAPI(r, "POST", "/memory:generate", []string{"qa.read"}, memory.Generate)
	handleAPI(r, "POST", "/memory:confirm", []string{"qa.read"}, memory.Confirm)
	handleAPI(r, "POST", "/memory:discard", []string{"qa.read"}, memory.Discard)
	handleAPI(r, "PUT", "/user-preference", []string{"qa.read"}, preference.Upsert)
	handleAPI(r, "GET", "/user-preference:draft-preview", []string{"qa.read"}, preference.DraftPreview)
	handleAPI(r, "POST", "/user-preference:generate", []string{"qa.read"}, preference.Generate)
	handleAPI(r, "POST", "/user-preference:confirm", []string{"qa.read"}, preference.Confirm)
	handleAPI(r, "POST", "/user-preference:discard", []string{"qa.read"}, preference.Discard)

	// :detail text {name} text，text /conversations/xxx:detail text {name} text GetConversation（text history）
	handleAPI(r, "GET", "/conversations/{name}:detail", []string{"qa.read"}, chat.GetConversationDetail)
	handleAPI(r, "GET", "/conversations/{name}", []string{"qa.read"}, chat.GetConversation)
	handleAPI(r, "DELETE", "/conversations/{name}", []string{"qa.read"}, chat.DeleteConversation)
	handleAPI(r, "POST", "/conversations:batchDelete", []string{"qa.read"}, chat.BatchDeleteConversations)
	handleAPI(r, "GET", "/conversations", []string{"qa.read"}, chat.ListConversations)
	handleAPI(r, "POST", "/conversations:setChatHistory", []string{"qa.read"}, chat.SetChatHistory)
	handleAPI(r, "POST", "/conversations:feedBackChatHistory", []string{"qa.read"}, chat.FeedBackChatHistory)

	handleAPI(r, "GET", "/conversation:switchStatus", []string{"qa.read"}, chat.GetMultiAnswersSwitchStatus)
	handleAPI(r, "POST", "/conversation:switchStatus", []string{"qa.read"}, chat.SetMultiAnswersSwitchStatus)
	handleAPI(r, "POST", "/conversation:export", []string{"qa.read"}, chat.ExportConversations)
	handleAPI(r, "GET", "/conversation:export/files/{file_id}", []string{"qa.read"}, chat.DownloadExportConversationFile)

	// ----- Word group -----
	handleAPI(r, "POST", "/word_group:checkExists", []string{}, wordgroup.CheckWordsExist)
	handleAPI(r, "POST", "/word_group:update", []string{}, wordgroup.UpdateWordGroup)
	handleAPI(r, "POST", "/word_group:search", []string{}, wordgroup.SearchWordGroups)
	handleAPI(r, "GET", "/word_group", []string{}, wordgroup.ListWordGroups)
	handleAPI(r, "GET", "/word_group/{group_id}", []string{}, wordgroup.GetWordGroup)
	handleAPI(r, "DELETE", "/word_group/{group_id}", []string{}, wordgroup.DeleteWordGroup)
	handleAPI(r, "POST", "/word_group:batchDelete", []string{}, wordgroup.BatchDeleteWordGroups)
	handleAPI(r, "POST", "/word_group:merge", []string{}, wordgroup.MergeWordGroups)
	handleAPI(r, "POST", "/word_group", []string{}, wordgroup.CreateWordGroup)

	handleAPI(r, "GET", "/word_group_conflict", []string{}, wordgroup.ListWordGroupConflicts)
	handleAPI(r, "POST", "/word_group_conflict:addToGroup", []string{}, wordgroup.AddWordGroupConflictToGroups)
	handleAPI(r, "POST", "/word_group_conflict:createGroup", []string{}, wordgroup.CreateWordGroupFromConflict)
	handleAPI(r, "DELETE", "/word_group_conflict/{id}", []string{}, wordgroup.DeleteWordGroupConflict)
	handleAPI(r, "POST", "/word_group_conflict:mergeAndAddWord", []string{}, wordgroup.MergeWordGroupsAndAddWord)
	// Internal endpoint for algorithm service. Uses user_id in payload, no request auth headers.
	handleAPI(r, "POST", "/inner/word_group:apply", []string{}, wordgroup.ApplyWordGroupAction)

	// ----- Model provider -----
	handleAPI(r, "GET", "/model_providers/features", nil, modelprovider.GetModelFeatures)
	handleAPI(r, "GET", "/model_providers", []string{}, modelprovider.ListUserProviders)
	handleAPI(r, "GET", "/model_providers:with_groups", []string{}, modelprovider.ListUserProvidersWithGroups)
	handleAPI(r, "POST", "/model_providers/{model_provider_id}/groups/{group_id}:check", []string{}, modelprovider.CheckGroup)
	handleAPI(r, "GET", "/model_providers/models", []string{}, modelprovider.ListUserModelsByModelType)
	handleAPI(r, "GET", "/model_providers/models/ready", nil, modelprovider.GetModelReady)
	handleAPI(r, "GET", "/model_providers/selected_models", []string{}, modelprovider.GetSelectedModels)
	handleAPI(r, "PUT", "/model_providers/selected_models", []string{}, modelprovider.SetSelectedModels)
	handleAPI(r, "PUT", "/model_providers/selected_models/share", []string{"document.write"}, modelprovider.SetSharedModel)
	handleAPI(r, "GET", "/model_providers/verified", []string{}, modelprovider.GetVerifiedProvider)
	handleAPI(r, "GET", "/model_providers/selected_providers", []string{}, modelprovider.GetSelectedProviders)
	handleAPI(r, "PUT", "/model_providers/selected_providers", []string{}, modelprovider.SetSelectedProvider)
	handleAPI(r, "PUT", "/model_providers/selected_providers/share", []string{"document.write"}, modelprovider.SetSharedProvider)
	handleAPI(r, "GET", "/model_providers/{model_provider_id}/groups", []string{}, modelprovider.ListGroups)
	handleAPI(r, "POST", "/model_providers/{model_provider_id}/groups", []string{}, modelprovider.CreateGroup)
	handleAPI(r, "PATCH", "/model_providers/{model_provider_id}/groups/{group_id}", []string{}, modelprovider.UpdateGroup)
	handleAPI(r, "DELETE", "/model_providers/{model_provider_id}/groups/{group_id}", []string{}, modelprovider.DeleteGroup)
	handleAPI(r, "GET", "/model_providers/{model_provider_id}/groups/{group_id}/models", []string{}, modelprovider.ListGroupModels)
	handleAPI(r, "POST", "/model_providers/{model_provider_id}/groups/{group_id}/models", []string{}, modelprovider.AddGroupModel)
	handleAPI(r, "DELETE", "/model_providers/{model_provider_id}/groups/{group_id}/models/{model_id}", []string{}, modelprovider.DeleteGroupModel)

	// ----- Prompttext -----
	handleAPI(r, "POST", "/prompts", []string{"document.write"}, chat.CreatePrompt)
	// :setDefault/:unsetDefault text {name} text，text :action text。
	handleAPI(r, "POST", "/prompts/{name}:setDefault", []string{"document.write"}, chat.SetDefaultPrompt)
	handleAPI(r, "POST", "/prompts/{name}:unsetDefault", []string{"document.write"}, chat.UnsetDefaultPrompt)
	handleAPI(r, "PATCH", "/prompts/{name}", []string{"document.write"}, chat.UpdatePrompt)
	handleAPI(r, "DELETE", "/prompts/{name}", []string{"document.write"}, chat.DeletePrompt)
	handleAPI(r, "GET", "/prompts/{name}", []string{"document.read"}, chat.GetPrompt)
	handleAPI(r, "GET", "/prompts", []string{"document.read"}, chat.ListPrompts)

	// ----- Evolution / long-term state -----
	handleAPI(r, "POST", "/skill/suggestion", []string{}, skill.Suggestion)
	handleAPI(r, "POST", "/skill/create", []string{}, skill.Create)
	handleAPI(r, "POST", "/skill/remove", []string{}, skill.Remove)
	handleAPI(r, "GET", "/remote-fs/list", []string{}, skill.RemoteFSList)
	handleAPI(r, "GET", "/remote-fs/info", []string{}, skill.RemoteFSInfo)
	handleAPI(r, "GET", "/remote-fs/exists", []string{}, skill.RemoteFSExists)
	handleAPI(r, "GET", "/remote-fs/content", []string{}, skill.RemoteFSContent)
	handleAPI(r, "POST", "/memory/suggestion", []string{}, memory.Suggestion)
	handleAPI(r, "POST", "/user_preference/suggestion", []string{}, preference.Suggestion)

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
