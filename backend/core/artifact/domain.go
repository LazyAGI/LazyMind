package artifact

import (
	"encoding/json"
	"time"
)

const (
	KindFile               = "file"
	ChannelDraft           = "draft"
	ChannelCurrent         = "current"
	ChannelPublished       = "published"
	ScopeConversation      = "conversation"
	ScopeHistory           = "history"
	ScopeLegacyRow         = "legacy_conversation_artifact"
	ScopeTask              = "subagent_task"
	ScopeSubAgentLegacyRow = "legacy_subagent_artifact"
	RoleOutput             = "output"
	RoleInput              = "input"
	ProducerMainChat       = "main_chat"
	ProducerSubAgent       = "subagent"
	ValidityEffective      = "effective"
)

type BindingSpec struct {
	ScopeType  string
	ScopeID    string
	Role       string
	RevisionID string
	SlotKey    string
	FollowHead bool
}

type CommitRequest struct {
	TenantID        string
	OwnerUserID     string
	ArtifactID      string
	LogicalKey      string
	Title           string
	Kind            string
	BaseRevisionID  string
	IdempotencyKey  string
	Content         []byte
	BlobID          string
	InlineJSON      json.RawMessage
	MIMEType        string
	ContentType     string
	Caption         *string
	ChangeSummary   string
	Metadata        json.RawMessage
	ProducerType    string
	ProducerID      string
	ProducerRunID   string
	ProducerEventID string
	Channel         string
	Bindings        []BindingSpec
	ExpectedHeadVer int64
}

type RevisionView struct {
	ArtifactID    string          `json:"artifact_id"`
	RevisionID    string          `json:"revision_id"`
	RevisionNo    int64           `json:"revision_no"`
	LogicalKey    string          `json:"logical_key,omitempty"`
	Title         string          `json:"title"`
	ContentType   string          `json:"content_type"`
	MIMEType      string          `json:"mime_type,omitempty"`
	ContentHash   string          `json:"content_hash"`
	Size          int64           `json:"size"`
	Caption       *string         `json:"caption,omitempty"`
	ChangeSummary string          `json:"change_summary,omitempty"`
	ProducerType  string          `json:"producer_type"`
	Channel       string          `json:"head_channel,omitempty"`
	HeadVersion   int64           `json:"head_version,omitempty"`
	InlineJSON    json.RawMessage `json:"inline_json,omitempty"`
	DownloadHint  string          `json:"download_capability,omitempty"`
	CreatedBy     string          `json:"created_by"`
	CreatedAt     time.Time       `json:"created_at"`
}
