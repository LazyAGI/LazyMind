package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/lazymind/scan_control_plane/internal/cloudsync/authclient"
	cloudprovider "github.com/lazymind/scan_control_plane/internal/cloudsync/provider"
	"github.com/lazymind/scan_control_plane/internal/cloudsync/provider/feishu"
	"github.com/lazymind/scan_control_plane/internal/coreclient"
	"github.com/lazymind/scan_control_plane/internal/model"
	"github.com/lazymind/scan_control_plane/internal/sourcelayout"
	"github.com/lazymind/scan_control_plane/internal/store"
)

const (
	scanFrontendPrefix  = "/api/scan"
	scanDocsPath        = scanFrontendPrefix + "/docs"
	scanOpenAPIJSONPath = scanFrontendPrefix + "/openapi.json"
	scanOpenAPIYAMLPath = scanFrontendPrefix + "/openapi.yaml"
	scanSwaggerJSONPath = scanFrontendPrefix + "/swagger.json"
	openAPIJSONPath     = "/openapi.json"
	openAPIYAMLPath     = "/openapi.yaml"
	swaggerJSONPath     = "/swagger.json"

	cloudBindingValidationCleanupWindow = 10 * time.Minute
)

type Handler struct {
	store          *store.Store
	merger         EventMerger
	core           coreclient.Client
	coreDatasetID  string
	agentToken     string
	cloudSyncTrig  func(sourceID, runID string) bool
	cloudAuth      cloudTokenClient
	cloudProviders map[string]cloudprovider.Provider
	client         *http.Client
	log            *zap.Logger
}

type EventMerger interface {
	Ingest(events []model.FileEvent)
}

type cloudTokenClient interface {
	GetAccessToken(ctx context.Context, connectionID string) (authclient.TokenResponse, error)
}

type cloudTargetValidationRequest struct {
	Provider         string
	AuthConnectionID string
	TargetType       string
	TargetRef        string
	ProviderOptions  map[string]any
}

func NewHandler(
	st *store.Store,
	merger EventMerger,
	core coreclient.Client,
	coreDatasetID string,
	agentToken string,
	cloudSyncTrigger func(sourceID, runID string) bool,
	cloudAuthBaseURL string,
	cloudAuthInternalToken string,
	cloudHTTPTimeout time.Duration,
	log *zap.Logger,
) *Handler {
	if core == nil {
		core = coreclient.NewNoop()
	}
	cloudAuthClient := authclient.New(cloudAuthBaseURL, cloudAuthInternalToken, cloudHTTPTimeout)
	return &Handler{
		store:         st,
		merger:        merger,
		core:          core,
		coreDatasetID: strings.TrimSpace(coreDatasetID),
		agentToken:    agentToken,
		cloudSyncTrig: cloudSyncTrigger,
		cloudAuth:     cloudAuthClient,
		cloudProviders: map[string]cloudprovider.Provider{
			"feishu": feishu.NewWithLogger(cloudHTTPTimeout, log),
		},
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		log: log,
	}
}

func NewHTTPServer(listenAddr string, h *Handler) *http.Server {
	mux := http.NewServeMux()
	h.registerRoutes(mux)
	handler := h.authMiddleware(mux)
	return &http.Server{
		Addr:         listenAddr,
		Handler:      accessLogMiddleware(h.log, handler),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func accessLogMiddleware(log *zap.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		reqSize := r.ContentLength
		if reqSize < 0 {
			reqSize = 0
		}
		log.Info("http access",
			zap.String("path", r.URL.Path),
			zap.String("method", r.Method),
			zap.Int("status", rec.status),
			zap.Duration("latency", time.Since(startedAt)),
			zap.Int64("request_size", reqSize),
		)
	})
}

func (h *Handler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSpace(r.URL.Path)
		switch {
		case strings.HasPrefix(path, scanFrontendPrefix+"/"):
			if isScanDocsPath(path) {
				next.ServeHTTP(w, r)
				return
			}
			if strings.TrimSpace(r.Header.Get("X-User-Id")) == "" {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing X-User-Id; frontend requests must pass through Kong auth")
				return
			}
		case strings.HasPrefix(path, "/api/v1/agents/"):
			if !h.validateAgentAuthorization(r.Header.Get("Authorization")) {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid agent authorization")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isScanDocsPath(path string) bool {
	switch strings.TrimSpace(path) {
	case scanDocsPath, scanOpenAPIJSONPath, scanOpenAPIYAMLPath, scanSwaggerJSONPath:
		return true
	default:
		return false
	}
}

func (h *Handler) validateAgentAuthorization(rawAuth string) bool {
	expected := strings.TrimSpace(h.agentToken)
	if expected == "" {
		// Keep backward compatibility when agent_token is intentionally unset.
		return true
	}
	rawAuth = strings.TrimSpace(rawAuth)
	if rawAuth == "" {
		return false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(rawAuth, prefix) {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(rawAuth, prefix))
	if got == "" {
		return false
	}
	expectedBytes := []byte(expected)
	gotBytes := []byte(got)
	if len(expectedBytes) != len(gotBytes) {
		return false
	}
	return subtle.ConstantTimeCompare(expectedBytes, gotBytes) == 1
}

func (h *Handler) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /docs", h.docs)
	mux.HandleFunc("GET "+openAPIJSONPath, h.openapiJSON)
	mux.HandleFunc("GET "+swaggerJSONPath, h.openapiJSON)
	mux.HandleFunc("GET "+openAPIYAMLPath, h.openapiYAML)
	mux.HandleFunc("GET "+scanDocsPath, h.docs)
	mux.HandleFunc("GET "+scanOpenAPIJSONPath, h.openapiJSON)
	mux.HandleFunc("GET "+scanSwaggerJSONPath, h.openapiJSON)
	mux.HandleFunc("GET "+scanOpenAPIYAMLPath, h.openapiYAML)

	// Frontend APIs (canonical).
	h.registerFrontendRoutes(mux, scanFrontendPrefix)

	// Agent-facing internal APIs (kept on /api/v1 for file-watcher compatibility).
	mux.HandleFunc("POST /api/v1/agents/register", h.registerAgent)
	mux.HandleFunc("POST /api/v1/agents/heartbeat", h.reportHeartbeat)
	mux.HandleFunc("POST /api/v1/agents/pull", h.pullCommands)
	mux.HandleFunc("POST /api/v1/agents/commands/ack", h.ackCommand)
	mux.HandleFunc("POST /api/v1/agents/snapshots/report", h.reportSnapshot)
	mux.HandleFunc("POST /api/v1/agents/events", h.reportEvents)
	mux.HandleFunc("POST /api/v1/agents/scan-results", h.reportScanResults)
	mux.HandleFunc("POST /api/v1/agents/fs/validate", h.validatePathByAgent)
	mux.HandleFunc("POST /api/v1/agents/fs/tree", h.pathTreeByAgent)
}

func (h *Handler) registerFrontendRoutes(mux *http.ServeMux, prefix string) {
	prefix = strings.TrimRight(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return
	}
	mux.HandleFunc("POST "+prefix+"/sources", h.createSource)
	mux.HandleFunc("POST "+prefix+"/knowledge-bases", h.createKnowledgeBase)
	mux.HandleFunc("POST "+prefix+"/cloud/target/validate", h.validateCloudTarget)
	mux.HandleFunc("GET "+prefix+"/sources", h.listSources)
	mux.HandleFunc("GET "+prefix+"/sources/{id}", h.getSource)
	mux.HandleFunc("PUT "+prefix+"/sources/{id}", h.updateSource)
	mux.HandleFunc("DELETE "+prefix+"/sources/{id}", h.deleteSource)
	mux.HandleFunc("POST "+prefix+"/sources/{id}/enable", h.enableSource)
	mux.HandleFunc("POST "+prefix+"/sources/{id}/disable", h.disableSource)
	mux.HandleFunc("POST "+prefix+"/sources/{id}/cloud/binding", h.upsertCloudBinding)
	mux.HandleFunc("GET "+prefix+"/sources/{id}/cloud/binding", h.getCloudBinding)
	mux.HandleFunc("POST "+prefix+"/sources/{id}/cloud/sync/trigger", h.triggerCloudSync)
	mux.HandleFunc("GET "+prefix+"/sources/{id}/cloud/sync/runs", h.listCloudSyncRuns)
	mux.HandleFunc("POST "+prefix+"/sources/{id}/tasks/generate", h.generateSourceTasks)
	mux.HandleFunc("POST "+prefix+"/sources/{id}/watch/enable", h.enableSourceWatch)
	mux.HandleFunc("POST "+prefix+"/sources/{id}/watch/disable", h.disableSourceWatch)
	mux.HandleFunc("POST "+prefix+"/sources/{id}/tasks/expedite", h.expediteSourceTasks)
	mux.HandleFunc("GET "+prefix+"/sources/{id}/documents", h.listSourceDocuments)
	mux.HandleFunc("GET "+prefix+"/sources/{id}/manual-pull-jobs", h.listManualPullJobs)
	mux.HandleFunc("GET "+prefix+"/parse-tasks", h.listParseTasks)
	mux.HandleFunc("GET "+prefix+"/parse-tasks/stats", h.parseTaskStats)
	mux.HandleFunc("GET "+prefix+"/parse-tasks/{id}", h.getParseTask)
	mux.HandleFunc("POST "+prefix+"/parse-tasks/{id}/retry", h.retryParseTask)
	mux.HandleFunc("GET "+prefix+"/agents", h.listAgents)
	mux.HandleFunc("GET "+prefix+"/agents/{id}", h.getAgent)
	// Frontend helper APIs: proxy path validation/tree via selected agent.
	mux.HandleFunc("POST "+prefix+"/agents/fs/validate", h.validatePathByAgent)
	mux.HandleFunc("POST "+prefix+"/agents/fs/tree", h.pathTreeByAgent)
}

func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) createSource(w http.ResponseWriter, r *http.Request) {
	var req model.CreateSourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.CreateUserID = strings.TrimSpace(r.Header.Get("X-User-Id"))
	if req.CreateUserID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_CURRENT_USER", "missing X-User-Id")
		return
	}
	req.DatasetID = strings.TrimSpace(req.DatasetID)
	if h.core != nil && h.core.Enabled() {
		// In core-task mode each source should have a concrete dataset binding.
		if req.DatasetID == "" {
			req.DatasetID = strings.TrimSpace(h.coreDatasetID)
		}
		if req.DatasetID == "" {
			writeError(w, http.StatusBadRequest, "DATASET_ID_REQUIRED", "dataset_id is required when core is enabled; set source dataset_id or configure core.dataset_id")
			return
		}
	}
	src, err := h.store.CreateSource(r.Context(), req)
	if err != nil {
		if errors.Is(err, store.ErrSourceAlreadyExists) {
			writeError(w, http.StatusConflict, "SOURCE_ALREADY_EXISTS", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "CREATE_SOURCE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, publicSourceModel(src))
}

func (h *Handler) createKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	if h.core == nil || !h.core.Enabled() {
		writeError(w, http.StatusBadRequest, "CORE_DISABLED", "core client is disabled")
		return
	}
	var req model.CreateKnowledgeBaseRequest
	if !decodeJSONStrict(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required")
		return
	}
	if strings.TrimSpace(req.Algo.AlgoID) == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "algo.algo_id is required")
		return
	}
	currentUserID := strings.TrimSpace(r.Header.Get("X-User-Id"))
	currentUserName := strings.TrimSpace(r.Header.Get("X-User-Name"))
	if currentUserID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_CURRENT_USER", "missing X-User-Id")
		return
	}

	result, err := h.core.CreateKnowledgeBase(r.Context(), coreclient.CreateKnowledgeBaseRequest{
		Name:            strings.TrimSpace(req.Name),
		AlgoID:          strings.TrimSpace(req.Algo.AlgoID),
		AlgoDescription: strings.TrimSpace(req.Algo.Description),
		AlgoDisplayName: strings.TrimSpace(req.Algo.DisplayName),
		CurrentUserID:   currentUserID,
		CurrentUserName: currentUserName,
	})
	if err != nil {
		if coreclient.IsConflictError(err) {
			result, ok, reuseErr := h.reuseUnboundScanKnowledgeBase(r.Context(), strings.TrimSpace(req.Name), currentUserID, currentUserName)
			if reuseErr != nil {
				writeError(w, http.StatusInternalServerError, "REUSE_KNOWLEDGE_BASE_FAILED", reuseErr.Error())
				return
			}
			if ok {
				writeJSON(w, http.StatusOK, model.CreateKnowledgeBaseResponse{
					DatasetID: result.DatasetID,
					Name:      result.Name,
				})
				return
			}
			writeError(w, http.StatusConflict, "KNOWLEDGE_BASE_ALREADY_EXISTS", "knowledge base already exists")
			return
		}
		writeError(w, http.StatusBadGateway, "CREATE_KNOWLEDGE_BASE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, model.CreateKnowledgeBaseResponse{
		DatasetID: result.DatasetID,
		Name:      result.Name,
	})
}

func (h *Handler) reuseUnboundScanKnowledgeBase(ctx context.Context, name, userID, userName string) (coreclient.CreateKnowledgeBaseResult, bool, error) {
	if h.core == nil || h.store == nil {
		return coreclient.CreateKnowledgeBaseResult{}, false, nil
	}
	kb, found, err := h.core.FindKnowledgeBaseByName(ctx, name, userID, userName)
	if err != nil {
		return coreclient.CreateKnowledgeBaseResult{}, false, err
	}
	if !found || !kb.ScanManaged || strings.TrimSpace(kb.DatasetID) == "" {
		return coreclient.CreateKnowledgeBaseResult{}, false, nil
	}
	bound, err := h.store.SourceExistsByDatasetID(ctx, kb.DatasetID)
	if err != nil {
		return coreclient.CreateKnowledgeBaseResult{}, false, err
	}
	if bound {
		return coreclient.CreateKnowledgeBaseResult{}, false, nil
	}
	resultName := strings.TrimSpace(kb.Name)
	if resultName == "" {
		resultName = strings.TrimSpace(name)
	}
	return coreclient.CreateKnowledgeBaseResult{
		DatasetID: strings.TrimSpace(kb.DatasetID),
		Name:      resultName,
	}, true, nil
}

func (h *Handler) listSources(w http.ResponseWriter, r *http.Request) {
	tenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	currentUserID := strings.TrimSpace(r.Header.Get("X-User-Id"))
	if currentUserID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_CURRENT_USER", "missing X-User-Id")
		return
	}
	sources, err := h.store.ListSources(r.Context(), tenantID, currentUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_SOURCES_FAILED", err.Error())
		return
	}

	sourceIDs := make([]string, 0, len(sources))
	for _, src := range sources {
		sourceIDs = append(sourceIDs, src.ID)
	}
	bindings, err := h.store.ListCloudSourceBindingsBySourceIDs(r.Context(), sourceIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_SOURCE_BINDINGS_FAILED", err.Error())
		return
	}
	documentOverviews, err := h.store.ListSourceDocumentOverviews(r.Context(), sources)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_SOURCE_DOCUMENTS_FAILED", err.Error())
		return
	}

	items := make([]model.Source, 0, len(sources))
	for _, src := range sources {
		item := publicSourceModel(src)
		if binding, ok := bindings[src.ID]; ok {
			binding := binding
			item.CloudBinding = &binding
		}
		if docs, ok := documentOverviews[src.ID]; ok {
			docs := docs
			docs.Source.RootPath = item.RootPath
			item.Documents = &docs
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) getSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := h.store.GetSource(r.Context(), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "GET_SOURCE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, publicSourceModel(src))
}

func (h *Handler) updateSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.UpdateSourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	src, err := h.store.UpdateSource(r.Context(), id, req)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
			return
		}
		if errors.Is(err, store.ErrSourceAlreadyExists) {
			writeError(w, http.StatusConflict, "SOURCE_ALREADY_EXISTS", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "UPDATE_SOURCE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, publicSourceModel(src))
}

func (h *Handler) deleteSource(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "DELETE_SOURCE_FAILED", "source_id is required")
		return
	}
	src, err := h.store.GetSource(r.Context(), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
			return
		}
		if isBadRequestError(err) {
			writeError(w, http.StatusBadRequest, "DELETE_SOURCE_FAILED", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "DELETE_SOURCE_FAILED", err.Error())
		return
	}
	if h.core != nil && h.core.Enabled() {
		datasetID := strings.TrimSpace(src.DatasetID)
		if datasetID != "" {
			userID := firstNonEmptyString(src.CreateUserID, r.Header.Get("X-User-Id"))
			userName := strings.TrimSpace(r.Header.Get("X-User-Name"))
			if err := h.core.DeleteDataset(r.Context(), datasetID, userID, userName); err != nil {
				writeError(w, http.StatusBadGateway, "DELETE_BOUND_KNOWLEDGE_BASE_FAILED", err.Error())
				return
			}
		}
	}
	if err := h.store.DeleteSource(r.Context(), id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
			return
		}
		if isBadRequestError(err) {
			writeError(w, http.StatusBadRequest, "DELETE_SOURCE_FAILED", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "DELETE_SOURCE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, model.DeleteSourceResponse{
		SourceID: id,
		Deleted:  true,
	})
}

func (h *Handler) enableSource(w http.ResponseWriter, r *http.Request) {
	h.setSourceEnabled(w, r, true)
}

func (h *Handler) disableSource(w http.ResponseWriter, r *http.Request) {
	h.setSourceEnabled(w, r, false)
}

func (h *Handler) setSourceEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id := r.PathValue("id")
	src, err := h.store.SetSourceEnabled(r.Context(), id, enabled)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
			return
		}
		writeError(w, http.StatusBadRequest, "SET_SOURCE_STATUS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, publicSourceModel(src))
}

func (h *Handler) validateCloudTarget(w http.ResponseWriter, r *http.Request) {
	var req model.ValidateCloudTargetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := h.validateCloudTargetConfig(r.Context(), cloudTargetValidationRequest{
		Provider:         req.Provider,
		AuthConnectionID: req.AuthConnectionID,
		TargetType:       req.TargetType,
		TargetRef:        req.TargetRef,
		ProviderOptions:  req.ProviderOptions,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "CLOUD_TARGET_INVALID", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, model.ValidateCloudTargetResponse{Valid: true})
}

func (h *Handler) upsertCloudBinding(w http.ResponseWriter, r *http.Request) {
	sourceID := strings.TrimSpace(r.PathValue("id"))
	var req model.UpsertCloudSourceBindingRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	src, err := h.store.GetSource(r.Context(), sourceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "GET_SOURCE_FAILED", err.Error())
		return
	}
	hadExistingBinding, err := h.validateCloudBindingTarget(r.Context(), sourceID, req)
	if err != nil {
		cleanupErr := h.cleanupNewCloudSourceAfterTargetValidationFailure(r.Context(), r, src, hadExistingBinding)
		msg := err.Error()
		if cleanupErr != nil {
			msg = fmt.Sprintf("%s; cleanup failed: %v", msg, cleanupErr)
		}
		writeError(w, http.StatusBadRequest, "CLOUD_TARGET_INVALID", msg)
		return
	}
	binding, err := h.store.UpsertCloudSourceBinding(r.Context(), sourceID, req)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
			return
		}
		writeError(w, http.StatusBadRequest, "UPSERT_CLOUD_BINDING_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, binding)
}

func (h *Handler) getCloudBinding(w http.ResponseWriter, r *http.Request) {
	sourceID := strings.TrimSpace(r.PathValue("id"))
	binding, err := h.store.GetCloudSourceBinding(r.Context(), sourceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "CLOUD_BINDING_NOT_FOUND", "cloud binding not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "GET_CLOUD_BINDING_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, binding)
}

func (h *Handler) triggerCloudSync(w http.ResponseWriter, r *http.Request) {
	sourceID := strings.TrimSpace(r.PathValue("id"))
	if h.cloudSyncTrig == nil {
		writeError(w, http.StatusServiceUnavailable, "CLOUD_SYNC_DISABLED", "cloud sync runner is disabled")
		return
	}
	req := model.TriggerCloudSyncRequest{}
	if r.ContentLength > 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	run, err := h.store.TriggerCloudSync(r.Context(), sourceID, req)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_OR_BINDING_NOT_FOUND", "source or cloud binding not found")
			return
		}
		if isBadRequestError(err) || strings.Contains(strings.ToLower(err.Error()), "trigger_type") {
			writeError(w, http.StatusBadRequest, "TRIGGER_CLOUD_SYNC_FAILED", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "TRIGGER_CLOUD_SYNC_FAILED", err.Error())
		return
	}
	if h.cloudSyncTrig != nil {
		if ok := h.cloudSyncTrig(sourceID, run.RunID); !ok {
			h.log.Warn("cloud sync trigger queue full; fallback to scheduler",
				zap.String("source_id", sourceID),
				zap.String("run_id", run.RunID),
			)
		}
	}
	if src, srcErr := h.store.GetSource(r.Context(), sourceID); srcErr == nil {
		h.log.Info("cloud sync trigger accepted",
			zap.String("source_id", sourceID),
			zap.String("run_id", run.RunID),
			zap.String("source_root", filepath.Clean(strings.TrimSpace(src.RootPath))),
			zap.String("mirror_root", sourcelayout.CloudMirrorRoot(src.RootPath)),
			zap.String("parse_root", sourcelayout.CloudParseRoot(src.RootPath)),
		)
	}
	writeJSON(w, http.StatusOK, model.TriggerCloudSyncResponse{
		RunID:    run.RunID,
		Accepted: true,
	})
}

func (h *Handler) listCloudSyncRuns(w http.ResponseWriter, r *http.Request) {
	sourceID := strings.TrimSpace(r.PathValue("id"))
	limit := parseIntDefault(r.URL.Query().Get("limit"), 20)
	items, err := h.store.ListCloudSyncRuns(r.Context(), sourceID, limit)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
			return
		}
		if isBadRequestError(err) {
			writeError(w, http.StatusBadRequest, "LIST_CLOUD_SYNC_RUNS_FAILED", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "LIST_CLOUD_SYNC_RUNS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, model.ListCloudSyncRunsResponse{Items: items})
}

func (h *Handler) generateSourceTasks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.GenerateTasksRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if h.core != nil && h.core.Enabled() {
		src, err := h.store.GetSource(r.Context(), id)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "GET_SOURCE_FAILED", err.Error())
			return
		}
		effectiveDatasetID := strings.TrimSpace(src.DatasetID)
		if effectiveDatasetID == "" {
			effectiveDatasetID = strings.TrimSpace(h.coreDatasetID)
		}
		if effectiveDatasetID == "" {
			writeError(w, http.StatusBadRequest, "MISSING_DATASET_BINDING", "source dataset_id is empty; bind dataset to source or configure core.dataset_id")
			return
		}
	}
	resp, err := h.store.GenerateTasksForSource(r.Context(), id, req)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
			return
		}
		writeError(w, http.StatusBadRequest, "GENERATE_TASKS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) enableSourceWatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.EnableWatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	_, err := h.store.EnableSourceWatch(r.Context(), id, req)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
			return
		}
		writeError(w, http.StatusBadRequest, "ENABLE_WATCH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, model.WatchToggleResponse{
		Accepted:        true,
		SkipInitialScan: true,
	})
}

func (h *Handler) disableSourceWatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, queued, err := h.store.DisableSourceWatch(r.Context(), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
			return
		}
		writeError(w, http.StatusBadRequest, "DISABLE_WATCH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, model.WatchToggleResponse{
		Accepted:               true,
		BaselineSnapshotQueued: queued,
	})
}

func (h *Handler) expediteSourceTasks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.ExpediteTasksRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := h.store.ExpediteTasksByPaths(r.Context(), id, req)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
			return
		}
		writeError(w, http.StatusBadRequest, "EXPEDITE_TASKS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listSourceDocuments(w http.ResponseWriter, r *http.Request) {
	sourceID := strings.TrimSpace(r.PathValue("id"))
	req := model.ListSourceDocumentsRequest{
		TenantID:   strings.TrimSpace(r.URL.Query().Get("tenant_id")),
		Keyword:    strings.TrimSpace(r.URL.Query().Get("keyword")),
		UpdateType: strings.TrimSpace(r.URL.Query().Get("update_type")),
		ParseState: strings.TrimSpace(r.URL.Query().Get("parse_state")),
		Page:       parseIntDefault(r.URL.Query().Get("page"), 1),
		PageSize:   parseIntDefault(r.URL.Query().Get("page_size"), 20),
	}
	coreStates := map[string]coreclient.TaskState{}
	if h.core != nil && h.core.Enabled() {
		snapshot, err := h.reconcileSourceCoreTasks(r.Context(), sourceID, req.TenantID)
		if err != nil {
			h.log.Warn("reconcile source core tasks before listing documents failed", zap.Error(err), zap.String("source_id", sourceID))
		} else {
			coreStates = snapshot.states
		}
	}
	resp, err := h.store.ListSourceDocuments(r.Context(), sourceID, req)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
			return
		}
		if isBadRequestError(err) {
			writeError(w, http.StatusBadRequest, "LIST_SOURCE_DOCUMENTS_FAILED", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "LIST_SOURCE_DOCUMENTS_FAILED", err.Error())
		return
	}
	if h.core != nil && h.core.Enabled() {
		if len(coreStates) == 0 {
			pageRefs := sourceDocumentItemsCoreRefs(resp.Items)
			if len(pageRefs) > 0 {
				states, err := h.searchCoreTaskStates(r.Context(), pageRefs)
				if err != nil {
					h.log.Warn("search core tasks for current page failed", zap.Error(err), zap.String("source_id", sourceID))
				} else {
					coreStates = states
				}
			}
		}
		for i := range resp.Items {
			id := strings.TrimSpace(resp.Items[i].CoreTaskID)
			if id == "" {
				continue
			}
			state, ok := coreStates[id]
			if !ok {
				continue
			}
			applyCoreTaskStateToSourceDocumentItem(&resp.Items[i], strings.TrimSpace(state.TaskState))
		}
	}
	if src, srcErr := h.store.GetSource(r.Context(), sourceID); srcErr == nil {
		resp.Source.RootPath = publicSourceModel(src).RootPath
	}
	normalizeSourceDocumentParseStatesForResponse(resp.Items)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listManualPullJobs(w http.ResponseWriter, r *http.Request) {
	sourceID := strings.TrimSpace(r.PathValue("id"))
	req := model.ListManualPullJobsRequest{
		SourceID: sourceID,
		Page:     parseIntDefault(r.URL.Query().Get("page"), 1),
		PageSize: parseIntDefault(r.URL.Query().Get("page_size"), 20),
		Status:   strings.TrimSpace(r.URL.Query().Get("status")),
	}
	resp, err := h.store.ListManualPullJobs(r.Context(), sourceID, req)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
			return
		}
		if isBadRequestError(err) {
			writeError(w, http.StatusBadRequest, "LIST_MANUAL_PULL_JOBS_FAILED", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "LIST_MANUAL_PULL_JOBS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listParseTasks(w http.ResponseWriter, r *http.Request) {
	req := model.ListParseTasksRequest{
		TenantID: strings.TrimSpace(r.URL.Query().Get("tenant_id")),
		SourceID: strings.TrimSpace(r.URL.Query().Get("source_id")),
		Status:   strings.TrimSpace(r.URL.Query().Get("status")),
		Keyword:  strings.TrimSpace(r.URL.Query().Get("keyword")),
		Page:     parseIntDefault(r.URL.Query().Get("page"), 1),
		PageSize: parseIntDefault(r.URL.Query().Get("page_size"), 20),
	}
	if h.core != nil && h.core.Enabled() && strings.TrimSpace(req.SourceID) != "" {
		if _, err := h.reconcileSourceCoreTasks(r.Context(), req.SourceID, req.TenantID); err != nil {
			h.log.Warn("reconcile source core tasks before listing parse tasks failed", zap.Error(err), zap.String("source_id", req.SourceID))
		}
	}
	resp, err := h.store.ListParseTasks(r.Context(), req)
	if err != nil {
		if isBadRequestError(err) {
			writeError(w, http.StatusBadRequest, "LIST_PARSE_TASKS_FAILED", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "LIST_PARSE_TASKS_FAILED", err.Error())
		return
	}
	if h.core != nil && h.core.Enabled() {
		refs := parseTaskItemsCoreRefs(resp.Items)
		if len(refs) > 0 {
			states, err := h.searchCoreTaskStates(r.Context(), refs)
			if err != nil {
				h.log.Warn("search core tasks for parse task list failed", zap.Error(err))
			} else {
				applyCoreTaskStatesToParseTaskItems(resp.Items, states)
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getParseTask(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parsePathInt64(w, r, "id")
	if !ok {
		return
	}
	resp, err := h.store.GetParseTask(r.Context(), taskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "PARSE_TASK_NOT_FOUND", "parse task not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "GET_PARSE_TASK_FAILED", err.Error())
		return
	}
	if h.core != nil && h.core.Enabled() && strings.TrimSpace(resp.CoreTaskID) != "" {
		if _, err := h.reconcileSourceCoreTasks(r.Context(), resp.SourceID, resp.TenantID); err != nil {
			h.log.Warn("reconcile source core tasks before getting parse task failed", zap.Error(err), zap.String("source_id", resp.SourceID))
		}
		refs := parseTaskItemsCoreRefs([]model.ParseTaskListItem{resp.ParseTaskListItem})
		states, err := h.searchCoreTaskStates(r.Context(), refs)
		if err != nil {
			h.log.Warn("search core task for parse task detail failed", zap.Error(err), zap.Int64("task_id", resp.TaskID))
		} else if state, ok := states[strings.TrimSpace(resp.CoreTaskID)]; ok {
			applyCoreTaskStateToParseTaskItem(&resp.ParseTaskListItem, state.TaskState)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) parseTaskStats(w http.ResponseWriter, r *http.Request) {
	tenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	sourceID := strings.TrimSpace(r.URL.Query().Get("source_id"))
	if h.core != nil && h.core.Enabled() && sourceID != "" {
		if _, err := h.reconcileSourceCoreTasks(r.Context(), sourceID, tenantID); err != nil {
			h.log.Warn("reconcile source core tasks before parse task stats failed", zap.Error(err), zap.String("source_id", sourceID))
		}
	}
	counts, err := h.store.CountParseTasksByStatusWithFilter(r.Context(), tenantID, sourceID)
	if err != nil {
		if isBadRequestError(err) {
			writeError(w, http.StatusBadRequest, "GET_PARSE_TASK_STATS_FAILED", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "GET_PARSE_TASK_STATS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, model.ParseTaskStatsResponse{Counts: counts})
}

func (h *Handler) retryParseTask(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parsePathInt64(w, r, "id")
	if !ok {
		return
	}
	resp, err := h.store.RetryParseTask(r.Context(), taskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "PARSE_TASK_NOT_FOUND", "parse task not found")
			return
		}
		if isBadRequestError(err) {
			writeError(w, http.StatusBadRequest, "RETRY_PARSE_TASK_FAILED", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "RETRY_PARSE_TASK_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listAgents(w http.ResponseWriter, r *http.Request) {
	tenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	agents, err := h.store.ListAgents(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_AGENTS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": agents})
}

func (h *Handler) getAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	agent, err := h.store.GetAgent(r.Context(), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "AGENT_NOT_FOUND", "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "GET_AGENT_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (h *Handler) registerAgent(w http.ResponseWriter, r *http.Request) {
	var req model.RegisterAgentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := h.store.RegisterAgent(r.Context(), req); err != nil {
		writeError(w, http.StatusBadRequest, "REGISTER_AGENT_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
}

func (h *Handler) reportHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req model.HeartbeatPayload
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := h.store.UpdateHeartbeat(r.Context(), req); err != nil {
		writeError(w, http.StatusBadRequest, "HEARTBEAT_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
}

func (h *Handler) pullCommands(w http.ResponseWriter, r *http.Request) {
	var req model.PullCommandsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := h.store.PullPendingCommands(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PULL_COMMANDS_FAILED", err.Error())
		return
	}
	if len(resp.Commands) > 0 {
		h.log.Info("commands pulled by agent",
			zap.String("agent_id", req.AgentID),
			zap.Int("count", len(resp.Commands)),
		)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ackCommand(w http.ResponseWriter, r *http.Request) {
	var req model.AckCommandRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := h.store.AckCommand(r.Context(), req); err != nil {
		writeError(w, http.StatusBadRequest, "ACK_COMMAND_FAILED", err.Error())
		return
	}
	h.log.Info("command ack received",
		zap.String("agent_id", req.AgentID),
		zap.Int64("command_id", req.CommandID),
		zap.Bool("success", req.Success),
		zap.String("error", strings.TrimSpace(req.Error)),
	)
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
}

func (h *Handler) reportSnapshot(w http.ResponseWriter, r *http.Request) {
	var req model.ReportSnapshotRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := h.store.ReportSnapshotMetadata(r.Context(), req); err != nil {
		writeError(w, http.StatusBadRequest, "REPORT_SNAPSHOT_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
}

func (h *Handler) reportEvents(w http.ResponseWriter, r *http.Request) {
	var req model.ReportEventsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	h.log.Info("agent events received",
		zap.String("agent_id", req.AgentID),
		zap.Int("count", len(req.Events)),
	)
	if h.merger != nil {
		h.merger.Ingest(req.Events)
	} else {
		if err := h.store.IngestEvents(r.Context(), req); err != nil {
			writeError(w, http.StatusInternalServerError, "INGEST_EVENTS_FAILED", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
}

func (h *Handler) reportScanResults(w http.ResponseWriter, r *http.Request) {
	var req model.ReportScanResultsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	h.log.Info("agent scan results received",
		zap.String("agent_id", req.AgentID),
		zap.String("source_id", req.SourceID),
		zap.String("mode", req.Mode),
		zap.Int("records", len(req.Records)),
	)
	events := make([]model.FileEvent, 0, len(req.Records))
	for _, rec := range req.Records {
		sourceID := strings.TrimSpace(rec.SourceID)
		if sourceID == "" {
			sourceID = strings.TrimSpace(req.SourceID)
		}
		events = append(events, model.FileEvent{
			SourceID:       sourceID,
			EventType:      "modified",
			Path:           rec.Path,
			IsDir:          rec.IsDir,
			OccurredAt:     rec.ModTime,
			OriginType:     rec.OriginType,
			OriginPlatform: rec.OriginPlatform,
			OriginRef:      rec.OriginRef,
			TriggerPolicy:  rec.TriggerPolicy,
		})
	}
	if h.merger != nil {
		if err := h.store.PersistScanResultSnapshotMetadata(r.Context(), req); err != nil {
			writeError(w, http.StatusInternalServerError, "INGEST_SCAN_RESULT_METADATA_FAILED", err.Error())
			return
		}
		h.merger.Ingest(events)
	} else {
		if err := h.store.IngestScanResults(r.Context(), req); err != nil {
			writeError(w, http.StatusInternalServerError, "INGEST_SCAN_RESULTS_FAILED", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
}

func (h *Handler) validatePathByAgent(w http.ResponseWriter, r *http.Request) {
	var req model.AgentPathRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	agent, err := h.store.GetAgent(r.Context(), req.AgentID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "AGENT_NOT_FOUND", "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "GET_AGENT_FAILED", err.Error())
		return
	}
	var resp model.AgentPathValidateResponse
	if err := h.callAgentJSON(r.Context(), agent.ListenAddr, "/api/v1/fs/validate", model.BrowseRequest{Path: req.Path}, &resp); err != nil {
		writeError(w, http.StatusBadGateway, "AGENT_VALIDATE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) pathTreeByAgent(w http.ResponseWriter, r *http.Request) {
	var req model.AgentPathTreeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.MaxDepth <= 0 {
		req.MaxDepth = 2
	}
	if req.MaxDepth > 8 {
		req.MaxDepth = 8
	}

	sourceID := strings.TrimSpace(req.SourceID)
	if sourceID != "" {
		src, err := h.store.GetSource(r.Context(), sourceID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "GET_SOURCE_FAILED", err.Error())
			return
		}
		_, bindErr := h.store.GetCloudSourceBinding(r.Context(), sourceID)
		hasCloudBinding := bindErr == nil
		if bindErr != nil && !errors.Is(bindErr, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusInternalServerError, "GET_CLOUD_BINDING_FAILED", bindErr.Error())
			return
		}
		isCloudSource := hasCloudBinding || sourcelayout.IsCloudOriginType(src.DefaultOriginType)
		if errors.Is(bindErr, gorm.ErrRecordNotFound) && sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
			writeError(w, http.StatusNotFound, "CLOUD_BINDING_NOT_FOUND", "cloud binding not found")
			return
		}

		rootScopePath := filepath.Clean(strings.TrimSpace(src.RootPath))
		if isCloudSource {
			rootScopePath = filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
		}
		treePath := strings.TrimSpace(req.Path)
		if treePath == "" || treePath == "." {
			treePath = rootScopePath
		} else {
			treePath = filepath.Clean(treePath)
			if isCloudSource {
				treePath = sourcelayout.ResolveCloudPublicPath(treePath, sourceID, rootScopePath)
			}
		}
		if !pathInSourceRoot(treePath, rootScopePath) {
			writeError(w, http.StatusBadRequest, "TREE_PATH_INVALID", "path must be inside source.root_path")
			return
		}

		coreSnapshot := sourceCoreTaskSnapshot{states: map[string]coreclient.TaskState{}}
		if h.core != nil && h.core.Enabled() {
			snapshot, err := h.reconcileSourceCoreTasks(r.Context(), sourceID, src.TenantID)
			if err != nil {
				h.log.Warn("reconcile source core tasks before building tree failed", zap.Error(err), zap.String("source_id", sourceID))
			} else {
				coreSnapshot = snapshot
			}
		}

		var (
			treeItems []model.TreeNode
			fileStats map[string]model.TreeFileStat
		)
		if hasCloudBinding {
			treeItems, fileStats, err = h.buildCloudTreeBySourceLive(r.Context(), src, sourceID, treePath, req.MaxDepth, req.IncludeFiles)
			if err != nil {
				switch {
				case errors.Is(err, gorm.ErrRecordNotFound):
					writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
				case errors.Is(err, store.ErrTreePathInvalid):
					writeError(w, http.StatusBadRequest, "TREE_PATH_INVALID", err.Error())
				default:
					writeError(w, http.StatusInternalServerError, "AGENT_TREE_FAILED", err.Error())
				}
				return
			}
		} else {
			agentID := strings.TrimSpace(req.AgentID)
			if agentID == "" {
				agentID = strings.TrimSpace(src.AgentID)
			}
			agent, err := h.store.GetAgent(r.Context(), agentID)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					writeError(w, http.StatusNotFound, "AGENT_NOT_FOUND", "agent not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "GET_AGENT_FAILED", err.Error())
				return
			}

			var treeResp model.AgentPathTreeResponse
			payload := map[string]any{
				"path":          treePath,
				"keyword":       "",
				"max_depth":     req.MaxDepth,
				"include_files": req.IncludeFiles,
			}
			if err := h.callAgentJSON(r.Context(), agent.ListenAddr, "/api/v1/fs/tree", payload, &treeResp); err != nil {
				writeError(w, http.StatusBadGateway, "AGENT_TREE_FAILED", err.Error())
				return
			}
			treeItems = treeResp.Items
			fileStats, err = h.fetchTreeFileStats(r.Context(), agent.ListenAddr, treeItems)
			if err != nil {
				writeError(w, http.StatusBadGateway, "AGENT_TREE_STAT_FAILED", err.Error())
				return
			}
		}
		items, token, err := h.store.BuildTreeUpdateState(r.Context(), sourceID, treeItems, fileStats)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				writeError(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "source not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "BUILD_TREE_STATE_FAILED", err.Error())
			return
		}
		if h.core != nil && h.core.Enabled() && len(coreSnapshot.states) > 0 {
			items = applyCoreTaskStatesToTreeNodes(items, coreSnapshot.refs, coreSnapshot.states)
		}
		if req.ChangesOnly || req.UpdatedOnly {
			items = filterTreeToChanged(items)
		}
		items = filterTreeByKeyword(items, req.Keyword)
		items = normalizeTreeParseQueueStatesForResponse(items)
		writeJSON(w, http.StatusOK, model.AgentPathTreeResponse{
			Items:          items,
			SelectionToken: token,
		})
		return
	}

	agent, err := h.store.GetAgent(r.Context(), req.AgentID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "AGENT_NOT_FOUND", "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "GET_AGENT_FAILED", err.Error())
		return
	}

	var treeResp model.AgentPathTreeResponse
	payload := map[string]any{
		"path":          req.Path,
		"keyword":       req.Keyword,
		"max_depth":     req.MaxDepth,
		"include_files": req.IncludeFiles,
	}
	if err := h.callAgentJSON(r.Context(), agent.ListenAddr, "/api/v1/fs/tree", payload, &treeResp); err != nil {
		writeError(w, http.StatusBadGateway, "AGENT_TREE_FAILED", err.Error())
		return
	}
	treeResp.Items = filterTreeByKeyword(treeResp.Items, req.Keyword)
	writeJSON(w, http.StatusOK, treeResp)
}

func (h *Handler) callAgentJSON(ctx context.Context, baseURL, apiPath string, reqBody, out any) error {
	if strings.TrimSpace(baseURL) == "" {
		return fmt.Errorf("empty agent listen_addr")
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + apiPath)
	if err != nil {
		return fmt.Errorf("invalid agent url: %w", err)
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.agentToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.agentToken)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("agent returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	return decodeJSONInternal(w, r, out, true)
}

func decodeJSONStrict(w http.ResponseWriter, r *http.Request, out any) bool {
	return decodeJSONInternal(w, r, out, true)
}

func decodeJSONInternal(w http.ResponseWriter, r *http.Request, out any, strict bool) bool {
	dec := json.NewDecoder(r.Body)
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON: "+err.Error())
		return false
	}
	// Reject trailing garbage to keep request validation deterministic.
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON: multiple JSON values are not allowed")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, model.ErrorResponse{Code: code, Message: msg})
}

func parseIntDefault(raw string, fallback int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func parsePathInt64(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := strings.TrimSpace(r.PathValue(name))
	if raw == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", fmt.Sprintf("%s is required", name))
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", fmt.Sprintf("invalid %s", name))
		return 0, false
	}
	return id, true
}

func publicSourceModel(src model.Source) model.Source {
	if sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		src.RootPath = sourcelayout.CloudPublicRoot(src.ID)
	}
	return src
}

func isBadRequestError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "required") ||
		strings.Contains(msg, "must be >") ||
		strings.Contains(msg, "does not support retry")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (h *Handler) validateCloudBindingTarget(ctx context.Context, sourceID string, req model.UpsertCloudSourceBindingRequest) (bool, error) {
	targetReq := cloudTargetValidationRequest{
		Provider:         req.Provider,
		AuthConnectionID: req.AuthConnectionID,
		TargetType:       req.TargetType,
		TargetRef:        req.TargetRef,
		ProviderOptions:  req.ProviderOptions,
	}
	hadExistingBinding := false
	existing, err := h.store.GetCloudSourceBinding(ctx, sourceID)
	if err == nil {
		hadExistingBinding = true
		if strings.TrimSpace(targetReq.Provider) == "" {
			targetReq.Provider = existing.Provider
		}
		if strings.TrimSpace(targetReq.AuthConnectionID) == "" {
			targetReq.AuthConnectionID = existing.AuthConnectionID
		}
		if strings.TrimSpace(targetReq.TargetType) == "" {
			targetReq.TargetType = existing.TargetType
		}
		if strings.TrimSpace(targetReq.TargetRef) == "" {
			targetReq.TargetRef = existing.TargetRef
		}
		if targetReq.ProviderOptions == nil {
			targetReq.ProviderOptions = existing.ProviderOptions
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, fmt.Errorf("load existing cloud binding failed: %w", err)
	}
	return hadExistingBinding, h.validateCloudTargetConfig(ctx, targetReq)
}

func (h *Handler) validateCloudTargetConfig(ctx context.Context, req cloudTargetValidationRequest) error {
	providerName := strings.ToLower(strings.TrimSpace(req.Provider))
	if providerName == "" {
		return fmt.Errorf("provider is required")
	}
	authConnectionID := strings.TrimSpace(req.AuthConnectionID)
	if authConnectionID == "" {
		return fmt.Errorf("auth_connection_id is required")
	}
	if h.cloudAuth == nil {
		return fmt.Errorf("cloud auth client is not configured")
	}
	impl := h.cloudProviders[providerName]
	if impl == nil {
		return fmt.Errorf("unsupported cloud provider: %s", providerName)
	}

	tokenResp, err := h.cloudAuth.GetAccessToken(ctx, authConnectionID)
	if err != nil {
		return fmt.Errorf("acquire cloud access token failed: %w", err)
	}
	accessToken := strings.TrimSpace(tokenResp.AccessToken)
	if accessToken == "" {
		return fmt.Errorf("auth token response missing access_token")
	}

	validateReq := cloudprovider.ListRequest{
		AccessToken:     accessToken,
		TargetType:      req.TargetType,
		TargetRef:       req.TargetRef,
		ProviderOptions: req.ProviderOptions,
	}
	if validator, ok := impl.(cloudprovider.TargetValidator); ok {
		if err := validator.ValidateTarget(ctx, validateReq); err != nil {
			return fmt.Errorf("%s target validation failed: %w", providerName, err)
		}
	} else if _, err := impl.ListObjects(ctx, validateReq); err != nil {
		return fmt.Errorf("%s target validation failed: %w", providerName, err)
	}
	if h.log != nil {
		h.log.Info("cloud target validated",
			zap.String("provider", providerName),
			zap.String("auth_connection_id", authConnectionID),
			zap.String("target_type", strings.TrimSpace(req.TargetType)),
			zap.String("target_ref", strings.TrimSpace(req.TargetRef)),
		)
	}
	return nil
}

func (h *Handler) cleanupNewCloudSourceAfterTargetValidationFailure(
	ctx context.Context,
	r *http.Request,
	src model.Source,
	hadExistingBinding bool,
) error {
	if !shouldCleanupNewCloudSourceAfterTargetValidationFailure(src, hadExistingBinding, time.Now().UTC(), strings.TrimSpace(r.Header.Get("X-User-Id"))) {
		return nil
	}

	var cleanupErrs []string
	datasetID := strings.TrimSpace(src.DatasetID)
	if h.core != nil && h.core.Enabled() && datasetID != "" {
		userID := firstNonEmptyString(src.CreateUserID, r.Header.Get("X-User-Id"))
		userName := strings.TrimSpace(r.Header.Get("X-User-Name"))
		if err := h.core.DeleteDataset(ctx, datasetID, userID, userName); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Sprintf("delete dataset %s: %v", datasetID, err))
		}
	}
	if err := h.store.DeleteSource(ctx, src.ID); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Sprintf("delete source %s: %v", src.ID, err))
	}
	if len(cleanupErrs) > 0 {
		if h.log != nil {
			h.log.Warn("cleanup newly-created cloud source after target validation failure failed",
				zap.String("source_id", strings.TrimSpace(src.ID)),
				zap.String("dataset_id", datasetID),
				zap.Strings("errors", cleanupErrs),
			)
		}
		return errors.New(strings.Join(cleanupErrs, "; "))
	}
	if h.log != nil {
		h.log.Info("cleaned newly-created cloud source after target validation failure",
			zap.String("source_id", strings.TrimSpace(src.ID)),
			zap.String("dataset_id", datasetID),
		)
	}
	return nil
}

func shouldCleanupNewCloudSourceAfterTargetValidationFailure(src model.Source, hadExistingBinding bool, now time.Time, currentUserID string) bool {
	if hadExistingBinding {
		return false
	}
	if strings.TrimSpace(src.ID) == "" {
		return false
	}
	if !sourcelayout.IsCloudOriginType(src.DefaultOriginType) && !strings.EqualFold(strings.TrimSpace(src.SourceType), "cloud_sync") {
		return false
	}
	if strings.TrimSpace(src.CreateUserID) != "" && strings.TrimSpace(currentUserID) != "" && strings.TrimSpace(src.CreateUserID) != strings.TrimSpace(currentUserID) {
		return false
	}
	if src.CreatedAt.IsZero() {
		return false
	}
	age := now.Sub(src.CreatedAt.UTC())
	return age >= 0 && age <= cloudBindingValidationCleanupWindow
}

func pathInSourceRoot(path, root string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	root = filepath.Clean(strings.TrimSpace(root))
	if path == "" || path == "." || root == "" || root == "." {
		return false
	}
	if root == string(filepath.Separator) {
		return strings.HasPrefix(path, string(filepath.Separator))
	}
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

func (h *Handler) buildCloudTreeBySourceLive(
	ctx context.Context,
	src model.Source,
	sourceID, treePath string,
	maxDepth int,
	includeFiles bool,
) ([]model.TreeNode, map[string]model.TreeFileStat, error) {
	if h.cloudAuth == nil {
		return nil, nil, fmt.Errorf("cloud auth client is not configured")
	}
	binding, err := h.store.GetCloudSourceBinding(ctx, sourceID)
	if err != nil {
		return nil, nil, err
	}
	providerName := strings.ToLower(strings.TrimSpace(binding.Provider))
	impl := h.cloudProviders[providerName]
	if impl == nil {
		return nil, nil, fmt.Errorf("unsupported cloud provider: %s", binding.Provider)
	}

	tokenResp, err := h.cloudAuth.GetAccessToken(ctx, binding.AuthConnectionID)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire cloud access token failed: %w", err)
	}
	accessToken := strings.TrimSpace(tokenResp.AccessToken)
	if accessToken == "" {
		return nil, nil, fmt.Errorf("acquire cloud access token failed: empty access_token")
	}
	if h.log != nil {
		expiresAt := ""
		if tokenResp.ExpiresAt != nil && !tokenResp.ExpiresAt.IsZero() {
			expiresAt = tokenResp.ExpiresAt.UTC().Format(time.RFC3339)
		}
		h.log.Info("cloud tree access token acquired",
			zap.String("source_id", sourceID),
			zap.String("provider", providerName),
			zap.String("auth_connection_id", strings.TrimSpace(binding.AuthConnectionID)),
			zap.String("token_provider", strings.TrimSpace(tokenResp.Provider)),
			zap.String("token_status", strings.TrimSpace(tokenResp.Status)),
			zap.Int("access_token_len", len(accessToken)),
			zap.String("access_token_expires_at", expiresAt),
		)
	}
	objects, err := impl.ListObjects(ctx, cloudprovider.ListRequest{
		AccessToken:     accessToken,
		TargetType:      binding.TargetType,
		TargetRef:       binding.TargetRef,
		ProviderOptions: binding.ProviderOptions,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list remote cloud objects failed: %w", err)
	}
	remoteObjectDetails, remoteObjectsOmitted := describeCloudObjectsForLog(objects, 500)
	if h.log != nil {
		h.log.Info("cloud tree remote list fetched",
			zap.String("source_id", sourceID),
			zap.String("provider", providerName),
			zap.String("target_type", strings.TrimSpace(binding.TargetType)),
			zap.String("target_ref", strings.TrimSpace(binding.TargetRef)),
			zap.Int("remote_objects_total", len(objects)),
			zap.Int("remote_objects_omitted", remoteObjectsOmitted),
			zap.Strings("remote_objects", remoteObjectDetails),
			zap.Strings("include_patterns", binding.IncludePatterns),
			zap.Strings("exclude_patterns", binding.ExcludePatterns),
		)
	}

	indexRows, err := h.store.ListCloudObjectIndex(ctx, sourceID)
	if err != nil {
		return nil, nil, fmt.Errorf("load cloud object index failed: %w", err)
	}
	existingByID := make(map[string]store.CloudObjectIndexRecord, len(indexRows))
	pathOwner := make(map[string]string, len(indexRows))
	for _, row := range indexRows {
		id := strings.TrimSpace(row.ExternalObjectID)
		if id == "" {
			continue
		}
		existingByID[id] = row
		if row.IsDeleted {
			continue
		}
		rel := strings.Trim(strings.ReplaceAll(strings.TrimSpace(row.LocalRelPath), "\\", "/"), "/")
		if rel != "" {
			pathOwner[rel] = id
		}
	}
	remoteParentIDs := make(map[string]struct{}, len(objects))
	for _, obj := range objects {
		parentID := strings.TrimSpace(obj.ExternalParentID)
		if parentID != "" {
			remoteParentIDs[parentID] = struct{}{}
		}
	}

	rootPath := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	if h.log != nil {
		h.log.Info("cloud tree scope resolved",
			zap.String("source_id", sourceID),
			zap.String("source_root", filepath.Clean(strings.TrimSpace(src.RootPath))),
			zap.String("mirror_root", rootPath),
			zap.String("requested_tree_path", treePath),
		)
	}
	nodeMap := make(map[string]*model.TreeNode, len(objects))
	childMap := make(map[string]map[string]struct{}, len(objects))
	fileStats := make(map[string]model.TreeFileStat, len(objects))
	now := time.Now().UTC()
	seenIndexIDs := make(map[string]struct{}, len(objects))
	indexUpserts := make([]store.CloudObjectIndexRecord, 0, len(objects))
	hasScopedObject := false
	pathIsFile := false
	filteredByPattern := 0
	keptByDirPassthrough := 0
	keptByIncludePattern := 0
	keptByNoIncludeRules := 0
	droppedByIncludeMiss := 0
	droppedByExcludeMatch := 0
	filteredByRootScope := 0
	filteredByTreeScope := 0
	filteredByDepth := 0
	filteredByIncludeFiles := 0
	addedNodeCount := 0
	filteredPatternSamples := make([]string, 0, 4)
	passedPatternSamples := make([]string, 0, 4)
	decisionSamples := make([]string, 0, 12)

	for _, obj := range objects {
		objectID := strings.TrimSpace(obj.ExternalObjectID)
		if objectID != "" {
			seenIndexIDs[objectID] = struct{}{}
		}
		decision := cloudIncludeObjectDecision(obj, binding.IncludePatterns, binding.ExcludePatterns)
		decisionSamples = appendCloudFilterDecisionSample(decisionSamples, obj, decision, 12)
		if !decision.Include {
			filteredByPattern++
			filteredPatternSamples = appendCloudObjectSample(filteredPatternSamples, obj, 4)
			switch decision.Reason {
			case "include_not_matched":
				droppedByIncludeMiss++
			case "excluded_by_pattern":
				droppedByExcludeMatch++
			}
			continue
		}
		passedPatternSamples = appendCloudObjectSample(passedPatternSamples, obj, 4)
		switch decision.Reason {
		case "directory_passthrough":
			keptByDirPassthrough++
		case "included_by_pattern":
			keptByIncludePattern++
		default:
			keptByNoIncludeRules++
		}
		if objectID == "" {
			continue
		}
		_, hasRemoteChild := remoteParentIDs[objectID]
		kind, providerMeta := cloudObjectKindAndMeta(obj, existingByID, hasRemoteChild)
		isDir := cloudIsDirKind(kind)
		pathObject := obj
		pathObject.ProviderMeta = providerMeta
		objectPath, relPath := cloudResolveObjectPath(rootPath, pathObject, kind, existingByID, pathOwner)
		if objectPath == "" {
			continue
		}
		if relPath != "" {
			pathOwner[relPath] = objectID
		}
		if !pathInSourceRoot(objectPath, rootPath) {
			filteredByRootScope++
			continue
		}
		externalVersion := strings.TrimSpace(obj.ExternalVersion)
		sizeBytes := obj.SizeBytes
		checksum := externalVersion
		if existing, ok := existingByID[objectID]; ok {
			if externalVersion == "" {
				externalVersion = strings.TrimSpace(existing.ExternalVersion)
			}
			if strings.TrimSpace(existing.Checksum) != "" {
				checksum = strings.TrimSpace(existing.Checksum)
			}
			if sizeBytes <= 0 {
				sizeBytes = existing.SizeBytes
			}
		}
		indexUpserts = append(indexUpserts, store.CloudObjectIndexRecord{
			SourceID:           sourceID,
			Provider:           providerName,
			ExternalObjectID:   objectID,
			ExternalParentID:   strings.TrimSpace(obj.ExternalParentID),
			ExternalPath:       strings.TrimSpace(obj.ExternalPath),
			ExternalName:       strings.TrimSpace(obj.ExternalName),
			ExternalKind:       kind,
			ExternalVersion:    externalVersion,
			ExternalModifiedAt: obj.ExternalModifiedAt,
			LocalRelPath:       strings.TrimSpace(relPath),
			LocalAbsPath:       objectPath,
			Checksum:           checksum,
			SizeBytes:          sizeBytes,
			IsDeleted:          false,
			LastSyncedAt:       &now,
			ProviderMeta:       providerMeta,
		})

		treeObjectPath := cloudObjectTreePath(objectPath, kind, providerMeta)
		if treeObjectPath == "" || treeObjectPath == "." || !pathInSourceRoot(treeObjectPath, rootPath) {
			filteredByRootScope++
			continue
		}

		treeIsDir := isDir && !cloudWikiPageShouldFold(objectPath, kind, providerMeta)
		if treeObjectPath == treePath {
			hasScopedObject = true
			if !isDir && treeObjectPath == objectPath {
				pathIsFile = true
			} else if treeIsDir {
				cloudEnsureNode(nodeMap, childMap, treeObjectPath, true, cloudObjectDisplayTitle(treeObjectPath, obj, true), "")
			}
		}
		if !pathInSourceRoot(treeObjectPath, treePath) {
			filteredByTreeScope++
			continue
		}
		hasScopedObject = true

		depth := cloudTreeRelativeDepth(treePath, treeObjectPath)
		if depth < 0 {
			continue
		}
		if depth > 0 {
			cloudEnsureAncestorNodes(nodeMap, childMap, treePath, treeObjectPath, maxDepth)
		}
		if depth == 0 {
			continue
		}
		if depth > maxDepth {
			filteredByDepth++
			continue
		}
		if !isDir && !includeFiles {
			filteredByIncludeFiles++
			continue
		}

		externalFileID := ""
		if !isDir {
			externalFileID = objectID
			stat := model.TreeFileStat{
				Path:     treeObjectPath,
				IsDir:    false,
				Size:     obj.SizeBytes,
				Checksum: strings.TrimSpace(obj.ExternalVersion),
			}
			if existing, ok := existingByID[objectID]; ok {
				if stat.Size <= 0 {
					stat.Size = existing.SizeBytes
				}
				if strings.TrimSpace(stat.Checksum) == "" {
					stat.Checksum = strings.TrimSpace(existing.Checksum)
				}
				if obj.ExternalModifiedAt == nil && existing.ExternalModifiedAt != nil {
					mt := existing.ExternalModifiedAt.UTC()
					stat.ModTime = &mt
				}
			}
			if obj.ExternalModifiedAt != nil && !obj.ExternalModifiedAt.IsZero() {
				mt := obj.ExternalModifiedAt.UTC()
				stat.ModTime = &mt
			}
			fileStats[treeObjectPath] = stat
		}
		parentTreePath := ""
		if parentID := strings.TrimSpace(obj.ExternalParentID); parentID != "" {
			parentTreePath = strings.TrimSpace(cloudTreePathByObjectID(rootPath, parentID, objects, existingByID, pathOwner))
		}
		if treeObjectPath == objectPath {
			cloudEnsureNodeWithParent(nodeMap, childMap, treeObjectPath, parentTreePath, treeIsDir, cloudObjectDisplayTitle(treeObjectPath, obj, treeIsDir), externalFileID)
		} else {
			cloudEnsureNodeWithParent(nodeMap, childMap, treeObjectPath, parentTreePath, false, cloudObjectDisplayTitle(treeObjectPath, obj, false), externalFileID)
		}
		addedNodeCount++
	}

	if h.log != nil {
		h.log.Info("cloud tree build summary",
			zap.String("source_id", sourceID),
			zap.String("provider", providerName),
			zap.String("tree_path", treePath),
			zap.Int("remote_total", len(objects)),
			zap.Int("filtered_by_pattern", filteredByPattern),
			zap.Int("kept_by_directory_passthrough", keptByDirPassthrough),
			zap.Int("kept_by_include_pattern", keptByIncludePattern),
			zap.Int("kept_without_include_rules", keptByNoIncludeRules),
			zap.Int("dropped_by_include_not_matched", droppedByIncludeMiss),
			zap.Int("dropped_by_exclude_matched", droppedByExcludeMatch),
			zap.Int("filtered_by_root_scope", filteredByRootScope),
			zap.Int("filtered_by_tree_scope", filteredByTreeScope),
			zap.Int("filtered_by_depth", filteredByDepth),
			zap.Int("filtered_by_include_files", filteredByIncludeFiles),
			zap.Int("added_nodes", addedNodeCount),
			zap.Strings("sample_filtered_by_pattern", filteredPatternSamples),
			zap.Strings("sample_passed_pattern", passedPatternSamples),
			zap.Strings("sample_filter_decisions", decisionSamples),
		)
		if len(objects) > 0 && addedNodeCount == 0 {
			h.log.Warn("cloud tree empty after filtering",
				zap.String("source_id", sourceID),
				zap.String("provider", providerName),
				zap.String("tree_path", treePath),
				zap.Strings("include_patterns", binding.IncludePatterns),
				zap.Strings("exclude_patterns", binding.ExcludePatterns),
				zap.Strings("sample_filtered_by_pattern", filteredPatternSamples),
				zap.Strings("sample_passed_pattern", passedPatternSamples),
			)
		}
	}
	if len(indexUpserts) > 0 {
		if err := h.store.UpsertCloudObjectIndexBatch(ctx, sourceID, providerName, indexUpserts, now); err != nil {
			return nil, nil, fmt.Errorf("refresh cloud object index failed: %w", err)
		}
	}
	missingIndexIDs := make([]string, 0)
	for _, row := range indexRows {
		id := strings.TrimSpace(row.ExternalObjectID)
		if id == "" || row.IsDeleted {
			continue
		}
		if _, ok := seenIndexIDs[id]; ok {
			continue
		}
		missingIndexIDs = append(missingIndexIDs, id)
	}
	if len(missingIndexIDs) > 0 {
		if err := h.store.MarkCloudObjectsDeleted(ctx, sourceID, missingIndexIDs, now); err != nil {
			return nil, nil, fmt.Errorf("refresh cloud object deleted marks failed: %w", err)
		}
	}

	if treePath != rootPath {
		if !hasScopedObject || pathIsFile {
			return nil, nil, store.ErrTreePathInvalid
		}
	}
	return cloudBuildTreeNodes(treePath, nodeMap, childMap), fileStats, nil
}

func cloudIncludeObjectDecision(obj cloudprovider.RemoteObject, includes, excludes []string) cloudObjectFilterDecision {
	kind := cloudNormalizeKind(obj.ExternalKind, obj.ProviderMeta)
	candidates := cloudObjectMatchCandidates(obj)
	decision := cloudObjectFilterDecision{
		Kind:       kind,
		Candidates: candidates,
	}
	if cloudIsDirKind(kind) {
		if ok, pattern, candidate := cloudMatchesAnyPattern(excludes, candidates...); ok {
			decision.Reason = "excluded_by_pattern"
			decision.MatchedPattern = pattern
			decision.MatchedCandidate = candidate
			return decision
		}
		decision.Include = true
		decision.Reason = "directory_passthrough"
		return decision
	}
	if len(includes) > 0 {
		if ok, pattern, candidate := cloudMatchesAnyPattern(includes, candidates...); !ok {
			decision.Reason = "include_not_matched"
			return decision
		} else {
			decision.Reason = "included_by_pattern"
			decision.MatchedPattern = pattern
			decision.MatchedCandidate = candidate
		}
	}
	if ok, pattern, candidate := cloudMatchesAnyPattern(excludes, candidates...); ok {
		decision.Reason = "excluded_by_pattern"
		decision.MatchedPattern = pattern
		decision.MatchedCandidate = candidate
		return decision
	}
	decision.Include = true
	if decision.Reason == "" {
		decision.Reason = "included_no_include_rules"
	}
	return decision
}

func cloudMatchesPattern(pattern string, candidates ...string) bool {
	ok, _ := cloudMatchPatternCandidate(pattern, candidates...)
	return ok
}

func cloudMatchPatternCandidate(pattern string, candidates ...string) (bool, string) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false, ""
	}
	altPattern := ""
	if strings.HasPrefix(pattern, "**/") {
		altPattern = strings.TrimPrefix(pattern, "**/")
	}
	for _, raw := range candidates {
		p := strings.Trim(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"), "/")
		if p == "" {
			continue
		}
		if ok, _ := path.Match(pattern, p); ok {
			return true, p
		}
		if ok, _ := path.Match(pattern, path.Base(p)); ok {
			return true, p
		}
		if strings.HasPrefix(pattern, "**/") {
			if ok, _ := path.Match(strings.TrimPrefix(pattern, "**/"), path.Base(p)); ok {
				return true, p
			}
		}
		if altPattern != "" {
			if ok, _ := path.Match(altPattern, p); ok {
				return true, p
			}
			if ok, _ := path.Match(altPattern, path.Base(p)); ok {
				return true, p
			}
		}
	}
	return false, ""
}

func cloudMatchesAnyPattern(patterns []string, candidates ...string) (bool, string, string) {
	for _, rawPattern := range patterns {
		pattern := strings.TrimSpace(rawPattern)
		if pattern == "" {
			continue
		}
		if ok, candidate := cloudMatchPatternCandidate(pattern, candidates...); ok {
			return true, pattern, candidate
		}
	}
	return false, "", ""
}

func cloudObjectMatchCandidates(obj cloudprovider.RemoteObject) []string {
	kind := cloudNormalizeKind(obj.ExternalKind, obj.ProviderMeta)
	remotePath := strings.Trim(strings.ReplaceAll(strings.TrimSpace(obj.ExternalPath), "\\", "/"), "/")
	remoteName := strings.Trim(strings.ReplaceAll(strings.TrimSpace(obj.ExternalName), "\\", "/"), "/")

	ordered := make([]string, 0, 12)
	seen := make(map[string]struct{}, 12)
	appendUnique := func(v string) {
		v = strings.Trim(strings.ReplaceAll(strings.TrimSpace(v), "\\", "/"), "/")
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		ordered = append(ordered, v)
	}

	appendUnique(remotePath)
	appendUnique(path.Base(remotePath))
	appendUnique(remoteName)
	appendUnique(path.Base(remoteName))

	primary := remotePath
	if primary == "" {
		primary = remoteName
	}
	ext := strings.ToLower(strings.TrimSpace(path.Ext(primary)))
	if ext != "" {
		appendUnique("ext:" + strings.TrimPrefix(ext, "."))
	}

	for _, suffix := range cloudKindMatchSuffixes(kind) {
		if suffix == "" {
			continue
		}
		suffix = strings.ToLower(strings.TrimSpace(suffix))
		if primary != "" && path.Ext(primary) == "" {
			appendUnique(primary + suffix)
			appendUnique(path.Base(primary + suffix))
		}
		if remoteName != "" && path.Ext(remoteName) == "" {
			appendUnique(remoteName + suffix)
			appendUnique(path.Base(remoteName + suffix))
		}
		appendUnique("ext:" + strings.TrimPrefix(suffix, "."))
	}

	if kind != "" {
		appendUnique("kind:" + kind)
	}
	if kind == "file" && ext != "" {
		appendUnique("kind:" + strings.TrimPrefix(ext, "."))
	}
	return ordered
}

func cloudKindMatchSuffixes(kind string) []string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "docx":
		return []string{".docx"}
	case "doc":
		return []string{".doc"}
	case "sheet":
		return []string{".xlsx", ".xls"}
	case "slides":
		return []string{".pptx", ".ppt"}
	case "pdf":
		return []string{".pdf"}
	default:
		return nil
	}
}

func cloudWikiPageWithChildren(kind string, meta map[string]any) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "doc", "docx":
	default:
		return false
	}
	return cloudBoolOption(meta, "has_child")
}

func cloudWikiPageObject(kind string, meta map[string]any) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "doc", "docx":
	default:
		return false
	}
	if cloudWikiPageWithChildren(kind, meta) {
		return true
	}
	for _, key := range []string{"node_token", "space_id", "obj_token", "obj_type", "wiki_token"} {
		if raw, ok := meta[key]; ok && raw != nil && strings.TrimSpace(fmt.Sprintf("%v", raw)) != "" {
			return true
		}
	}
	return false
}

func cloudObjectKindAndMeta(obj cloudprovider.RemoteObject, existingByID map[string]store.CloudObjectIndexRecord, hasRemoteChild bool) (string, map[string]any) {
	kind := cloudNormalizeKind(obj.ExternalKind, obj.ProviderMeta)
	meta := copyCloudProviderMeta(nil)
	if existingByID != nil {
		if existing, ok := existingByID[strings.TrimSpace(obj.ExternalObjectID)]; ok {
			if strings.TrimSpace(obj.ExternalKind) == "" && strings.TrimSpace(existing.ExternalKind) != "" {
				kind = cloudNormalizeKind(existing.ExternalKind, obj.ProviderMeta)
			}
			meta = copyCloudProviderMeta(existing.ProviderMeta)
		}
	}
	if len(obj.ProviderMeta) > 0 {
		if meta == nil {
			meta = make(map[string]any, len(obj.ProviderMeta))
		}
		for key, value := range obj.ProviderMeta {
			meta[key] = value
		}
	}
	if hasRemoteChild && cloudRemoteObjectShouldBeWikiParent(kind, meta) && !cloudWikiPageWithChildren(kind, meta) {
		if meta == nil {
			meta = make(map[string]any, 1)
		}
		meta["has_child"] = true
	}
	return kind, meta
}

func copyCloudProviderMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	for key, value := range meta {
		out[key] = value
	}
	return out
}

func cloudRemoteObjectShouldBeWikiParent(kind string, meta map[string]any) bool {
	if cloudWikiPageObject(kind, meta) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "doc", "docx":
		return true
	default:
		return false
	}
}

func cloudWikiPageShouldFold(objectPath, kind string, meta map[string]any) bool {
	if !cloudWikiPageObject(kind, meta) {
		return false
	}
	if cloudWikiPageWithChildren(kind, meta) {
		return true
	}
	cleanPath := filepath.Clean(strings.TrimSpace(objectPath))
	base := filepath.Base(cleanPath)
	parent := filepath.Base(filepath.Dir(cleanPath))
	if base == "." || parent == "." || parent == string(filepath.Separator) {
		return false
	}
	return strings.EqualFold(strings.TrimSuffix(base, filepath.Ext(base)), parent)
}

func cloudNormalizeKind(kind string, meta map[string]any) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "" {
		return kind
	}
	rawType := ""
	if meta != nil {
		if v, ok := meta["obj_type"]; ok && v != nil {
			rawType = strings.TrimSpace(fmt.Sprintf("%v", v))
		}
	}
	if rawType != "" {
		return strings.ToLower(rawType)
	}
	return "file"
}

func cloudIsDirKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "folder", "directory", "dir", "wiki", "space":
		return true
	default:
		return false
	}
}

func cloudObjectDisplayTitle(objectPath string, obj cloudprovider.RemoteObject, isDir bool) string {
	title := strings.TrimSpace(obj.ExternalName)
	if !isDir {
		base := strings.TrimSpace(filepath.Base(filepath.Clean(objectPath)))
		parent := strings.TrimSpace(filepath.Base(filepath.Dir(filepath.Clean(objectPath))))
		if title == "" && base != "" && base != "." && base != string(filepath.Separator) {
			title = base
		} else if base != "" && strings.EqualFold(parent, title) {
			title = base
		}
	}
	if title == "" {
		title = cloudNodeTitleFromPath(objectPath)
	}
	return title
}

func cloudObjectTreePath(objectPath, kind string, meta map[string]any) string {
	objectPath = filepath.Clean(strings.TrimSpace(objectPath))
	if objectPath == "" || objectPath == "." {
		return objectPath
	}
	if !cloudWikiPageShouldFold(objectPath, kind, meta) {
		return objectPath
	}
	parent := filepath.Clean(filepath.Dir(objectPath))
	if parent == "" || parent == "." {
		return objectPath
	}
	return parent
}

func cloudTreePathByObjectID(
	rootPath string,
	objectID string,
	objects []cloudprovider.RemoteObject,
	existingByID map[string]store.CloudObjectIndexRecord,
	pathOwner map[string]string,
) string {
	objectID = strings.TrimSpace(objectID)
	if objectID == "" {
		return ""
	}
	for _, obj := range objects {
		if strings.TrimSpace(obj.ExternalObjectID) != objectID {
			continue
		}
		kind, providerMeta := cloudObjectKindAndMeta(obj, existingByID, cloudRemoteObjectHasChild(objectID, objects))
		pathObject := obj
		pathObject.ProviderMeta = providerMeta
		objectPath, _ := cloudResolveObjectPath(rootPath, pathObject, kind, existingByID, pathOwner)
		if objectPath == "" {
			return ""
		}
		return cloudObjectTreePath(objectPath, kind, providerMeta)
	}
	return ""
}

func cloudRemoteObjectHasChild(objectID string, objects []cloudprovider.RemoteObject) bool {
	objectID = strings.TrimSpace(objectID)
	if objectID == "" {
		return false
	}
	for _, obj := range objects {
		if strings.TrimSpace(obj.ExternalParentID) == objectID {
			return true
		}
	}
	return false
}

func cloudResolveObjectPath(
	rootPath string,
	obj cloudprovider.RemoteObject,
	kind string,
	existingByID map[string]store.CloudObjectIndexRecord,
	pathOwner map[string]string,
) (string, string) {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	objectID := strings.TrimSpace(obj.ExternalObjectID)
	if rootPath == "" || rootPath == "." || objectID == "" {
		return "", ""
	}
	useCanonicalPath := cloudWikiPageWithChildren(kind, obj.ProviderMeta)
	if existing, ok := existingByID[objectID]; ok && !useCanonicalPath {
		rel := strings.Trim(strings.ReplaceAll(strings.TrimSpace(existing.LocalRelPath), "\\", "/"), "/")
		if rel != "" {
			absFromRel := filepath.Clean(filepath.Join(rootPath, filepath.FromSlash(rel)))
			if pathInSourceRoot(absFromRel, rootPath) {
				return absFromRel, rel
			}
		}

		abs := filepath.Clean(strings.TrimSpace(existing.LocalAbsPath))
		if abs != "" && abs != "." && pathInSourceRoot(abs, rootPath) {
			if rel == "" {
				relFromAbs, err := filepath.Rel(rootPath, abs)
				if err == nil {
					relFromAbs = strings.Trim(strings.ReplaceAll(filepath.ToSlash(filepath.Clean(relFromAbs)), "\\", "/"), "/")
					if relFromAbs != "" && relFromAbs != "." && !strings.HasPrefix(relFromAbs, "../") && relFromAbs != ".." {
						rel = relFromAbs
					}
				}
			}
			return abs, rel
		}
	}
	rel := cloudSanitizeRelativePathForObject(obj, objectID, kind)
	rel = cloudResolvePathCollision(rel, objectID, pathOwner)
	abs := filepath.Clean(filepath.Join(rootPath, filepath.FromSlash(rel)))
	return abs, rel
}

func cloudSanitizeRelativePath(externalPath, externalName, objectID, kind string) string {
	rel := strings.TrimSpace(externalPath)
	if rel == "" {
		rel = strings.TrimSpace(externalName)
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	if rel == "" {
		rel = objectID
	}
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") || rel == ".." {
		rel = cloudSanitizeName(cloudFirstNonEmptyString(externalName, objectID))
	}
	if !cloudIsDirKind(kind) && path.Ext(rel) == "" {
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "doc", "docx":
			rel += ".md"
		}
	}
	return rel
}

func cloudSanitizeRelativePathForObject(obj cloudprovider.RemoteObject, objectID, kind string) string {
	rel := cloudSanitizeRelativePath(obj.ExternalPath, obj.ExternalName, objectID, kind)
	if cloudWikiPageWithChildren(kind, obj.ProviderMeta) {
		dir := strings.TrimSuffix(rel, path.Ext(rel))
		if dir == "" || dir == "." {
			dir = cloudSanitizeName(cloudFirstNonEmptyString(obj.ExternalName, objectID))
		}
		rel = path.Join(dir, path.Base(rel))
	}
	return rel
}

func cloudResolvePathCollision(relPath, objectID string, owner map[string]string) string {
	relPath = strings.Trim(strings.TrimSpace(relPath), "/")
	if relPath == "" {
		relPath = objectID
	}
	if owner == nil {
		return relPath
	}
	currentOwner := strings.TrimSpace(owner[relPath])
	if currentOwner == "" || currentOwner == strings.TrimSpace(objectID) {
		return relPath
	}
	dir := path.Dir(relPath)
	base := path.Base(relPath)
	ext := path.Ext(base)
	name := strings.TrimSuffix(base, ext)
	suffix := cloudShortHash(objectID)
	candidate := path.Join(dir, name+"_"+suffix+ext)
	if dir == "." || dir == "/" {
		candidate = name + "_" + suffix + ext
	}
	i := 1
	for {
		ownerID := strings.TrimSpace(owner[candidate])
		if ownerID == "" || ownerID == strings.TrimSpace(objectID) {
			return candidate
		}
		candidate = path.Join(dir, fmt.Sprintf("%s_%s_%d%s", name, suffix, i, ext))
		if dir == "." || dir == "/" {
			candidate = fmt.Sprintf("%s_%s_%d%s", name, suffix, i, ext)
		}
		i++
	}
}

func cloudTreeRelativeDepth(rootPath, targetPath string) int {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	targetPath = filepath.Clean(strings.TrimSpace(targetPath))
	if rootPath == "" || targetPath == "" || rootPath == "." || targetPath == "." {
		return -1
	}
	rel, err := filepath.Rel(rootPath, targetPath)
	if err != nil {
		return -1
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return 0
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return -1
	}
	parts := strings.Split(rel, string(filepath.Separator))
	depth := 0
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." {
			continue
		}
		depth++
	}
	return depth
}

func cloudEnsureAncestorNodes(nodeMap map[string]*model.TreeNode, childMap map[string]map[string]struct{}, rootPath, targetPath string, maxDepth int) {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	targetPath = filepath.Clean(strings.TrimSpace(targetPath))
	if rootPath == "" || targetPath == "" || rootPath == "." || targetPath == "." {
		return
	}
	rel, err := filepath.Rel(rootPath, targetPath)
	if err != nil {
		return
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) <= 1 {
		return
	}
	maxAncestorDepth := len(parts) - 1
	if maxAncestorDepth > maxDepth {
		maxAncestorDepth = maxDepth
	}
	current := rootPath
	for i := 0; i < maxAncestorDepth; i++ {
		part := strings.TrimSpace(parts[i])
		if part == "" || part == "." {
			continue
		}
		current = filepath.Clean(filepath.Join(current, part))
		cloudEnsureNode(nodeMap, childMap, current, true, part, "")
	}
}

func cloudEnsureNode(nodeMap map[string]*model.TreeNode, childMap map[string]map[string]struct{}, nodePath string, isDir bool, title, externalFileID string) {
	cloudEnsureNodeWithParent(nodeMap, childMap, nodePath, "", isDir, title, externalFileID)
}

func cloudEnsureNodeWithParent(nodeMap map[string]*model.TreeNode, childMap map[string]map[string]struct{}, nodePath, parentPath string, isDir bool, title, externalFileID string) {
	nodePath = filepath.Clean(strings.TrimSpace(nodePath))
	if nodePath == "" || nodePath == "." {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = cloudNodeTitleFromPath(nodePath)
	}
	node, ok := nodeMap[nodePath]
	if !ok {
		node = &model.TreeNode{
			Title: title,
			Key:   nodePath,
			IsDir: isDir,
		}
		if !isDir {
			node.ExternalFileID = strings.TrimSpace(externalFileID)
		}
		nodeMap[nodePath] = node
	} else {
		if !isDir {
			node.IsDir = false
			if strings.TrimSpace(externalFileID) != "" {
				node.ExternalFileID = strings.TrimSpace(externalFileID)
			}
		} else if node.IsDir {
			node.IsDir = true
			node.ExternalFileID = ""
		}
		if strings.TrimSpace(node.Title) == "" || node.Title == cloudNodeTitleFromPath(nodePath) {
			node.Title = title
		}
	}
	parent := filepath.Clean(strings.TrimSpace(parentPath))
	if parent == "" || parent == "." {
		parent = filepath.Clean(filepath.Dir(nodePath))
	}
	if parent == "" || parent == "." {
		parent = string(filepath.Separator)
	}
	if _, ok := childMap[parent]; !ok {
		childMap[parent] = make(map[string]struct{}, 4)
	}
	childMap[parent][nodePath] = struct{}{}
}

func cloudBuildTreeNodes(rootPath string, nodeMap map[string]*model.TreeNode, childMap map[string]map[string]struct{}) []model.TreeNode {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	if rootPath == "" || rootPath == "." {
		rootPath = string(filepath.Separator)
	}
	var walk func(parent string) []model.TreeNode
	walk = func(parent string) []model.TreeNode {
		childrenSet, ok := childMap[parent]
		if !ok || len(childrenSet) == 0 {
			return nil
		}
		keys := make([]string, 0, len(childrenSet))
		for key := range childrenSet {
			if _, exists := nodeMap[key]; !exists {
				continue
			}
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			left := nodeMap[keys[i]]
			right := nodeMap[keys[j]]
			leftTitle := strings.ToLower(strings.TrimSpace(left.Title))
			rightTitle := strings.ToLower(strings.TrimSpace(right.Title))
			if leftTitle == rightTitle {
				if left.IsDir != right.IsDir {
					return left.IsDir
				}
				return left.Key < right.Key
			}
			return leftTitle < rightTitle
		})
		nodes := make([]model.TreeNode, 0, len(keys))
		for _, key := range keys {
			base := nodeMap[key]
			if base == nil {
				continue
			}
			node := *base
			if len(childMap[key]) > 0 {
				node.Children = walk(key)
			}
			nodes = append(nodes, node)
		}
		return nodes
	}
	return walk(rootPath)
}

func cloudNodeTitleFromPath(p string) string {
	base := strings.TrimSpace(filepath.Base(filepath.Clean(strings.TrimSpace(p))))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return strings.TrimSpace(filepath.Clean(strings.TrimSpace(p)))
	}
	return base
}

func cloudShortHash(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:4])
}

func cloudBoolOption(m map[string]any, key string) bool {
	if len(m) == 0 {
		return false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		x = strings.TrimSpace(strings.ToLower(x))
		return x == "true" || x == "1" || x == "yes"
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	default:
		return strings.EqualFold(strings.TrimSpace(fmt.Sprintf("%v", v)), "true")
	}
}

func cloudSanitizeName(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "unnamed"
	}
	v = strings.ReplaceAll(v, "/", "_")
	v = strings.ReplaceAll(v, "\\", "_")
	v = strings.ReplaceAll(v, "\n", "_")
	v = strings.ReplaceAll(v, "\r", "_")
	return v
}

func cloudFirstNonEmptyString(values ...string) string {
	for _, item := range values {
		if strings.TrimSpace(item) != "" {
			return strings.TrimSpace(item)
		}
	}
	return ""
}

func appendCloudObjectSample(samples []string, obj cloudprovider.RemoteObject, limit int) []string {
	if limit <= 0 || len(samples) >= limit {
		return samples
	}
	samples = append(samples, cloudObjectLogLine(obj))
	return samples
}

func describeCloudObjectsForLog(objects []cloudprovider.RemoteObject, limit int) ([]string, int) {
	if limit <= 0 {
		limit = 200
	}
	if len(objects) == 0 {
		return []string{}, 0
	}
	count := len(objects)
	used := count
	if used > limit {
		used = limit
	}
	out := make([]string, 0, used)
	for i := 0; i < used; i++ {
		out = append(out, cloudObjectLogLine(objects[i]))
	}
	return out, count - used
}

func cloudObjectLogLine(obj cloudprovider.RemoteObject) string {
	return fmt.Sprintf(
		"id=%s parent=%s name=%s kind=%s path=%s version=%s size=%d",
		strings.TrimSpace(obj.ExternalObjectID),
		strings.TrimSpace(obj.ExternalParentID),
		strings.TrimSpace(obj.ExternalName),
		strings.TrimSpace(obj.ExternalKind),
		strings.TrimSpace(obj.ExternalPath),
		strings.TrimSpace(obj.ExternalVersion),
		obj.SizeBytes,
	)
}

type cloudObjectFilterDecision struct {
	Include          bool
	Reason           string
	MatchedPattern   string
	MatchedCandidate string
	Kind             string
	Candidates       []string
}

func appendCloudFilterDecisionSample(samples []string, obj cloudprovider.RemoteObject, decision cloudObjectFilterDecision, limit int) []string {
	if limit <= 0 || len(samples) >= limit {
		return samples
	}
	const maxCandidates = 8
	used := decision.Candidates
	if len(used) > maxCandidates {
		used = used[:maxCandidates]
	}
	samples = append(samples, fmt.Sprintf(
		"id=%s decision=%s include=%t kind=%s matched_pattern=%s matched_candidate=%s candidates=%s",
		strings.TrimSpace(obj.ExternalObjectID),
		strings.TrimSpace(decision.Reason),
		decision.Include,
		strings.TrimSpace(decision.Kind),
		strings.TrimSpace(decision.MatchedPattern),
		strings.TrimSpace(decision.MatchedCandidate),
		strings.Join(used, "|"),
	))
	return samples
}

func (h *Handler) fetchTreeFileStats(ctx context.Context, agentAddr string, items []model.TreeNode) (map[string]model.TreeFileStat, error) {
	paths := store.CollectTreeFilePaths(items)
	stats := make(map[string]model.TreeFileStat, len(paths))
	if len(paths) == 0 {
		return stats, nil
	}

	type statResult struct {
		path string
		stat model.TreeFileStat
		err  error
	}
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	const maxWorkers = 8
	workerCount := maxWorkers
	if len(paths) < workerCount {
		workerCount = len(paths)
	}

	jobs := make(chan string)
	results := make(chan statResult, len(paths))
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for path := range jobs {
			if workerCtx.Err() != nil {
				return
			}
			var resp struct {
				Path     string    `json:"path"`
				Size     int64     `json:"size"`
				ModTime  time.Time `json:"mod_time"`
				IsDir    bool      `json:"is_dir"`
				Checksum string    `json:"checksum"`
			}
			if err := h.callAgentJSON(workerCtx, agentAddr, "/api/v1/fs/stat", map[string]any{"path": path}, &resp); err != nil {
				select {
				case results <- statResult{err: err}:
				default:
				}
				cancel()
				return
			}
			stat := model.TreeFileStat{
				Path:     path,
				Size:     resp.Size,
				IsDir:    resp.IsDir,
				Checksum: strings.TrimSpace(resp.Checksum),
			}
			if !resp.ModTime.IsZero() {
				mt := resp.ModTime.UTC()
				stat.ModTime = &mt
			}
			select {
			case results <- statResult{path: path, stat: stat}:
			case <-workerCtx.Done():
				return
			}
		}
	}

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go worker()
	}
	go func() {
		defer close(jobs)
		for _, path := range paths {
			select {
			case <-workerCtx.Done():
				return
			case jobs <- path:
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	var firstErr error
	for res := range results {
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
			continue
		}
		stats[res.path] = res.stat
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return stats, nil
}

func filterTreeToChanged(items []model.TreeNode) []model.TreeNode {
	out := make([]model.TreeNode, 0, len(items))
	for _, node := range items {
		item := node
		if len(item.Children) > 0 {
			item.Children = filterTreeToChanged(item.Children)
		}
		if item.IsDir {
			if len(item.Children) > 0 || nodeHasChanged(item) {
				out = append(out, item)
			}
			continue
		}
		if len(item.Children) > 0 || nodeHasChanged(item) {
			out = append(out, item)
		}
	}
	return out
}

func filterTreeByKeyword(items []model.TreeNode, keyword string) []model.TreeNode {
	normalized := strings.ToLower(strings.TrimSpace(keyword))
	if normalized == "" {
		return items
	}
	out := make([]model.TreeNode, 0, len(items))
	for _, node := range items {
		item := node
		item.Children = filterTreeByKeyword(item.Children, normalized)
		if treeNodeMatchesKeyword(item, normalized) || len(item.Children) > 0 {
			out = append(out, item)
		}
	}
	return out
}

func treeNodeMatchesKeyword(node model.TreeNode, normalizedKeyword string) bool {
	return strings.Contains(strings.ToLower(node.Title), normalizedKeyword)
}

func nodeHasChanged(node model.TreeNode) bool {
	if node.HasUpdate != nil {
		return *node.HasUpdate
	}
	switch strings.ToUpper(strings.TrimSpace(node.UpdateType)) {
	case "NEW", "MODIFIED", "DELETED":
		return true
	default:
		return false
	}
}

func (h *Handler) searchCoreTaskStates(ctx context.Context, refs []store.SourceDocumentCoreRef) (map[string]coreclient.TaskState, error) {
	states := make(map[string]coreclient.TaskState, len(refs))
	if len(refs) == 0 {
		return states, nil
	}
	type datasetUserKey struct {
		datasetID string
		userID    string
		userName  string
	}
	byDatasetUser := make(map[datasetUserKey][]string, 4)
	seenByDatasetUser := make(map[datasetUserKey]map[string]struct{}, 4)
	legacyIDs := make([]string, 0, len(refs))
	legacySeen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		taskID := strings.TrimSpace(ref.CoreTaskID)
		if taskID == "" {
			continue
		}
		datasetID := strings.TrimSpace(ref.CoreDatasetID)
		if datasetID == "" {
			if _, ok := legacySeen[taskID]; ok {
				continue
			}
			legacySeen[taskID] = struct{}{}
			legacyIDs = append(legacyIDs, taskID)
			continue
		}
		key := datasetUserKey{
			datasetID: datasetID,
			userID:    strings.TrimSpace(ref.SourceCreateUserID),
			userName:  strings.TrimSpace(ref.SourceCreateUserName),
		}
		if _, ok := seenByDatasetUser[key]; !ok {
			seenByDatasetUser[key] = make(map[string]struct{}, 16)
		}
		if _, ok := seenByDatasetUser[key][taskID]; ok {
			continue
		}
		seenByDatasetUser[key][taskID] = struct{}{}
		byDatasetUser[key] = append(byDatasetUser[key], taskID)
	}
	for key, taskIDs := range byDatasetUser {
		datasetStates, err := h.core.SearchTasksByDatasetAs(ctx, key.datasetID, taskIDs, key.userID, key.userName)
		if err != nil {
			return nil, fmt.Errorf("dataset %s search failed: %w", key.datasetID, err)
		}
		for taskID, st := range datasetStates {
			states[taskID] = st
		}
	}
	if len(legacyIDs) > 0 {
		legacyStates, err := h.core.SearchTasks(ctx, legacyIDs)
		if err != nil {
			return nil, err
		}
		for taskID, st := range legacyStates {
			states[taskID] = st
		}
	}
	return states, nil
}

type sourceCoreTaskSnapshot struct {
	refs   []store.SourceDocumentCoreRef
	states map[string]coreclient.TaskState
}

func (h *Handler) reconcileSourceCoreTasks(ctx context.Context, sourceID, tenantID string) (sourceCoreTaskSnapshot, error) {
	snapshot := sourceCoreTaskSnapshot{states: map[string]coreclient.TaskState{}}
	if h.core == nil || !h.core.Enabled() {
		return snapshot, nil
	}
	sourceID = strings.TrimSpace(sourceID)
	tenantID = strings.TrimSpace(tenantID)
	if sourceID == "" || tenantID == "" {
		return snapshot, nil
	}
	refs, err := h.store.ListSourceDocumentCoreRefs(ctx, sourceID, tenantID)
	if err != nil {
		return snapshot, err
	}
	states, err := h.searchCoreTaskStates(ctx, refs)
	if err != nil {
		return snapshot, err
	}
	snapshot.refs = refs
	snapshot.states = states
	for _, ref := range refs {
		state, ok := states[strings.TrimSpace(ref.CoreTaskID)]
		if !ok {
			continue
		}
		if !shouldMarkSourceDocumentRefSucceededFromCore(ref, state.TaskState) {
			continue
		}
		if err := h.store.MarkTaskSucceeded(ctx, ref.TaskID, ref.DocumentID, ref.TargetVersionID); err != nil {
			h.log.Warn("reconcile source document core success failed",
				zap.Error(err),
				zap.String("source_id", sourceID),
				zap.Int64("document_id", ref.DocumentID),
				zap.Int64("task_id", ref.TaskID),
				zap.String("core_task_id", ref.CoreTaskID),
			)
		}
	}
	return snapshot, nil
}

func sourceDocumentItemsCoreRefs(items []model.SourceDocumentItem) []store.SourceDocumentCoreRef {
	refs := make([]store.SourceDocumentCoreRef, 0, len(items))
	for _, item := range items {
		taskID := strings.TrimSpace(item.CoreTaskID)
		if taskID == "" {
			continue
		}
		refs = append(refs, store.SourceDocumentCoreRef{
			DocumentID:           item.DocumentID,
			SourceCreateUserID:   strings.TrimSpace(item.SourceCreateUserID),
			SourceCreateUserName: strings.TrimSpace(item.SourceCreateUserName),
			ParseStatus:          item.ParseState,
			DesiredVersionID:     item.DesiredVersionID,
			CurrentVersionID:     item.CurrentVersionID,
			TaskID:               item.ParseTaskID,
			TaskAction:           item.ParseTaskAction,
			TargetVersionID:      item.ParseTaskTargetVersion,
			CoreDatasetID:        strings.TrimSpace(item.CoreDatasetID),
			CoreTaskID:           taskID,
		})
	}
	return refs
}

func parseTaskItemsCoreRefs(items []model.ParseTaskListItem) []store.SourceDocumentCoreRef {
	refs := make([]store.SourceDocumentCoreRef, 0, len(items))
	for _, item := range items {
		taskID := strings.TrimSpace(item.CoreTaskID)
		if taskID == "" {
			continue
		}
		refs = append(refs, store.SourceDocumentCoreRef{
			DocumentID:           item.DocumentID,
			SourceCreateUserID:   strings.TrimSpace(item.SourceCreateUserID),
			SourceCreateUserName: strings.TrimSpace(item.SourceCreateUserName),
			TaskID:               item.TaskID,
			TaskAction:           item.TaskAction,
			TargetVersionID:      item.TargetVersionID,
			CoreDatasetID:        strings.TrimSpace(item.CoreDatasetID),
			CoreTaskID:           taskID,
		})
	}
	return refs
}

func applyCoreTaskStateToSourceDocumentItem(item *model.SourceDocumentItem, coreTaskState string) {
	rawState := strings.TrimSpace(coreTaskState)
	state := normalizeCoreTaskState(rawState)
	if state == "" {
		return
	}
	if !itemCoreTaskTargetsDesired(*item) {
		return
	}
	if !isKnownCoreTaskState(state) {
		item.CoreTaskState = rawState
		return
	}
	item.CoreTaskState = state
	item.ParseState = state
}

func normalizeSourceDocumentParseStatesForResponse(items []model.SourceDocumentItem) {
	for i := range items {
		items[i].ParseState = publicParseState(items[i].ParseState)
		items[i].CoreTaskState = publicParseState(items[i].CoreTaskState)
		items[i].ScanOrchestrationStatus = publicParseState(items[i].ScanOrchestrationStatus)
	}
}

func normalizeTreeParseQueueStatesForResponse(items []model.TreeNode) []model.TreeNode {
	out := make([]model.TreeNode, 0, len(items))
	for _, node := range items {
		item := node
		if len(item.Children) > 0 {
			item.Children = normalizeTreeParseQueueStatesForResponse(item.Children)
		}
		item.ParseQueueState = publicParseState(item.ParseQueueState)
		item.CoreTaskState = publicParseState(item.CoreTaskState)
		out = append(out, item)
	}
	return out
}

func publicParseState(state string) string {
	normalized := normalizeCoreTaskState(state)
	switch normalized {
	case "":
		return ""
	case "SUCCEEDED", "DELETED":
		return "SUCCESS"
	case "FAILED", "SUBMIT_FAILED", "CANCELED", "SUSPENDED":
		return "FAILED"
	default:
		return "PROCESSING"
	}
}

func applyCoreTaskStatesToParseTaskItems(items []model.ParseTaskListItem, states map[string]coreclient.TaskState) {
	for i := range items {
		taskID := strings.TrimSpace(items[i].CoreTaskID)
		if taskID == "" {
			continue
		}
		state, ok := states[taskID]
		if !ok {
			continue
		}
		applyCoreTaskStateToParseTaskItem(&items[i], state.TaskState)
	}
}

func applyCoreTaskStateToParseTaskItem(item *model.ParseTaskListItem, coreTaskState string) {
	rawState := strings.TrimSpace(coreTaskState)
	state := normalizeCoreTaskState(rawState)
	if state == "" {
		return
	}
	if !isKnownCoreTaskState(state) {
		item.CoreTaskState = rawState
		return
	}
	item.CoreTaskState = state
	item.Status = state
}

func applyCoreTaskStatesToTreeNodes(items []model.TreeNode, refs []store.SourceDocumentCoreRef, states map[string]coreclient.TaskState) []model.TreeNode {
	refsByPath := make(map[string]store.SourceDocumentCoreRef, len(refs))
	for _, ref := range refs {
		path := strings.TrimSpace(ref.SourceObjectID)
		if path == "" || strings.TrimSpace(ref.CoreTaskID) == "" || !sourceDocumentCoreTaskTargetsDesired(ref) {
			continue
		}
		refsByPath[path] = ref
	}
	var walk func([]model.TreeNode) []model.TreeNode
	walk = func(nodes []model.TreeNode) []model.TreeNode {
		out := make([]model.TreeNode, 0, len(nodes))
		for _, node := range nodes {
			item := node
			if item.IsDir {
				item.Children = walk(item.Children)
				out = append(out, item)
				continue
			}
			ref, ok := refsByPath[strings.TrimSpace(item.Key)]
			if !ok {
				out = append(out, item)
				continue
			}
			state, ok := states[strings.TrimSpace(ref.CoreTaskID)]
			if !ok {
				out = append(out, item)
				continue
			}
			rawState := strings.TrimSpace(state.TaskState)
			normalized := normalizeCoreTaskState(rawState)
			if normalized == "" {
				out = append(out, item)
				continue
			}
			if !isKnownCoreTaskState(normalized) {
				item.CoreTaskState = rawState
				out = append(out, item)
				continue
			}
			item.CoreTaskState = normalized
			item.ParseQueueState = normalized
			out = append(out, item)
		}
		return out
	}
	return walk(items)
}

func itemCoreTaskTargetsDesired(item model.SourceDocumentItem) bool {
	targetVersion := strings.TrimSpace(item.ParseTaskTargetVersion)
	desiredVersion := strings.TrimSpace(item.DesiredVersionID)
	return targetVersion != "" && desiredVersion != "" && targetVersion == desiredVersion
}

func itemCoreStateMatchesDesired(item model.SourceDocumentItem, coreTaskState string) bool {
	if !isCoreParsedState(coreTaskState) {
		return false
	}
	return itemCoreTaskTargetsDesired(item)
}

func shouldMarkSourceDocumentSucceededFromCore(item model.SourceDocumentItem) bool {
	if item.ParseTaskID <= 0 || !itemCoreStateMatchesDesired(item, item.CoreTaskState) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(item.ParseTaskAction), "DELETE") {
		return true
	}
	return strings.TrimSpace(item.CurrentVersionID) != strings.TrimSpace(item.ParseTaskTargetVersion) ||
		!isCoreParsedState(item.ParseState)
}

func shouldMarkSourceDocumentRefSucceededFromCore(ref store.SourceDocumentCoreRef, coreTaskState string) bool {
	if ref.TaskID <= 0 || !isCoreParsedState(coreTaskState) || !sourceDocumentCoreTaskTargetsDesired(ref) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(ref.TaskAction), "DELETE") {
		return true
	}
	return strings.TrimSpace(ref.CurrentVersionID) != strings.TrimSpace(ref.TargetVersionID) ||
		!isCoreParsedState(ref.ParseStatus) ||
		!strings.EqualFold(strings.TrimSpace(ref.ScanOrchestrationStatus), "SUCCEEDED")
}

func sourceDocumentCoreTaskTargetsDesired(ref store.SourceDocumentCoreRef) bool {
	targetVersion := strings.TrimSpace(ref.TargetVersionID)
	desiredVersion := strings.TrimSpace(ref.DesiredVersionID)
	return targetVersion != "" && desiredVersion != "" && targetVersion == desiredVersion
}

func buildSourceDocumentsSummaryWithCore(refs []store.SourceDocumentCoreRef, states map[string]coreclient.TaskState, storageBytes int64) model.SourceDocumentsSummary {
	var (
		parsedCount int64
		newCount    int64
		modCount    int64
		delCount    int64
	)
	for _, ref := range refs {
		taskID := strings.TrimSpace(ref.CoreTaskID)
		taskState := ""
		if taskID != "" {
			if state, ok := states[taskID]; ok {
				taskState = strings.ToUpper(strings.TrimSpace(state.TaskState))
			}
		}
		update := store.InferDocumentUpdateType(ref.DesiredVersionID, ref.CurrentVersionID, ref.ParseStatus)
		switch update {
		case "NEW":
			newCount++
		case "MODIFIED":
			modCount++
		case "DELETED":
			delCount++
		}
		parseState := strings.ToUpper(strings.TrimSpace(ref.ParseStatus))
		if taskState != "" && sourceDocumentCoreTaskTargetsDesired(ref) {
			parseState = taskState
		}
		if isCoreParsedState(parseState) ||
			(!sourceDocumentCoreTaskTargetsDesired(ref) && strings.TrimSpace(ref.CurrentVersionID) != "" && strings.ToUpper(strings.TrimSpace(ref.ParseStatus)) != "DELETED") {
			parsedCount++
		}
	}
	return model.SourceDocumentsSummary{
		ParsedDocumentCount: parsedCount,
		StorageBytes:        storageBytes,
		TotalDocumentCount:  int64(len(refs)),
		NewCount:            newCount,
		ModifiedCount:       modCount,
		DeletedCount:        delCount,
		PendingPullCount:    newCount + modCount + delCount,
	}
}

func isCoreParsedState(state string) bool {
	switch normalizeCoreTaskState(state) {
	case "SUCCEEDED":
		return true
	default:
		return false
	}
}

func isKnownCoreTaskState(state string) bool {
	switch normalizeCoreTaskState(state) {
	case "CREATING", "UPLOADING", "UPLOADED", "RUNNING", "SUCCEEDED", "FAILED", "CANCELED", "SUSPENDED":
		return true
	default:
		return false
	}
}

func normalizeCoreTaskState(state string) string {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "":
		return ""
	case "SUCCEEDED", "SUCCESS", "COMPLETED", "DONE", "FINISHED", "TASK_STATE_SUCCEEDED", "TASK_STATE_SUCCESS":
		return "SUCCEEDED"
	case "FAILED", "FAIL", "ERROR", "TASK_STATE_FAILED", "TASK_STATE_FAIL":
		return "FAILED"
	case "CANCELED", "CANCELLED", "TASK_STATE_CANCELED", "TASK_STATE_CANCELLED":
		return "CANCELED"
	case "SUSPENDED", "TASK_STATE_SUSPENDED":
		return "SUSPENDED"
	case "CREATING", "TASK_STATE_CREATING":
		return "CREATING"
	case "UPLOADING", "TASK_STATE_UPLOADING":
		return "UPLOADING"
	case "UPLOADED", "TASK_STATE_UPLOADED":
		return "UPLOADED"
	case "RUNNING", "STARTED", "SUBMITTED", "PROCESSING", "TASK_STATE_RUNNING", "TASK_STATE_STARTED", "TASK_STATE_SUBMITTED", "TASK_STATE_PROCESSING":
		return "RUNNING"
	case "PENDING", "QUEUED", "WAITING", "TASK_STATE_PENDING", "TASK_STATE_QUEUED", "TASK_STATE_WAITING":
		return "CREATING"
	default:
		return strings.ToUpper(strings.TrimSpace(state))
	}
}
