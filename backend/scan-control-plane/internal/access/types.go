package access

import (
	"context"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type Actor struct {
	UserID   string
	TenantID string
	Role     string
}

type BindingTargetRequest struct {
	SourceID         string
	BindingID        string
	ConnectorType    connector.ConnectorType
	AgentID          string
	AuthConnectionID string
}

type AuthConnectionVerifier interface {
	VerifyAuthConnection(ctx context.Context, actor Actor, authConnectionID string) error
}

type SourceAction string

const (
	SourceActionRead   SourceAction = "read"
	SourceActionWrite  SourceAction = "write"
	SourceActionDelete SourceAction = "delete"
	SourceActionCreate SourceAction = "create"
)

type SourcePermissionVerifier interface {
	CanCreateSource(ctx context.Context, actor Actor) error
	CanAccessSource(ctx context.Context, actor Actor, source store.Source, action SourceAction) error
}

type Checker interface {
	ListReadableSourceIDs(ctx context.Context, actor Actor) ([]string, error)
	CanCreateSource(ctx context.Context, actor Actor) error
	CanReadSource(ctx context.Context, actor Actor, sourceID string) error
	CanWriteSource(ctx context.Context, actor Actor, sourceID string) error
	CanDeleteSource(ctx context.Context, actor Actor, sourceID string) error
	CanReadBinding(ctx context.Context, actor Actor, sourceID, bindingID string) error
	CanWriteBinding(ctx context.Context, actor Actor, sourceID, bindingID string) error
	CanDeleteBinding(ctx context.Context, actor Actor, sourceID, bindingID string) error
	CanReadTask(ctx context.Context, actor Actor, taskID string) error
	CanWriteTask(ctx context.Context, actor Actor, taskID string) error
	CanAccessBindingTarget(ctx context.Context, actor Actor, req BindingTargetRequest) error
	CanUseAgent(ctx context.Context, actor Actor, agentID string) error
	CanUseAuthConnection(ctx context.Context, actor Actor, authConnectionID string) error
}

type SourceStore interface {
	GetSource(ctx context.Context, sourceID string) (store.Source, error)
	GetBinding(ctx context.Context, sourceID, bindingID string) (store.Binding, error)
	GetParseTask(ctx context.Context, taskID string) (store.ParseTaskWithRefs, error)
	ListSourceAccess(ctx context.Context, tenantID string) ([]store.Source, error)
	GetAgent(ctx context.Context, agentID string) (store.Agent, error)
}
