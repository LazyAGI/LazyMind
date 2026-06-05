package localfs

import (
	"context"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/worker"
)

const (
	ConnectorType       connector.ConnectorType = "local_fs"
	TargetTypeLocalPath connector.TargetType    = "local_path"

	ErrorCodeAgentNotAvailable connector.ErrorCode = "AGENT_NOT_AVAILABLE"
	ErrorCodeTargetNotFound    connector.ErrorCode = "TARGET_NOT_FOUND"
	ErrorCodeObjectNotFound    connector.ErrorCode = "OBJECT_NOT_FOUND"
)

type AgentClient interface {
	ValidatePath(ctx context.Context, req ValidatePathRequest) (PathInfo, error)
	ListDir(ctx context.Context, req ListDirRequest) (ListDirPage, error)
	StatPath(ctx context.Context, req StatPathRequest) (PathInfo, error)
	ExportFile(ctx context.Context, req ExportFileRequest) (ExportedFile, error)
}

type RootListingAgent interface {
	ListRoots(ctx context.Context, req ListRootsRequest) ([]PathInfo, error)
}

type TempObjectStore interface {
	Put(ctx context.Context, input worker.TempObjectInput) (worker.TempObject, error)
}

type ValidatePathRequest struct {
	AgentID string `json:"agent_id"`
	Path    string `json:"path"`
	UserID  string `json:"user_id"`
}

type ListDirRequest struct {
	AgentID      string `json:"agent_id"`
	Path         string `json:"path"`
	Cursor       string `json:"cursor,omitempty"`
	PageSize     int    `json:"page_size"`
	IncludeFiles bool   `json:"include_files"`
}

type StatPathRequest struct {
	AgentID string `json:"agent_id"`
	Path    string `json:"path"`
}

type ExportFileRequest struct {
	AgentID         string `json:"agent_id"`
	Path            string `json:"path"`
	ExpectedVersion string `json:"expected_version"`
}

type ListRootsRequest struct {
	AgentID string `json:"agent_id"`
	UserID  string `json:"user_id"`
}

type PathInfo struct {
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
	StableID       string `json:"stable_id,omitempty"`
	ParentStableID string `json:"parent_stable_id,omitempty"`
	ParentPath     string `json:"parent_path,omitempty"`
}

type ListDirPage struct {
	Items      []PathInfo `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
	HasMore    bool       `json:"has_more"`
}

type ExportedFile struct {
	ContentURI    string `json:"content_uri"`
	SizeBytes     int64  `json:"size_bytes"`
	MTimeUnixNano int64  `json:"mtime_unix_nano"`
	MimeType      string `json:"mime_type,omitempty"`
	FileExtension string `json:"file_extension,omitempty"`
	CleanupToken  string `json:"cleanup_token,omitempty"`
}
