package localfs

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func (c *LocalFSConnector) validateFetchRequest(req connector.FetchPageRequest) error {
	if err := validateTarget(req.TargetType, req.TargetRef); err != nil {
		return err
	}
	if req.AgentID == "" {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "agent_id is required")
	}
	if req.BindingGeneration <= 0 {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "binding_generation must be positive")
	}
	return validatePageSize(req.PageSize, c.Spec().MaxPageSize)
}

func (c *LocalFSConnector) fetchOnePage(ctx context.Context, req connector.FetchPageRequest) (connector.RawObjectPage, error) {
	switch req.ScopeType {
	case connector.ScopeTypeFull, connector.ScopeTypePartial:
		return c.fetchDirectoryPage(ctx, req)
	case connector.ScopeTypeWatchEvent:
		return c.fetchWatchEvent(ctx, req)
	case connector.ScopeTypeDelta:
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeUnsupportedDelta, "local_fs delta fetch is not supported")
	default:
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeUnsupported, "scope_type is not supported")
	}
}

func (c *LocalFSConnector) fetchDirectoryPage(ctx context.Context, req connector.FetchPageRequest) (connector.RawObjectPage, error) {
	scopePath, err := c.decodeScopePath(req.TargetRef, req.ScopeRef)
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	info, err := c.agent.StatPath(ctx, StatPathRequest{AgentID: req.AgentID, Path: scopePath})
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	if !info.IsDir {
		return c.singleObjectPage(req.AgentID, info, filepath.Dir(canonicalPath(info))), nil
	}
	listReq := connector.ListChildrenRequest{
		TargetType: req.TargetType,
		TargetRef:  req.TargetRef,
		Cursor:     req.Cursor,
		PageSize:   req.PageSize,
		AgentID:    req.AgentID,
	}
	page, err := c.listOnePage(ctx, listReq, canonicalPath(info), req.Cursor)
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	page.Watermark = versionFor(info)
	return page, nil
}

func (c *LocalFSConnector) fetchWatchEvent(ctx context.Context, req connector.FetchPageRequest) (connector.RawObjectPage, error) {
	scopePath, err := c.decodeScopePath(req.TargetRef, req.ScopeRef)
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	if watchEventDeleted(req.ScopeRef) {
		return c.deletedObjectPage(req.AgentID, scopePath), nil
	}
	info, err := c.agent.StatPath(ctx, StatPathRequest{AgentID: req.AgentID, Path: scopePath})
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	return c.singleObjectPage(req.AgentID, info, filepath.Dir(canonicalPath(info))), nil
}

func (c *LocalFSConnector) decodeScopePath(targetRef string, scopeRef connector.ScopeRef) (string, error) {
	for _, key := range []string{"path", "node_ref"} {
		if value := strings.TrimSpace(scopeRef[key]); value != "" {
			return c.decodeNodeRef(targetRef, value)
		}
	}
	if objectKey := strings.TrimSpace(scopeRef["object_key"]); objectKey != "" {
		if path, ok := pathFromObjectKey(objectKey); ok {
			if err := c.rejectOutsidePublicRoot(path); err != nil {
				return "", err
			}
			return path, nil
		}
		return "", connector.NewError(connector.ErrorCodeInvalidArgument, "object_key does not include a local path")
	}
	return c.publicPath(targetRef), nil
}

func watchEventDeleted(scopeRef connector.ScopeRef) bool {
	return strings.EqualFold(strings.TrimSpace(scopeRef["event_type"]), "deleted")
}

func (c *LocalFSConnector) singleObjectPage(agentID string, info PathInfo, parentPath string) connector.RawObjectPage {
	raw := c.rawObject(agentID, info, parentPath)
	return connector.RawObjectPage{
		Items:        []connector.RawObject{raw},
		Watermark:    raw.SourceVersion,
		ListComplete: true,
	}
}

func (c *LocalFSConnector) deletedObjectPage(agentID, path string) connector.RawObjectPage {
	deletedAt := c.clock().UTC()
	cleaned := cleanPath(path)
	info := PathInfo{
		Path:           cleaned,
		NormalizedPath: cleaned,
		DisplayName:    displayName("", cleaned),
		IsDir:          false,
	}
	raw := c.rawObject(agentID, info, filepath.Dir(cleaned))
	raw.DeletedAtSource = &deletedAt
	raw.SourceVersion = deletedVersion(deletedAt)
	raw.ProviderMeta["deleted_at"] = deletedAt.Format(time.RFC3339Nano)
	return connector.RawObjectPage{
		Items:        []connector.RawObject{raw},
		Watermark:    raw.SourceVersion,
		ListComplete: true,
	}
}

func deletedVersion(deletedAt time.Time) string {
	return "deleted:" + deletedAt.Format(time.RFC3339Nano)
}
