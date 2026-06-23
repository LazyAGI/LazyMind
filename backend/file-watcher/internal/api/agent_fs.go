package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lazymind/file_watcher/internal/fs"
)

const (
	agentErrInvalidArgument  = "INVALID_ARGUMENT"
	agentErrPermissionDenied = "PERMISSION_DENIED"
	agentErrTargetNotFound   = "TARGET_NOT_FOUND"
	agentErrObjectNotFound   = "OBJECT_NOT_FOUND"
	agentErrUnsupported      = "UNSUPPORTED"
	agentErrVersionMismatch  = "VERSION_MISMATCH"
	agentErrExportFailed     = "EXPORT_FAILED"
)

type agentFSValidateRequest struct {
	AgentID string `json:"agent_id,omitempty"`
	Path    string `json:"path"`
	UserID  string `json:"user_id,omitempty"`
}

type agentFSListRequest struct {
	AgentID      string `json:"agent_id,omitempty"`
	Path         string `json:"path"`
	Cursor       string `json:"cursor,omitempty"`
	PageSize     int    `json:"page_size,omitempty"`
	IncludeFiles bool   `json:"include_files,omitempty"`
}

type agentFSStatRequest struct {
	AgentID string `json:"agent_id,omitempty"`
	Path    string `json:"path"`
}

type agentFSRootsRequest struct {
	AgentID string `json:"agent_id,omitempty"`
	UserID  string `json:"user_id,omitempty"`
}

type agentFSExportRequest struct {
	AgentID         string `json:"agent_id"`
	Path            string `json:"path"`
	ExpectedVersion string `json:"expected_version,omitempty"`
}

type agentFSPathInfo struct {
	Name           string `json:"name,omitempty"`
	Path           string `json:"path"`
	NormalizedPath string `json:"normalized_path,omitempty"`
	DisplayName    string `json:"display_name,omitempty"`
	Exists         bool   `json:"exists,omitempty"`
	Readable       bool   `json:"readable,omitempty"`
	IsDir          bool   `json:"is_dir"`
	SizeBytes      int64  `json:"size_bytes,omitempty"`
	MTimeUnixNano  int64  `json:"mtime_unix_nano,omitempty"`
	MimeType       string `json:"mime_type,omitempty"`
	FileExtension  string `json:"file_extension,omitempty"`
}

type agentFSListResponse struct {
	Items      []agentFSPathInfo `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
	HasMore    bool              `json:"has_more"`
}

type agentFSRootsResponse struct {
	Items []agentFSPathInfo `json:"items"`
}

type agentFSExportResponse struct {
	ContentURI    string `json:"content_uri"`
	SizeBytes     int64  `json:"size_bytes"`
	MTimeUnixNano int64  `json:"mtime_unix_nano"`
	MimeType      string `json:"mime_type,omitempty"`
	FileExtension string `json:"file_extension,omitempty"`
	CleanupToken  string `json:"cleanup_token,omitempty"`
}

// AgentListRoots POST /api/v1/agents/fs/roots
func (h *Handler) AgentListRoots(w http.ResponseWriter, r *http.Request) {
	var req agentFSRootsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.AgentID) == "" {
		writeError(w, http.StatusBadRequest, agentErrInvalidArgument, "agent_id is required")
		return
	}
	roots := h.validator.AllowedRoots()
	items := make([]agentFSPathInfo, 0, len(roots))
	for _, runtimeRoot := range roots {
		info, ok := h.agentPathInfo(w, h.mapper.ToPublic(runtimeRoot), agentErrTargetNotFound)
		if !ok {
			return
		}
		if info.IsDir {
			items = append(items, info)
		}
	}
	writeJSON(w, http.StatusOK, agentFSRootsResponse{Items: items})
}

// AgentValidatePath POST /api/v1/agents/fs/validate
func (h *Handler) AgentValidatePath(w http.ResponseWriter, r *http.Request) {
	var req agentFSValidateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.AgentID) == "" {
		writeError(w, http.StatusBadRequest, agentErrInvalidArgument, "agent_id is required")
		return
	}
	info, ok := h.agentPathInfo(w, req.Path, agentErrTargetNotFound)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// AgentListDir POST /api/v1/agents/fs/list
func (h *Handler) AgentListDir(w http.ResponseWriter, r *http.Request) {
	var req agentFSListRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.AgentID) == "" {
		writeError(w, http.StatusBadRequest, agentErrInvalidArgument, "agent_id is required")
		return
	}
	if req.PageSize <= 0 {
		req.PageSize = 100
	}
	offset, err := parseAgentCursor(req.Cursor)
	if err != nil {
		writeError(w, http.StatusBadRequest, agentErrInvalidArgument, err.Error())
		return
	}
	runtimePath := h.mapper.ToRuntime(req.Path)
	if err := h.validator.EnsureAllowed(runtimePath); err != nil {
		writeError(w, http.StatusForbidden, agentErrPermissionDenied, err.Error())
		return
	}
	entries, err := os.ReadDir(runtimePath)
	if err != nil {
		writeAgentFSError(w, agentErrObjectNotFound, err)
		return
	}
	items := make([]agentFSPathInfo, 0, len(entries))
	for _, entry := range entries {
		childRuntimePath := filepath.Join(runtimePath, entry.Name())
		if fs.IsTransientFile(childRuntimePath, entry.IsDir()) {
			continue
		}
		if !entry.IsDir() && !req.IncludeFiles {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		items = append(items, h.agentPathInfoFromFileInfo(childRuntimePath, info))
	}
	if offset >= len(items) {
		writeJSON(w, http.StatusOK, agentFSListResponse{Items: []agentFSPathInfo{}})
		return
	}
	end := offset + req.PageSize
	if end > len(items) {
		end = len(items)
	}
	resp := agentFSListResponse{Items: items[offset:end]}
	if end < len(items) {
		resp.HasMore = true
		resp.NextCursor = strconv.Itoa(end)
	}
	writeJSON(w, http.StatusOK, resp)
}

// AgentStatPath POST /api/v1/agents/fs/stat
func (h *Handler) AgentStatPath(w http.ResponseWriter, r *http.Request) {
	var req agentFSStatRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.AgentID) == "" {
		writeError(w, http.StatusBadRequest, agentErrInvalidArgument, "agent_id is required")
		return
	}
	info, ok := h.agentPathInfo(w, req.Path, agentErrObjectNotFound)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// AgentExportFile POST /api/v1/agents/fs/export
func (h *Handler) AgentExportFile(w http.ResponseWriter, r *http.Request) {
	var req agentFSExportRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.AgentID) == "" {
		writeError(w, http.StatusBadRequest, agentErrInvalidArgument, "agent_id is required")
		return
	}
	if h.staging == nil {
		writeError(w, http.StatusServiceUnavailable, agentErrExportFailed, "staging service is not configured")
		return
	}
	info, ok := h.agentPathInfo(w, req.Path, agentErrObjectNotFound)
	if !ok {
		return
	}
	if info.IsDir {
		writeError(w, http.StatusBadRequest, agentErrInvalidArgument, "path is a directory")
		return
	}
	version := agentVersion(info.MTimeUnixNano, info.SizeBytes)
	if req.ExpectedVersion != "" && req.ExpectedVersion != version {
		writeError(w, http.StatusConflict, agentErrVersionMismatch, "file version does not match expected_version")
		return
	}
	runtimePath := h.mapper.ToRuntime(req.Path)
	result, err := h.staging.StageFile(context.WithoutCancel(r.Context()), agentExportSourceID(req.AgentID), "export", version, runtimePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, agentErrExportFailed, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agentFSExportResponse{
		ContentURI:    result.URI,
		SizeBytes:     info.SizeBytes,
		MTimeUnixNano: info.MTimeUnixNano,
		MimeType:      info.MimeType,
		FileExtension: info.FileExtension,
		CleanupToken:  result.URI,
	})
}

func (h *Handler) agentPathInfo(w http.ResponseWriter, publicPath, notFoundCode string) (agentFSPathInfo, bool) {
	runtimePath := h.mapper.ToRuntime(publicPath)
	if err := h.validator.EnsureAllowed(runtimePath); err != nil {
		writeError(w, http.StatusForbidden, agentErrPermissionDenied, err.Error())
		return agentFSPathInfo{}, false
	}
	info, err := os.Stat(runtimePath)
	if err != nil {
		writeAgentFSError(w, notFoundCode, err)
		return agentFSPathInfo{}, false
	}
	if fs.IsTransientFile(runtimePath, info.IsDir()) {
		writeError(w, http.StatusNotFound, notFoundCode, "transient editor file is ignored")
		return agentFSPathInfo{}, false
	}
	return h.agentPathInfoFromFileInfo(runtimePath, info), true
}

func (h *Handler) agentPathInfoFromFileInfo(runtimePath string, info os.FileInfo) agentFSPathInfo {
	publicPath := h.mapper.ToPublic(runtimePath)
	extension := ""
	if !info.IsDir() {
		extension = filepath.Ext(info.Name())
	}
	return agentFSPathInfo{
		Name:           info.Name(),
		Path:           publicPath,
		NormalizedPath: publicPath,
		DisplayName:    info.Name(),
		Exists:         true,
		Readable:       true,
		IsDir:          info.IsDir(),
		SizeBytes:      info.Size(),
		MTimeUnixNano:  info.ModTime().UnixNano(),
		MimeType:       agentMimeType(extension, info.IsDir()),
		FileExtension:  extension,
	}
}

func parseAgentCursor(cursor string) (int, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("cursor is invalid")
	}
	return offset, nil
}

func writeAgentFSError(w http.ResponseWriter, code string, err error) {
	status := http.StatusInternalServerError
	switch {
	case os.IsNotExist(err):
		status = http.StatusNotFound
	case os.IsPermission(err):
		status = http.StatusForbidden
		code = agentErrPermissionDenied
	}
	writeError(w, status, code, err.Error())
}

func agentMimeType(extension string, isDir bool) string {
	if isDir {
		return "inode/directory"
	}
	if mimeType := mime.TypeByExtension(extension); mimeType != "" {
		return mimeType
	}
	return "application/octet-stream"
}

func agentVersion(mtimeUnixNano, sizeBytes int64) string {
	return fmt.Sprintf("%d:%d", mtimeUnixNano, sizeBytes)
}

func agentExportSourceID(agentID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(agentID)))
	return "agent-" + hex.EncodeToString(sum[:8])
}
