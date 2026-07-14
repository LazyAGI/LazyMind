// Package main text Core API text Swagger text，text swag init text docs，text。
package main

// Swagger text（swag init text）
// @title           Backend Core API
// @version         0.1.0
// @description     LazyMind Go backend core API - proxies to algorithm services. text Kong text /api/core。
// @BasePath        /api/core
// @schemes         http https
func _swagGeneral() {}

// --- health & misc ---
// @Summary  Health check
// @Router   /health [get]
func _swagHealth() {}

// @Summary  Hello (requires user.read)
// @Router   /hello [get]
func _swagHello() {}

// @Summary  Admin (requires document.write)
// @Router   /admin [get]
func _swagAdmin() {}

// --- dataset ---
// @Summary  Dataset algorithm list
// @Tags      dataset
// @Router    /dataset/algos [get]
func _swagListAlgos() {}

// @Summary  Dataset tags
// @Tags      dataset
// @Router    /dataset/tags [get]
func _swagDatasetTags() {}

// @Summary  Dataset list
// @Tags      datasets
// @Router    /datasets [get]
func _swagListDatasets() {}

// @Summary  Create dataset
// @Tags      datasets
// @Router    /datasets [post]
func _swagCreateDataset() {}

// @Summary  Get dataset
// @Tags      datasets
// @Router    /datasets/{dataset} [get]
func _swagGetDataset() {}

// @Summary  Delete dataset
// @Tags      datasets
// @Router    /datasets/{dataset} [delete]
func _swagDeleteDataset() {}

// @Summary  Update dataset
// @Tags      datasets
// @Router    /datasets/{dataset} [patch]
func _swagUpdateDataset() {}

// @Summary  Set as default dataset
// @Tags      datasets
// @Router    /datasets/{dataset}:setDefault [post]
func _swagSetDefault() {}

// @Summary  Unset default dataset
// @Tags      datasets
// @Router    /datasets/{dataset}:unsetDefault [post]
func _swagUnsetDefault() {}

// @Summary  textDefaultDataset
// @Tags      datasets
// @Router    /datasets:allDefaultDatasets [get]
func _swagAllDefaultDatasets() {}

// @Summary  textUpload URL
// @Tags      datasets
// @Router    /datasets:presignUploadCoverImageUrl [post]
func _swagPresignUploadCoverImageURL() {}

// @Summary  SearchDataset
// @Tags      datasets
// @Router    /datasets:search [post]
func _swagSearchDatasets() {}

// @Summary  DatasetTasktext
// @Tags      datasets
// @Router    /datasets/{dataset}/tasks:callback [post]
func _swagCallbackTask() {}

// --- documents ---
// @Summary  Document list
// @Tags      documents
// @Router    /datasets/{dataset}/documents [get]
func _swagListDocuments() {}

// @Summary  Create document
// @Tags      documents
// @Router    /datasets/{dataset}/documents [post]
func _swagCreateDocument() {}

// @Summary  Get document
// @Tags      documents
// @Router    /datasets/{dataset}/documents/{document} [get]
func _swagGetDocument() {}

// @Summary  Delete document
// @Tags      documents
// @Router    /datasets/{dataset}/documents/{document} [delete]
func _swagDeleteDocument() {}

// @Summary  Update document
// @Tags      documents
// @Router    /datasets/{dataset}/documents/{document} [patch]
func _swagUpdateDocument() {}

// @Summary  Search documents
// @Tags      documents
// @Router    /datasets/{dataset}/documents:search [post]
func _swagSearchDocuments() {}

// @Summary  textSearch documents
// @Tags      documents
// @Router    /documents:search [post]
func _swagSearchAllDocuments() {}

// @Summary  BatchDelete
// @Tags      datasets
// @Router    /datasets/{dataset}:batchDelete [post]
func _swagBatchDelete() {}

// @Summary  Document creator list
// @Tags      document
// @Router    /document/creators [get]
func _swagDocumentCreators() {}

// @Summary  Documenttext
// @Tags      document
// @Router    /document/tags [get]
func _swagDocumentTags() {}

// @Summary  text
// @Tags      table
// @Router    /datasets/{dataset}/documents/{document}/table:add [post]
func _swagAddTableData() {}

// @Summary  textBatchDelete
// @Tags      table
// @Router    /datasets/{dataset}/documents/{document}/table:batchDelete [post]
func _swagBatchDeleteTableData() {}

// @Summary  text
// @Tags      table
// @Router    /datasets/{dataset}/documents/{document}/table:modify [post]
func _swagModifyTableData() {}

// @Summary  textSearch
// @Tags      table
// @Router    /datasets/{dataset}/documents/{document}/table:search [get]
func _swagSearchTableData() {}

// --- segments ---
// @Summary  text
// @Tags      segments
// @Router    /datasets/{dataset}/documents/{document}/segments [get]
func _swagListSegments() {}

// @Summary  Gettext
// @Tags      segments
// @Router    /datasets/{dataset}/documents/{document}/segments/{segment} [get]
func _swagGetSegment() {}

// @Summary  text
// @Tags      segments
// @Router    /datasets/{dataset}/documents/{document}/segments/{segment}:edit [post]
func _swagEditSegment() {}

// @Summary  text
// @Tags      segments
// @Router    /datasets/{dataset}/documents/{document}/segments/{segment}:modifyStatus [post]
func _swagModifyStatus() {}

// @Summary  Searchtext
// @Tags      segments
// @Router    /datasets/{dataset}/documents/{document}/segments:search [post]
func _swagSearchSegments() {}

// @Summary  Deletetext
// @Tags      segments
// @Router    /datasets/{dataset}/group/{group}/documents/{document}/segments/{segment} [delete]
func _swagDeleteSegment() {}

// @Summary  text URI Batchtext
// @Tags      segments
// @Router    /segment/imageURIs:batchSign [post]
func _swagBatchSignImageURI() {}

// @Summary  textBatchDelete
// @Tags      segments
// @Router    /segments:bulkDelete [post]
func _swagBulkDelete() {}

// @Summary  textSearch
// @Tags      segments
// @Router    /segments:hybrid [post]
func _swagHybridSearchSegments() {}

// @Summary  text
// @Tags      segments
// @Router    /segments:scroll [post]
func _swagScrollSegments() {}

// --- table ---
// @Summary  text
// @Tags      table
// @Router    /datasets/{dataset}/documents/{document}/table/meta [get]
func _swagGetMeta() {}

// @Summary  text
// @Tags      table
// @Router    /table:findMeta [post]
func _swagFindMeta() {}

// @Summary  text
// @Tags      table
// @Router    /table:query [post]
func _swagQueryTable() {}

// --- members ---
// @Summary  Dataset member list
// @Tags      members
// @Router    /datasets/{dataset}/members [get]
func _swagListDatasetMembers() {}

// @Summary  GetMember
// @Tags      members
// @Router    /datasets/{dataset}/members/{member} [get]
func _swagGetDatasetMember() {}

// @Summary  DeleteMember
// @Tags      members
// @Router    /datasets/{dataset}/members/{member} [delete]
func _swagDeleteDatasetMember() {}

// @Summary  UpdateMember
// @Tags      members
// @Router    /datasets/{dataset}/members/{member} [patch]
func _swagUpdateDatasetMember() {}

// @Summary  DeleteGroupMember
// @Tags      members
// @Router    /datasets/{dataset}/members/groups/{group_id} [delete]
func _swagDeleteDatasetGroupMember() {}

// @Summary  UpdateGroupMember
// @Tags      members
// @Router    /datasets/{dataset}/members/groups/{group_id} [patch]
func _swagUpdateDatasetGroupMember() {}

// @Summary  SearchMember
// @Tags      members
// @Router    /datasets/{dataset}/members:search [post]
func _swagSearchDatasetMember() {}

// @Summary  BatchtextMember
// @Tags      members
// @Router    /datasets/{dataset}:batchAddMember [post]
func _swagBatchAddDatasetMember() {}

// --- tasks ---
// @Summary  Task list
// @Tags      tasks
// @Router    /datasets/{dataset}/tasks [get]
func _swagListTasks() {}

// @Summary  Create task
// @Tags      tasks
// @Router    /datasets/{dataset}/tasks [post]
func _swagCreateTask() {}

// @Summary  Get task
// @Tags      tasks
// @Router    /datasets/{dataset}/tasks/{task} [get]
func _swagGetTask() {}

// @Summary  Delete task
// @Tags      tasks
// @Router    /datasets/{dataset}/tasks/{task} [delete]
func _swagDeleteTask() {}

// @Summary  UnsetTask
// @Tags      tasks
// @Router    /datasets/{dataset}/tasks/{task}:cancel [post]
func _swagCancelTask() {}

// @Summary  Suspend task
// @Tags      tasks
// @Router    /datasets/{dataset}/tasks/{task}:suspend [post]
func _swagSuspendTask() {}

// @Summary  Resume task
// @Tags      tasks
// @Router    /datasets/{dataset}/tasks/{task}:resume [post]
func _swagResumeTask() {}

// @Summary  Tasktext
// @Tags      tasks
// @Router    /datasets/{dataset}/tasks/{task}:callback [post]
func _swagTaskCallback() {}

// --- file ---
// @Summary  Upload filetextKnowledge base
// @Tags      file
// @Router    /upload_files [post]
func _swagUploadFiles() {}

// @Summary  UploadtextKnowledge basetext
// @Tags      file
// @Router    /add_files_to_group [post]
func _swagAddFilesToGroup() {}

// @Summary  Knowledge basetext
// @Tags      file
// @Router    /list_files [get]
func _swagListFiles() {}

// @Summary  text
// @Tags      file
// @Router    /list_files_in_group [get]
func _swagListFilesInGroup() {}

// @Summary  Knowledge basetext
// @Tags      file
// @Router    /list_kb_groups [get]
func _swagListKBGroups() {}

// --- chat ---
// @Summary  text（Knowledge base）
// @Tags      chat
// @Router    /chat [post]
func _swagChat() {}

// --- conversations ---
// @Summary  Conversationtext
// @Tags      conversations
// @Router    /conversations:chat [post]
func _swagConversationsChat() {}

// @Summary  Resume conversation stream
// @Tags      conversations
// @Router    /conversations:resumeChat [post]
func _swagConversationsResumeChat() {}

// @Summary  Stop conversation generation
// @Tags      conversations
// @Router    /conversations:stopChatGeneration [post]
func _swagConversationsStopChatGeneration() {}

// @Summary  Get conversation status
// @Tags      conversations
// @Router    /conversations/{conversation_id}:status [get]
func _swagGetChatStatus() {}

// @Summary  Get conversation
// @Tags      conversations
// @Router    /conversations/{name} [get]
func _swagGetConversation() {}

// @Summary  Get conversation metadata
// @Tags      conversations
// @Router    /conversations/{name}:detail [get]
func _swagGetConversationDetail() {}

// @Summary  List conversation history (paginated)
// @Tags      conversations
// @Router    /conversations/{name}:history [get]
func _swagGetConversationHistory() {}

// @Summary  Delete conversation
// @Tags      conversations
// @Router    /conversations/{name} [delete]
func _swagDeleteConversation() {}

// @Summary  Batch delete conversations
// @Tags      conversations
// @Router    /conversations:batchDelete [post]
func _swagBatchDeleteConversations() {}

// @Summary  Conversation list
// @Tags      conversations
// @Router    /conversations [get]
func _swagListConversations() {}

// @Summary  Set conversation history
// @Tags      conversations
// @Router    /conversations:setChatHistory [post]
func _swagSetChatHistory() {}

// @Summary  Feedback conversation history
// @Tags      conversations
// @Router    /conversations:feedBackChatHistory [post]
func _swagFeedBackChatHistory() {}

// @Summary  Get multi-answer switch status
// @Tags      conversations
// @Router    /conversation:switchStatus [get]
func _swagGetMultiAnswersSwitchStatus() {}

// @Summary  SetMulti-answer switch status
// @Tags      conversations
// @Router    /conversation:switchStatus [post]
func _swagSetMultiAnswersSwitchStatus() {}

// --- prompts ---
// @Summary  Create prompt
// @Tags      prompts
// @Router    /prompts [post]
func _swagCreatePrompt() {}

// @Summary  Update prompt
// @Tags      prompts
// @Router    /prompts/{name} [patch]
func _swagUpdatePrompt() {}

// @Summary  Delete prompt
// @Tags      prompts
// @Router    /prompts/{name} [delete]
func _swagDeletePrompt() {}

// @Summary  Get prompt
// @Tags      prompts
// @Router    /prompts/{name} [get]
func _swagGetPrompt() {}

// @Summary  Prompt list
// @Tags      prompts
// @Router    /prompts [get]
func _swagListPrompts() {}

// --- rag databases ---
// @Summary  RAG text
// @Tags      rag
// @Router    /rag/database/tags [get]
func _swagGetUserDatabaseTags() {}

// @Summary  Usertext
// @Tags      rag
// @Router    /rag/databases [post]
func _swagGetUserDatabases() {}

// @Summary  Createtext
// @Tags      rag
// @Router    /rag/databases/create [post]
func _swagCreateDatabase() {}

// @Summary  text
// @Tags      rag
// @Router    /rag/databases/summary [get]
func _swagGetUserDatabaseSummaries() {}

// @Summary  text
// @Tags      rag
// @Router    /rag/databases/validate-connection [post]
func _swagValidateConnection() {}

// @Summary  Deletetext
// @Tags      rag
// @Router    /rag/databases/{database_id} [delete]
func _swagDeleteDatabase() {}

// @Summary  text
// @Tags      rag
// @Router    /rag/databases/{database_id}/tables [post]
func _swagGetDatabaseTables() {}

// @Summary  Updatetext
// @Tags      rag
// @Router    /rag/databases/{database_id}/tables/{table_id}/cell [post]
func _swagUpdateTableCell() {}

// @Summary  textPreview
// @Tags      rag
// @Router    /rag/databases/{database_id}/tables/{table_id}/preview [post]
func _swagListTableRows() {}

// @Summary  Updatetext
// @Tags      rag
// @Router    /rag/databases/{database_id}/update [post]
func _swagUpdateDatabase() {}

// --- inner ---
// @Summary  textGet dataset
// @Tags      inner
// @Router    /inner/datasets/{dataset}:internal [get]
func _swagGetDatasetInternal() {}

// @Summary  text
// @Tags      inner
// @Router    /inner/rag:knowledgeRetrieve [post]
func _swagKnowledgeRetrieve() {}

// --- writer segment job ---
// @Summary  text WriterSegmentJob
// @Tags      job
// @Router    /writerSegmentJob:submit [post]
func _swagSubmit() {}

// @Summary  Get WriterSegmentJob
// @Tags      job
// @Router    /writerSegmentJobs/{writerSegmentJob} [get]
func _swagGetWriterSegmentJob() {}

// --- kb acl ---
// @Summary  Knowledge base list
// @Tags      kb
// @Router    /kb/list [get]
func _swagListKB() {}

// @Summary  PermissionBatchtext
// @Tags      kb
// @Router    /kb/permission/batch [post]
func _swagPermissionBatch() {}

// @Summary  Knowledge basePermission
// @Tags      kb
// @Router    /kb/{kb_id}/permission [get]
func _swagGetPermission() {}

// @Summary  Permissiontext
// @Tags      kb
// @Router    /kb/{kb_id}/can [get]
func _swagCanHandler() {}

// @Summary  ACL list
// @Tags      kb
// @Router    /kb/{kb_id}/acl [get]
func _swagListACL() {}

// @Summary  Add ACL
// @Tags      kb
// @Router    /kb/{kb_id}/acl [post]
func _swagAddACL() {}

// @Summary  BatchAdd ACL
// @Tags      kb
// @Router    /kb/{kb_id}/acl/batch [post]
func _swagBatchAddACL() {}

// @Summary  Update ACL
// @Tags      kb
// @Router    /kb/{kb_id}/acl/{acl_id} [put]
func _swagUpdateACL() {}

// @Summary  Delete ACL
// @Tags      kb
// @Router    /kb/{kb_id}/acl/{acl_id} [delete]
func _swagDeleteACL() {}
