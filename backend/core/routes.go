package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"lazymind/core/academic"
	"lazymind/core/acl"
	"lazymind/core/agent"
	"lazymind/core/agentinvocation"
	"lazymind/core/browser"
	"lazymind/core/chat"
	"lazymind/core/cloudbinding"
	"lazymind/core/cloudclient"
	"lazymind/core/cloudresource"
	"lazymind/core/cloudsession"
	"lazymind/core/cloudusage"
	"lazymind/core/conversationgroup"
	"lazymind/core/credentialvault"
	"lazymind/core/currentmemory"
	"lazymind/core/datasource"
	"lazymind/core/doc"
	"lazymind/core/episode"
	"lazymind/core/evalset"
	"lazymind/core/evolution"
	"lazymind/core/exporter"
	"lazymind/core/externalcapability"
	"lazymind/core/file"
	"lazymind/core/knowledge_market"
	"lazymind/core/knowledgeplaza"
	"lazymind/core/learning"
	"lazymind/core/localworkspace"
	applog "lazymind/core/log"
	"lazymind/core/mcp"
	"lazymind/core/modelconfig"
	"lazymind/core/modelprovider"
	coreproviderconnection "lazymind/core/providerconnection"
	"lazymind/core/remotefs"
	"lazymind/core/resourceupdate"
	"lazymind/core/scheduler"
	"lazymind/core/showcase"
	skillv2handler "lazymind/core/skillv2/handler"
	skillv2service "lazymind/core/skillv2/service"
	corestore "lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/systemdeps"
	"lazymind/core/taskcenter"
	"lazymind/core/translation"
	"lazymind/core/userprefs"
	"lazymind/core/vocabulary"
	"lazymind/core/wordgroup"
	"lazymind/core/workflow"
	workflowattempt "lazymind/core/workflow/attempt"
	workflowexecution "lazymind/core/workflow/execution"
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
	handleAPI(r, "GET", "/local-workspaces", []string{"qa.read"}, localworkspace.List)
	handleAPI(r, "POST", "/local-workspaces/{workspace_id}:revoke", []string{"qa.write"}, localworkspace.Revoke)
	handleAPI(r, "GET", "/conversations/{conversation_id}:workspace", []string{"qa.read"}, localworkspace.ConversationBinding)
	handleAPI(r, "PUT", "/conversations/{conversation_id}:workspace-permission", []string{"qa.write"}, localworkspace.UpdateConversationPermission)
	handleAPI(r, "POST", "/internal/local-workspaces", nil, localworkspace.InternalRegister)
	handleAPI(r, "POST", "/internal/local-workspaces/{workspace_id}:select", nil, localworkspace.InternalPrepareReauthorization)
	handleAPI(r, "POST", "/internal/conversations/{conversation_id}/workspace-operations:prepare-batch", nil, localworkspace.InternalPrepareOperationBatch)
	handleAPI(r, "GET", "/internal/conversations/{conversation_id}/workspace-operations/{operation_id}", nil, localworkspace.InternalOperationStatus)
	handleAPI(r, "POST", "/internal/conversations/{conversation_id}/workspace-operations/{operation_id}:claim", nil, localworkspace.InternalClaimLocalOperation)
	handleAPI(r, "POST", "/internal/conversations/{conversation_id}/workspace-operations/{operation_id}:complete", nil, localworkspace.InternalCompleteLocalOperation)
	handleAPI(r, "GET", "/conversations/{conversation_id}:workspace-approvals", []string{"qa.write"}, localworkspace.ListOperationApprovals)
	handleAPI(r, "POST", "/conversations/{conversation_id}/workspace-approvals/{operation_id}:decide", []string{"qa.write"}, localworkspace.DecideOperationHandler)
	cloudSession := cloudsession.DefaultService()
	cloudSessionHandler := cloudsession.Handler{Service: cloudSession}
	credentialBackupHandler := credentialvault.BackupHandler{Service: credentialvault.DefaultBackupService()}
	credentialRestoreHandler := credentialvault.DefaultRestoreHandler()
	providerConnectionHandler := coreproviderconnection.Handler{Service: coreproviderconnection.DefaultService()}
	providerTokenBridge := coreproviderconnection.TokenBridge{
		Service: coreproviderconnection.DefaultService(), InternalToken: os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"),
	}
	cloudSessionHandler.TemporaryCredentials = credentialRestoreHandler
	cloudKnowledgeHandler := knowledgeplaza.Handler{}
	cloudKnowledgeMarketHandler := knowledgeplaza.MarketHandler{}
	cloudUsageHandler := cloudusage.Handler{}
	var cloudSkillHandler cloudresource.Handler
	var cloudWorkflowHandler cloudresource.Handler
	if client, err := cloudclient.New(os.Getenv("LAZYMIND_CLOUD_BASE_URL"), nil); err == nil {
		locale := cloudLocale()
		authorizationPath := "/" + locale + "/desktop/authorize"
		if login, loginErr := cloudsession.NewLoginCoordinator(cloudsession.LoginCoordinatorDeps{
			Session: cloudSession, Handoff: client, CloudOrigin: client.Origin(),
			AuthorizationPath:     authorizationPath,
			CallbackListenAddress: os.Getenv("LAZYMIND_CLOUD_CALLBACK_LISTEN_ADDRESS"),
			Locale:                locale,
			ReportError: func(err error) {
				applog.Logger.Warn().Err(err).Str("error_type", fmt.Sprintf("%T", err)).Msg("LazyMind Cloud browser login did not complete")
			},
		}); loginErr == nil {
			cloudSessionHandler.Login = login
		}
		if registrationURL, registrationErr := client.RegistrationURL(locale); registrationErr == nil {
			cloudSessionHandler.RegistrationURL = registrationURL
		}
		cloudRuntimeProvider := &modelconfig.CloudRuntimeProvider{Session: cloudSession, Client: client, Locale: locale}
		modelconfig.SetRuntimeProvider(cloudRuntimeProvider)
		modelprovider.SetCloudReadinessProvider(cloudRuntimeProvider)
		modelprovider.SetCloudCatalogProvider(cloudRuntimeProvider)
		cloudKnowledgeHandler.Source = knowledgeplaza.CloudSource{Tokens: cloudSession, Client: client}
		cloudKnowledgeMarketHandler = knowledgeplaza.MarketHandler{Tokens: cloudSession, Client: client}
		cloudUsageHandler.Source = cloudusage.CloudSource{Tokens: cloudSession, Client: client}
		cloudResources := &cloudresource.Service{
			Session: cloudSession, Cloud: client, Bindings: cloudbinding.NewRepository(corestore.DB()),
			DesktopVersion: strings.TrimSpace(os.Getenv("LAZYMIND_DESKTOP_APP_VERSION")),
		}
		skillService := skillv2service.NewSkillService(skillv2service.SkillServiceDeps{
			DB: corestore.DB(), BlobStore: skillv2service.NewBlobStore(corestore.DB(), skillv2service.NewLocalObjectStore(skillv2service.DefaultObjectRoot())),
		})
		cloudSkillHandler = cloudresource.Handler{
			Service: cloudResources, ResourceType: "skill",
			Adapter: skillv2service.CloudAdapter{Service: skillService, DesktopVersion: cloudResources.DesktopVersion},
		}
		cloudWorkflowHandler = cloudresource.Handler{
			Service: cloudResources, ResourceType: "workflow",
			Adapter: workflow.CloudAdapter{DB: corestore.DB(), DesktopVersion: cloudResources.DesktopVersion},
		}
	} else {
		modelconfig.SetRuntimeProvider(nil)
		modelprovider.SetCloudReadinessProvider(nil)
		modelprovider.SetCloudCatalogProvider(nil)
	}
	handleAPI(r, "GET", "/cloud/session", []string{"user.read"}, cloudSessionHandler.Get)
	handleAPI(r, "POST", "/cloud/login", []string{"user.read"}, cloudSessionHandler.BeginLogin)
	handleAPI(r, "POST", "/cloud/logout", []string{"user.read"}, cloudSessionHandler.Logout)
	handleAPI(r, "GET", "/cloud/token-plan", []string{"user.read"}, cloudUsageHandler.Get)
	handleAPI(r, "POST", "/provider-connections/sessions", []string{"user.write"}, providerConnectionHandler.CreateSession)
	handleAPI(r, "GET", "/provider-connections/sessions/{session_id}", []string{"user.read"}, providerConnectionHandler.GetSession)
	handleAPI(r, "DELETE", "/provider-connections/sessions/{session_id}", []string{"user.write"}, providerConnectionHandler.CancelSession)
	handleAPI(r, "GET", "/provider-connections", []string{"user.read"}, providerConnectionHandler.List)
	handleAPI(r, "POST", "/provider-connections/{auth_connection_id}:reauthorize", []string{"user.write"}, providerConnectionHandler.Reauthorize)
	handleAPI(r, "DELETE", "/provider-connections/{auth_connection_id}", []string{"user.write"}, providerConnectionHandler.Revoke)
	handleAPI(r, "POST", "/v1/internal/provider-connections/{auth_connection_id}/access-token:resolve", nil, providerTokenBridge.Resolve)
	handleAPI(r, "POST", "/v1/internal/provider-connections/{auth_connection_id}/access-token:report", nil, providerTokenBridge.Report)
	handleAPI(r, "POST", "/v1/internal/provider-connections/feishu-cli:execute", nil, providerTokenBridge.ExecuteFeishuCLI)
	handleAPI(r, "GET", "/credential-vault/backup", []string{"user.read"}, credentialBackupHandler.Status)
	handleAPI(r, "POST", "/credential-vault/backup:enable", []string{"user.write"}, credentialBackupHandler.Enable)
	handleAPI(r, "POST", "/credential-vault/backup:disable", []string{"user.write"}, credentialBackupHandler.Disable)
	handleAPI(r, "GET", "/credential-vault/restores", []string{"user.read"}, credentialRestoreHandler.Discover)
	handleAPI(r, "POST", "/credential-vault/restores", []string{"user.write"}, credentialRestoreHandler.Start)
	handleAPI(r, "GET", "/credential-vault/restores/{operation_id}", []string{"user.read"}, credentialRestoreHandler.Get)
	handleAPI(r, "DELETE", "/credential-vault/restores/{operation_id}", []string{"user.write"}, credentialRestoreHandler.Cancel)
	handleAPI(r, "POST", "/credential-vault/restores:clear-temporary", []string{"user.write"}, credentialRestoreHandler.ClearTemporaryCredentials)
	handleAPI(r, "POST", "/internal/credential-vault/restores:clear-temporary", nil, credentialRestoreHandler.InternalClearTemporaryCredentials)
	handleAPI(r, "GET", "/cloud/knowledge-square", []string{"document.read"}, cloudKnowledgeHandler.List)
	handleAPI(r, "GET", "/cloud/knowledge-market", []string{"document.read"}, cloudKnowledgeMarketHandler.List)
	handleAPI(r, "GET", "/cloud/knowledge-market/items/{catalog_key}", []string{"document.read"}, cloudKnowledgeMarketHandler.Get)
	handleAPI(r, "GET", "/cloud/skills", []string{"qa.read"}, cloudSkillHandler.List)
	handleAPI(r, "GET", "/cloud/skills/{resource_id}", []string{"qa.read"}, cloudSkillHandler.Get)
	handleAPI(r, "GET", "/cloud/skills/{resource_id}/tree", []string{"qa.read"}, cloudSkillHandler.Tree)
	handleAPI(r, "GET", "/cloud/skills/{resource_id}/content", []string{"qa.read"}, cloudSkillHandler.Content)
	handleAPI(r, "POST", "/cloud/skills/{skill_id}:upload", []string{"qa.write"}, cloudSkillHandler.Upload)
	handleAPI(r, "POST", "/cloud/skills/{resource_id}:download", []string{"qa.write"}, cloudSkillHandler.Download)
	handleAPI(r, "GET", "/cloud/workflows", []string{"qa.read"}, cloudWorkflowHandler.List)
	handleAPI(r, "GET", "/cloud/workflows/{resource_id}", []string{"qa.read"}, cloudWorkflowHandler.Get)
	handleAPI(r, "GET", "/cloud/workflows/{resource_id}/tree", []string{"qa.read"}, cloudWorkflowHandler.Tree)
	handleAPI(r, "GET", "/cloud/workflows/{resource_id}/content", []string{"qa.read"}, cloudWorkflowHandler.Content)
	handleAPI(r, "POST", "/cloud/workflows/{resource_id}:download", []string{"qa.write"}, cloudWorkflowHandler.Download)

	browserHandler := browser.NewHTTPHandler(browser.DefaultHub)
	// Management routes are served through the authenticated /api/core path.
	// Extension routes have their own one-time/device credential protocol.
	handleAPI(r, "POST", "/browser/manage/pairings", []string{"qa.write"}, browserHandler.CreatePairing)
	handleAPI(r, "GET", "/browser/manage/devices", []string{"qa.read"}, browserHandler.ListDevices)
	handleAPI(r, "DELETE", "/browser/manage/devices", []string{"qa.write"}, browserHandler.RevokeAllDevices)
	handleAPI(r, "DELETE", "/browser/manage/devices/{device_id}", []string{"qa.write"}, browserHandler.RevokeDevice)
	r.HandleFunc("/browser/extension/pair", browserHandler.PairExtension).Methods(http.MethodPost)
	r.HandleFunc("/browser/extension/connect", browserHandler.ConnectExtension).Methods(http.MethodGet)

	invocationHandler := agentinvocation.Handler{Service: agentinvocation.New(corestore.DB())}
	handleAPI(r, "POST", "/agent-invocations/{invocation_id}:start", []string{"qa.write"}, invocationHandler.Start)
	handleAPI(r, "POST", "/agent-invocations/{invocation_id}:finish", []string{"qa.write"}, invocationHandler.Finish)
	handleAPI(r, "GET", "/agent-invocations", []string{"qa.read"}, invocationHandler.List)

	workflowRepository := workflowstore.New(corestore.DB())
	workflowFacade := workflowfacade.Handler{
		Store:      workflowRepository,
		Hosts:      workflowexecutor.DefaultHostRegistry,
		Projection: http.HandlerFunc(workflow.GetSessionProjection),
	}
	completionService := &workflowexecution.Service{DB: corestore.DB(), Store: workflowRepository,
		Attempts: workflowattempt.New(corestore.DB(), workflowattempt.Config{}), Contexts: workflowexecutor.DBContextLoader{DB: corestore.DB()}}
	hostedService := &workflowhosted.Service{
		Completion: completionService,
		DB:         corestore.DB(), Store: workflowRepository,
		Attempts:  workflowattempt.New(corestore.DB(), workflowattempt.Config{LeaseDuration: 30 * time.Minute}),
		Contexts:  workflowexecutor.DBContextLoader{DB: corestore.DB()},
		Artifacts: workflowexecutor.DBArtifactSink{DB: corestore.DB()},
	}
	attemptHandler := workflowattempt.Handler{Service: workflowattempt.New(corestore.DB(), workflowattempt.Config{})}
	remoteExecutorHandler := workflowexecutor.RemoteHandler{
		DB: corestore.DB(), Attempts: attemptHandler.Service,
		Finish: func(ctx context.Context, owner, sessionID, attemptID string, input workflowexecutor.Completion) error {
			_, err := completionService.Complete(ctx, owner, sessionID, attemptID, input)
			return err
		},
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
	handleAPI(r, "POST", "/internal/workflow-attempts/{attempt_id}:fail", nil, remoteExecutorHandler.Fail)
	handleAPI(r, "POST", "/internal/workflow-attempts/{attempt_id}:cancel", nil, remoteExecutorHandler.Cancel)
	hostedHandler := workflowhosted.Handler{Service: hostedService}
	workflowControl := workflow.WorkflowControlHandler{Service: workflow.WorkflowControlService{DB: corestore.DB()}}
	handleAPI(r, "GET", "/workflow-control/capabilities", []string{"qa.read"}, workflowControl.Capabilities)

	// ----- Datasettext -----
	handleAPI(r, "GET", "/dataset/algos", []string{"document.read"}, doc.ListAlgos)
	handleAPI(r, "GET", "/dataset/tags", []string{"document.read"}, doc.AllDatasetTags)
	handleAPI(r, "GET", "/datasets", []string{"document.read"}, doc.ListDatasets)
	handleAPI(r, "POST", "/internal/datasets/usage:batch", nil, doc.InternalBatchDatasetUsage)
	handleAPI(r, "POST", "/datasets", []string{"document.write"}, doc.CreateDataset)
	handleAPI(r, "POST", "/datasets/processing/preflight", []string{"document.write"}, doc.ProcessingPreflight)
	handleAPI(r, "GET", "/datasets/{dataset}", []string{"document.read"}, doc.GetDataset)
	handleAPI(r, "DELETE", "/datasets/{dataset}", []string{"document.write"}, doc.DeleteDataset)
	handleAPI(r, "PATCH", "/datasets/{dataset}", []string{"document.write"}, doc.UpdateDataset)
	handleAPI(r, "PATCH", "/datasets/{dataset}/processing-level", []string{"document.write"}, doc.UpdateProcessingLevel)
	handleAPI(r, "POST", "/datasets/{dataset}/documents/{document}:ensure-parsed", []string{"document.read"}, doc.EnsureParsed)
	handleAPI(r, "GET", "/datasets/{dataset}/processing-status", []string{"document.read"}, doc.GetProcessingStatus)

	// ----- Academic references and paper imports -----
	handleAPI(r, "GET", "/academic/documents/{document_id}/references", []string{"document.read"}, academic.ListReferences)
	handleAPI(r, "POST", "/academic/documents/{document_id}/references:extract", []string{"document.write"}, academic.ExtractReferences)
	handleAPI(r, "POST", "/academic/references:resolve", []string{"document.write"}, academic.ResolveReferences)
	handleAPI(r, "POST", "/academic/works:presence", []string{"document.read"}, academic.CheckPresence)
	handleAPI(r, "POST", "/academic/imports:preview", []string{"document.read"}, academic.PreviewImport)
	handleAPI(r, "POST", "/academic/imports", []string{"document.write"}, academic.CreateImport)
	handleAPI(r, "GET", "/academic/imports", []string{"document.read"}, academic.ListImports)
	handleAPI(r, "GET", "/academic/imports/{batch_id}", []string{"document.read"}, academic.GetImport)
	handleAPI(r, "POST", "/academic/imports/{batch_id}:cancel", []string{"document.write"}, academic.CancelImport)
	handleAPI(r, "POST", "/academic/imports/{batch_id}:retry", []string{"document.write"}, academic.RetryImport)
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
	handleAPI(r, "GET", "/system-dependencies/browser-extension", []string{"document.read"}, systemdeps.GetBrowserExtensionDependency)
	handleAPI(r, "POST", "/system-dependencies/browser-extension:check", []string{"document.read"}, systemdeps.CheckBrowserExtensionDependency)
	handleAPI(r, "POST", "/system-dependencies/browser-extension:install", []string{"document.write"}, systemdeps.InstallBrowserExtensionDependency)
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
	handleAPI(r, "GET", "/datasets/{dataset}/documents/{document}/pdf-capabilities", []string{"document.read"}, doc.GetPDFCapabilities)
	handleAPI(r, "POST", "/datasets/{dataset}/documents/{document}/pdf-artifacts/searchable", []string{"document.write"}, doc.CreateSearchablePDFJob)
	handleAPI(r, "POST", "/datasets/{dataset}/documents/{document}/pdf-translations", []string{"document.write"}, doc.CreateTranslationPDFJob)
	handleAPI(r, "GET", "/datasets/{dataset}/documents/{document}/pdf-translations", []string{"document.read"}, doc.ListPDFTranslations)
	handleAPI(r, "PATCH", "/datasets/{dataset}/documents/{document}/pdf-render-jobs/{job}", []string{"document.write"}, doc.UpdatePDFRenderJob)
	handleAPI(r, "POST", "/datasets/{dataset}/documents/{document}/pdf-render-jobs/{job}:complete", []string{"document.write"}, doc.CompletePDFRenderJob)
	handleAPI(r, "GET", "/datasets/{dataset}/documents/{document}/pdf-artifacts/{artifact}:content", []string{"document.read"}, doc.GetPDFArtifact)
	handleAPI(r, "GET", "/datasets/{dataset}/documents/{document}/pdf-artifacts/{artifact}:layout", []string{"document.read"}, doc.GetPDFArtifactLayout)
	handleAPI(r, "DELETE", "/datasets/{dataset}/documents/{document}/pdf-artifacts/{artifact}", []string{"document.write"}, doc.DeletePDFArtifact)
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

	// ----- Explicit external Agent model/tool authorization -----
	handleAPI(r, "GET", "/external-agent-capabilities", []string{"qa.read"}, externalcapability.List)
	handleAPI(r, "PUT", "/external-agent-capabilities", []string{"qa.write"}, externalcapability.Update)
	handleAPI(r, "GET", "/external-agent-capability-invocations", []string{"qa.read"}, externalcapability.ListInvocations)

	// ----- Agent thread stream -----
	handleAPI(r, "GET", "/agent/evolution-models", []string{"qa.write"}, agent.ListEvolutionModels)
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
	handleAgentThreadAPI(r, "POST", "/agent/threads/{thread_id}/resume", []string{"qa.write"}, agent.ResumeThread)
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
	handleAPI(r, "GET", "/conversations/metadata-backfill", []string{"qa.read"}, chat.ConversationTitleBackfill)
	handleAPI(r, "POST", "/conversations/metadata-backfill", []string{"qa.write"}, chat.ConversationTitleBackfill)
	handleAPI(r, "PATCH", "/conversations/{name}/title", []string{"qa.write"}, chat.RenameConversation)
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
	handleAPI(r, "POST", "/conversations/{conversation_id}/fork-preview", []string{"qa.read"}, chat.PreviewConversationFork)
	handleAPI(r, "POST", "/conversations/{conversation_id}/forks", []string{"qa.write"}, chat.CreateConversationFork)
	handleAPI(r, "POST", "/conversations/{parent_id}/sidechat", []string{"qa.write"}, chat.CreateSidechat)
	handleAPI(r, "POST", "/conversations/{child_id}/retain", []string{"qa.write"}, chat.RetainSidechat)
	handleAPI(r, "DELETE", "/conversations/{child_id}/sidechat", []string{"qa.write"}, chat.DiscardSidechat)
	handleAPI(r, "POST", "/conversations/{conversation_id}:promote", []string{"qa.write"}, chat.PromoteConversation)
	handleAPI(r, "POST", "/conversations/{conversation_id}:pin", []string{"qa.write"}, chat.PinConversation)
	handleAPI(r, "POST", "/conversations/{conversation_id}:unpin", []string{"qa.write"}, chat.UnpinConversation)
	handleAPI(r, "POST", "/conversations/{conversation_id}:reorder", []string{"qa.write"}, chat.ReorderConversation)
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
	handleAPI(r, "GET", "/internal/subagent/conversations/{conversation_id}/tasks", nil, subagent.InternalListConversationTasks)
	handleAPI(r, "GET", "/internal/subagent/artifacts", nil, subagent.InternalGetTaskArtifactsBatch)
	handleAPI(r, "GET", "/internal/subagent/tasks/{task_id}/artifacts", nil, subagent.InternalGetTaskArtifacts)
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
	handleAPI(r, "POST", "/workflows/{workflow_id}:copy", []string{"qa.write"}, workflow.CopyBuiltinWorkflow)
	handleAPI(r, "GET", "/workflow-drafts:trash", []string{"qa.read"}, workflow.ListWorkflowDraftTrash)
	handleAPI(r, "DELETE", "/workflow-drafts:trash", []string{"qa.write"}, workflow.EmptyWorkflowDraftTrash)
	handleAPI(r, "POST", "/workflow-drafts:polish-info", []string{"qa.write"}, workflow.PolishWorkflowDraftInfo)
	handleAPI(r, "POST", "/workflow-conversions:preflight", []string{"qa.read"}, workflow.PreflightSkillWorkflowConversion)
	handleAPI(r, "GET", "/workflow-drafts/{draft_id}", []string{"qa.read"}, workflow.GetWorkflowDraft)
	handleAPI(r, "POST", "/workflow-drafts/{draft_id}:copy", []string{"qa.write"}, workflow.CopyWorkflowDraft)
	handleAPI(r, "POST", "/workflow-drafts/{draft_id}:save", []string{"qa.write"}, workflow.SaveWorkflowDraft)
	handleAPI(r, "POST", "/workflow-drafts/{draft_id}:validate", []string{"qa.read"}, workflow.ValidateWorkflowDraft)
	handleAPI(r, "POST", "/workflow-drafts/{draft_id}:ai-generate", []string{"qa.write"}, workflow.AIGenerateWorkflowDraft)
	handleAPI(r, "POST", "/workflow-drafts/{draft_id}:cancel-generation", []string{"qa.write"}, workflow.CancelWorkflowDraftGeneration)
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
	handleAPI(r, "GET", "/skills/{skill_id}/linked-workflows", []string{"qa.read"}, workflow.ListSkillLinkedWorkflows)
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
	handleAPI(r, "POST", "/external-agent/workflow-tasks", []string{"qa.write"}, workflow.CreateExternalAgentWorkflowTask)
	handleAPI(r, "GET", "/external-agent/workflow-capabilities", []string{"qa.read"}, workflow.GetExternalWorkflowCapabilities)
	handleAPI(r, "GET", "/external-agent/workflow-tasks/{task_id}", []string{"qa.read"}, workflow.GetExternalAgentWorkflowTask)
	handleAPI(r, "GET", "/external-agent/workflow-tasks/{task_id}/result", []string{"qa.read"}, workflow.GetExternalAgentWorkflowTaskResult)
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
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/hosted-attempts/{attempt_id}:complete", []string{"qa.write"}, hostedHandler.Complete)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/hosted-attempts/{attempt_id}/artifacts", []string{"qa.write"}, hostedHandler.Publish)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/input-bindings", []string{"qa.write"}, workflowFacade.BindInput)
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/input-bindings", []string{"qa.read"}, workflowFacade.ListInputs)
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/artifacts", []string{"qa.read"}, workflowFacade.ListArtifacts)
	handleAPI(r, "GET", "/writer-download-conversions/{source_hash}/{target_format}", []string{"qa.read"}, workflow.GetWriterDownloadConversion)
	handleAPI(r, "PUT", "/writer-download-conversions/{source_hash}/{target_format}", []string{"qa.write"}, workflow.PutWriterDownloadConversion)
	handleAPI(r, "POST", "/writer-download-conversions:convert", []string{"qa.write"}, workflow.ConvertWriterDownload)
	handleAPI(r, "GET", "/workflow-artifacts/{artifact_id}", []string{"qa.read"}, workflowFacade.ReadArtifact)
	handleAPI(r, "GET", "/document-publications/{operation_id}", []string{"qa.read"}, workflow.ReadDocumentPublication)
	handleAPI(r, "GET", "/workflow-artifacts/{artifact_id}/publication", []string{"qa.read"}, workflow.ReadArtifactDocumentPublication)
	handleAPI(r, "POST", "/document-publications/{operation_id}:recover", []string{"qa.write"}, workflow.RecoverDocumentPublicationHTTP)
	handleAPI(r, "POST", "/document-publications/{operation_id}:cancel", []string{"qa.write"}, workflow.CancelDocumentPublicationHTTP)
	handleAPI(r, "POST", "/document-publications/{operation_id}:retry-local", []string{"qa.write"}, workflow.RetryDocumentPublicationLocal)
	handleAPI(r, "GET", "/document-providers", []string{"qa.read"}, workflow.ListDocumentProviders)
	handleAPI(r, "POST", "/workflow-artifacts/{artifact_id}/document-actions:preview", []string{"qa.write"}, workflow.PreviewDocumentAction)
	handleAPI(r, "POST", "/workflow-artifacts/{artifact_id}/document-actions:execute", []string{"qa.write"}, workflow.ExecuteDocumentAction)
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
	handleAPI(r, "GET", "/workflow-sessions/{session_id}", []string{"qa.read"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.GetSessionDetail)))
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/control", []string{"qa.read"}, workflowFacade.SessionAccess(http.HandlerFunc(workflowControl.Read)))
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/control", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflowControl.Command)))
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/executions:begin", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflowControl.Begin)))
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/executions:stop", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflowControl.StopExecution)))
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/host-binding", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflowControl.Bind)))
	handleAPI(r, "GET", "/workflow-host-actions", []string{"qa.read"}, workflowControl.Actions)
	handleAPI(r, "GET", "/workflow-host-actions/{action_id}", []string{"qa.read"}, workflowControl.Action)
	handleAPI(r, "POST", "/workflow-host-actions/{action_id}:claim", []string{"qa.write"}, workflowControl.ClaimAction)
	handleAPI(r, "POST", "/workflow-host-actions/{action_id}:settle", []string{"qa.write"}, workflowControl.SettleAction)

	handleAPI(r, "GET", "/workflow-sessions/{session_id}/slots", []string{"qa.read"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.GetSessionSlots)))
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/steps", []string{"qa.read"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.GetSessionSteps)))
	handleAPI(r, "POST", "/workflow-sessions/{session_id}:approval-preference", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.SetWorkflowApprovalPreference)))
	// Compatibility alias: old clients receive the same authoritative projection;
	// no independent BFS state calculation remains on an active route.
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/state-graph", []string{"qa.read"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.GetSessionProjection)))
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/projection", []string{"qa.read"}, workflowFacade.GetProjection)
	handleAPI(r, "GET", "/internal/workflow-sessions/{session_id}/projection", nil, workflow.GetSessionProjection)
	handleAPI(r, "POST", "/internal/workflow-sessions:plan-start", nil, workflow.PlanWorkflowSessionStart)
	handleAPI(r, "POST", "/internal/workflow-sessions:start", nil, workflow.StartWorkflowSession)
	handleAPI(r, "POST", "/internal/workflow-sessions/{session_id}:transition", nil, workflow.TransitionWorkflowSession)
	handleAPI(r, "GET", "/internal/workflow-transition-commands/{command_id}", nil, workflow.GetTransitionCommand)
	handleAPI(r, "PATCH", "/workflow-sessions/{session_id}/slots/{slot_id}", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.PatchSessionSlot)))
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}:action-preview", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.PreviewArtifactAction)))
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}:action-execute", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.ExecuteArtifactAction)))
	handleAPI(r, "POST", "/workflow-sessions/{session_id}:sync-search-config", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.SyncSessionSearchConfig)))
	// Phase 3: slot item management.
	// Stable list_index-based routes (preferred).
	handleAPI(r, "DELETE", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.DeleteSlotItemByIndex)))
	handleAPI(r, "PATCH", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.PatchSlotItemByIndex)))
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}:sync-writer-document", []string{"qa.write"}, chat.SyncWriterDocument)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/writer-document:write-back", []string{"qa.write"}, chat.WriteBackWriterDocument)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/writer-document:render", []string{"qa.read"}, chat.RenderWriterDocument)
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/writer-document:save", []string{"qa.write"}, chat.SaveWriterDocument)
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/versions", []string{"qa.read"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.GetSlotItemVersionsByIndex)))
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/rollback", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.RollbackSlotItemByIndex)))
	handleAPI(r, "PATCH", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/caption", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.PatchSlotCaptionByIndex)))
	// Order management
	handleAPI(r, "PATCH", "/workflow-sessions/{session_id}/slots/{slot_id}/order", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.ReorderSlotItems)))
	handleAPI(r, "GET", "/workflow-sessions/{session_id}/slots/{slot_id}/order", []string{"qa.read"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.GetSlotOrderHandler)))
	// Phase 4: caption editing and manual item creation
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/slots/{slot_id}/items", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.CreateSlotItem)))
	handleAPI(r, "POST", "/workflow-sessions/{session_id}/artifacts", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.SaveArtifactByKey)))
	// Dismiss and restore workflow sessions.
	handleAPI(r, "POST", "/workflow-sessions/{session_id}:dismiss", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.DismissSessionHandler)))
	handleAPI(r, "POST", "/workflow-sessions/{session_id}:restore", []string{"qa.write"}, workflowFacade.SessionAccess(http.HandlerFunc(workflow.RestoreSessionHandler)))
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
	handleAPI(r, "POST", "/skill-recordings/browser", []string{"qa.write"}, skillv2handler.RecordingBrowser)
	handleAPI(r, "GET", "/skill-recordings/setup", []string{"qa.read"}, skillv2handler.RecordingSkillSetup)
	handleAPI(r, "POST", "/skill-recordings/setup", []string{"qa.write"}, skillv2handler.RecordingSkillSetup)
	handleAPI(r, "GET", "/skill-recordings", []string{"qa.read"}, skillv2handler.ListSkillRecordings)
	handleAPI(r, "POST", "/skill-recordings", []string{"qa.write"}, skillv2handler.SubmitSkillRecording)
	handleAPI(r, "POST", "/skill-recordings/decision", []string{"qa.write"}, skillv2handler.DecideSkillRecording)
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
	handleAPI(r, "DELETE", "/knowledge-market/tasks/{job_id}", []string{"qa.write"}, knowledge_market.MarketDeleteTask)
	handleAPI(r, "POST", "/knowledge-market/tasks/{job_id}:retry", []string{"qa.write"}, knowledge_market.MarketRetryTask)
	handleAPI(r, "POST", "/knowledge-market/tasks/{job_id}:cancel", []string{"qa.write"}, knowledge_market.MarketCancelTask)
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
	handleAPI(r, "GET", "/conversation-groups", []string{"qa.read"}, conversationgroup.ListGroups)
	handleAPI(r, "POST", "/conversation-groups", []string{"qa.write"}, conversationgroup.CreateGroup)
	handleAPI(r, "GET", "/conversation-groups/{group_id}", []string{"qa.read"}, conversationgroup.GetGroup)
	handleAPI(r, "PATCH", "/conversation-groups/{group_id}/placement", []string{"qa.write"}, conversationgroup.UpdateGroupPlacement)
	handleAPI(r, "PATCH", "/conversation-groups/{group_id}", []string{"qa.write"}, conversationgroup.UpdateGroup)
	handleAPI(r, "DELETE", "/conversation-groups/{group_id}", []string{"qa.write"}, chat.DeleteConversationGroup)
	handleAPI(r, "POST", "/conversation-groups/{group_id}/conversations", []string{"qa.write"}, conversationgroup.AddMember)
	handleAPI(r, "DELETE", "/conversation-groups/{group_id}/conversations/{conversation_id}", []string{"qa.write"}, conversationgroup.RemoveMember)
	handleAPI(r, "POST", "/conversation-organizer-runs", []string{"qa.write"}, conversationgroup.StartOrganizer)
	handleAPI(r, "GET", "/conversation-organizer-runs:latest", []string{"qa.read"}, conversationgroup.GetLatestOrganizer)
	handleAPI(r, "GET", "/conversation-organizer-runs/{run_id}", []string{"qa.read"}, conversationgroup.GetOrganizer)
	handleAPI(r, "POST", "/conversation-organizer-runs/{run_id}:cancel", []string{"qa.write"}, conversationgroup.CancelOrganizer)
	handleAPI(r, "POST", "/conversation-organizer-runs/{run_id}:retry", []string{"qa.write"}, conversationgroup.RetryOrganizer)
	handleAPI(r, "POST", "/conversation-organizer-runs/{run_id}:confirm", []string{"qa.write"}, conversationgroup.ConfirmOrganizer)
	handleAPI(r, "POST", "/conversation-organizer-runs/{run_id}:undo", []string{"qa.write"}, conversationgroup.UndoOrganizer)
	handleAPI(r, "PATCH", "/conversation-organizer-runs/{run_id}/items/{conversation_id}", []string{"qa.write"}, conversationgroup.CorrectOrganizerItem)
	handleAPI(r, "POST", "/conversations:batchStatus", []string{"qa.read"}, chat.BatchConversationStatus)
	handleAPI(r, "POST", "/conversations/{conversation_id}:readResult", []string{"qa.read"}, chat.ReadConversationResult)
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
	handleAPI(r, "GET", "/model_providers/{model_provider_id}/groups/{group_id}/remote_models", []string{"model.read"}, modelprovider.ListRemoteGroupModels)
	handleAPI(r, "GET", "/model_providers/{model_provider_id}/groups/{group_id}/models", []string{"model.read"}, modelprovider.ListGroupModels)
	handleAPI(r, "POST", "/model_providers/{model_provider_id}/groups/{group_id}/models", []string{"model.write"}, modelprovider.AddGroupModel)
	handleAPI(r, "PATCH", "/model_providers/{model_provider_id}/groups/{group_id}/models/{model_id}", []string{"model.write"}, modelprovider.UpdateGroupModel)
	handleAPI(r, "DELETE", "/model_providers/{model_provider_id}/groups/{group_id}/models/{model_id}", []string{"model.write"}, modelprovider.DeleteGroupModel)
	handleAPI(r, "POST", "/model_providers/{model_provider_id}/groups/{group_id}/keys", []string{"model.write"}, modelprovider.AddKey)
	handleAPI(r, "DELETE", "/model_providers/{model_provider_id}/groups/{group_id}/keys", []string{"model.write"}, modelprovider.RemoveKey)
	handleAPI(r, "GET", "/translation/status", []string{"document.read"}, translation.Status)
	handleAPI(r, "POST", "/translation:translate", []string{"document.read"}, translation.Translate)

	// ----- Vocabulary / Anki provider -----
	handleAPI(r, "GET", "/learning/catalog", []string{"document.read"}, learning.Catalog)
	handleAPI(r, "GET", "/learning/profiles", []string{"document.read"}, learning.ListProfiles)
	handleAPI(r, "POST", "/learning/profiles", []string{"document.write"}, learning.CreateProfile)
	handleAPI(r, "GET", "/learning/datasets/{dataset_id}/capabilities", []string{"document.read"}, learning.ListKBCapabilities)
	handleAPI(r, "PUT", "/learning/datasets/{dataset_id}/capabilities", []string{"document.write"}, learning.PutKBCapabilities)
	handleAPI(r, "PUT", "/learning/presets", []string{"document.write"}, learning.PutPreset)
	handleAPI(r, "GET", "/learning/presets", []string{"document.read"}, learning.ListPresets)
	handleAPI(r, "PATCH", "/learning/presets/{preset_id}", []string{"document.write"}, learning.UpdatePreset)
	handleAPI(r, "DELETE", "/learning/presets/{preset_id}", []string{"document.write"}, learning.DeletePreset)
	handleAPI(r, "POST", "/learning/content:resolve", []string{"document.write"}, learning.ResolveContent)
	handleAPI(r, "GET", "/learning/books", []string{"document.read"}, learning.ListBooks)
	handleAPI(r, "POST", "/learning/books", []string{"document.write"}, learning.CreateBook)
	handleAPI(r, "PATCH", "/learning/books/{book_id}", []string{"document.write"}, learning.UpdateBook)
	handleAPI(r, "DELETE", "/learning/books/{book_id}", []string{"document.write"}, learning.ArchiveBook)
	handleAPI(r, "POST", "/learning/dictionaries:import", []string{"document.write"}, learning.ImportDictionary)
	handleAPI(r, "POST", "/learning/review/sessions", []string{"document.write"}, learning.CreateReviewSession)
	handleAPI(r, "GET", "/learning/review/sessions/{session_id}", []string{"document.read"}, learning.GetReviewSession)
	handleAPI(r, "POST", "/learning/review/sessions/{session_id}/answers", []string{"document.write"}, learning.AnswerReviewQuestion)
	handleAPI(r, "POST", "/learning/preanalysis/tasks", []string{"document.write"}, learning.CreatePreanalysisTask)
	handleAPI(r, "GET", "/learning/preanalysis/tasks/latest", []string{"document.read"}, learning.GetLatestPreanalysisTask)
	handleAPI(r, "GET", "/learning/preanalysis/tasks/{task_id}", []string{"document.read"}, learning.GetPreanalysisTask)
	handleAPI(r, "POST", "/learning/preanalysis/tasks/{task_id}:run", []string{"document.write"}, learning.RunPreanalysisTask)
	handleAPI(r, "POST", "/learning/preanalysis/tasks/{task_id}:cancel", []string{"document.write"}, learning.CancelPreanalysisTask)
	handleAPI(r, "GET", "/learning/preanalysis/tasks/{task_id}/drafts", []string{"document.read"}, learning.ListPreanalysisDrafts)
	handleAPI(r, "POST", "/learning/preanalysis/tasks/{task_id}/drafts:publish", []string{"document.write"}, learning.PublishPreanalysisDrafts)
	if vocabulary.Enabled() {
		handleAPI(r, "GET", "/vocabulary/capabilities", []string{"document.read"}, vocabulary.ListCapabilities)
		handleAPI(r, "GET", "/vocabulary/provider", []string{"document.read"}, vocabulary.GetProvider)
		handleAPI(r, "PUT", "/vocabulary/provider", []string{"document.write"}, vocabulary.PutProvider)
		handleAPI(r, "GET", "/vocabulary/providers/anki/status", []string{"document.read"}, vocabulary.Status)
		handleAPI(r, "POST", "/vocabulary/providers/anki:request-permission", []string{"document.write"}, vocabulary.RequestPermission)
		handleAPI(r, "POST", "/vocabulary/providers/anki:initialize", []string{"document.write"}, vocabulary.Initialize)
		handleAPI(r, "POST", "/vocabulary/providers/anki:sync", []string{"document.write"}, vocabulary.Sync)
		handleAPI(r, "GET", "/vocabulary/providers/anki/decks", []string{"document.read"}, vocabulary.ListAnkiDecks)
		handleAPI(r, "POST", "/vocabulary/providers/anki/decks", []string{"document.write"}, vocabulary.CreateAnkiDeck)
		handleAPI(r, "DELETE", "/vocabulary/providers/anki/decks/{name}", []string{"document.write"}, vocabulary.DeleteAnkiDeck)
		handleAPI(r, "POST", "/vocabulary/words", []string{"document.write"}, vocabulary.AddWord)
		handleAPI(r, "GET", "/vocabulary/documents/{document_id}/words", []string{"document.read"}, vocabulary.ListDocumentWords)
		handleAPI(r, "GET", "/vocabulary/words", []string{"document.read"}, vocabulary.ListWords)
		handleAPI(r, "GET", "/vocabulary/review/next", []string{"document.read"}, vocabulary.NextReview)
		handleAPI(r, "POST", "/vocabulary/review/sessions", []string{"document.write"}, vocabulary.StartReviewSession)
		handleAPI(r, "GET", "/vocabulary/review/sessions/active/candidates", []string{"document.read"}, vocabulary.PreviewReviewSession)
		handleAPI(r, "GET", "/vocabulary/review/sessions/active", []string{"document.read"}, vocabulary.GetActiveReviewSession)
		handleAPI(r, "POST", "/vocabulary/review/sessions/active/words:issue", []string{"document.write"}, vocabulary.IssueReviewSessionWords)
		handleAPI(r, "GET", "/vocabulary/review/sessions/{session_id}/questions:next", []string{"document.read"}, vocabulary.NextReviewSessionQuestions)
		handleAPI(r, "POST", "/vocabulary/review/sessions/{session_id}/questions:prepare", []string{"document.write"}, vocabulary.PrepareReviewSessionQuestions)
		handleAPI(r, "POST", "/vocabulary/review/sessions/{session_id}/answers", []string{"document.write"}, vocabulary.SubmitSessionReview)
		handleAPI(r, "POST", "/vocabulary/review/sessions/{session_id}/answers:register", []string{"document.write"}, vocabulary.RegisterSessionReview)
		handleAPI(r, "POST", "/vocabulary/review/sessions/{session_id}:complete", []string{"document.write"}, vocabulary.CompleteReviewSession)
		handleAPI(r, "POST", "/vocabulary/words/{word_id}:review", []string{"document.write"}, vocabulary.SubmitReview)
		handleAPI(r, "POST", "/vocabulary/words/{word_id}:master", []string{"document.write"}, vocabulary.MasterWord)
		handleAPI(r, "DELETE", "/vocabulary/words/{word_id}", []string{"document.write"}, vocabulary.DeleteVocabularyWord)
		handleAPI(r, "GET", "/vocabulary/wordbooks", []string{"document.read"}, vocabulary.ListWordbooks)
		handleAPI(r, "POST", "/vocabulary/wordbooks", []string{"document.write"}, vocabulary.CreateWordbook)
		handleAPI(r, "PATCH", "/vocabulary/wordbooks/{id}", []string{"document.write"}, vocabulary.UpdateWordbook)
		handleAPI(r, "DELETE", "/vocabulary/wordbooks/{id}", []string{"document.write"}, vocabulary.DeleteWordbook)
		handleAPI(r, "GET", "/vocabulary/words/{id}", []string{"document.read"}, vocabulary.GetWord)
		handleAPI(r, "PATCH", "/vocabulary/words/{id}", []string{"document.write"}, vocabulary.UpdateWord)
		handleAPI(r, "POST", "/vocabulary/words/{id}:resume", []string{"document.write"}, vocabulary.ResumeWord)
		handleAPI(r, "POST", "/vocabulary/words/{id}:reset", []string{"document.write"}, vocabulary.ResetWord)
		handleAPI(r, "POST", "/vocabulary/words:reset", []string{"document.write"}, vocabulary.ResetWords)
		handleAPI(r, "POST", "/vocabulary/selection:resolve", []string{"document.read"}, vocabulary.ResolveSelection)
		handleAPI(r, "DELETE", "/vocabulary/documents/{document_id}/words/{word_id}", []string{"document.write"}, vocabulary.RemoveDocumentSource)
		handleAPI(r, "DELETE", "/vocabulary/documents/{document_id}/words/{word_id}/word", []string{"document.write"}, vocabulary.DeleteDocumentWord)
		handleAPI(r, "GET", "/vocabulary/dictionary:lookup", []string{"document.read"}, vocabulary.DictionaryLookup)
		handleAPI(r, "GET", "/vocabulary/stats", []string{"document.read"}, vocabulary.ReviewStatsHandler)
		handleAPI(r, "GET", "/vocabulary/fsrs/profile", []string{"document.read"}, vocabulary.GetFSRSProfile)
		handleAPI(r, "PUT", "/vocabulary/fsrs/profile", []string{"document.write"}, vocabulary.PutFSRSProfile)
		handleAPI(r, "GET", "/vocabulary/review/logs:export", []string{"document.read"}, vocabulary.ExportReviewLogs)
		handleAPI(r, "GET", "/vocabulary/words/{id}/review-history", []string{"document.read"}, vocabulary.ReviewHistory)
		handleAPI(r, "POST", "/vocabulary/words/{id}/examples", []string{"document.write"}, vocabulary.AddExample)
		handleAPI(r, "PATCH", "/vocabulary/examples/{id}", []string{"document.write"}, vocabulary.UpdateExample)
		handleAPI(r, "DELETE", "/vocabulary/examples/{id}", []string{"document.write"}, vocabulary.DeleteExample)
		handleAPI(r, "POST", "/vocabulary/words/{id}/tags", []string{"document.write"}, vocabulary.AddWordTags)
		handleAPI(r, "DELETE", "/vocabulary/words/{id}/tags/{tag}", []string{"document.write"}, vocabulary.RemoveWordTag)
	}

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

func cloudLocale() string {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("LAZYMIND_CLOUD_REGISTER_LOCALE")))
	if value == "en" || value == "en-us" {
		return "en"
	}
	return "zh"
}
