package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	capabilitybootstrap "lazymind/core/capability/bootstrap"
	capabilityhttp "lazymind/core/capability/httpadapter"
	"lazymind/core/chat"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/currentmemory"
	"lazymind/core/episode"
	"lazymind/core/evalset"
	"lazymind/core/externallease"
	"lazymind/core/log"
	"lazymind/core/migrate"
	"lazymind/core/modelprovider"
	"lazymind/core/resourceupdate"
	"lazymind/core/scheduler"
	"lazymind/core/state"
	"lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/workflow"
	workflowexecutor "lazymind/core/workflow/executor"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"
)

//go:embed docs.html
var swaggerUIHTML []byte

func backgroundJobsEnabled() bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv("LAZYMIND_BACKGROUND_JOBS_ENABLED")))
	if raw == "" {
		return true
	}
	return raw != "0" && raw != "false" && raw != "no" && raw != "off"
}

func openAPIArtifactExportEnabled() bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv("LAZYMIND_OPENAPI_ARTIFACT_EXPORT_ENABLED")))
	if raw == "" {
		return true
	}
	return raw != "0" && raw != "false" && raw != "no" && raw != "off"
}

func buildCapabilityRuntime() (*capabilitybootstrap.Runtime, error) {
	return capabilitybootstrap.NewRuntime(capabilitybootstrap.Config{
		DB:                        store.DB(),
		LazyDB:                    store.LazyLLMDB(),
		AuthServiceBaseURL:        common.AuthServiceBaseURL(),
		AuthHTTPClient:            &http.Client{Timeout: 10 * time.Second},
		KnowledgeSearchBaseURL:    common.ChatServiceEndpoint(),
		InternalServiceToken:      os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"),
		KnowledgeSearchHTTPClient: &http.Client{Timeout: 60 * time.Second},
		ScanBaseURL:               common.ScanControlPlaneEndpoint(),
		ScanHTTPClient:            &http.Client{Timeout: 10 * time.Second},
	})
}

func exportOpenAPIArtifacts(openAPIJSON []byte) {
	if !openAPIArtifactExportEnabled() {
		return
	}

	wd, err := os.Getwd()
	if err != nil {
		log.Logger.Warn().Err(err).Msg("get working directory failed; skip exporting OpenAPI artifacts")
		return
	}

	var spec map[string]any
	if err := json.Unmarshal(openAPIJSON, &spec); err != nil {
		log.Logger.Warn().Err(err).Msg("decode OpenAPI json failed; skip exporting OpenAPI artifacts")
		return
	}
	openAPIYAML, err := yaml.Marshal(spec)
	if err != nil {
		log.Logger.Warn().Err(err).Msg("marshal OpenAPI yaml failed; skip exporting OpenAPI artifacts")
		return
	}

	outputs := map[string][]byte{
		filepath.Join(wd, "openapi.json"):                                                   openAPIJSON,
		filepath.Join(wd, "swagger.json"):                                                   openAPIJSON,
		filepath.Join(wd, "docs", "swagger.json"):                                           openAPIJSON,
		filepath.Join(wd, "..", "..", "api", "backend", "core", "swagger.json"):             openAPIJSON,
		filepath.Join(wd, "..", "..", "api", "backend", "core", "openapi.yml"):              openAPIYAML,
		filepath.Join(string(filepath.Separator), "openapi-export", "core", "swagger.json"): openAPIJSON,
		filepath.Join(string(filepath.Separator), "openapi-export", "core", "openapi.yml"):  openAPIYAML,
	}
	for path, body := range outputs {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			log.Logger.Warn().Err(err).Str("path", path).Msg("create OpenAPI output directory failed")
			continue
		}
		normalizedBody := append(bytes.TrimRight(body, "\r\n"), '\n')
		if err := os.WriteFile(path, normalizedBody, 0o644); err != nil {
			log.Logger.Warn().Err(err).Str("path", path).Msg("write OpenAPI artifact failed")
			continue
		}
	}
}

// handleAPI textPermissiontext。perms text extract_api_permissions.py text api_permissions.json（Kong RBAC），
// text core text（text Kong + auth-service Authorization）。text gorilla/mux，text path text，text ":action" text。
func handleAPI(r *mux.Router, method, path string, perms []string, h http.HandlerFunc) *mux.Route {
	return r.HandleFunc(path, withMutationRequestAudit(method, path, withExternalAgentLease(h))).Methods(method)
}

func withExternalAgentLease(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := externallease.ValidateRequest(
			r.Context(), store.DB(), externallease.Request{
				Owner: strings.TrimSpace(r.Header.Get("X-User-Id")),
				RunID: r.Header.Get("X-LazyMind-External-Ref"), LeaseToken: r.Header.Get("X-LazyMind-External-Lease"),
				HostID: r.Header.Get("X-LazyMind-External-Host"), ConversationID: r.Header.Get("X-LazyMind-Conversation-Id"),
				Operation: externalAgentOperation(r.Method, r.URL.Path),
			}, time.Now().UTC(),
		); err != nil {
			common.ReplyErr(w, err.Error(), http.StatusConflict)
			return
		}
		next(w, r)
	}
}

func externalAgentOperation(method, path string) externallease.Operation {
	method = strings.ToUpper(strings.TrimSpace(method))
	path = strings.TrimPrefix(strings.TrimSpace(path), "/api/core")
	if method == http.MethodPost && path == "/mcp/capabilities/v1" {
		return externallease.OperationCapabilityRead
	}
	if method == http.MethodPost && strings.HasPrefix(path, "/agent-invocations/") &&
		(strings.HasSuffix(path, ":start") || strings.HasSuffix(path, ":finish")) {
		return externallease.OperationInvocationWrite
	}
	if method == http.MethodGet && (path == "/workflow-runtime/v1/workflows" ||
		strings.HasPrefix(path, "/workflow-runtime/v1/workflows/") || path == "/workflow-sessions" ||
		strings.HasPrefix(path, "/workflow-input-resources/") || strings.HasPrefix(path, "/workflow-artifacts/") ||
		(strings.HasPrefix(path, "/workflow-sessions/") &&
			(strings.HasSuffix(path, "/projection") || strings.HasSuffix(path, "/artifacts")))) {
		return externallease.OperationWorkflowRead
	}
	if method == http.MethodPost && (path == "/workflow-input-resources" || path == "/workflow-preparations" ||
		(strings.HasPrefix(path, "/workflow-preparations/") && strings.HasSuffix(path, ":consume")) ||
		(strings.HasPrefix(path, "/workflow-sessions/") &&
			(strings.HasSuffix(path, ":stop") || strings.HasSuffix(path, ":resume") ||
				strings.HasSuffix(path, ":advance-step-and-hand-off") ||
				(strings.Contains(path, "/hosted-attempts/") &&
					(strings.HasSuffix(path, ":begin") || strings.HasSuffix(path, ":resume") || strings.HasSuffix(path, ":submit")))))) {
		return externallease.OperationWorkflowWrite
	}
	return ""
}

func registerCoreRoutes(r *mux.Router) {
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}).Methods(http.MethodGet)
	handleAPI(r, "GET", "/hello", []string{"user.read"}, func(w http.ResponseWriter, r *http.Request) {
		common.ReplyJSON(w, map[string]string{"message": "Hello from Backend"})
	})
	handleAPI(r, "GET", "/admin", []string{"document.write"}, func(w http.ResponseWriter, r *http.Request) {
		common.ReplyJSON(w, map[string]string{"message": "Admin only area"})
	})
	registerAllRoutes(r)
}

func registerCapabilityMCPRoute(r *mux.Router, handler http.Handler) {
	handleAPI(r, "POST", "/mcp/capabilities/v1", []string{"qa.read"}, handler.ServeHTTP)
	r.Handle("/mcp/capabilities/v1", handler).Methods(http.MethodGet, http.MethodDelete)
}

func registerCapabilityChannelRoutes(
	r *mux.Router,
	handler *capabilityhttp.Handler,
) {
	handleAPI(r, "POST", "/channel-capabilities/cloud-documents:list", []string{"qa.read"}, handler.ListCloudDocuments)
	handleAPI(r, "POST", "/channel-capabilities/cloud-documents:get", []string{"qa.read"}, handler.GetCloudDocument)
	handleAPI(r, "POST", "/channel-capabilities/cloud-documents:search", []string{"qa.read"}, handler.SearchCloudDocuments)
}

func coreListenAddr() string {
	host := strings.TrimSpace(os.Getenv("LAZYMIND_CORE_HOST"))
	port := strings.TrimSpace(os.Getenv("LAZYMIND_CORE_PORT"))
	if port == "" {
		port = "8000"
	}
	if host == "" {
		return ":" + port
	}
	return net.JoinHostPort(host, port)
}

func exportRegisteredOpenAPIArtifacts() error {
	r := mux.NewRouter()
	r.UseEncodedPath()
	registerCoreRoutes(r)

	openAPIJSON, err := buildOpenAPISpecFromRouter(r)
	if err != nil {
		return err
	}
	exportOpenAPIArtifacts(openAPIJSON)
	return nil
}

func validateStartupConfig() error {
	if err := episode.ValidateInternalTokenConfig(); err != nil {
		return err
	}
	_, err := currentmemory.PreferenceIndexMaxItemsFromEnv()
	return err
}

func main() {
	log.Init()

	if len(os.Args) > 1 && os.Args[1] == "--export-openapi" {
		if err := exportRegisteredOpenAPIArtifacts(); err != nil {
			log.Logger.Fatal().Err(err).Msg("export OpenAPI artifacts failed")
		}
		log.Logger.Info().Msg("OpenAPI artifacts exported")
		return
	}
	if err := validateStartupConfig(); err != nil {
		log.Logger.Fatal().Err(err).Msg("invalid Core internal API configuration")
	}

	// textInitialize ACL text（text：postgres/sqlite/mysql）。
	// textSet ACL_DB_DRIVER textDefaulttext sqlite，text ./acl.db。
	driver := os.Getenv("ACL_DB_DRIVER")
	dsn := os.Getenv("ACL_DB_DSN")
	if driver == "" {
		driver = "sqlite"
		dsn = "./acl.db"
	} else if dsn == "" {
		log.Logger.Fatal().Msg("ACL_DB_DRIVER set but ACL_DB_DSN is empty")
	}
	db := orm.MustConnect(driver, dsn)
	if err := migrate.RunUp(); err != nil {
		log.Logger.Fatal().Err(err).Msg("run SQL migrations failed")
	}
	if err := episode.Initialize(db.DB); err != nil {
		log.Logger.Fatal().Err(err).Msg("initialize Episode Memory search failed")
	}
	if err := modelprovider.MigrateLegacyAPIKeys(db.DB); err != nil {
		log.Logger.Fatal().Err(err).Msg("migrate model provider credentials failed")
	}
	catalogPath := filepath.Join(".", "config", "model_catalog.yaml")
	modelprovider.MustSeedModelCatalog(context.Background(), db.DB, catalogPath)
	datasourceCatalogPath := filepath.Join(".", "config", "datasource_catalog.yaml")
	modelprovider.MustSeedDatasourceCatalog(context.Background(), db.DB, datasourceCatalogPath)

	readonlyDriver := strings.TrimSpace(os.Getenv("LAZYMIND_READONLY_DB_DRIVER"))
	readonlyDSN := strings.TrimSpace(os.Getenv("LAZYMIND_READONLY_DB_DSN"))
	if readonlyDriver == "" {
		readonlyDriver = strings.TrimSpace(os.Getenv("LAZYMIND_LAZYLLM_DB_DRIVER"))
	}
	if readonlyDSN == "" {
		readonlyDSN = strings.TrimSpace(os.Getenv("LAZYMIND_LAZYLLM_DB_DSN"))
	}
	readonlyDB := db
	if readonlyDriver != "" || readonlyDSN != "" {
		if readonlyDriver == "" {
			readonlyDriver = driver
		}
		if readonlyDSN == "" {
			log.Logger.Fatal().Msg("LAZYMIND_READONLY_DB_DSN is empty")
		}
		readonlyDB = orm.MustConnect(readonlyDriver, readonlyDSN)
	}

	// Optional: validate readonly external tables at startup.
	// Enable with LAZYMIND_READONLY_VALIDATE=1 and list tables via LAZYMIND_READONLY_TABLES.
	if strings.TrimSpace(os.Getenv("LAZYMIND_READONLY_VALIDATE")) == "1" {
		sqlDB, err := readonlyDB.DB.DB()
		if err != nil {
			log.Logger.Fatal().Err(err).Msg("get readonly sql.DB failed")
		}
		specs := readonlyorm.Specs()
		if len(specs) == 0 {
			log.Logger.Warn().Msg("readonly schema validation enabled but no LAZYMIND_READONLY_TABLES configured; skipping")
		} else if err := readonlyorm.Validate(context.Background(), sqlDB, specs); err != nil {
			log.Logger.Fatal().Err(err).Msg("readonly schema validation failed")
		} else {
			log.Logger.Info().Int("tables", len(specs)).Msg("readonly schema validation ok")
		}
	}
	acl.InitStore(db)
	log.Logger.Info().Str("driver", driver).Msg("ACL store initialized")

	// text/PrompttextInitialize（DB + Redis）。DB text ACL text；Redis textConversationtext/text/text。
	store.Init(db.DB, readonlyDB.DB, store.MustStateFromEnv())
	if err := workflow.SeedBuiltinWorkflows(context.Background(), store.DB()); err != nil {
		log.Logger.Fatal().Err(err).Msg("seed built-in Workflows failed")
	}
	evalset.RegisterAsyncJobs()
	workflow.RegisterWorkflowDraftGenerateJob()
	workflowHosts := workflowexecutor.DefaultHostRegistry
	workflowHosts.RegisterHost("lazymind", workflowexecutor.HostRegistration{
		AllowAllCapabilities: true,
		AllowLegacyTools:     true,
	})
	workflowHosts.RegisterHost("external-agent", workflowexecutor.HostRegistration{
		AllowAllCapabilities: true,
		AllowLegacyTools:     true,
	})
	startBackgroundJobs := backgroundJobsEnabled()
	if !startBackgroundJobs {
		log.Logger.Info().Msg("core background jobs are disabled")
	} else {
		asyncConfig := evalset.LoadAsyncJobRuntimeConfigFromEnv()
		asyncjob.Start(context.Background(), store.DB(), asyncjob.Options{
			Concurrency:  asyncConfig.Concurrency,
			PollInterval: asyncConfig.PollInterval,
			LockTTL:      asyncConfig.LockTTL,
		})
		importConfig := evalset.LoadImportRuntimeConfigFromEnv()
		evalset.StartImportPreviewCleanup(context.Background(), store.DB(), importConfig.CleanupInterval)
		resourceUpdateEnabled := resourceupdate.EnabledFromEnv()
		resourceupdate.LogStartup(resourceUpdateEnabled)
		if resourceUpdateEnabled {
			resourceupdate.Start(context.Background(), store.DB(), store.State(), resourceupdate.DefaultConfig())
		}

		// Mark stale running SubAgent tasks (no heartbeat for >5m) as interrupted on startup.
		if n, err := subagent.MarkInterrupted(context.Background(), store.DB(), 5*time.Minute); err != nil {
			log.Logger.Warn().Err(err).Msg("mark interrupted subagent tasks failed")
		} else if n > 0 {
			log.Logger.Info().Int64("count", n).Msg("marked stale subagent tasks as interrupted")
		}
	}

	// Register plugin lifecycle hooks into the subagent EventHooks.
	workflow.RegisterSubAgentHooks()
	// Wire the conversation SSE hook so plugin events reach the frontend via the
	// conversation-level events channel (history-independent real-time push).
	subagent.EventHooks.RegisterConversationEventHook(
		func(_ context.Context, stateStore state.Store, convID, _ string, eventType string, payload map[string]any) {
			enriched := make(map[string]any, len(payload)+2)
			for k, v := range payload {
				enriched[k] = v
			}
			enriched["event_type"] = eventType
			if _, ok := enriched["conversation_id"]; !ok {
				enriched["conversation_id"] = convID
			}
			_ = chat.AppendConvEvent(context.Background(), stateStore, convID, &chat.ConvEvent{
				Type:    eventType,
				Payload: enriched,
			})
		},
	)
	log.Logger.Info().Msg("plugin subagent hooks registered")

	// Start the schedule ticker.
	if startBackgroundJobs {
		scheduler.RunScheduler(context.Background(), store.DB(), "")
	}

	r := mux.NewRouter()
	r.UseEncodedPath()
	registerCoreRoutes(r)

	// Starttext OpenAPI spec，text doc_swag.go / swag init
	openAPIJSON, err := buildOpenAPISpecFromRouter(r)
	if err != nil {
		log.Logger.Fatal().Err(err).Msg("build OpenAPI spec from router failed")
	}
	exportOpenAPIArtifacts(openAPIJSON)
	r.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(openAPIJSON)
	}).Methods(http.MethodGet)
	r.HandleFunc("/swagger.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(openAPIJSON)
	}).Methods(http.MethodGet)
	r.HandleFunc("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		var m map[string]interface{}
		if err := json.Unmarshal(openAPIJSON, &m); err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "request failed", err), http.StatusInternalServerError)
			return
		}
		out, err := yaml.Marshal(m)
		if err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "request failed", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-yaml")
		w.Write(out)
	}).Methods(http.MethodGet)
	r.HandleFunc("/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(swaggerUIHTML)
	}).Methods(http.MethodGet)

	capabilityRuntime, err := buildCapabilityRuntime()
	if err != nil {
		log.Logger.Fatal().Err(err).Msg("initialize capability MCP failed")
	}
	registerCapabilityMCPRoute(r, capabilityRuntime.MCP)
	channelHandler, err := capabilityhttp.New(capabilityRuntime.Service)
	if err != nil {
		log.Logger.Fatal().Err(err).Msg("initialize channel capability HTTP failed")
	}
	registerCapabilityChannelRoutes(r, channelHandler)
	log.Logger.Info().Str("path", "/mcp/capabilities/v1").Msg("capability MCP enabled")

	listenAddr := coreListenAddr()
	log.Logger.Info().Str("addr", listenAddr).Msg("Core listening")
	if err := http.ListenAndServe(listenAddr, r); err != nil {
		log.Logger.Fatal().Err(err).Msg("http listen failed")
	}
}
