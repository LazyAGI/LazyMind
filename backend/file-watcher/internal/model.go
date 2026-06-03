package internal

import "time"

// Agent status enum.

type AgentStatus string

const (
	AgentStatusRegistering AgentStatus = "REGISTERING"
	AgentStatusOnline      AgentStatus = "ONLINE"
	AgentStatusDegraded    AgentStatus = "DEGRADED"
	AgentStatusOffline     AgentStatus = "OFFLINE"
	AgentStatusUnhealthy   AgentStatus = "UNHEALTHY"
)

// Source runtime status enum.

type SourceRuntimeStatus string

const (
	SourceRuntimeStatusStarting SourceRuntimeStatus = "STARTING"
	SourceRuntimeStatusWatching SourceRuntimeStatus = "WATCHING"
	SourceRuntimeStatusRunning  SourceRuntimeStatus = "RUNNING"
	SourceRuntimeStatusStopped  SourceRuntimeStatus = "STOPPED"
	SourceRuntimeStatusDegraded SourceRuntimeStatus = "DEGRADED"
	SourceRuntimeStatusError    SourceRuntimeStatus = "ERROR"
)

// Control-plane command type enum.

type CommandType string

const (
	CommandReloadSource   CommandType = "reload_source"
	CommandStartSource    CommandType = "start_source"
	CommandStopSource     CommandType = "stop_source"
	CommandScanSource     CommandType = "scan_source"
	CommandStageFile      CommandType = "stage_file"
	CommandSnapshotSource CommandType = "snapshot_source"
)

// Error code enum.

type ErrorCode string

const (
	ErrInvalidPath      ErrorCode = "INVALID_PATH"
	ErrPathNotAllowed   ErrorCode = "PATH_NOT_ALLOWED"
	ErrStageFailed      ErrorCode = "STAGE_FAILED"
	ErrControlPlaneDown ErrorCode = "CONTROL_PLANE_DOWN"
)

// File event type enum.

type FileEventType string

const (
	FileCreated  FileEventType = "created"
	FileModified FileEventType = "modified"
	FileDeleted  FileEventType = "deleted"
	FileRenamed  FileEventType = "renamed"
)

// Core data structures.

// SourceRuntime describes a local Source running on the Agent side.
type SourceRuntime struct {
	SourceID         string
	TenantID         string
	RootPath         string
	Status           SourceRuntimeStatus
	WatcherEnabled   bool
	WatcherHealthy   bool
	WatcherLastError string
	LastEventAt      time.Time
	Cancel           func() // context.CancelFunc
}

// FileEvent stores a file change event.
type FileEvent struct {
	SourceID   string        `json:"source_id"`
	TenantID   string        `json:"tenant_id"`
	EventType  FileEventType `json:"event_type"`
	Path       string        `json:"path"`
	ObjectKey  string        `json:"object_key,omitempty"`
	OldPath    string        `json:"old_path,omitempty"`
	IsDir      bool          `json:"is_dir"`
	OccurredAt time.Time     `json:"occurred_at"`
	TraceID    string        `json:"trace_id,omitempty"`
}

// HeartbeatPayload is the heartbeat report payload.
type HeartbeatPayload struct {
	AgentID          string         `json:"agent_id"`
	TenantID         string         `json:"tenant_id"`
	Hostname         string         `json:"hostname"`
	Version          string         `json:"version"`
	Status           AgentStatus    `json:"status"`
	LastHeartbeatAt  time.Time      `json:"last_heartbeat_at"`
	SourceCount      int            `json:"source_count"`
	ActiveWatchCount int            `json:"active_watch_count"`
	ActiveTaskCount  int            `json:"active_task_count"`
	ListenAddr       string         `json:"listen_addr,omitempty"`
	LastError        string         `json:"last_error,omitempty"`
	ResourceUsage    map[string]any `json:"resource_usage_json,omitempty"`
}

// StageResult stores the staging copy result.
type StageResult struct {
	HostPath      string
	ContainerPath string
	URI           string
	Size          int64
}

type StartSourceRequest struct {
	SourceID        string `json:"source_id"`
	TenantID        string `json:"tenant_id"`
	RootPath        string `json:"root_path"`
	SkipInitialScan bool   `json:"skip_initial_scan,omitempty"`
}

type StartSourceResponse struct {
	Started bool `json:"started"`
}

type StopSourceRequest struct {
	SourceID string `json:"source_id"`
}

type AcceptedResponse struct {
	Accepted bool `json:"accepted"`
}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Control-plane client DTOs.

type RegisterAgentRequest struct {
	AgentID    string `json:"agent_id"`
	TenantID   string `json:"tenant_id"`
	Hostname   string `json:"hostname"`
	Version    string `json:"version"`
	ListenAddr string `json:"listen_addr,omitempty"`
}

type ReportEventsRequest struct {
	AgentID string      `json:"agent_id"`
	Events  []FileEvent `json:"events"`
}

type PullCommandsRequest struct {
	AgentID  string `json:"agent_id"`
	TenantID string `json:"tenant_id"`
}

type Command struct {
	ID              int64       `json:"id"`
	Type            CommandType `json:"type"`
	TenantID        string      `json:"tenant_id,omitempty"`
	SourceID        string      `json:"source_id,omitempty"`
	RootPath        string      `json:"root_path,omitempty"`
	Mode            string      `json:"mode,omitempty"`
	Reason          string      `json:"reason,omitempty"`
	SkipInitialScan bool        `json:"skip_initial_scan,omitempty"`
	DocumentID      string      `json:"document_id,omitempty"`
	VersionID       string      `json:"version_id,omitempty"`
	SrcPath         string      `json:"src_path,omitempty"`
}

type AckCommandRequest struct {
	AgentID    string `json:"agent_id"`
	CommandID  int64  `json:"command_id"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	ResultJSON string `json:"result_json,omitempty"`
}

type PullCommandsResponse struct {
	Commands []Command `json:"commands"`
}
