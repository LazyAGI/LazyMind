package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/lazymind/scan_control_plane/internal/access"
	adminservice "github.com/lazymind/scan_control_plane/internal/admin"
	"github.com/lazymind/scan_control_plane/internal/config"
	"github.com/lazymind/scan_control_plane/internal/coreclient"
	"github.com/lazymind/scan_control_plane/internal/dbbootstrap"
	"github.com/lazymind/scan_control_plane/internal/observability"
	"github.com/lazymind/scan_control_plane/internal/server"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector/feishu"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector/localfs"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/crawl"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/schedule"
	sourceengine "github.com/lazymind/scan_control_plane/internal/sourceengine/source"
	stateengine "github.com/lazymind/scan_control_plane/internal/sourceengine/state"
	taskengine "github.com/lazymind/scan_control_plane/internal/sourceengine/task"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/tree"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/worker"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
	_ "github.com/lib/pq"
)

type App struct {
	server  *http.Server
	runtime *Runtime
}

type Components struct {
	Repository                        *store.SQLRepository
	CoreResource                      coreclient.ResourceClient
	CoreClient                        coreclient.Client
	AgentClient                       localfs.AgentClient
	LocalFSDefaultAgentID             string
	LocalFSPublicRoot                 string
	AuthConnectionClient              feishu.AuthConnectionClient
	FeishuClient                      feishu.FeishuClient
	TempObjectStore                   worker.TempObjectStore
	JobQueue                          taskengine.JobQueue
	Scheduler                         *schedule.CheckpointScheduleEngine
	ParseWorkerRunner                 *worker.Runner
	CrawlWorker                       *crawl.RunOnceWorker
	CoreResultRunner                  *worker.ReconcilerRunner
	TempCleanupRunner                 *worker.TempCleanupRunner
	Metrics                           *observability.Registry
	Logger                            *observability.Logger
	ConnectorTypes                    []connector.ConnectorType
	GenerateTasksMaxObjectsPerRequest int
	ParseWorkerGlobalConcurrency      int
	ParseWorkerSourceConcurrency      int
	DefaultDatasetAlgo                coreclient.DatasetAlgo
}

type DBOpener func(driverName, dsn string) (*sql.DB, error)

type BuildOption func(*buildOptions)

type buildOptions struct {
	dbOpener DBOpener
}

type handlerRepository interface {
	access.SourceStore
	sourceengine.SourceRepository
	schedule.Store
	taskengine.Store
	taskengine.QueryStore
	tree.SourceTreeReadRepository
}

func New(cfg config.Config) *App {
	application, err := NewWithConfig(cfg)
	if err != nil {
		panic(err)
	}
	return application
}

func NewWithConfig(cfg config.Config, options ...BuildOption) (*App, error) {
	built, err := Build(cfg, options...)
	if err != nil {
		return nil, err
	}
	handler := newHandlerWithComponents(built)
	return &App{
		server:  server.NewHTTPServer(cfg.ListenAddr(), handler),
		runtime: NewRuntime(built, cfg),
	}, nil
}

func WithDBOpener(opener DBOpener) BuildOption {
	return func(options *buildOptions) {
		if opener != nil {
			options.dbOpener = opener
		}
	}
}

func Build(cfg config.Config, options ...BuildOption) (Components, error) {
	if err := cfg.Validate(); err != nil {
		return Components{}, err
	}
	buildOpts := buildOptions{dbOpener: sql.Open}
	for _, option := range options {
		option(&buildOpts)
	}
	return buildSQLComponents(cfg, buildOpts.dbOpener)
}

func buildSQLComponents(cfg config.Config, opener DBOpener) (Components, error) {
	if opener == nil {
		return Components{}, fmt.Errorf("db opener is required for sql repository")
	}
	adapters, err := buildAdapters(cfg)
	if err != nil {
		return Components{}, err
	}
	db, err := opener("postgres", cfg.DBDSN)
	if err != nil {
		return Components{}, fmt.Errorf("open sql repository: %w", err)
	}
	bootstrapResult, err := dbbootstrap.Bootstrap(context.Background(), db, dbbootstrap.Options{
		MigrationFile: cfg.DBMigrationFile,
	})
	if err != nil {
		_ = db.Close()
		return Components{}, err
	}
	if bootstrapResult.ResetLegacy {
		fmt.Fprintf(os.Stdout, "scan-control-plane reset legacy database and applied migration %s\n", dbbootstrap.BaselineVersion)
	} else if bootstrapResult.AppliedMigration {
		fmt.Fprintf(os.Stdout, "scan-control-plane applied migration %s to empty database\n", dbbootstrap.BaselineVersion)
	}
	if err := applyRuntimeSchemaRepairs(db); err != nil {
		_ = db.Close()
		return Components{}, err
	}
	repo := store.NewSQLRepository(db)
	adapters.Repository = repo
	adapters.JobQueue = taskengine.NewDBJobQueue(repo)
	adapters.Scheduler = buildScheduleEngine(adapters, cfg)
	parseRunner, err := buildParseWorkerRunner(adapters, cfg)
	if err != nil {
		return Components{}, err
	}
	adapters.ParseWorkerRunner = parseRunner
	crawlWorker, err := buildCrawlWorker(adapters, cfg)
	if err != nil {
		return Components{}, err
	}
	adapters.CrawlWorker = crawlWorker
	adapters.CoreResultRunner = buildCoreResultRunner(adapters, cfg)
	adapters.TempCleanupRunner = buildTempCleanupRunner(adapters, cfg)
	return adapters, nil
}

func applyRuntimeSchemaRepairs(db *sql.DB) error {
	if db == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, statement := range []string{
		"ALTER TABLE IF EXISTS public.source_bindings ADD COLUMN IF NOT EXISTS schedule_policy_json jsonb",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply runtime schema repair: %w", err)
		}
	}
	return nil
}

func buildAdapters(cfg config.Config) (Components, error) {
	coreResource, coreWorker, err := buildCoreClients(cfg)
	if err != nil {
		return Components{}, err
	}
	agent, err := buildAgentClient(cfg)
	if err != nil {
		return Components{}, err
	}
	auth, feishuClient, err := buildFeishuClients(cfg)
	if err != nil {
		return Components{}, err
	}
	temp := buildTempObjectStore(cfg)
	connectorTypes := enabledConnectorTypes()
	return Components{
		CoreResource:                      coreResource,
		CoreClient:                        coreWorker,
		AgentClient:                       agent,
		LocalFSDefaultAgentID:             cfg.LocalFSDefaultAgentID,
		LocalFSPublicRoot:                 cfg.LocalFSPublicRoot,
		AuthConnectionClient:              auth,
		FeishuClient:                      feishuClient,
		TempObjectStore:                   temp,
		Metrics:                           observability.NewRegistry(),
		Logger:                            observability.DefaultLogger(),
		ConnectorTypes:                    connectorTypes,
		GenerateTasksMaxObjectsPerRequest: cfg.GenerateTasksMaxObjectsPerRequest,
		ParseWorkerGlobalConcurrency:      cfg.ParseWorkerGlobalConcurrency,
		ParseWorkerSourceConcurrency:      cfg.ParseWorkerSourceConcurrency,
		DefaultDatasetAlgo: coreclient.DatasetAlgo{
			AlgoID:      cfg.DefaultDatasetAlgoID,
			DisplayName: cfg.DefaultDatasetAlgoName,
		},
	}, nil
}

func newHandlerWithComponents(built Components) http.Handler {
	if built.Repository == nil {
		panic("app repository is required")
	}
	var repo handlerRepository = built.Repository
	registry, err := connectorRegistryFromTypes(built.ConnectorTypes, built.AgentClient, built.LocalFSDefaultAgentID, built.LocalFSPublicRoot, built.AuthConnectionClient, built.FeishuClient, built.TempObjectStore)
	if err != nil {
		panic(err)
	}
	metrics := built.Metrics
	if metrics == nil {
		metrics = observability.NewRegistry()
	}
	logger := built.Logger
	if logger == nil {
		logger = observability.DefaultLogger()
	}
	jobQueue := built.JobQueue
	if jobQueue == nil {
		panic("app job queue is required")
	}
	taskPlanner := taskengine.NewDBTaskPlanner(repo, taskengine.WithMaxObjectsPerGenerateRequest(built.GenerateTasksMaxObjectsPerRequest))
	scheduler := built.Scheduler
	if scheduler == nil {
		scheduler = schedule.NewCheckpointScheduleEngine(repo, jobQueue, schedule.WithTaskPlanner(pendingTaskPlanner{planner: taskPlanner}))
	}
	coreResource := built.CoreResource
	if coreResource == nil {
		panic("app core resource client is required")
	}
	sourceEngine := sourceengine.NewDefaultEngine(
		repo,
		registry,
		coreResource,
		scheduler,
		sourceengine.WithDefaultDatasetAlgo(built.DefaultDatasetAlgo),
	)
	taskPlanner.SetManualSyncScheduler(sourceEngine)
	taskQuery := taskengine.NewDBParseTaskQuery(repo)
	limits := tree.TreeQueryLimits{DefaultPageSize: 50, MaxPageSize: 100, MaxAllCurrentLevelItems: 1000}
	sourceTree := tree.NewDBSourceTreeQueryEngine(repo, limits, tree.WithSourceTreeConnectorRegistry(registry))
	documents := tree.NewDBSourceDocumentQuery(repo, limits)
	targetTree := tree.NewDefaultTargetTreeEngine(
		registry,
		tree.WithTargetTreeLimits(limits),
		tree.WithFallbackSearch(tree.NewIndexedTargetTreeFallbackSearch(repo, limits)),
	)
	adminSvc := adminservice.NewService(built.Repository, taskPlanner, coreResource, metrics, logger)
	return server.NewHandler(
		server.WithConnectorRegistry(registry),
		server.WithSourceEngine(sourceEngine),
		server.WithTargetTreeEngine(targetTree),
		server.WithSourceTreeQueryEngine(sourceTree),
		server.WithSourceDocumentQuery(documents),
		server.WithTaskPlanner(taskPlanner),
		server.WithParseTaskQuery(taskQuery),
		server.WithAdminService(adminSvc),
		server.WithMetricsRegistry(metrics),
		server.WithAccessChecker(access.NewDefaultChecker(
			repo,
			access.WithAuthConnectionVerifier(newAuthConnectionVerifier(built.AuthConnectionClient)),
		)),
	)
}

type pendingTaskPlanner struct {
	planner *taskengine.DBTaskPlanner
}

func (p pendingTaskPlanner) GeneratePendingTasks(ctx context.Context, sourceID, bindingID, runID string) error {
	return p.planner.GeneratePendingTasksForRun(ctx, sourceID, bindingID, runID)
}

func connectorRegistryFromTypes(types []connector.ConnectorType, agent localfs.AgentClient, localFSDefaultAgentID, localFSPublicRoot string, auth feishu.AuthConnectionClient, feishuClient feishu.FeishuClient, temp worker.TempObjectStore) (*connector.DefaultConnectorRegistry, error) {
	registry, err := connector.NewDefaultConnectorRegistry()
	if err != nil {
		return nil, err
	}
	for _, connectorType := range types {
		connector, err := connectorForType(connectorType, agent, localFSDefaultAgentID, localFSPublicRoot, auth, feishuClient, temp)
		if err != nil {
			return nil, err
		}
		if err := registry.Register(connector); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func connectorForType(connectorType connector.ConnectorType, agent localfs.AgentClient, localFSDefaultAgentID, localFSPublicRoot string, auth feishu.AuthConnectionClient, feishuClient feishu.FeishuClient, temp worker.TempObjectStore) (connector.SourceConnector, error) {
	switch connectorType {
	case localfs.ConnectorType:
		options := []localfs.Option{
			localfs.WithDefaultAgentID(localFSDefaultAgentID),
			localfs.WithRecommendedRoots("/"),
			localfs.WithTempObjectStore(temp),
		}
		if localFSPublicRoot != "" {
			options = append(options, localfs.WithPublicRoot(localFSPublicRoot))
		}
		return localfs.NewLocalFSConnector(agent, options...), nil
	case feishu.ConnectorType:
		conn := feishu.NewFeishuConnector(auth, feishuClient)
		conn.UseTempObjectStore(temp)
		return conn, nil
	default:
		return nil, fmt.Errorf("unsupported connector type %q", connectorType)
	}
}

func enabledConnectorTypes() []connector.ConnectorType {
	return []connector.ConnectorType{localfs.ConnectorType, feishu.ConnectorType}
}

func buildCoreClients(cfg config.Config) (coreclient.ResourceClient, coreclient.Client, error) {
	client, err := coreclient.NewHTTPCoreClient(cfg.CoreBaseURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("configure core client: %w", err)
	}
	return client, client, nil
}

func buildAgentClient(cfg config.Config) (localfs.AgentClient, error) {
	client, err := localfs.NewHTTPAgentClient(cfg.AgentBaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("configure agent client: %w", err)
	}
	return client, nil
}

func buildFeishuClients(cfg config.Config) (feishu.AuthConnectionClient, feishu.FeishuClient, error) {
	auth, err := feishu.NewHTTPAuthConnectionClient(cfg.AuthServiceBaseURL, cfg.AuthServiceInternalToken, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("configure auth service client: %w", err)
	}
	api, err := feishu.NewDefaultFeishuAPIClient(cfg.FeishuBaseURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("configure feishu client: %w", err)
	}
	return auth, api, nil
}

func buildTempObjectStore(cfg config.Config) worker.TempObjectStore {
	return worker.NewFileTempObjectStore(cfg.TempDir)
}

func buildParseWorkerRunner(built Components, cfg config.Config) (*worker.Runner, error) {
	registry, err := connectorRegistryFromTypes(built.ConnectorTypes, built.AgentClient, built.LocalFSDefaultAgentID, built.LocalFSPublicRoot, built.AuthConnectionClient, built.FeishuClient, built.TempObjectStore)
	if err != nil {
		return nil, err
	}
	reducer := stateengine.NewDBStateReducer(built.Repository)
	parseWorker := worker.NewDefaultParseWorker(
		built.Repository,
		registry,
		built.CoreClient,
		reducer,
		built.TempObjectStore,
		worker.WithLeaseTTL(cfg.WorkerLeaseTTL),
		worker.WithMaxBackoff(cfg.WorkerMaxBackoff),
		worker.WithDeadLetterAfter(cfg.ParseDeadLetterAfter),
	)
	return worker.NewRunner(
		parseWorker,
		worker.WithGlobalConcurrency(cfg.ParseWorkerGlobalConcurrency),
		worker.WithSourceConcurrency(cfg.ParseWorkerSourceConcurrency),
	), nil
}

func buildCrawlWorker(built Components, cfg config.Config) (*crawl.RunOnceWorker, error) {
	registry, err := connectorRegistryFromTypes(built.ConnectorTypes, built.AgentClient, built.LocalFSDefaultAgentID, built.LocalFSPublicRoot, built.AuthConnectionClient, built.FeishuClient, built.TempObjectStore)
	if err != nil {
		return nil, err
	}
	reducer := stateengine.NewDBStateReducer(built.Repository)
	crawler := crawl.NewDefaultCrawlEngine(built.Repository, registry, built.Repository, reducer)
	scheduler := built.Scheduler
	if scheduler == nil {
		scheduler = buildScheduleEngine(built, cfg)
	}
	return crawl.NewRunOnceWorker(built.Repository, crawler, scheduler, crawl.WithRunLeaseTTL(cfg.WorkerLeaseTTL)), nil
}

func buildScheduleEngine(built Components, cfg config.Config) *schedule.CheckpointScheduleEngine {
	return schedule.NewCheckpointScheduleEngine(built.Repository, built.JobQueue, schedule.WithTaskPlanner(pendingTaskPlanner{
		planner: taskengine.NewDBTaskPlanner(built.Repository, taskengine.WithMaxObjectsPerGenerateRequest(cfg.GenerateTasksMaxObjectsPerRequest)),
	}))
}

func buildCoreResultRunner(built Components, cfg config.Config) *worker.ReconcilerRunner {
	reducer := stateengine.NewDBStateReducer(built.Repository)
	reconciler := worker.NewCoreResultReconciler(
		built.Repository,
		built.CoreClient,
		reducer,
		worker.WithReconcilerPollInterval(cfg.CoreResultPollInterval),
		worker.WithReconcilerLeaseTTL(cfg.WorkerLeaseTTL),
	)
	return worker.NewReconcilerRunner(reconciler, cfg.ParseWorkerGlobalConcurrency)
}

func buildTempCleanupRunner(built Components, cfg config.Config) *worker.TempCleanupRunner {
	cleaner, ok := built.TempObjectStore.(worker.TempObjectCleaner)
	if !ok {
		return nil
	}
	return worker.NewTempCleanupRunner(cleaner, cfg.TempTTL)
}

func (a *App) Run(ctx context.Context) error {
	runtimeCtx, stopRuntime := context.WithCancel(ctx)
	if a.runtime != nil {
		a.runtime.Start(runtimeCtx)
	}
	serverErr := make(chan error, 1)
	go func() {
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		stopRuntime()
		if err := a.server.Shutdown(context.Background()); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return ctx.Err()
	case err := <-serverErr:
		stopRuntime()
		return err
	}
}

type Runtime struct {
	workerID             string
	scheduler            dueSyncRunEnqueuer
	parseRunner          *worker.Runner
	crawlWorker          *crawl.RunOnceWorker
	reconcilerRunner     *worker.ReconcilerRunner
	tempCleanupRunner    *worker.TempCleanupRunner
	workerPollInterval   time.Duration
	tempCleanupInterval  time.Duration
	compensationInterval time.Duration
}

type dueSyncRunEnqueuer interface {
	EnqueueDueSyncRuns(ctx context.Context, limit int) ([]schedule.SyncRunIntent, error)
}

const runtimeDueSyncRunLimit = 50

func NewRuntime(built Components, cfg config.Config) *Runtime {
	return &Runtime{
		workerID:             defaultWorkerID(),
		scheduler:            built.Scheduler,
		parseRunner:          built.ParseWorkerRunner,
		crawlWorker:          built.CrawlWorker,
		reconcilerRunner:     built.CoreResultRunner,
		tempCleanupRunner:    built.TempCleanupRunner,
		workerPollInterval:   cfg.WorkerPollInterval,
		tempCleanupInterval:  cfg.TempTTL,
		compensationInterval: cfg.CompensationPollInterval,
	}
}

func (r *Runtime) Start(ctx context.Context) {
	if r == nil {
		return
	}
	var wg sync.WaitGroup
	r.startLoop(ctx, &wg, r.workerPollInterval, func(ctx context.Context) {
		if r.scheduler != nil {
			_, _ = r.scheduler.EnqueueDueSyncRuns(ctx, runtimeDueSyncRunLimit)
		}
		if r.crawlWorker != nil {
			_, _, _ = r.crawlWorker.RunOnce(ctx, r.workerID+"-crawl")
		}
		if r.parseRunner != nil {
			_ = r.parseRunner.RunPending(ctx, r.workerID+"-parse")
		}
		if r.reconcilerRunner != nil {
			_ = r.reconcilerRunner.RunPending(ctx, r.workerID+"-reconcile")
		}
	})
	if r.tempCleanupRunner != nil {
		r.startLoop(ctx, &wg, r.tempCleanupInterval, func(ctx context.Context) {
			_ = r.tempCleanupRunner.RunOnce(ctx)
		})
	}
	go func() {
		<-ctx.Done()
		wg.Wait()
	}()
}

func (r *Runtime) startLoop(ctx context.Context, wg *sync.WaitGroup, interval time.Duration, fn func(context.Context)) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			fn(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func defaultWorkerID() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return "scan-control-plane"
	}
	return "scan-control-plane-" + hostname
}
