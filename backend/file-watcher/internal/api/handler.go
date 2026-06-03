package api

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"

	internal "github.com/lazymind/file_watcher/internal"
	"github.com/lazymind/file_watcher/internal/fs"
	"github.com/lazymind/file_watcher/internal/source"
)

// Handler holds all HTTP handler dependencies.
type Handler struct {
	manager   source.Manager
	validator fs.PathValidator
	staging   fs.StagingService
	mapper    fs.PathMapper
	log       *zap.Logger
}

// Tree POST /api/v1/fs/tree
func (h *Handler) Tree(w http.ResponseWriter, r *http.Request) {
	writeV2Disabled(w, "legacy /api/v1/fs/tree is disabled; use /api/v1/agents/fs/list")
}

func NewHandler(manager source.Manager, validator fs.PathValidator, staging fs.StagingService, mapper fs.PathMapper, log *zap.Logger) *Handler {
	if mapper == nil {
		mapper = fs.NewPathMapper("", nil)
	}
	return &Handler{manager: manager, validator: validator, staging: staging, mapper: mapper, log: log}
}

// Healthz GET /healthz
func (h *Handler) Healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Browse POST /api/v1/fs/browse
func (h *Handler) Browse(w http.ResponseWriter, r *http.Request) {
	writeV2Disabled(w, "legacy /api/v1/fs/browse is disabled; use /api/v1/agents/fs/list")
}

// ValidatePath POST /api/v1/fs/validate
func (h *Handler) ValidatePath(w http.ResponseWriter, r *http.Request) {
	writeV2Disabled(w, "legacy /api/v1/fs/validate is disabled; use /api/v1/agents/fs/validate")
}

// StatFile POST /api/v1/fs/stat
func (h *Handler) StatFile(w http.ResponseWriter, r *http.Request) {
	writeV2Disabled(w, "legacy /api/v1/fs/stat is disabled; use /api/v1/agents/fs/stat")
}

// StartSource POST /api/v1/sources/start
func (h *Handler) StartSource(w http.ResponseWriter, r *http.Request) {
	var req internal.StartSourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.manager.StartSource(r.Context(), req); err != nil {
		writeError(w, http.StatusBadRequest, "START_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, internal.StartSourceResponse{Started: true})
}

// StopSource POST /api/v1/sources/stop
func (h *Handler) StopSource(w http.ResponseWriter, r *http.Request) {
	var req internal.StopSourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.manager.StopSource(r.Context(), req.SourceID); err != nil {
		writeError(w, http.StatusBadRequest, "STOP_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, internal.AcceptedResponse{Accepted: true})
}

// StageFile POST /api/v1/fs/stage
func (h *Handler) StageFile(w http.ResponseWriter, r *http.Request) {
	writeV2Disabled(w, "legacy /api/v1/fs/stage is disabled; use /api/v1/agents/fs/export")
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, internal.ErrorResponse{Code: code, Message: msg})
}

func writeV2Disabled(w http.ResponseWriter, message string) {
	writeError(w, http.StatusGone, "V2_DISABLED", message)
}
