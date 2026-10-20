package doc

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"time"
)

type ResourceRef struct {
	Kind            string `json:"kind"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	FolderID        string `json:"folder_id,omitempty"`
	DocumentID      string `json:"document_id,omitempty"`
}

func (r ResourceRef) ID() string {
	if r.Kind == "folder" {
		return r.FolderID
	}
	if r.Kind == "document" {
		return r.DocumentID
	}
	return r.KnowledgeBaseID
}

type ResourceOperationContext struct {
	Caller DatasetCatalogCaller
	// ValidateExecution fences the live run on this transaction before any mutation.
	ValidateExecution func(context.Context, *gorm.DB) error
	// ValidateBeforeCommit refreshes remote identity after preparation and before opening a transaction.
	ValidateBeforeCommit func(context.Context) error
	// OperationKey and ExpectedSnapshot are trusted transport context, never model arguments.
	InputDigest      string // Trusted digest of canonical source selections, before acquisition.
	OperationKey     string
	ExpectedSnapshot string
	// nil is unrestricted, non-nil empty scope permits no existing library.
	AllowedDatasetIDs []string
}
type CreateKnowledgeRequest struct {
	PageOptions     *Dataset `json:"-"`
	PreferredID     string   `json:"-"`
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	Scene           string   `json:"scene,omitempty"`
	ProcessingLevel string   `json:"processing_level,omitempty"`
}
type CreateFoldersRequest struct {
	PreferredRootID string   `json:"-"` // Existing page API may choose a single new folder ID.
	KnowledgeBaseID string   `json:"knowledge_base_id"`
	Folders         []string `json:"folders"`
	ParentID        string   `json:"parent_id,omitempty"`
}
type ResourceMetadataChanges struct {
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Scene       *string   `json:"scene,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
}
type ResourceMetadataItem struct {
	Target  ResourceRef             `json:"target"`
	Changes ResourceMetadataChanges `json:"changes"`
}
type UpdateMetadataRequest struct {
	Items []ResourceMetadataItem `json:"items"`
}
type MoveResourcesRequest struct {
	PageMetadata *ResourceMetadataChanges `json:"-"` // Legacy single-resource page update, committed with the move.
	Sources      []ResourceRef            `json:"sources"`
	Destination  ResourceRef              `json:"destination"`
}
type DeleteResourcesRequest struct {
	Targets []ResourceRef `json:"targets"`
}
type ResourceOperationSummary struct {
	Total       int `json:"total"`
	Succeeded   int `json:"succeeded"`
	Failed      int `json:"failed"`
	Unconfirmed int `json:"unconfirmed"`
	Skipped     int `json:"skipped"`
}
type ResourceOperationItem struct {
	ResourceRef      ResourceRef       `json:"resource_ref"`
	PreviousRef      *ResourceRef      `json:"previous_ref,omitempty"`
	Name             string            `json:"name"`
	ParentID         string            `json:"parent_id"`
	ResourceStatus   string            `json:"resource_status"`
	ProcessingStatus string            `json:"processing_status,omitempty"`
	CleanupStatus    string            `json:"cleanup_status,omitempty"`
	DocumentCount    int               `json:"document_count,omitempty"`
	FolderCount      int               `json:"folder_count,omitempty"`
	IDMapping        map[string]string `json:"id_mapping,omitempty"`
	Before           map[string]any    `json:"before,omitempty"`
	After            map[string]any    `json:"after,omitempty"`
	EntryURL         string            `json:"entry_url"`
	Error            *ResourceError    `json:"error,omitempty"`
}
type ResourceOperationResult struct {
	Action  string                   `json:"action"`
	Status  string                   `json:"status"`
	Summary ResourceOperationSummary `json:"summary"`
	Items   []ResourceOperationItem  `json:"items"`
}
type ResourceError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *ResourceError) Error() string { return e.Message }

type ResourceBudget struct {
	Calibrated         bool // Cross-library moves remain disabled until deployment budgets are measured.
	MaxResources       int
	MaxBytes           int64
	MaxPrepareDuration time.Duration
	CopyBytesPerSecond int64
}
type ResourcePreview struct {
	Ref  ResourceRef `json:"resource_ref"`
	Name string      `json:"name"`
	Path string      `json:"path"`
}
type ResourceSnapshot struct {
	PreviewResources []ResourcePreview `json:"preview_resources"`
	Destination      *ResourcePreview  `json:"destination,omitempty"`
	Digest           string            `json:"digest"`
	Resources        []ResourceRef     `json:"resources"`
	DocumentCount    int               `json:"document_count"`
	FolderCount      int               `json:"folder_count"`
	TotalBytes       int64             `json:"total_bytes"`
}
type ResourceServiceDeps struct {
	DB     *gorm.DB
	Budget ResourceBudget
	// SourceAccess validates live source policy. Default uses Scan's strict batch endpoint.
	SourceAccess func(context.Context, DatasetCatalogCaller, string, string) error
	// ProvisionKnowledge creates the existing algorithm KB before the Core transaction.
	ProvisionKnowledge func(context.Context, DatasetCatalogCaller, *orm.Dataset) error
	Originals          *OriginalService
}

// Hash input is encoded before mutation; receipt response is committed with resource rows.
type resourceReceiptPayload = json.RawMessage
