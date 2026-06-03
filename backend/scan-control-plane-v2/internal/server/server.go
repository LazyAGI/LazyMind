package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/access"
	adminservice "github.com/lazymind/scan_control_plane/internal/admin"
	"github.com/lazymind/scan_control_plane/internal/observability"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	sourceengine "github.com/lazymind/scan_control_plane/internal/sourceengine/source"
	taskengine "github.com/lazymind/scan_control_plane/internal/sourceengine/task"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/tree"
)

type Handler struct {
	registry   connector.ConnectorRegistry
	sources    sourceengine.Engine
	targetTree tree.TargetTreeEngine
	sourceTree tree.SourceTreeQueryEngine
	documents  tree.SourceDocumentQuery
	tasks      taskengine.Planner
	taskQuery  taskengine.Query
	admin      *adminservice.Service
	metrics    *observability.Registry
	access     access.Checker
}

type Option func(*Handler)

func NewHandler(options ...Option) http.Handler {
	h := &Handler{}
	for _, option := range options {
		option(h)
	}
	if h.access == nil {
		h.access = unavailableAccessChecker{}
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

func NewHTTPServer(listenAddr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:    listenAddr,
		Handler: handler,
	}
}

func (h *Handler) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /metrics", h.metricsHandler)
	mux.HandleFunc("GET /openapi.json", h.openapi)
	mux.HandleFunc("GET /openapi.yaml", h.openapiYAML)
	mux.HandleFunc("GET /docs", h.docs)
	mux.HandleFunc("GET /api/scan/openapi.json", h.openapi)
	mux.HandleFunc("GET /api/scan/openapi.yaml", h.openapiYAML)
	mux.HandleFunc("GET /api/scan/docs", h.docs)
	mux.HandleFunc("GET /api/scan/connectors", h.listConnectors)
	mux.HandleFunc("POST /api/scan/binding-targets/tree/children", h.listBindingTargetChildren)
	mux.HandleFunc("POST /api/scan/binding-targets/tree/search", h.searchBindingTargets)
	mux.HandleFunc("POST /api/scan/binding-targets/validate", h.validateBindingTarget)
	mux.HandleFunc("POST /api/scan/sources", h.createSource)
	mux.HandleFunc("GET /api/scan/sources", h.listSources)
	mux.HandleFunc("GET /api/scan/sources/{source_id}", h.getSource)
	mux.HandleFunc("PUT /api/scan/sources/{source_id}", h.updateSource)
	mux.HandleFunc("DELETE /api/scan/sources/{source_id}", h.deleteSource)
	mux.HandleFunc("POST /api/scan/sources/{source_id}/bindings", h.createSourceBinding)
	mux.HandleFunc("PUT /api/scan/sources/{source_id}/bindings/{binding_id}", h.updateSourceBinding)
	mux.HandleFunc("DELETE /api/scan/sources/{source_id}/bindings/{binding_id}", h.deleteSourceBinding)
	mux.HandleFunc("POST /api/scan/sources/{source_id}/tree/children", h.listSourceTreeChildren)
	mux.HandleFunc("POST /api/scan/sources/{source_id}/tree/search", h.searchSourceTree)
	mux.HandleFunc("GET /api/scan/sources/{source_id}/documents", h.listSourceDocuments)
	mux.HandleFunc("POST /api/scan/sources/{source_id}/sync", h.triggerSourceSync)
	mux.HandleFunc("GET /api/scan/sources/{source_id}/summary", h.getSourceSummary)
	mux.HandleFunc("POST /api/scan/sources/{source_id}/tasks/generate", h.generateParseTasks)
	mux.HandleFunc("POST /api/scan/sources/{source_id}/tasks/expedite", h.expediteParseTasks)
	mux.HandleFunc("GET /api/scan/parse-tasks", h.listParseTasks)
	mux.HandleFunc("GET /api/scan/parse-tasks/stats", h.getParseTaskStats)
	mux.HandleFunc("GET /api/scan/parse-tasks/{task_id}", h.getParseTask)
	mux.HandleFunc("POST /api/scan/parse-tasks/{task_id}/retry", h.retryParseTask)
	mux.HandleFunc("GET /api/scan/admin/deleting", h.listDeletingResources)
	mux.HandleFunc("GET /api/scan/admin/compensations", h.listCompensations)
	mux.HandleFunc("POST /api/scan/admin/compensations/{operation_id}/retry", h.retryCompensation)
	mux.HandleFunc("GET /api/scan/admin/dead-letters", h.listDeadLetters)
	mux.HandleFunc("POST /api/scan/admin/dead-letters/{dead_letter_id}/retry", h.retryDeadLetter)
	mux.HandleFunc("POST /api/scan/admin/sources/{source_id}/bindings/{binding_id}/reconcile", h.reconcileBinding)
}

func actorFromRequest(r *http.Request) (access.Actor, error) {
	actor := access.Actor{
		UserID:   strings.TrimSpace(r.Header.Get("X-User-ID")),
		TenantID: strings.TrimSpace(r.Header.Get("X-Tenant-ID")),
	}
	if actor.TenantID == "" {
		actor.TenantID = strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	}
	if actor.UserID == "" {
		return access.Actor{}, access.NewError(access.ErrCodeUnauthorized, "missing caller")
	}
	if actor.TenantID == "" {
		return access.Actor{}, access.NewError(access.ErrCodeUnauthorized, "missing tenant")
	}
	return actor, nil
}
