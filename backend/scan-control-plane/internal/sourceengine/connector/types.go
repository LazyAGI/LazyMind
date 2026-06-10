package connector

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type ConnectorType string
type TargetType string
type ObjectType string
type ListMode string
type ScopeType string
type ExportFormat string
type VersionStrategy string
type ErrorCode string

const (
	ObjectTypeFolder ObjectType = "folder"
	ObjectTypeFile   ObjectType = "file"
	ObjectTypePage   ObjectType = "page"

	ListModePage            ListMode = "page"
	ListModeAllCurrentLevel ListMode = "all_current_level"

	ScopeTypeFull       ScopeType = "full"
	ScopeTypePartial    ScopeType = "partial"
	ScopeTypeDelta      ScopeType = "delta"
	ScopeTypeWatchEvent ScopeType = "watch_event"

	ExportFormatOriginal ExportFormat = "original"
	ExportFormatMarkdown ExportFormat = "markdown"

	VersionStrategyMTime    VersionStrategy = "mtime"
	VersionStrategyRevision VersionStrategy = "revision"
	VersionStrategyHash     VersionStrategy = "hash"

	ErrorCodeInvalidArgument     ErrorCode = "INVALID_ARGUMENT"
	ErrorCodeInvalidTarget       ErrorCode = "INVALID_TARGET"
	ErrorCodeNotFound            ErrorCode = "NOT_FOUND"
	ErrorCodeAlreadyExists       ErrorCode = "ALREADY_EXISTS"
	ErrorCodeUnsupported         ErrorCode = "UNSUPPORTED"
	ErrorCodeUnsupportedDelta    ErrorCode = "UNSUPPORTED_DELTA"
	ErrorCodeUnsupportedListMode ErrorCode = "UNSUPPORTED_LIST_MODE"
	ErrorCodeResultTooLarge      ErrorCode = "RESULT_TOO_LARGE"
	ErrorCodeVersionMismatch     ErrorCode = "VERSION_MISMATCH"
	ErrorCodePermissionDenied    ErrorCode = "PERMISSION_DENIED"
	ErrorCodeRateLimited         ErrorCode = "RATE_LIMITED"
	ErrorCodeTransient           ErrorCode = "TRANSIENT"
)

type ProviderOptions map[string]any
type ProviderMeta map[string]string
type ScopeRef map[string]string

func (o ProviderOptions) String(key string) string {
	if o == nil {
		return ""
	}
	value, ok := o[key].(string)
	if !ok {
		return ""
	}
	return value
}

type ConnectorSpec struct {
	ConnectorType            ConnectorType
	DisplayName              string
	TargetTypes              []TargetType
	SupportsSearch           bool
	SupportsDelta            bool
	SupportsRecursiveFetch   bool
	SupportsDualRoleObject   bool
	SupportsExportFormats    []ExportFormat
	DefaultVersionStrategy   VersionStrategy
	MaxPageSize              int
	RateLimitPolicy          string
	RequiresAgentID          bool
	RequiresAuthConnectionID bool
	RequiredProviderOptions  []string
}

func (s ConnectorSpec) ConfigSpec() ConfigSpec {
	return ConfigSpec{
		ConnectorType:            s.ConnectorType,
		TargetTypes:              slices.Clone(s.TargetTypes),
		MaxPageSize:              s.MaxPageSize,
		RequiresAgentID:          s.RequiresAgentID,
		RequiresAuthConnectionID: s.RequiresAuthConnectionID,
		RequiredProviderOptions:  slices.Clone(s.RequiredProviderOptions),
	}
}

type ConfigSpec struct {
	ConnectorType            ConnectorType
	TargetTypes              []TargetType
	MaxPageSize              int
	RequiresAgentID          bool
	RequiresAuthConnectionID bool
	RequiredProviderOptions  []string
}

func (s ConfigSpec) Validate(req ValidateTargetRequest) error {
	if req.ConnectorType != "" && req.ConnectorType != s.ConnectorType {
		return NewError(ErrorCodeInvalidArgument, "connector_type does not match connector spec")
	}
	if strings.TrimSpace(req.UserID) == "" {
		return NewError(ErrorCodeInvalidArgument, "user_id is required")
	}
	if !slices.Contains(s.TargetTypes, req.TargetType) {
		return NewError(ErrorCodeInvalidTarget, "target_type is not supported")
	}
	if s.RequiresAgentID && strings.TrimSpace(req.AgentID) == "" {
		return NewError(ErrorCodeInvalidArgument, "agent_id is required")
	}
	if s.RequiresAuthConnectionID && strings.TrimSpace(req.AuthConnectionID) == "" {
		return NewError(ErrorCodeInvalidArgument, "auth_connection_id is required")
	}
	for _, key := range s.RequiredProviderOptions {
		if strings.TrimSpace(req.ProviderOptions.String(key)) == "" {
			return NewError(ErrorCodeInvalidArgument, "provider option "+key+" is required")
		}
	}
	return nil
}

type ValidateTargetRequest struct {
	ConnectorType    ConnectorType   `json:"connector_type"`
	TargetType       TargetType      `json:"target_type"`
	TargetRef        string          `json:"target_ref"`
	AgentID          string          `json:"agent_id,omitempty"`
	AuthConnectionID string          `json:"auth_connection_id,omitempty"`
	ProviderOptions  ProviderOptions `json:"provider_options,omitempty"`
	UserID           string          `json:"-"`
}

type NormalizedTarget struct {
	TargetType        TargetType   `json:"target_type"`
	TargetRef         string       `json:"target_ref"`
	TargetFingerprint string       `json:"target_fingerprint"`
	DisplayName       string       `json:"display_name"`
	ProviderMeta      ProviderMeta `json:"provider_meta,omitempty"`
	RootObjectKey     string       `json:"root_object_key"`
}

type ListChildrenRequest struct {
	TargetType       TargetType
	TargetRef        string
	NodeRef          string
	ListMode         ListMode
	Cursor           string
	PageSize         int
	MaxItems         int
	AgentID          string
	AuthConnectionID string
	ProviderOptions  ProviderOptions
}

type SearchRequest struct {
	TargetType       TargetType
	TargetRef        string
	NodeRef          string
	Keyword          string
	Cursor           string
	PageSize         int
	AgentID          string
	AuthConnectionID string
	ProviderOptions  ProviderOptions
}

type FetchPageRequest struct {
	SourceID          string
	BindingID         string
	BindingGeneration int64
	TargetType        TargetType
	TargetRef         string
	ScopeType         ScopeType
	ScopeRef          ScopeRef
	Cursor            string
	PageSize          int
	AgentID           string
	AuthConnectionID  string
	ProviderOptions   ProviderOptions
}

type ExportObjectRequest struct {
	SourceID          string
	BindingID         string
	ObjectKey         string
	BindingGeneration int64
	SourceVersion     string
	TargetVersionID   string
	ExportFormat      ExportFormat
	ProviderOptions   ProviderOptions
	ProviderMeta      ProviderMeta
}

type RawObjectPage struct {
	Items        []RawObject
	HasMore      bool
	NextCursor   string
	Watermark    string
	ListComplete bool
}

type RawObject struct {
	ObjectRef         string
	ObjectKey         string
	ParentRef         string
	ParentKey         string
	DisplayName       string
	SearchName        string
	ObjectType        ObjectType
	IsDocument        bool
	IsContainer       bool
	HasChildren       bool
	Bindable          bool
	BindingTargetType TargetType
	BindingTargetRef  string
	TreeKey           string
	SourceVersion     string
	SizeBytes         int64
	MimeType          string
	FileExtension     string
	ModifiedAt        *time.Time
	DeletedAtSource   *time.Time
	ProviderMeta      ProviderMeta
}

type NormalizedSourceObject struct {
	ObjectKey       string
	ParentKey       string
	DisplayName     string
	SearchName      string
	ObjectType      ObjectType
	IsDocument      bool
	IsContainer     bool
	HasChildren     bool
	SourceVersion   string
	SizeBytes       int64
	MimeType        string
	FileExtension   string
	ModifiedAt      *time.Time
	DeletedAtSource *time.Time
	ProviderMeta    ProviderMeta
}

type ExportedObject struct {
	ContentURI      string
	MimeType        string
	FileExtension   string
	SizeBytes       int64
	CleanupToken    string
	ExportedVersion string
}

type ConnectorError struct {
	Code    ErrorCode
	Message string
}

func (e *ConnectorError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewError(code ErrorCode, message string) error {
	return &ConnectorError{Code: code, Message: message}
}

func ErrorCodeOf(err error) (ErrorCode, bool) {
	var connectorErr *ConnectorError
	if errors.As(err, &connectorErr) {
		return connectorErr.Code, true
	}
	return "", false
}

func ValidateTargetConfig(spec ConnectorSpec, req ValidateTargetRequest) error {
	return spec.ConfigSpec().Validate(req)
}
