package feishu

import (
	"context"
	"io"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/worker"
)

const (
	ConnectorType           connector.ConnectorType = "feishu"
	TargetTypeDriveFolder   connector.TargetType    = "drive_folder"
	TargetTypeWikiNode      connector.TargetType    = "wiki_node"
	ErrorCodeAuthInvalid    connector.ErrorCode     = "AUTH_CONNECTION_INVALID"
	ErrorCodeExportTimedOut connector.ErrorCode     = "TRANSIENT_SOURCE_ERROR"
	ErrorCodeExportDenied   connector.ErrorCode     = "UNSUPPORTED_EXPORT"
)

type AuthConnectionClient interface {
	GetToken(ctx context.Context, req TokenRequest) (Token, error)
}

type FeishuClient interface {
	GetDriveRoot(ctx context.Context, token string) (Object, error)
	GetDriveFolder(ctx context.Context, token, folderToken string) (Object, error)
	ListDriveChildren(ctx context.Context, token, folderToken, cursor string, pageSize int) (ObjectPage, error)
	DownloadDriveFile(ctx context.Context, token, fileToken, expectedVersion string) (ExportedContent, error)
	ExportDriveDocumentMarkdown(ctx context.Context, token, docToken, expectedVersion string) (ExportedContent, error)
	ListWikiSpaces(ctx context.Context, token, cursor string, pageSize int) (ObjectPage, error)
	GetWikiNode(ctx context.Context, token, spaceID, nodeToken string) (Object, error)
	ListWikiChildren(ctx context.Context, token, spaceID, nodeToken, cursor string, pageSize int) (ObjectPage, error)
	ExportWikiNodeMarkdown(ctx context.Context, token, spaceID, nodeToken, expectedVersion string) (ExportedContent, error)
}

type TempObjectStore interface {
	Put(ctx context.Context, input worker.TempObjectInput) (worker.TempObject, error)
}

type TokenRequest struct {
	AuthConnectionID string
	UserID           string
}

type Token struct {
	AccessToken string
}

type ObjectKind string

const (
	ObjectKindDriveFolder ObjectKind = "drive_folder"
	ObjectKindDriveFile   ObjectKind = "drive_file"
	ObjectKindVirtualRoot ObjectKind = "virtual_root"
	ObjectKindWikiSpace   ObjectKind = "wiki_space"
	ObjectKindWikiNode    ObjectKind = "wiki_node"
)

type Object struct {
	Kind            ObjectKind
	Token           string
	ParentToken     string
	SpaceID         string
	Name            string
	IsDocument      bool
	IsContainer     bool
	HasChildren     bool
	Revision        string
	ModifiedUnixSec int64
	SizeBytes       int64
	MimeType        string
	FileExtension   string
	DriveType       string
	StableID        string
}

type ObjectPage struct {
	Items      []Object
	NextCursor string
	HasMore    bool
	Watermark  string
}

type ExportedContent struct {
	ContentURI      string
	Content         []byte
	Reader          io.Reader
	MimeType        string
	FileExtension   string
	SizeBytes       int64
	CleanupToken    string
	ExportedVersion string
}
