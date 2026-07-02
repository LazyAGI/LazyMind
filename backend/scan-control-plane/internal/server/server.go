package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/access"
	adminservice "github.com/lazymind/scan_control_plane/internal/admin"
	"github.com/lazymind/scan_control_plane/internal/observability"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	scheduleengine "github.com/lazymind/scan_control_plane/internal/sourceengine/schedule"
	sourceengine "github.com/lazymind/scan_control_plane/internal/sourceengine/source"
	taskengine "github.com/lazymind/scan_control_plane/internal/sourceengine/task"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/tree"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type Handler struct {
	registry   connector.ConnectorRegistry
	sources    sourceengine.Engine
	targetTree tree.TargetTreeEngine
	sourceTree tree.SourceTreeQueryEngine
	documents  tree.SourceDocumentQuery
	refresher  tree.SourceReadRefresher
	tasks      taskengine.Planner
	taskQuery  taskengine.Query
	admin      *adminservice.Service
	metrics    *observability.Registry
	access     access.Checker
	agents     AgentStore
	scheduler  WatchEventScheduler
	agentToken string
	clock      func() time.Time
}

type Option func(*Handler)

type AgentStore interface {
	UpsertAgent(ctx context.Context, agent store.Agent) error
	ListWatchBindingsForAgentEvent(ctx context.Context, sourceID, agentID string) ([]store.Binding, error)
	ListLocalWatcherBindingsForAgent(ctx context.Context, agentID string) ([]store.Binding, error)
	CreateAgentCommand(ctx context.Context, command store.AgentCommand) error
	ListPendingAgentCommands(ctx context.Context, agentID string, now time.Time, limit int) ([]store.AgentCommand, error)
	AckAgentCommand(ctx context.Context, ack store.AgentCommandAck) error
}

type WatchEventScheduler interface {
	EnqueueWatchEventSync(ctx context.Context, req scheduleengine.WatchEventSyncRequest) (scheduleengine.SyncRunIntent, error)
}

func NewHandler(options ...Option) http.Handler {
	h := &Handler{}
	for _, option := range options {
		option(h)
	}
	if h.access == nil {
		h.access = unavailableAccessChecker{}
	}
	if h.clock == nil {
		h.clock = time.Now
	}
	mux := http.NewServeMux()
	h.registerRoutes(mux)
	return mux
}

type unavailableAccessChecker struct{}

func (unavailableAccessChecker) ListReadableSourceIDs(context.Context, access.Actor) ([]string, error) {
	return nil, access.NewError(access.ErrCodeForbidden, "access checker is not configured")
}

func (unavailableAccessChecker) CanCreateSource(context.Context, access.Actor) error {
	return access.NewError(access.ErrCodeForbidden, "access checker is not configured")
}

func (unavailableAccessChecker) CanReadSource(context.Context, access.Actor, string) error {
	return access.NewError(access.ErrCodeForbidden, "access checker is not configured")
}

func (unavailableAccessChecker) CanWriteSource(context.Context, access.Actor, string) error {
	return access.NewError(access.ErrCodeForbidden, "access checker is not configured")
}

func (unavailableAccessChecker) CanDeleteSource(context.Context, access.Actor, string) error {
	return access.NewError(access.ErrCodeForbidden, "access checker is not configured")
}

func (unavailableAccessChecker) CanReadBinding(context.Context, access.Actor, string, string) error {
	return access.NewError(access.ErrCodeForbidden, "access checker is not configured")
}

func (unavailableAccessChecker) CanWriteBinding(context.Context, access.Actor, string, string) error {
	return access.NewError(access.ErrCodeForbidden, "access checker is not configured")
}

func (unavailableAccessChecker) CanDeleteBinding(context.Context, access.Actor, string, string) error {
	return access.NewError(access.ErrCodeForbidden, "access checker is not configured")
}

func (unavailableAccessChecker) CanReadTask(context.Context, access.Actor, string) error {
	return access.NewError(access.ErrCodeForbidden, "access checker is not configured")
}

func (unavailableAccessChecker) CanWriteTask(context.Context, access.Actor, string) error {
	return access.NewError(access.ErrCodeForbidden, "access checker is not configured")
}

func (unavailableAccessChecker) CanAccessBindingTarget(context.Context, access.Actor, access.BindingTargetRequest) error {
	return access.NewError(access.ErrCodeForbidden, "access checker is not configured")
}

func (unavailableAccessChecker) CanUseAgent(context.Context, access.Actor, string) error {
	return access.NewError(access.ErrCodeForbidden, "access checker is not configured")
}

func (unavailableAccessChecker) CanUseAuthConnection(context.Context, access.Actor, string) error {
	return access.NewError(access.ErrCodeForbidden, "access checker is not configured")
}

func WithConnectorRegistry(registry connector.ConnectorRegistry) Option {
	return func(h *Handler) {
		h.registry = registry
	}
}

func WithSourceEngine(engine sourceengine.Engine) Option {
	return func(h *Handler) {
		h.sources = engine
	}
}

func WithTargetTreeEngine(engine tree.TargetTreeEngine) Option {
	return func(h *Handler) {
		h.targetTree = engine
	}
}

func WithSourceTreeQueryEngine(engine tree.SourceTreeQueryEngine) Option {
	return func(h *Handler) {
		h.sourceTree = engine
	}
}

func WithSourceDocumentQuery(query tree.SourceDocumentQuery) Option {
	return func(h *Handler) {
		h.documents = query
	}
}

func WithSourceReadRefresher(refresher tree.SourceReadRefresher) Option {
	return func(h *Handler) {
		h.refresher = refresher
	}
}

func WithTaskPlanner(planner taskengine.Planner) Option {
	return func(h *Handler) {
		h.tasks = planner
	}
}

func WithParseTaskQuery(query taskengine.Query) Option {
	return func(h *Handler) {
		h.taskQuery = query
	}
}

func WithAdminService(service *adminservice.Service) Option {
	return func(h *Handler) {
		h.admin = service
	}
}

func WithMetricsRegistry(registry *observability.Registry) Option {
	return func(h *Handler) {
		h.metrics = registry
	}
}

func WithAccessChecker(checker access.Checker) Option {
	return func(h *Handler) {
		h.access = checker
	}
}

func WithAgentStore(store AgentStore) Option {
	return func(h *Handler) {
		h.agents = store
	}
}

func WithScheduleEngine(engine WatchEventScheduler) Option {
	return func(h *Handler) {
		h.scheduler = engine
	}
}

func WithAgentToken(token string) Option {
	return func(h *Handler) {
		h.agentToken = strings.TrimSpace(token)
	}
}

func WithClock(clock func() time.Time) Option {
	return func(h *Handler) {
		if clock != nil {
			h.clock = clock
		}
	}
}

func NewHTTPServer(listenAddr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:    listenAddr,
		Handler: handler,
	}
}

// routeAPI registers a route and serves as an annotation anchor for extract_api_permissions.py,
// which parses routeAPI(mux, METHOD, PATH, []string{PERMISSIONS...}, handler) to build api_permissions.json.
// Passing nil skips RBAC (public/internal endpoints); passing []string{} means login-only (no specific perm).
func routeAPI(mux *http.ServeMux, method, path string, _ []string, handler http.HandlerFunc) {
	mux.HandleFunc(method+" "+path, handler)
}

func (h *Handler) registerRoutes(mux *http.ServeMux) {
	// Health / metrics / docs — no RBAC required.
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /metrics", h.metricsHandler)
	mux.HandleFunc("GET /openapi.json", h.openapi)
	mux.HandleFunc("GET /openapi.yaml", h.openapiYAML)
	mux.HandleFunc("GET /docs", h.docs)
	mux.HandleFunc("GET /api/scan/openapi.json", h.openapi)
	mux.HandleFunc("GET /api/scan/openapi.yaml", h.openapiYAML)
	mux.HandleFunc("GET /api/scan/docs", h.docs)

	// File-watcher agent control-plane API. These endpoints use the shared
	// agent bearer token instead of end-user RBAC because they are called by
	// local file-watcher processes.
	mux.HandleFunc("POST /api/v1/agents/register", h.agentRegister)
	mux.HandleFunc("POST /api/v1/agents/heartbeat", h.agentHeartbeat)
	mux.HandleFunc("POST /api/v1/agents/events", h.agentReportEvents)
	mux.HandleFunc("POST /api/v1/agents/pull", h.agentPullCommands)
	mux.HandleFunc("POST /api/v1/agents/commands/ack", h.agentAckCommand)

	// Connectors — read-only metadata, any authenticated user.
	routeAPI(mux, "GET", "/api/scan/connectors", []string{"scan.read"}, h.listConnectors)

	// Binding target tree — used during source creation/editing.
	routeAPI(mux, "POST", "/api/scan/binding-targets/tree/children", []string{"scan.write"}, h.listBindingTargetChildren)
	routeAPI(mux, "POST", "/api/scan/binding-targets/tree/search", []string{"scan.write"}, h.searchBindingTargets)
	routeAPI(mux, "POST", "/api/scan/binding-targets/validate", []string{"scan.write"}, h.validateBindingTarget)

	// Sources CRUD.
	routeAPI(mux, "POST", "/api/scan/sources", []string{"scan.write"}, h.createSource)
	routeAPI(mux, "GET", "/api/scan/sources", []string{"scan.read"}, h.listSources)
	routeAPI(mux, "DELETE", "/api/scan/internal/sources/by-dataset/{dataset_id}", nil, h.deleteSourceByDataset)
	routeAPI(mux, "GET", "/api/scan/sources/{source_id}", []string{"scan.read"}, h.getSource)
	routeAPI(mux, "PUT", "/api/scan/sources/{source_id}", []string{"scan.write"}, h.updateSource)
	routeAPI(mux, "DELETE", "/api/scan/sources/{source_id}", []string{"scan.write"}, h.deleteSource)

	// Bindings.
	routeAPI(mux, "POST", "/api/scan/sources/{source_id}/bindings", []string{"scan.write"}, h.createSourceBinding)
	routeAPI(mux, "PUT", "/api/scan/sources/{source_id}/bindings/{binding_id}", []string{"scan.write"}, h.updateSourceBinding)
	routeAPI(mux, "DELETE", "/api/scan/sources/{source_id}/bindings/{binding_id}", []string{"scan.write"}, h.deleteSourceBinding)

	// Source tree / document browse.
	routeAPI(mux, "POST", "/api/scan/sources/{source_id}/tree/children", []string{"scan.read"}, h.listSourceTreeChildren)
	routeAPI(mux, "POST", "/api/scan/sources/{source_id}/tree/search", []string{"scan.read"}, h.searchSourceTree)
	routeAPI(mux, "GET", "/api/scan/sources/{source_id}/documents", []string{"scan.read"}, h.listSourceDocuments)

	// Source sync / summary / task generation.
	routeAPI(mux, "POST", "/api/scan/sources/{source_id}/sync", []string{"scan.write"}, h.triggerSourceSync)
	routeAPI(mux, "GET", "/api/scan/sources/{source_id}/summary", []string{"scan.read"}, h.getSourceSummary)
	routeAPI(mux, "POST", "/api/scan/sources/{source_id}/tasks/generate", []string{"scan.write"}, h.generateParseTasks)
	routeAPI(mux, "POST", "/api/scan/sources/{source_id}/tasks/expedite", []string{"scan.write"}, h.expediteParseTasks)

	// Parse tasks.
	routeAPI(mux, "GET", "/api/scan/parse-tasks", []string{"scan.read"}, h.listParseTasks)
	routeAPI(mux, "GET", "/api/scan/parse-tasks/stats", []string{"scan.read"}, h.getParseTaskStats)
	routeAPI(mux, "GET", "/api/scan/parse-tasks/{task_id}", []string{"scan.read"}, h.getParseTask)
	routeAPI(mux, "POST", "/api/scan/parse-tasks/{task_id}/retry", []string{"scan.write"}, h.retryParseTask)

	// Admin endpoints — restricted to system-admin role.
	routeAPI(mux, "GET", "/api/scan/admin/deleting", []string{"user.admin"}, h.listDeletingResources)
	routeAPI(mux, "GET", "/api/scan/admin/compensations", []string{"user.admin"}, h.listCompensations)
	routeAPI(mux, "POST", "/api/scan/admin/compensations/{operation_id}/retry", []string{"user.admin"}, h.retryCompensation)
	routeAPI(mux, "GET", "/api/scan/admin/dead-letters", []string{"user.admin"}, h.listDeadLetters)
	routeAPI(mux, "POST", "/api/scan/admin/dead-letters/{dead_letter_id}/retry", []string{"user.admin"}, h.retryDeadLetter)
	routeAPI(mux, "POST", "/api/scan/admin/sources/{source_id}/bindings/{binding_id}/reconcile", []string{"user.admin"}, h.reconcileBinding)
}

func actorFromRequest(r *http.Request) (access.Actor, error) {
	actor := access.Actor{
		UserID:   strings.TrimSpace(r.Header.Get("X-User-ID")),
		TenantID: strings.TrimSpace(r.Header.Get("X-Tenant-ID")),
		Role:     strings.TrimSpace(r.Header.Get("X-User-Role")),
	}
	if actor.TenantID == "" {
		actor.TenantID = strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	}
	if actor.UserID == "" {
		return access.Actor{}, access.NewError(access.ErrCodeUnauthorized, "missing caller")
	}
	return actor, nil
}
