package notion

import (
	"context"
	"io"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector/feishu"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/worker"
)

const (
	ConnectorType        connector.ConnectorType = "notion"
	TargetTypePage       connector.TargetType    = "page"
	TargetTypeDatabase   connector.TargetType    = "database"
	ErrorCodeAuthInvalid connector.ErrorCode     = "AUTH_CONNECTION_INVALID"
)

type AuthConnectionClient interface {
	GetToken(ctx context.Context, req feishu.TokenRequest) (feishu.Token, error)
}

type TempObjectStore interface {
	Put(ctx context.Context, input worker.TempObjectInput) (worker.TempObject, error)
}

type ObjectKind string

const (
	ObjectKindPage     ObjectKind = "page"
	ObjectKindDatabase ObjectKind = "database"
)

type Object struct {
	Kind            ObjectKind
	ID              string
	ParentKind      ObjectKind
	ParentID        string
	Name            string
	URL             string
	HasChildren     bool
	LastEditedTime  string
	ModifiedUnixSec int64
}

type ExportedContent struct {
	Content         []byte
	Reader          io.Reader
	MimeType        string
	FileExtension   string
	SizeBytes       int64
	CleanupToken    string
	ExportedVersion string
}
