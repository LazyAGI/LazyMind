package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lazymind/core/acl"
	"lazymind/core/agent"
	"lazymind/core/agentinvocation"
	"lazymind/core/chat"
	"lazymind/core/currentmemory"
	"lazymind/core/datasource"
	"lazymind/core/doc"
	"lazymind/core/episode"
	"lazymind/core/evalset"
	"lazymind/core/evolution"
	"lazymind/core/exporter"
	"lazymind/core/file"
	"lazymind/core/knowledge_market"
	"lazymind/core/mcp"
	"lazymind/core/modelprovider"
	"lazymind/core/remotefs"
	"lazymind/core/resourceupdate"
	"lazymind/core/scheduler"
	"lazymind/core/showcase"
	skillv2handler "lazymind/core/skillv2/handler"
	corestore "lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/systemdeps"
	"lazymind/core/taskcenter"
	"lazymind/core/userprefs"
	"lazymind/core/wordgroup"
	"lazymind/core/workflow"
	workflowattempt "lazymind/core/workflow/attempt"
	workflowexecutor "lazymind/core/workflow/executor"
	workflowfacade "lazymind/core/workflow/facade"
	workflowhosted "lazymind/core/workflow/hosted"
	workflowstore "lazymind/core/workflow/store"
	workflowstream "lazymind/core/workflow/stream"

	"github.com/gorilla/mux"
)

type routeCapture struct {
	header http.Header
	body   bytes.Buffer
	status int
}

type routeProjectionError string

func (e routeProjectionError) Error() string { return string(e) }

func (c *routeCapture) Header() http.Header    { return c.header }
func (c *routeCapture) WriteHeader(status int) { c.status = status }
func (c *routeCapture) Write(body []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.body.Write(body)
}

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
	invocationHandler := agentinvocation.Handler{Service: agentinvocation.New(corestore.DB())}
	handleAPI(r, "POST", "/agent-invocations/{invocation_id}:start", []string{"qa.write"}, invocationHandler.Start)
	handleAPI(r, "POST", "/agent-invocations/{invocation_id}:finish", []string{"qa.write"}, invocationHandler.Finish)
	handleAPI(r, "GET", "/agent-invocations", []string{"qa.read"}, invocationHandler.List)

	attemptHandler := workflowattempt.Handler{Service: workflowattempt.New(corestore.DB(), workflowattempt.Config{})}
	remoteExecutorHandler := workflowexecutor.RemoteHandler{
		DB: corestore.DB(), Attempts: attemptHandler.Service,
		Contexts:  workflowexecutor.DBContextLoader{DB: corestore.DB()},
		Artifacts: workflowexecutor.DBArtifactSink{DB: corestore.DB()},
	}
	handleAPI(r, "POST", "/internal/workflow-attempts:claim", nil, attemptHandler.Claim)
	handleAPI(r, "GET", "/internal/workflow-attempts/{attempt_id}/context", nil, remoteExecutorHandler.Context)
	handleAPI(r, "GET", "/internal/workflow-attempts/{attempt_id}/inputs/{material_id}", nil, remoteExecutorHandler.Input)
	handleAPI(r, "POST", "/internal/workflow-attempts/{attempt_id}/artifact-files", nil, remoteExecutorHandler.UploadArtifactFile)
	handleAPI(r, "POST", "/internal/workflow-attempts/{attempt_id}/artifacts", nil, remoteExecutorHandler.SaveArtifact)
	handleAPI(r, "POST", "/internal/workflow-attempts/{attempt_id}:heartbeat", nil, attemptHandler.Heartbeat)
	handleAPI(r, "POST", "/internal/workflow-attempts/{attempt_id}:progress", nil, attemptHandler.Progress)
	handleAPI(r, "POST", "/internal/workflow-attempts/{attempt_id}:complete", nil, remoteExecutorHandler.Complete)
	handleAPI(r, "POST", "/internal/workflow-attempts/{attempt_id}:fail", nil, attemptHandler.Fail)
	handleAPI(r, "POST", "/internal/workflow-attempts/{attempt_id}:cancel", nil, attemptHandler.Cancel)
	workflowRepository := workflowstore.New(corestore.DB())
	workflowFacade := workflowfacade.Handler{
		Store:      workflowRepository,
		Hosts:      workflowexecutor.DefaultHostRegistry,
		Projection: http.HandlerFunc(workflow.GetSessionProjection),
	}
	hostedService := &workflowhosted.Service{
		DB: corestore.DB(), Store: workflowRepository,
		Attempts:  workflowattempt.New(corestore.DB(), workflowattempt.Config{LeaseDuration: 30 * time.Minute}),
		Contexts:  workflowexecutor.DBContextLoader{DB: corestore.DB()},
		Artifacts: workflowexecutor.DBArtifactSink{DB: corestore.DB()},
	}
	hostedHandler := workflowhosted.Handler{Service: hostedService}

	// ----- Datasettext -----
	handleAPI(r, "GET", "/dataset/algos", []string{"document.read"}, doc.ListAlgos)
	handleAPI(r, "GET", "/dataset/tags", []string{"document.read"}, doc.AllDatasetTags)
	handleAPI(r, "GET", "/datasets", []string{"document.read"}, doc.ListDatasets)
	handleAPI(r, "POST", "/internal/datasets/usage:batch", nil, doc.InternalBatchDatasetUsage)
	handleAPI(r, "POST", "/datasets", []string{"document.write"}, doc.CreateDataset)
	handleAPI(r, "GET", "/datasets/{dataset}", []string{"document.read"}, doc.GetDataset)
	handleAPI(r, "DELETE", "/datasets/{dataset}", []string{"document.write"}, doc.DeleteDataset)
	handleAPI(r, "PATCH", "/datasets/{dataset}", []string{"document.write"}, doc.UpdateDataset)
	handleAPI(r, "POST", "/datasets/{dataset}:setDefault", []string{"document.write"}, doc.SetDefault)
	handleAPI(r, "POST", "/datasets/{dataset}:unsetDefault", []string{"document.write"}, doc.UnsetDefault)
	handleAPI(r, "GET", "/data-sources/local-fs-chat-setting", []string{"document.read"}, datasource.GetLocalFSChatSetting)
	handleAPI(r, "PUT", "/data-sources/local-fs-chat-setting", []string{"document.write"}, datasource.SetLocalFSChatSetting)
	handleAPI(r, "GET", "/system-dependencies/ffmpeg", []string{"document.read"}, systemdeps.GetFFmpegDependency)
	handleAPI(r, "PUT", "/system-dependencies/ffmpeg", []string{"document.write"}, systemdeps.UpdateFFmpegDependency)
	handleAPI(r, "POST", "/system-dependencies/ffmpeg:check", []string{"document.read"}, systemdeps.CheckFFmpegDependency)
	handleAPI(r, "POST", "/system-dependencies/ffmpeg:install", []string{"document.write"}, systemdeps.InstallFFmpegDependency)
	handleAPI(r, "GET", "/system-dependencies/editable-ppt", []string{"document.read"}, systemdeps.GetEditablePPTDependency)
	handleAPI(r, "POST", "/system-dependencies/editable-ppt:check", []string{"document.read"}, systemdeps.CheckEditablePPTDependency)
	handleAPI(r, "POST", "/system-dependencies/editable-ppt:install", []string{"document.write"}, systemdeps.InstallEditablePPTDependency)
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
	handleAPI(r, "POST", "/datasets/{dataset}/uploads:checkHashes", []string{"document.write"}, doc.CheckFileHashes)
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
	handleAPI(r, "PATCH", "/mcp_servers:enabled", []string{"qa.write"}, mcp.BulkUpdateEnabled)
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
	handleAPI(r, "GET", "/agent/router/status", []string{"user.admin"}, agent.GetRouterStatus)
	handleAPI(r, "GET", "/agent/router/algorithms", []string{"user.admin"}, agent.ListRouterAlgorithms)
	handleAPI(r, "POST", "/agent/router/algorithms/{algorithm_id}/action", []string{"user.admin"}, agent.PostRouterAlgorithmAction)
	handleAPI(r, "DELETE", "/agent/router/algorithms/{algorithm_id}", []string{"user.admin"}, agent.DeleteRouterAlgorithm)
	handleAPI(r, "GET", "/agent/router/ab-strategy", []string{"user.admin"}, agent.GetRouterABStrategy)
	handleAPI(r, "PUT", "/agent/router/ab-strategy", []string{"user.admin"}, agent.PutRouterABStrategy)
	handleAPI(r, "GET", "/agent/router/traffic-stats", []string{"user.admin"}, agent.GetRouterTrafficStats)

	// ----- Conversation -----
	handleAPI(r, "POST", "/conversations:chat", []string{"qa.write"}, chat.ChatConversations)
	handleAPI(r, "POST", "/conversations:estimateContextUsage", []string{"qa.read"}, chat.EstimateContextUsage)
	handleAPI(r, "POST", "/conversations:exportContextPrompt", []string{"qa.read"}, chat.ExportContextPrompt)
	handleAPI(r, "POST", "/conversations:resumeChat", []string{"qa.write"}, chat.ResumeChat)
	handleAPI(r, "POST", "/conversations:stopChatGeneration", []string{"qa.write"}, chat.StopChatGeneration)
	handleAPI(r, "POST", "/conversations/{conversation_id}:stop", []string{"qa.write"}, chat.StopChatGeneration)
	handleAPI(r, "POST", "/conversations/{conversation_id}:toolLimitDecision", []string{"qa.write"}, chat.DecideToolLimit)
	handleAPI(r, "GET", "/conversations/{conversation_id}:status", []string{"qa.read"}, chat.GetChatStatus)
	handleAPI(r, "GET", "/chat/models", []string{"qa.read"}, chat.ListChatModels)
	handleAPI(r, "PATCH", "/conversations/{conversation_id}/model", []string{"qa.write"}, chat.PatchConversationModel)
	handleAPI(r, "POST", "/conversations/{parent_id}/sidechat", []string{"qa.write"}, chat.CreateSidechat)
	handleAPI(r, "POST", "/conversations/{child_id}/retain", []string{"qa.write"}, chat.RetainSidechat)
	handleAPI(r, "DELETE", "/conversations/{child_id}/sidechat", []string{"qa.write"}, chat.DiscardSidechat)
	handleAPI(r, "POST", "/conversations/{conversation_id}:promote", []string{"qa.write"}, chat.PromoteConversation)
	handleAPI(r, "POST", "/conversations/{conversation_id}:pin", []string{"qa.write"}, chat.PinConversation)
	handleAPI(r, "POST", "/conversations/{conversation_id}:unpin", []string{"qa.write"}, chat.UnpinConversation)
	handleAPI(r, "GET", "/chat/executors", []string{"qa.read"}, chat.ListChatExecutors)
	handleAPI(r, "GET", "/external-chat/hosts/{provider}/status", []string{"qa.read"}, chat.ExternalChatHostStatus)
	handleAPI(r, "GET", "/external-chat/providers/{provider}/sessions", []string{"qa.read"}, chat.ListExternalAgentSessions)
	handleAPI(r, "POST", "/external-chat/providers/{provider}/sessions/{thread_id}/binding", []string{"qa.write"}, chat.BindExternalAgentSession)
	handleAPI(r, "POST", "/external-chat/providers/{provider}/sessions:sync", []string{"qa.write"}, chat.SyncExternalAgentSessions)
	handleAPI(r, "GET", "/external-chat/runs", []string{"qa.read"}, chat.ListExternalChatRuns)
	handleAPI(r, "POST", "/external-chat/hosts/{provider}/claim", []string{"qa.write"}, chat.ClaimExternalChatRun)
	handleAPI(r, "POST", "/external-chat/runs/{run_id}:heartbeat", []string{"qa.write"}, chat.HeartbeatExternalChatRun)
	handleAPI(r, "POST", "/external-chat/runs/{run_id}:event", []string{"qa.write"}, chat.PublishExternalChatEvent)
	handleAPI(r, "POST", "/external-chat/runs/{run_id}:attachment", []string{"qa.write"}, chat.PublishExternalChatAttachment)

	// ----- SubAgent (Task Center) -----
	handleAPI(r, "GET", "/conversations/{conversation_id}/tasks", []string{"qa.read"}, subagent.ListConversationTasks)
	handleAPI(r, "GET", "/conversations/{conversation_id}/artifacts", []string{"qa.read"}, chat.ListConversationArtifacts)
	handleAPI(r, "GET", "/conversations/{conversation_id}/events", []string{"qa.read"}, chat.StreamConvEvents)
	handleAPI(r, "GET", "/tasks/{task_id}:stream", []string{"qa.read"}, subagent.StreamTask)
	handleAPI(r, "GET", "/tasks/{task_id}/artifacts", []string{"qa.read"}, subagent.GetTaskArtifacts)
	handleAPI(r, "GET", "/tasks/{task_id}", []string{"qa.read"}, subagent.GetTaskDetail)
	// Internal endpoint for algorithm service auto polling; no request-level RBAC.
	handleAPI(r, "GET", "/internal/subagent/tasks/{task_id}", nil, subagent.InternalGetTaskStatus)
	handleAPI(r, "GET", "/internal/subagent/tasks/{task_id}/events", nil, subagent.InternalGetTaskEvents)
	handleAPI(r, "GET", "/internal/subagent/tasks/{task_id}/execution-spec", nil, subagent.InternalGetExecutionSpec)
	handleAPI(r, "POST", "/internal/subagent/tasks/{task_id}/events", nil, subagent.InternalIngestTaskEvent)

	// ----- Workflow Info -----
	// Keep these catalog endpoints on the legacy response shape consumed by the
	// Workflow management UI. The versioned facade remains in use for runtime
	// preparation, commands, inputs, and artifacts below.
	handleAPI(r, "GET", "/workflows", []string{"qa.read"}, workflow.ListWorkflows)
	handleAPI(r, "GET", "/workflows/{workflow_id}", []string{"qa.read"}, func(w http.ResponseWriter, req *http.Request) {
		workflow.GetWorkflowInfo(w, req)
	})
	// Export providers are independent from workflow installation and slot names.
	handleAPI(r, "GET", "/exporters/{provider_id}:capabilities", []string{"qa.read"}, exporter.Capabilities)
	handleAPI(r, "POST", "/exporters/{provider_id}:export", []string{"qa.write"}, exporter.Export)
	// ----- Workflow Drafts (user-created workflow authoring) -----
	handleAPI(r, "GET", "/workflow-drafts", []string{"qa.read"}, workflow.ListWorkflowDrafts)
	handleAPI(r, "POST", "/workflow-drafts", []string{"qa.write"}, workflow.CreateWorkflowDraft)
	handleAPI(r, "GET", "/workflow-drafts:trash", []string{"qa.read"}, workflow.ListWorkflowDraftTrash)
	handleAPI(r, "DELETE", "/workflow-drafts:trash", []string{"qa.write"}, workflow.EmptyWorkflowDraftTrash)
	handleAPI(r, "POST", "/workflow-drafts:polish-info", []string{"qa.write"}, workflow.PolishWorkflowDraftInfo)
	handleAPI(r, "GET", "/workflow-drafts/{draft_id}", []string{"qa.read"}, workflow.GetWorkflowDraft)
	handleAPI(r, "POST", "/workflow-drafts/{draft_id}:save", []string{"qa.write"}, workflow.SaveWorkflowDraft)
	handleAPI(r, "POST", "/workflow-drafts/{draft_id}:validate", []string{"qa.read"}, workflow.ValidateWorkflowDraft)
	handleAPI(r, "POST", "/workflow-drafts/{draft_id}:ai-generate", []string{"qa.write"}, workflow.AIGenerateWorkflowDraft)
	handleAPI(r, "POST", "/workflow-drafts/{draft_id}:ai-repair", []string{"qa.write"}, workflow.AIRepairWorkflowDraft)
	handleAPI(r, "GET", "/workflow-drafts/{draft_id}/generation-analysis", []string{"qa.read"}, workflow.GetWorkflowGenerationAnalysis)
	handleAPI(r, "POST", "/workflow-drafts/{draft_id}:confirm-workflow", []string{"qa.write"}, workflow.ConfirmWorkflowWorkflow)
	handleAPI(r, "GET", "/workflow-drafts/{draft_id}/repair-runs/{repair_id}", []string{"qa.read"}, workflow.GetWorkflowRepairRun)
	handleAPI(r, "POST", "/workflow-drafts/{draft_id}:repair-preview", []string{"qa.read"}, workflow.PreviewWorkflowRepair)
	handleAPI(r, "POST", "/workflow-drafts/{draft_id}:publish", []string{"qa.write"}, workflow.PublishWorkflowDraft)
	handleAPI(r, "POST", "/workflow-drafts/{draft_id}:restore", []string{"qa.write"}, workflow.RestoreWorkflowDraft)
	handleAPI(r, "DELETE", "/workflow-drafts/{draft_id}:purge", []string{"qa.write"}, workflow.PurgeWorkflowDraft)
	handleAPI(r, "DELETE", "/workflow-drafts/{draft_id}", []string{"qa.write"}, workflow.DeleteWorkflowDraft)
	handleAPI(r, "GET", "/chat/settings/workflows", []string{"qa.read"}, workflow.ListUserWorkflowSettings)
	handleAPI(r, "PATCH", "/chat/settings/workflows/{workflow_ref:.+}", []string{"qa.write"}, workflow.PatchUserWorkflowSetting)
	handleAPI(r, "POST", "/published-workflows/{workflow_ref:.+}:rollback", []string{"qa.write"}, workflow.RollbackWorkflow)
	handleAPI(r, "POST", "/published-workflows/{workflow_ref:.+}:archive", []string{"qa.write"}, workflow.ArchiveWorkflow)
	handleAPI(r, "POST", "/published-workflows/{workflow_ref:.+}:restore", []string{"qa.write"}, workflow.RestoreWorkflow)
	handleAPI(r, "GET", "/published-workflows/{workflow_ref:.+}/versions", []string{"qa.read"}, workflow.ListWorkflowVersions)
	handleAPI(r, "GET", "/published-workflows/{workflow_ref:.+}/versions/{revision_id}", []string{"qa.read"}, workflow.GetWorkflowVersion)
	handleAPI(r, "POST", "/published-workflows/{workflow_ref:.+}/versions/{revision_id}:edit", []string{"qa.write"}, workflow.ReplaceDraftFromWorkflowVersion)

	// ----- Task Center -----
	handleAPI(r, "GET", "/task-center/tasks", []string{"qa.read"}, taskcenter.ListTasks)
	handleAPI(r, "GET", "/task-center/tasks/{task_id}", []string{"qa.read"}, taskcenter.GetTaskByID)
	handleAPI(r, "POST", "/task-center/tasks/{task_id}:cancel", []string{"qa.write"}, taskcenter.CancelTaskByID)
	handleAPI(r, "POST", "/task-center/tasks/{task_id}:remove", []string{"qa.write"}, taskcenter.RemoveTaskHandler)
	handleAPI(r, "GET", "/task-center/schedules/{schedule_id}/tasks", []string{"qa.read"}, taskcenter.ListScheduleTasks)

	// ----- Schedules -----
	handleAPI(r, "GET", "/schedules", []string{"qa.read"}, scheduler.ListSchedulesHandler)
	handleAPI(r, "POST", "/schedules", []string{"qa.write"}, scheduler.CreateScheduleHandler)
	handleAPI(r, "PUT", "/schedules/{schedule_id}", []string{"qa.write"}, scheduler.UpdateScheduleHandler)
	handleAPI(r, "DELETE", "/schedules/{schedule_id}", []string{"qa.write"}, scheduler.DeleteScheduleHandler)
	handleAPI(r, "POST", "/schedules/{schedule_id}:cancel", []string{"qa.write"}, scheduler.CancelScheduleHandler)
	handleAPI(r, "POST", "/schedules/{schedule_id}:enable", []string{"qa.write"}, scheduler.EnableScheduleHandler)
	handleAPI(r, "POST", "/schedules/{schedule_id}:run-now", []string{"qa.write"}, scheduler.RunNowHandler)
	handleAPI(r, "POST", "/schedules/{schedule_id}:move", []string{"qa.write"}, scheduler.MoveScheduleHandler)
	handleAPI(r, "GET", "/automation-groups", []string{"qa.read"}, scheduler.ListGroupsHandler)
	handleAPI(r, "POST", "/automation-groups", []string{"qa.write"}, scheduler.CreateGroupHandler)
	handleAPI(r, "DELETE", "/automation-groups/{group_id}", []string{"qa.write"}, scheduler.DeleteGroupHandler)
	handleAPI(r, "POST", "/automation-groups:batch-create", []string{"qa.write"}, scheduler.BatchCreateHandler)

	// ----- User Chat Settings (quick-question/new-task defaults) -----
	handleAPI(r, "GET", "/user/chat-settings", []string{"qa.read"}, chat.GetChatSettings)
	handleAPI(r, "PATCH", "/user/chat-settings", []string{"qa.write"}, chat.PatchChatSettings)
	// Legal consent is a login prerequisite and must not depend on optional QA permissions.
	// The handlers still require the gateway-injected X-User-Id identity.
	handleAPI(r, "GET", "/user/ui-preferences", []string{}, userprefs.GetUIPreferences)
	handleAPI(r, "PATCH", "/user/ui-preferences", []string{}, userprefs.PatchUIPreferences)
	handleAPI(r, "GET", "/settings/overview", []string{}, userprefs.GetSettingsOverview)
	handleAPI(r, "POST", "/settings/checks", []string{}, userprefs.RunSettingsChecks)
	handleAPI(r, "PATCH", "/conversations/{conversation_id}/settings", []string{"qa.write"}, chat.PatchConversationSettings)
	// Compatibility for clients installed before conversation settings included
	// the Chat executor. New clients use /settings.
	handleAPI(r, "PATCH", "/conversations/{conversation_id}/workflow-settings", []string{"qa.write"}, chat.PatchConversationSettings)

	// ----- Workflow Sessions -----
	// Public Runtime package endpoints are intentionally separate from the
	// legacy /workflows management-UI response shape. Runtime callers require a
	// pinned revision, hashes, compiled graph, and package files.
	handleAPI(r, "GET", "/workflow-runtime/v1/workflows", []string{"qa.read"}, workflowFacade.ListWorkflows)
	handleAPI(r, "GET", "/workflow-runtime/v1/workflows/{workflow_id}", []string{"qa.read"}, workflowFacade.GetWorkflow)
	handleAPI(r, "GET", "/workflow-authoring/v1/skill-context", []string{"qa.read"}, workflow.GetSkillConversionContext)
	handleAPI(r, "POST", "/workflow-authoring/v1/drafts", []string{"qa.write"}, workflow.CreateAuthoringWorkflowDraft)
	handleAPI(r, "PUT", "/workflow-authoring/v1/drafts/{draft_id}/files", []string{"qa.write"}, workflow.UpdateAuthoringWorkflowDraftFile)
	handleAPI(r, "GET", "/workflow-authoring/v1/drafts/{draft_id}/diagnostics", []string{"qa.read"}, workflow.GetAuthoringWorkflowDiagnostics)
	handleAPI(r, "POST", "/workflow-authoring/v1/drafts/{draft_id}:publish", []string{"qa.write"}, workflow.PublishAuthoringWorkflow)
	handleAPI(r, "GET", "/workflow-authoring/v1/fixture", []string{"qa.read"}, workflow.GenerateAuthoringFixture)
	handleAPI(r, "POST", "/workflow-input-resources", []string{"qa.write"}, workflowFacade.ImportInputResource)
	handleAPI(r, "GET", "/workflow-input-resources/{resource_id}", []string{"qa.read"}, workflowFacade.ReadInputResource)
	handleAPI(r, "POST", "/workflow-preparations", []string{"qa.write"}, workflowFacade.Prepare)
	handleAPI(r, "POST", "/workflow-preparations/{preparation_id}:consume", []string{"qa.write"}, workflowFacade.Consume)
	handleAPI(r, "GET", "/workflow-sessions", []string{"qa.read"}, workflowFacade.ListSessions)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}:advance-step", []string{"qa.write"}, workflowFacade.Command(http.HandlerFunc(workflow.TransitionWorkflowSession)))
	handleAPI(r, "POST", "/workflow-sessions/{session_id}:advance-step-and-hand-off", []string{"qa.write"}, workflowFacade.Command(http.HandlerFunc(workflow.TransitionWorkflowSession)))
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/hosted-attempts/{attempt_id}:begin", []string{"qa.write"}, hostedHandler.Begin)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/hosted-attempts/{attempt_id}:resume", []string{"qa.write"}, hostedHandler.Resume)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/hosted-attempts/{attempt_id}:submit", []string{"qa.write"}, hostedHandler.Submit)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/input-bindings", []string{"qa.write"}, workflowFacade.BindInput)
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/input-bindings", []string{"qa.read"}, workflowFacade.ListInputs)
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/artifacts", []string{"qa.read"}, workflowFacade.ListArtifacts)
	handleAPI(r, "GET", "/writer-download-conversions/{source_hash}/{target_format}", []string{"qa.read"}, workflow.GetWriterDownloadConversion)
	handleAPI(r, "PUT", "/writer-download-conversions/{source_hash}/{target_format}", []string{"qa.write"}, workflow.PutWriterDownloadConversion)
	handleAPI(r, "POST", "/writer-download-conversions:convert", []string{"qa.write"}, workflow.ConvertWriterDownload)
	handleAPI(r, "GET", "/workflow-artifacts/{artifact_id}", []string{"qa.read"}, workflowFacade.ReadArtifact)
	handleAPI(r, "PATCH", "/workflow-artifacts/{artifact_id}", []string{"qa.write"}, workflowFacade.PatchArtifact)
	handleAPI(r, "DELETE", "/workflow-artifacts/{artifact_id}", []string{"qa.write"}, workflowFacade.DeleteArtifact)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}:stop", []string{"qa.write"}, workflowFacade.StopWorkflow)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}:resume", []string{"qa.write"}, workflowFacade.ResumeWorkflow)
	handleAPI(r, "GET", "/workflow-commands/{command_id}", []string{"qa.read"}, workflowFacade.GetCommand)
	workflowEvents := workflowstream.Handler{Store: workflowRepository, Snapshot: func(req *http.Request, sessionID, owner string) (any, error) {
		if err := workflowRepository.AuthorizeSession(req.Context(), sessionID, owner); err != nil {
			return nil, err
		}
		recorder := &routeCapture{header: http.Header{}}
		projectionRequest := mux.SetURLVars(req.Clone(req.Context()), map[string]string{"session_id": sessionID})
		workflow.GetSessionProjection(recorder, projectionRequest)
		if recorder.status >= http.StatusBadRequest {
			return nil, routeProjectionError(fmt.Sprintf("projection status %d: %s", recorder.status, recorder.body.String()))
		}
		var projection any
		if err := json.Unmarshal(recorder.body.Bytes(), &projection); err != nil {
			return nil, err
		}
		return projection, nil
	}}
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/events", []string{"qa.read"}, workflowEvents.ServeHTTP)
	handleAPI(r, "GET", "/conversations/{conversation_id}/workflow-sessions", []string{"qa.read"}, workflow.ListConversationSessions)
	handleAPI(r, "GET", "/conversations/{conversation_id}/workflow-sessions:active", []string{"qa.read"}, workflow.GetActiveConversationSession)
	handleAPI(r, "GET", "/conversations/{conversation_id}/workflow-sessions:latest", []string{"qa.read"}, workflow.GetLatestConversationSession)
	handleAPI(r, "GET", "/workflow-sessions/{session_id}", []string{"qa.read"}, workflow.GetSessionDetail)
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/slots", []string{"qa.read"}, workflow.GetSessionSlots)
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/steps", []string{"qa.read"}, workflow.GetSessionSteps)
	// Compatibility alias: old clients receive the same authoritative projection;
	// no independent BFS state calculation remains on an active route.
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/state-graph", []string{"qa.read"}, workflow.GetSessionProjection)
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/projection", []string{"qa.read"}, workflowFacade.GetProjection)
	handleAPI(r, "GET", "/internal/workflow-sessions/{session_id}/projection", nil, workflow.GetSessionProjection)
	handleAPI(r, "POST", "/internal/workflow-sessions:plan-start", nil, workflow.PlanWorkflowSessionStart)
	handleAPI(r, "POST", "/internal/workflow-sessions:start", nil, workflow.StartWorkflowSession)
	handleAPI(r, "POST", "/internal/workflow-sessions/{session_id}:transition", nil, workflow.TransitionWorkflowSession)
	handleAPI(r, "GET", "/internal/workflow-transition-commands/{command_id}", nil, workflow.GetTransitionCommand)
	handleAPI(r, "PATCH", "/workflow-sessions/{session_id}/slots/{slot_id}", []string{"qa.write"}, workflow.PatchSessionSlot)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}:action-preview", []string{"qa.write"}, workflow.PreviewArtifactAction)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}:action-execute", []string{"qa.write"}, workflow.ExecuteArtifactAction)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}:sync-search-config", []string{"qa.write"}, workflow.SyncSessionSearchConfig)
	// Phase 3: slot item management.
	// Stable list_index-based routes (preferred).
	handleAPI(r, "DELETE", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}", []string{"qa.write"}, workflow.DeleteSlotItemByIndex)
	handleAPI(r, "PATCH", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}", []string{"qa.write"}, workflow.PatchSlotItemByIndex)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}:sync-writer-document", []string{"qa.write"}, chat.SyncWriterDocument)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/writer-document:write-back", []string{"qa.write"}, chat.WriteBackWriterDocument)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/writer-document:render", []string{"qa.read"}, chat.RenderWriterDocument)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/writer-document:save", []string{"qa.write"}, chat.SaveWriterDocument)
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/versions", []string{"qa.read"}, workflow.GetSlotItemVersionsByIndex)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/rollback", []string{"qa.write"}, workflow.RollbackSlotItemByIndex)
	handleAPI(r, "PATCH", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/caption", []string{"qa.write"}, workflow.PatchSlotCaptionByIndex)
	// Order management
	handleAPI(r, "PATCH", "/workflow-sessions/{session_id}/slots/{slot_id}/order", []string{"qa.write"}, workflow.ReorderSlotItems)
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/slots/{slot_id}/order", []string{"qa.read"}, workflow.GetSlotOrderHandler)
	// Phase 4: caption editing and manual item creation
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/slots/{slot_id}/items", []string{"qa.write"}, workflow.CreateSlotItem)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/artifacts", []string{"qa.write"}, workflow.SaveArtifactByKey)
	// Dismiss and restore workflow sessions.
	handleAPI(r, "POST", "/workflow-sessions/{session_id}:dismiss", []string{"qa.write"}, workflow.DismissSessionHandler)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}:restore", []string{"qa.write"}, workflow.RestoreSessionHandler)
	// List dismissed sessions for a conversation (used by restore UI).
	handleAPI(r, "GET", "/conversations/{conversation_id}/dismissed-workflow-sessions", []string{"qa.read"}, workflow.ListDismissedSessionsHandler)
	handleAPI(r, "GET", "/personalization-setting", []string{"qa.read"}, evolution.GetPersonalizationSetting)
	handleAPI(r, "PUT", "/personalization-setting", []string{"qa.write"}, evolution.SetPersonalizationSetting)
	handleAPI(r, "GET", "/memory/soul", []string{"qa.read"}, currentmemory.GetSoul)
	handleAPI(r, "PATCH", "/memory/soul", []string{"qa.write"}, currentmemory.PatchSoul)
	handleAPI(r, "GET", "/memory/soul/avatar", []string{"qa.read"}, currentmemory.GetSoulAvatar)
	handleAPI(r, "PUT", "/memory/soul/avatar", []string{"qa.write"}, currentmemory.PutSoulAvatar)
	handleAPI(r, "DELETE", "/memory/soul/avatar", []string{"qa.write"}, currentmemory.DeleteSoulAvatar)
	handleAPI(r, "GET", "/memory/profile", []string{"qa.read"}, currentmemory.GetProfile)
	handleAPI(r, "PATCH", "/memory/profile", []string{"qa.write"}, currentmemory.PatchProfile)
	handleAPI(r, "GET", "/memory/profile/avatar", []string{"qa.read"}, currentmemory.GetProfileAvatar)
	handleAPI(r, "PUT", "/memory/profile/avatar", []string{"qa.write"}, currentmemory.PutProfileAvatar)
	handleAPI(r, "DELETE", "/memory/profile/avatar", []string{"qa.write"}, currentmemory.DeleteProfileAvatar)
	handleAPI(r, "GET", "/memory/preferences", []string{"qa.read"}, currentmemory.ListPreferences)
	handleAPI(r, "POST", "/memory/preferences:organize", []string{"qa.write"}, resourceupdate.SubmitPreferenceOrganizer)
	handleAPI(r, "GET", "/memory/preferences:organize/{task_id}", []string{"qa.read"}, resourceupdate.GetPreferenceOrganizer)
	handleAPI(r, "GET", "/memory/preferences:organize", []string{"qa.read"}, resourceupdate.GetLatestPreferenceOrganizer)
	handleAPI(r, "PUT", "/memory/preferences:order", []string{"qa.write"}, currentmemory.ReorderPreferences)
	handleAPI(r, "GET", "/memory/preferences/{name}", []string{"qa.read"}, currentmemory.GetPreference)
	handleAPI(r, "DELETE", "/memory/preferences/{name}", []string{"qa.write"}, currentmemory.DeletePreference)
	handleAPI(r, "GET", "/memory/episodes", []string{"qa.read"}, episode.ListEpisodes)
	handleAPI(r, "GET", "/memory/episodes/{episode_id}", []string{"qa.read"}, episode.GetEpisode)
	handleAPI(r, "DELETE", "/memory/episodes/{episode_id}", []string{"qa.write"}, episode.DeleteEpisode)
	handleAPI(r, "POST", "/internal/memory/episodes", nil, episode.InternalCreate)
	handleAPI(r, "DELETE", "/internal/memory/episodes/{episode_id}", nil, episode.InternalDelete)
	handleAPI(r, "POST", "/internal/memory/episodes:searchCandidates", nil, episode.InternalSearchCandidates)
	handleAPI(r, "POST", "/internal/memory/episodes:listRecent", nil, episode.InternalListRecent)
	handleAPI(r, "GET", "/internal/memory/episodes", nil, episode.InternalListByConversation)
	handleAPI(r, "POST", "/internal/memory/episodes:recordHits", nil, episode.InternalRecordHits)
	handleAPI(r, "GET", "/skills", []string{"qa.read"}, skillv2handler.List)
	handleAPI(r, "GET", "/skills:trash", []string{"qa.read"}, skillv2handler.ListTrash)
	handleAPI(r, "DELETE", "/skills:trash", []string{"qa.write"}, skillv2handler.EmptyTrash)
	handleAPI(r, "POST", "/skill_organize", []string{"qa.write"}, skillv2handler.SubmitSkillOrganize)
	handleAPI(r, "GET", "/skills/maintenance-task", []string{"qa.read"}, skillv2handler.MaintenanceTaskStatus)
	handleAPI(r, "GET", "/skills/tags", []string{"qa.read"}, skillv2handler.ListTags)
	handleAPI(r, "GET", "/skills/categories", []string{"qa.read"}, skillv2handler.ListCategories)
	handleAPI(r, "POST", "/skills", []string{"qa.write"}, skillv2handler.Create)
	handleAPI(r, "GET", "/builtin-skills", []string{"qa.read"}, skillv2handler.ListBuiltinSkills)
	handleAPI(r, "POST", "/builtin-skills/{builtin_skill_uid}:enable", []string{"qa.write"}, skillv2handler.EnableBuiltinSkill)
	handleAPI(r, "GET", "/skills/{skill_id}/distribution-upgrade", []string{"qa.read"}, skillv2handler.DistributionUpgradeStatus)
	handleAPI(r, "POST", "/skills/{skill_id}/distribution-upgrade:prepare", []string{"qa.write"}, skillv2handler.PrepareDistributionUpgrade)
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
	handleAPI(r, "GET", "/skill-market/tags", []string{"qa.read"}, skillv2handler.MarketTags)
	handleAPI(r, "GET", "/skill-market/{market_item_id}", []string{"qa.read"}, skillv2handler.MarketGet)
	handleAPI(r, "POST", "/skill-market:install", []string{"qa.write"}, skillv2handler.MarketInstall)
	handleAPI(r, "POST", "/skill-market/{market_item_id}:install", []string{"qa.write"}, skillv2handler.MarketInstall)
	handleAPI(r, "POST", "/admin/skill-market", []string{"user.admin"}, skillv2handler.MarketPublish)
	handleAPI(r, "PATCH", "/admin/skill-market/{market_item_id}", []string{"user.admin"}, skillv2handler.MarketEdit)
	handleAPI(r, "DELETE", "/admin/skill-market/{market_item_id}", []string{"user.admin"}, skillv2handler.MarketDelete)
	handleAPI(r, "POST", "/admin/skill-market/{market_item_id}:offline", []string{"user.admin"}, skillv2handler.MarketUnpublish)
	handleAPI(r, "POST", "/skill-market/admin/items", []string{"user.admin"}, skillv2handler.MarketPublish)
	handleAPI(r, "PATCH", "/skill-market/admin/items/{market_item_id}", []string{"user.admin"}, skillv2handler.MarketEdit)
	handleAPI(r, "DELETE", "/skill-market/admin/items/{market_item_id}", []string{"user.admin"}, skillv2handler.MarketDelete)
	handleAPI(r, "POST", "/skill-market/admin/items/{market_item_id}:unpublish", []string{"user.admin"}, skillv2handler.MarketUnpublish)
	// ----- Knowledge market (read-only) -----
	handleAPI(r, "GET", "/knowledge-market", []string{"qa.read"}, knowledge_market.MarketList)
	handleAPI(r, "GET", "/knowledge-market/domains", []string{"qa.read"}, knowledge_market.MarketDomains)
	handleAPI(r, "GET", "/knowledge-market/items/{market_item_id}", []string{"qa.read"}, knowledge_market.MarketGet)
	handleAPI(r, "POST", "/knowledge-market/items/{market_item_id}:install", []string{"qa.write"}, knowledge_market.MarketInstall)
	handleAPI(r, "POST", "/knowledge-market/items/{market_item_id}:update", []string{"qa.write"}, knowledge_market.MarketUpdate)
	handleAPI(r, "POST", "/knowledge-market:update-all", []string{"qa.write"}, knowledge_market.MarketUpdateAll)
	handleAPI(r, "GET", "/knowledge-market/tasks", []string{"qa.read"}, knowledge_market.MarketListInstallTasks)
	handleAPI(r, "GET", "/knowledge-market/tasks/{job_id}", []string{"qa.read"}, knowledge_market.MarketGetInstallTask)
	handleAPI(r, "GET", "/knowledge-market/installs", []string{"qa.read"}, knowledge_market.MarketListInstalls)
	handleAPI(r, "GET", "/skill-review:summary", []string{"qa.read"}, resourceupdate.GetSkillReviewSummary)
	handleAPI(r, "POST", "/skill-review:run", []string{"qa.write"}, resourceupdate.RunSkillReview)
	handleAPI(r, "GET", "/skill-review/tasks", []string{"qa.read"}, resourceupdate.ListSkillReviewTasks)
	handleAPI(r, "GET", "/skill-organize/tasks", []string{"qa.read"}, resourceupdate.ListSkillOrganizeTasks)
	handleAPI(r, "PATCH", "/conversations/{name}:search-config", []string{"qa.write"}, chat.PatchConversationSearchConfig)
	handleAPI(r, "GET", "/conversations/{name}:detail", []string{"qa.read"}, chat.GetConversationDetail)
	handleAPI(r, "GET", "/conversations/{name}:history", []string{"qa.read"}, chat.GetConversationHistory)
	handleAPI(r, "GET", "/conversations/{name}:trail", []string{"qa.read"}, chat.GetConversationTrail)
	handleAPI(r, "GET", "/conversation-archive-folders", []string{"qa.read"}, chat.ListConversationArchiveFolders)
	handleAPI(r, "POST", "/conversation-archive-folders", []string{"qa.write"}, chat.CreateConversationArchiveFolder)
	handleAPI(r, "PATCH", "/conversation-archive-folders/{folder_id}", []string{"qa.write"}, chat.UpdateConversationArchiveFolder)
	handleAPI(r, "DELETE", "/conversation-archive-folders/{folder_id}", []string{"qa.write"}, chat.DeleteConversationArchiveFolder)
	handleAPI(r, "GET", "/conversations:archived", []string{"qa.read"}, chat.ListArchivedConversations)
	handleAPI(r, "GET", "/conversations:trash", []string{"qa.read"}, chat.ListTrashedConversations)
	handleAPI(r, "DELETE", "/conversations:trash", []string{"qa.write"}, chat.EmptyConversationTrash)
	handleAPI(r, "POST", "/conversations/{conversation_id}:archive", []string{"qa.write"}, chat.ArchiveConversation)
	handleAPI(r, "POST", "/conversations/{conversation_id}:unarchive", []string{"qa.write"}, chat.UnarchiveConversation)
	handleAPI(r, "POST", "/conversations/{conversation_id}:restore", []string{"qa.write"}, chat.RestoreConversation)
	handleAPI(r, "DELETE", "/conversations/{conversation_id}:purge", []string{"qa.write"}, chat.PurgeConversation)
	handleAPI(r, "GET", "/conversations/{name}", []string{"qa.read"}, chat.GetConversation)
	handleAPI(r, "DELETE", "/conversations/{name}", []string{"qa.write"}, chat.DeleteConversation)
	handleAPI(r, "POST", "/conversations:batchDelete", []string{"qa.write"}, chat.BatchDeleteConversations)
	handleAPI(r, "GET", "/conversations", []string{"qa.read"}, chat.ListConversations)
	handleAPI(r, "POST", "/conversations:setChatHistory", []string{"qa.write"}, chat.SetChatHistory)
	handleAPI(r, "POST", "/conversations:feedBackChatHistory", []string{"qa.write"}, chat.FeedBackChatHistory)
	handleAPI(r, "PATCH", "/conversations/{name}:ask-answers", []string{"qa.write"}, chat.SaveAskAnswers)
	handleAPI(r, "PATCH", "/conversations:editable-block", []string{"qa.write"}, chat.PatchEditableBlock)

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
	handleAPI(r, "POST", "/prompts/{name}:favorite", []string{"document.write"}, chat.FavoritePrompt)
	handleAPI(r, "POST", "/prompts/{name}:unfavorite", []string{"document.write"}, chat.UnfavoritePrompt)
	handleAPI(r, "POST", "/prompts/{name}:use", []string{"document.write"}, chat.UsePrompt)
	handleAPI(r, "PATCH", "/prompts/{name}", []string{"document.write"}, chat.UpdatePrompt)
	handleAPI(r, "DELETE", "/prompts/{name}", []string{"document.write"}, chat.DeletePrompt)
	handleAPI(r, "GET", "/prompts/{name}", []string{"document.read"}, chat.GetPrompt)
	handleAPI(r, "GET", "/prompts", []string{"document.read"}, chat.ListPrompts)
	handleAPI(r, "GET", "/prompt_categories", []string{"document.read"}, chat.ListPromptCategories)
	handleAPI(r, "POST", "/prompt_categories", []string{"document.write"}, chat.CreatePromptCategory)
	handleAPI(r, "DELETE", "/prompt_categories/{name}", []string{"document.write"}, chat.DeletePromptCategory)

	// ----- Showcase cases -----
	handleAPI(r, "GET", "/showcase/cases", []string{"document.read"}, showcase.ListCases)
	handleAPI(r, "GET", "/showcase/cases/{case_id}", []string{"document.read"}, showcase.GetCase)

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
