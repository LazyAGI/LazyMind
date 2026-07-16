package resourcefs

import (
	"errors"
	"time"

	"lazymind/core/filediff"
)

type ResourceType string

const (
	ResourceTypeMemory         ResourceType = "memory"
	ResourceTypeUserPreference ResourceType = "user_preference"
)

type FileRefType string

const (
	FileRefHead     FileRefType = "head"
	FileRefDraft    FileRefType = "draft"
	FileRefRevision FileRefType = "revision"
)

var (
	ErrInvalidResourceType = errors.New("invalid resource type")
	ErrInvalidPath         = errors.New("invalid resource path")
	ErrResourceNotFound    = errors.New("personal resource not found")
	ErrRevisionNotFound    = errors.New("personal resource revision not found")
	ErrDraftNotFound       = errors.New("personal resource draft not found")
	ErrReviewNotFound      = errors.New("personal resource review session not found")
	ErrConflict            = errors.New("personal resource conflict")
	ErrUnsupported         = errors.New("personal resource operation unsupported")
	ErrInvalidReview       = errors.New("invalid personal resource review request")
)

type ResourceRef struct {
	UserID       string       `json:"user_id"`
	ResourceType ResourceType `json:"resource_type"`
}

type ResourceState struct {
	ID             string       `json:"id"`
	UserID         string       `json:"user_id"`
	ResourceType   ResourceType `json:"resource_type"`
	Path           string       `json:"path"`
	HeadRevisionID string       `json:"head_revision_id"`
	Version        int64        `json:"version"`
	DraftVersion   int64        `json:"draft_version"`
	DraftStatus    string       `json:"draft_status"`
}

type FileMeta struct {
	Path        string `json:"path"`
	BlobHash    string `json:"blob_hash"`
	ContentHash string `json:"content_hash"`
	Size        int64  `json:"size"`
	Mime        string `json:"mime"`
	FileType    string `json:"file_type"`
	Binary      bool   `json:"binary"`
}

type ReadFileRequest struct {
	Ref        ResourceRef
	RefType    FileRefType
	RevisionID string
}

type FileResponse struct {
	ResourceType  ResourceType `json:"resource_type"`
	Path          string       `json:"path"`
	Content       string       `json:"content,omitempty"`
	BlobHash      string       `json:"blob_hash"`
	ContentHash   string       `json:"content_hash"`
	Size          int64        `json:"size"`
	Mime          string       `json:"mime"`
	FileType      string       `json:"file_type"`
	Binary        bool         `json:"binary"`
	RevisionID    string       `json:"revision_id,omitempty"`
	RevisionNo    int64        `json:"revision_no,omitempty"`
	DraftVersion  int64        `json:"draft_version,omitempty"`
	DraftStatus   string       `json:"draft_status,omitempty"`
	AgentPersona  string       `json:"agent_persona,omitempty"`
	PreferredName string       `json:"preferred_name,omitempty"`
	ResponseStyle string       `json:"response_style,omitempty"`
}

type WriteDraftRequest struct {
	Ref                  ResourceRef
	Content              string
	ExpectedDraftVersion int64
	ConversationID       string
	TaskID               string
	UpdatedBy            string
}

type DraftResponse struct {
	Ref            ResourceRef `json:"ref"`
	Path           string      `json:"path"`
	DraftVersion   int64       `json:"draft_version"`
	DraftStatus    string      `json:"draft_status"`
	BaseRevisionID string      `json:"base_revision_id,omitempty"`
	BlobHash       string      `json:"blob_hash"`
	ContentHash    string      `json:"content_hash"`
	DraftUpdatedAt *time.Time  `json:"draft_updated_at,omitempty"`
}

type UpdateMetadataRequest struct {
	Ref           ResourceRef
	AutoEvo       *bool
	AgentPersona  *string
	PreferredName *string
	ResponseStyle *string
	UpdatedBy     string
	UpdatedByName string
}

type MetadataResponse struct {
	Ref                ResourceRef `json:"ref"`
	ResourceID         string      `json:"resource_id"`
	AutoEvo            bool        `json:"auto_evo"`
	AutoEvoApplyStatus string      `json:"auto_evo_apply_status"`
	AutoEvoGeneration  int64       `json:"auto_evo_generation"`
	AutoEvoError       string      `json:"auto_evo_error"`
	UpdatedBy          string      `json:"updated_by,omitempty"`
	UpdatedByName      string      `json:"updated_by_name,omitempty"`
	UpdatedAt          time.Time   `json:"updated_at"`
	EnabledFromOff     bool        `json:"enabled_from_off"`
	AgentPersona       *string     `json:"agent_persona,omitempty"`
	PreferredName      *string     `json:"preferred_name,omitempty"`
	ResponseStyle      *string     `json:"response_style,omitempty"`
}

type DraftPreviewRequest struct {
	Ref ResourceRef
}

type DraftPreviewResponse struct {
	Ref            ResourceRef       `json:"ref"`
	ResourceType   ResourceType      `json:"resource_type"`
	Path           string            `json:"path"`
	BaseRevisionID string            `json:"base_revision_id,omitempty"`
	DraftVersion   int64             `json:"draft_version"`
	DraftStatus    string            `json:"draft_status"`
	HeadContent    string            `json:"head_content"`
	DraftContent   string            `json:"draft_content"`
	Diff           filediff.FileDiff `json:"file_diff"`
	ReviewID       string            `json:"review_id,omitempty"`
	ReviewVersion  int64             `json:"review_version,omitempty"`
	CanUndo        bool              `json:"can_undo"`
	PendingCount   int               `json:"pending_count"`
	AcceptedCount  int               `json:"accepted_count"`
	RejectedCount  int               `json:"rejected_count"`
}

type ReviewActionItem struct {
	Path     string `json:"path,omitempty"`
	HunkID   string `json:"hunk_id"`
	Decision string `json:"decision"`
}

type ReviewActionRequest struct {
	Ref                   ResourceRef
	ReviewID              string
	ExpectedReviewVersion int64
	Items                 []ReviewActionItem
	UpdatedBy             string
}

type ReviewActionResponse struct {
	Ref           ResourceRef        `json:"ref"`
	ReviewID      string             `json:"review_id"`
	ReviewVersion int64              `json:"review_version"`
	BatchID       string             `json:"batch_id"`
	DraftVersion  int64              `json:"draft_version"`
	CanUndo       bool               `json:"can_undo"`
	DraftContent  string             `json:"draft_content"`
	Items         []ReviewActionItem `json:"items"`
}

type ReviewUndoRequest struct {
	Ref                   ResourceRef
	ReviewID              string
	ExpectedReviewVersion int64
	UpdatedBy             string
}

type ReviewUndoResponse struct {
	Ref             ResourceRef        `json:"ref"`
	ReviewID        string             `json:"review_id"`
	ReviewVersion   int64              `json:"review_version"`
	UndoneBatchID   string             `json:"undone_batch_id"`
	DraftVersion    int64              `json:"draft_version"`
	CanUndo         bool               `json:"can_undo"`
	DraftContent    string             `json:"draft_content"`
	RestoredActions []ReviewActionItem `json:"items"`
}

type CommitDraftRequest struct {
	Ref                    ResourceRef
	Message                string
	SourceRefType          string
	SourceRefID            string
	ExpectedHeadRevisionID string
	ExpectedDraftVersion   int64
	CreatedBy              string
	CreatedByName          string
}

type CommitResponse struct {
	Ref        ResourceRef `json:"ref"`
	Path       string      `json:"path"`
	RevisionID string      `json:"revision_id"`
	RevisionNo int64       `json:"revision_no"`
	Content    string      `json:"content"`
}

type ListRevisionsRequest struct {
	Ref ResourceRef
}

type RevisionListResponse struct {
	Items []RevisionSummary `json:"items"`
}

type RevisionSummary struct {
	ID               string    `json:"id"`
	RevisionID       string    `json:"revision_id"`
	RevisionNo       int64     `json:"revision_no"`
	ParentRevisionID string    `json:"parent_revision_id,omitempty"`
	Path             string    `json:"path"`
	ContentHash      string    `json:"content_hash"`
	Size             int64     `json:"size"`
	Message          string    `json:"message"`
	ChangeSource     string    `json:"change_source"`
	SourceRefType    string    `json:"source_ref_type"`
	SourceRefID      string    `json:"source_ref_id"`
	CreatedBy        string    `json:"created_by,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	IsHead           bool      `json:"is_head"`
}

type RevisionDetailResponse struct {
	RevisionSummary
	Content  string `json:"content"`
	BlobHash string `json:"blob_hash"`
	Mime     string `json:"mime"`
	FileType string `json:"file_type"`
	Binary   bool   `json:"binary"`
}

type RollbackRequest struct {
	Ref                    ResourceRef
	RevisionID             string
	Message                string
	ExpectedHeadRevisionID string
	CreatedBy              string
	CreatedByName          string
}

type RollbackResponse struct {
	Ref        ResourceRef `json:"ref"`
	Path       string      `json:"path"`
	RevisionID string      `json:"revision_id"`
	RevisionNo int64       `json:"revision_no"`
	Content    string      `json:"content"`
}
