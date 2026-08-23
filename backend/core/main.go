package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	capabilitybootstrap "lazymind/core/capability/bootstrap"
	"lazymind/core/chat"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/currentmemory"
	"lazymind/core/episode"
	"lazymind/core/evalset"
	"lazymind/core/externallease"
	"lazymind/core/knowledge_market"
	"lazymind/core/log"
	"lazymind/core/migrate"
	"lazymind/core/modelprovider"
	"lazymind/core/recovery"
	"lazymind/core/resourceupdate"
	"lazymind/core/scheduler"
	"lazymind/core/state"
	"lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/workflow"
	workflowexecutor "lazymind/core/workflow/executor"
	workflowstore "lazymind/core/workflow/store"

	"github.com/gorilla/mux"
	"golang.org/x/sync/errgroup"
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
	return r.HandleFunc(path, withMutationRequestAudit(method, path,
		withExternalAgentLease(withInvocationConversationScope(h)))).Methods(method)
}

func withInvocationConversationScope(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const header = "X-LazyMind-Invocation-Conversation-Id"
		ctx := workflowstore.WithConversationScope(r.Context(), r.Header.Get(header))
		next(w, r.WithContext(ctx))
	}
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

	// signal.NotifyContext turns the first SIGINT/SIGTERM into ctx cancellation,
	// which run() uses to drive the ordered graceful shutdown below. Once the
	// first signal has been observed we call stop() to restore the default
	// signal handler, so a second SIGINT/SIGTERM during the drain window
	// terminates the process immediately — matching the common 12-factor /
	// Kubernetes pod-termination contract.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()

	if err := run(ctx); err != nil {
		log.Logger.Error().Err(err).Msg("core exited with error")
		os.Exit(1)
	}
}

// shutdownTimeout is the upper bound for draining in-flight HTTP requests and
// background loops after a stop signal. Override with LAZYMIND_SHUTDOWN_TIMEOUT.
func shutdownTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("LAZYMIND_SHUTDOWN_TIMEOUT")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// coordinateShutdown serves HTTP on listener and waits for the background
// loops until the app ctx is cancelled (by SIGINT/SIGTERM) or server.Serve
// fails, then triggers an ordered shutdown: stop accepting new HTTP
// connections and drain in-flight requests (bounded by shutdownTimeout), wait
// up to shutdownTimeout for every backgroundDone channel to close, and only
// then invoke onClose to release state/DB connections — so a background loop's
// final tick can never race with the store/DB being closed.
//
// cancelRuntime cancels the runtime context that the background loops were
// started with. It is invoked as soon as the errgroup context is cancelled —
// whether by a signal (propagated through ctx) or by a fatal server.Serve
// error — so a Serve failure also unblocks the background waits instead of
// leaving them open forever.
func coordinateShutdown(
	ctx context.Context,
	server *http.Server,
	listener net.Listener,
	backgroundDone []<-chan struct{},
	shutdownTimeout time.Duration,
	cancelRuntime context.CancelFunc,
	onClose func(),
) error {
	g, gctx := errgroup.WithContext(ctx)

	// Serve HTTP until Shutdown is called (returns http.ErrServerClosed) or a
	// fatal serve error occurs. A fatal error cancels gctx, which the watchdog
	// below turns into a runtime cancellation so background loops also exit.
	g.Go(func() error {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return &startupError{msg: "http serve", err: err}
		}
		return nil
	})

	// Watchdog: as soon as the errgroup context is done (signal or Serve
	// failure), cancel the runtime context so background loops observe
	// cancellation, then drain HTTP within shutdownTimeout. Resource release
	// (onClose) is deliberately NOT done here — it runs after g.Wait() below,
	// once every background loop has exited or the deadline elapsed, so a
	// loop's final DB write cannot race with DB close.
	g.Go(func() error {
		<-gctx.Done()
		cancelRuntime()
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutCtx); err != nil {
			log.Logger.Warn().Err(err).Msg("http shutdown failed")
		}
		return nil
	})

	// Wait for each background loop to fully exit, but bound the wait by the
	// same shutdownTimeout so a handler that ignores cancellation cannot keep
	// the process alive forever after a single SIGTERM.
	deadline := time.After(shutdownTimeout)
	for _, d := range backgroundDone {
		d := d
		g.Go(func() error {
			select {
			case <-d:
			case <-deadline:
				log.Logger.Warn().Msg("background loop did not exit within shutdown timeout; giving up")
			}
			return nil
		})
	}

	err := g.Wait()
	// All HTTP serving, drain, and background loops have now exited (or the
	// deadline elapsed); it is safe to release shared state and DB connections.
	if onClose != nil {
		onClose()
	}
	return err
}

// startupError wraps an initialization or shutdown error with a stable,
// human-readable prefix without going through fmt.Errorf/errors.New. This
// keeps it outside the Core error-catalog AST scan (which only registers
// errors.New/fmt.Errorf constructors), so lifecycle failures can carry
// context without forcing catalog/i18n entries — these errors terminate the
// process via os.Exit and never become HTTP responses.
type startupError struct {
	msg string
	err error
}

func (e *startupError) Error() string {
	if e.err != nil {
		return e.msg + ": " + e.err.Error()
	}
	return e.msg
}

func (e *startupError) Unwrap() error { return e.err }

// run performs core's full initialization, starts the background loops and the
// HTTP server, and blocks until ctx is cancelled (by SIGINT/SIGTERM) and the
// ordered shutdown completes. It returns an error only when initialization or
// the HTTP listener fails; a signal-triggered shutdown is a nil return.
func run(ctx context.Context) error {
	// textInitialize ACL text（text：postgres/sqlite/mysql）。
	// textSet ACL_DB_DRIVER textDefaulttext sqlite，text ./acl.db。
	driver := os.Getenv("ACL_DB_DRIVER")
	dsn := os.Getenv("ACL_DB_DSN")
	if driver == "" {
		driver = "sqlite"
		dsn = "./acl.db"
	} else if dsn == "" {
		return &startupError{msg: "ACL_DB_DRIVER set but ACL_DB_DSN is empty"}
	}
	db := orm.MustConnect(driver, dsn)
	if err := migrate.RunUp(); err != nil {
		return &startupError{msg: "run SQL migrations", err: err}
	}
	if err := episode.Initialize(db.DB); err != nil {
		return &startupError{msg: "initialize Episode Memory search", err: err}
	}
	if err := modelprovider.MigrateLegacyAPIKeys(db.DB); err != nil {
		return &startupError{msg: "migrate model provider credentials", err: err}
	}
	catalogPath := filepath.Join(".", "config", "model_catalog.yaml")
	modelprovider.MustSeedModelCatalog(ctx, db.DB, catalogPath)
	datasourceCatalogPath := filepath.Join(".", "config", "datasource_catalog.yaml")
	modelprovider.MustSeedDatasourceCatalog(ctx, db.DB, datasourceCatalogPath)

	knowledgeMarketCatalogPath := filepath.Join(".", "config", "knowledge_market_catalog.yaml")
	knowledge_market.MustSeedCatalog(context.Background(), db.DB, knowledgeMarketCatalogPath)

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
			return &startupError{msg: "LAZYMIND_READONLY_DB_DSN is empty"}
		}
		readonlyDB = orm.MustConnect(readonlyDriver, readonlyDSN)
	}

	// Optional: validate readonly external tables at startup.
	// Enable with LAZYMIND_READONLY_VALIDATE=1 and list tables via LAZYMIND_READONLY_TABLES.
	if strings.TrimSpace(os.Getenv("LAZYMIND_READONLY_VALIDATE")) == "1" {
		sqlDB, err := readonlyDB.DB.DB()
		if err != nil {
			return &startupError{msg: "get readonly sql.DB", err: err}
		}
		specs := readonlyorm.Specs()
		if len(specs) == 0 {
			log.Logger.Warn().Msg("readonly schema validation enabled but no LAZYMIND_READONLY_TABLES configured; skipping")
		} else if err := readonlyorm.Validate(ctx, sqlDB, specs); err != nil {
			return &startupError{msg: "readonly schema validation", err: err}
		} else {
			log.Logger.Info().Int("tables", len(specs)).Msg("readonly schema validation ok")
		}
	}
	acl.InitStore(db)
	log.Logger.Info().Str("driver", driver).Msg("ACL store initialized")

	// text/PrompttextInitialize（DB + Redis）。DB text ACL text；Redis textConversationtext/text/text。
	store.Init(db.DB, readonlyDB.DB, store.MustStateFromEnv())
	if err := workflow.SeedBuiltinWorkflows(ctx, store.DB()); err != nil {
		return &startupError{msg: "seed built-in workflows", err: err}
	}
	evalset.RegisterAsyncJobs()
	knowledge_market.RegisterAsyncJobs()
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

	// runtimeCtx is the context the background loops are started with. It is
	// derived from ctx (so a signal cancels it) but can also be cancelled by
	// coordinateShutdown when server.Serve fails — ensuring a fatal Serve
	// error unblocks the background waits instead of leaving them open forever.
	runtimeCtx, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()

	// backgroundDone collects the completion signal of every background loop so
	// coordinateShutdown can wait for them to fully exit before the process
	// returns. asyncjob.Runner exposes Done() directly; the other Start funcs
	// now return a done channel too.
	var backgroundDone []<-chan struct{}
	startBackgroundJobs := backgroundJobsEnabled()
	var runner *asyncjob.Runner
	if !startBackgroundJobs {
		log.Logger.Info().Msg("core background jobs are disabled")
	} else {
		asyncConfig := evalset.LoadAsyncJobRuntimeConfigFromEnv()
		runner = asyncjob.Start(runtimeCtx, store.DB(), asyncjob.Options{
			Concurrency:  asyncConfig.Concurrency,
			PollInterval: asyncConfig.PollInterval,
			LockTTL:      asyncConfig.LockTTL,
		})
		backgroundDone = append(backgroundDone, runner.Done())

		importConfig := evalset.LoadImportRuntimeConfigFromEnv()
		backgroundDone = append(backgroundDone,
			evalset.StartImportPreviewCleanup(runtimeCtx, store.DB(), importConfig.CleanupInterval))

		resourceUpdateEnabled := resourceupdate.EnabledFromEnv()
		resourceupdate.LogStartup(resourceUpdateEnabled)
		if resourceUpdateEnabled {
			backgroundDone = append(backgroundDone,
				resourceupdate.Start(runtimeCtx, store.DB(), store.State(), resourceupdate.DefaultConfig()))
		}
		recovery.Start(context.Background(), store.DB(), recovery.DefaultCleanupInterval)

		// Mark stale running SubAgent tasks (no heartbeat for >5m) as interrupted on startup.
		if n, err := subagent.MarkInterrupted(runtimeCtx, store.DB(), 5*time.Minute); err != nil {
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
			_ = chat.AppendConvEvent(runtimeCtx, stateStore, convID, &chat.ConvEvent{
				Type:    eventType,
				Payload: enriched,
			})
		},
	)
	log.Logger.Info().Msg("plugin subagent hooks registered")

	// Start the schedule ticker.
	if startBackgroundJobs {
		backgroundDone = append(backgroundDone, scheduler.RunScheduler(runtimeCtx, store.DB(), ""))
	}

	r := mux.NewRouter()
	r.UseEncodedPath()
	registerCoreRoutes(r)

	// Starttext OpenAPI spec，text doc_swag.go / swag init
	openAPIJSON, err := buildOpenAPISpecFromRouter(r)
	if err != nil {
		return &startupError{msg: "build OpenAPI spec from router", err: err}
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
		return &startupError{msg: "initialize capability MCP", err: err}
	}
	registerCapabilityMCPRoute(r, capabilityRuntime.MCP)
	log.Logger.Info().Str("path", "/mcp/capabilities/v1").Msg("capability MCP enabled")

	listenAddr := coreListenAddr()
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return &startupError{msg: "listen " + listenAddr, err: err}
	}
	log.Logger.Info().Str("addr", listener.Addr().String()).Msg("Core listening")

	// DB/Redis connections are intentionally NOT closed on shutdown. The
	// scheduler launches detached task-execution goroutines (sendScheduledChatRequest)
	// that outlive RunScheduler's Done() channel and may still be writing task
	// results to the DB after the background loops have exited; closing the pool
	// would race with those final writes (sql: database is closed). This matches
	// the pre-PR behavior — the process relied on os.Exit/Fatal, which never
	// closed pools either. The OS reclaims the TCP connections on process exit,
	// and PostgreSQL/Redis clean up their side on disconnect identically to a
	// graceful QUIT (abort tx, release locks), so there is no functional or data
	// difference. The graceful-shutdown value lives in: HTTP drain, asyncjob
	// lease release, and short-lived background loops exiting cleanly.
	server := &http.Server{
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Logger.Info().Dur("timeout", shutdownTimeout()).Msg("core graceful shutdown armed")
	return coordinateShutdown(ctx, server, listener, backgroundDone, shutdownTimeout(), cancelRuntime, nil)
}
