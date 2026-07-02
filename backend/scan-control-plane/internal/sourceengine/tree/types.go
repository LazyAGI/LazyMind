package tree

import (
	"context"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const (
	ListModePage            = "page"
	ListModeAllCurrentLevel = "all_current_level"

	SearchModeConnector = "connector"
	SearchModeFallback  = "fallback"
	SearchModeCache     = "cache"
)

type TreeQueryLimits struct {
	DefaultPageSize         int
	MaxPageSize             int
	MaxAllCurrentLevelItems int
}

type TreeNode struct {
	Key             string         `json:"key"`
	NodeRef         string         `json:"node_ref,omitempty"`
	DisplayName     string         `json:"display_name"`
	SearchName      string         `json:"search_name,omitempty"`
	ConnectorType   string         `json:"connector_type,omitempty"`
	TargetType      string         `json:"target_type,omitempty"`
	TargetRef       string         `json:"target_ref,omitempty"`
	SourceID        string         `json:"source_id,omitempty"`
	BindingID       string         `json:"binding_id,omitempty"`
	TreeKey         string         `json:"tree_key,omitempty"`
	ObjectKey       string         `json:"object_key,omitempty"`
	ParentKey       string         `json:"parent_key,omitempty"`
	IsDocument      bool           `json:"is_document"`
	IsContainer     bool           `json:"is_container"`
	HasChildren     bool           `json:"has_children"`
	Selectable      bool           `json:"selectable"`
	SourceState     string         `json:"source_state,omitempty"`
	SyncState       string         `json:"sync_state,omitempty"`
	PendingAction   string         `json:"pending_action,omitempty"`
	ParseQueueState string         `json:"parse_queue_state,omitempty"`
	HasUpdate       bool           `json:"has_update,omitempty"`
	UpdateType      string         `json:"update_type,omitempty"`
	UpdateDesc      string         `json:"update_desc,omitempty"`
	ProviderMeta    map[string]any `json:"provider_meta,omitempty"`
	Children        []TreeNode     `json:"children,omitempty"`
}

type TreeNodePage struct {
	Items         []TreeNode `json:"items"`
	NextCursor    string     `json:"next_cursor,omitempty"`
	HasMore       bool       `json:"has_more"`
	ListComplete  bool       `json:"list_complete"`
	Truncated     bool       `json:"truncated"`
	SearchMode    string     `json:"search_mode,omitempty"`
	CacheStatus   string     `json:"cache_status,omitempty"`
	CacheBuilding bool       `json:"cache_building,omitempty"`
	CacheComplete bool       `json:"cache_complete,omitempty"`
	CacheError    string     `json:"cache_error,omitempty"`
}

type TargetTreeChildrenRequest struct {
	ConnectorType    connector.ConnectorType `json:"connector_type"`
	TargetType       connector.TargetType    `json:"target_type,omitempty"`
	TargetRef        string                  `json:"target_ref,omitempty"`
	NodeRef          string                  `json:"node_ref,omitempty"`
	AgentID          string                  `json:"agent_id,omitempty"`
	AuthConnectionID string                  `json:"auth_connection_id,omitempty"`
	ProviderOptions  map[string]any          `json:"provider_options,omitempty"`
	IncludeFiles     bool                    `json:"include_files,omitempty"`
	ListMode         string                  `json:"list_mode,omitempty"`
	PageSize         int                     `json:"page_size,omitempty"`
	Cursor           string                  `json:"cursor,omitempty"`
	MaxItems         int                     `json:"max_items,omitempty"`
}

type TargetTreeSearchRequest struct {
	ConnectorType    connector.ConnectorType `json:"connector_type"`
	Keyword          string                  `json:"keyword"`
	TargetType       connector.TargetType    `json:"target_type,omitempty"`
	TargetRef        string                  `json:"target_ref,omitempty"`
	NodeRef          string                  `json:"node_ref,omitempty"`
	AgentID          string                  `json:"agent_id,omitempty"`
	AuthConnectionID string                  `json:"auth_connection_id,omitempty"`
	ProviderOptions  map[string]any          `json:"provider_options,omitempty"`
	IncludeFiles     bool                    `json:"include_files,omitempty"`
	ListMode         string                  `json:"list_mode,omitempty"`
	PageSize         int                     `json:"page_size,omitempty"`
	Cursor           string                  `json:"cursor,omitempty"`
	MaxItems         int                     `json:"max_items,omitempty"`
}

type SourceTreeChildrenRequest struct {
	SourceID          string         `json:"-"`
	BindingID         string         `json:"binding_id,omitempty"`
	TreeKey           string         `json:"tree_key,omitempty"`
	ParentKey         string         `json:"parent_key,omitempty"`
	NodeRef           string         `json:"node_ref,omitempty"`
	ParentRef         string         `json:"parent_ref,omitempty"`
	Key               string         `json:"key,omitempty"`
	UseCache          *bool          `json:"use_cache,omitempty"`
	RefreshState      *bool          `json:"refresh_state,omitempty"`
	ProviderOptions   map[string]any `json:"-"`
	IncludeDocuments  bool           `json:"include_documents"`
	IncludeContainers bool           `json:"include_containers"`
	StateFilter       []string       `json:"state_filter,omitempty"`
	ListMode          string         `json:"list_mode,omitempty"`
	PageSize          int            `json:"page_size,omitempty"`
	Cursor            string         `json:"cursor,omitempty"`
	MaxItems          int            `json:"max_items,omitempty"`
}

type SourceTreeSearchRequest struct {
	SourceID          string   `json:"-"`
	Keyword           string   `json:"keyword"`
	BindingID         string   `json:"binding_id,omitempty"`
	TreeKey           string   `json:"tree_key,omitempty"`
	RefreshState      *bool    `json:"refresh_state,omitempty"`
	IncludeDocuments  bool     `json:"include_documents"`
	IncludeContainers bool     `json:"include_containers"`
	StateFilter       []string `json:"state_filter,omitempty"`
	ListMode          string   `json:"list_mode,omitempty"`
	PageSize          int      `json:"page_size,omitempty"`
	Cursor            string   `json:"cursor,omitempty"`
	MaxItems          int      `json:"max_items,omitempty"`
}

type SourceDocumentListRequest struct {
	SourceID      string
	BindingID     string
	Keyword       string
	StateFilter   []string
	ParseStatuses []string
	Page          int
	PageSize      int
}

type SourceDocumentItem struct {
	DocumentID           string         `json:"document_id,omitempty"`
	SourceID             string         `json:"source_id"`
	BindingID            string         `json:"binding_id"`
	ObjectKey            string         `json:"object_key"`
	DisplayName          string         `json:"display_name"`
	Name                 string         `json:"name,omitempty"`
	Path                 string         `json:"path,omitempty"`
	Directory            string         `json:"directory,omitempty"`
	FileType             string         `json:"file_type,omitempty"`
	SizeBytes            int64          `json:"size_bytes"`
	SourceVersion        string         `json:"source_version,omitempty"`
	BaselineVersion      string         `json:"baseline_version,omitempty"`
	SourceState          string         `json:"source_state,omitempty"`
	SyncState            string         `json:"sync_state,omitempty"`
	PendingAction        string         `json:"pending_action,omitempty"`
	ParseQueueState      string         `json:"parse_queue_state,omitempty"`
	ParseStatus          string         `json:"parse_status,omitempty"`
	ParseState           string         `json:"parse_state,omitempty"`
	EffectiveParseStatus string         `json:"effective_parse_status,omitempty"`
	HasUpdate            bool           `json:"has_update,omitempty"`
	UpdateType           string         `json:"update_type,omitempty"`
	UpdateDesc           string         `json:"update_desc,omitempty"`
	CoreDocumentID       string         `json:"core_document_id,omitempty"`
	ModifiedAt           *time.Time     `json:"modified_at,omitempty"`
	SourceModifiedAt     *time.Time     `json:"source_modified_at,omitempty"`
	LastSyncedAt         *time.Time     `json:"last_synced_at,omitempty"`
	LastError            map[string]any `json:"last_error,omitempty"`
}

type SourceDocumentListResponse struct {
	Items    []SourceDocumentItem `json:"items"`
	Total    int                  `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
	Summary  map[string]any       `json:"summary,omitempty"`
}

type TargetTreeEngine interface {
	ListChildren(ctx context.Context, req TargetTreeChildrenRequest) (TreeNodePage, error)
	Search(ctx context.Context, req TargetTreeSearchRequest) (TreeNodePage, error)
}

type SourceTreeQueryEngine interface {
	ListChildren(ctx context.Context, req SourceTreeChildrenRequest) (TreeNodePage, error)
	Search(ctx context.Context, req SourceTreeSearchRequest) (TreeNodePage, error)
}

type SourceDocumentQuery interface {
	ListDocuments(ctx context.Context, req SourceDocumentListRequest) (SourceDocumentListResponse, error)
}

type TargetTreeFallbackSearch interface {
	Search(ctx context.Context, req TargetTreeSearchRequest) (TreeNodePage, error)
}

type SourceTreeReadRepository interface {
	GetSource(ctx context.Context, sourceID string) (store.Source, error)
	ListBindings(ctx context.Context, sourceID string) ([]store.Binding, error)
	GetBinding(ctx context.Context, sourceID, bindingID string) (store.Binding, error)
	ListObjects(ctx context.Context, req store.ObjectListRequest) ([]store.ObjectWithState, string, bool, error)
	SearchObjects(ctx context.Context, req store.ObjectSearchRequest) ([]store.ObjectWithState, string, bool, error)
	ListDocuments(ctx context.Context, req store.SourceDocumentListRequest) ([]store.DocumentWithState, int, error)
	GetSourceSummary(ctx context.Context, req store.SourceSummaryRequest) (store.SourceSummary, error)
}

type ObjectListRequest = store.ObjectListRequest
type ObjectSearchRequest = store.ObjectSearchRequest
type ObjectWithState = store.ObjectWithState
type DocumentWithState = store.DocumentWithState
