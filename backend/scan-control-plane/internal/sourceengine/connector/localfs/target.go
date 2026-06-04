package localfs

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func (c *LocalFSConnector) validateTargetRequest(req connector.ValidateTargetRequest) error {
	if err := connector.ValidateTargetConfig(c.Spec(), req); err != nil {
		return err
	}
	if strings.TrimSpace(req.TargetRef) == "" {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "target_ref is required")
	}
	if c.agent == nil {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "local_fs agent client is not configured")
	}
	return nil
}

func (c *LocalFSConnector) probeTarget(ctx context.Context, req connector.ValidateTargetRequest) (PathInfo, error) {
	info, err := c.agent.ValidatePath(ctx, ValidatePathRequest{
		AgentID: req.AgentID,
		Path:    req.TargetRef,
		UserID:  req.UserID,
	})
	if err != nil {
		return PathInfo{}, err
	}
	return c.validateProbedPath(info)
}

func (c *LocalFSConnector) validateProbedPath(info PathInfo) (PathInfo, error) {
	if !info.Exists {
		return PathInfo{}, connector.NewError(ErrorCodeTargetNotFound, "target path was not found")
	}
	if !info.Readable {
		return PathInfo{}, connector.NewError(connector.ErrorCodePermissionDenied, "target path is not readable")
	}
	info.NormalizedPath = canonicalPath(info)
	if !filepath.IsAbs(info.NormalizedPath) {
		return PathInfo{}, connector.NewError(connector.ErrorCodeInvalidTarget, "target path must be absolute after agent normalization")
	}
	if c.publicRoot != "" && !c.isPublicRootPath(info.NormalizedPath) {
		return PathInfo{}, connector.NewError(connector.ErrorCodePermissionDenied, "target path is outside local_fs public root")
	}
	if !c.pathAllowed(info.NormalizedPath) {
		return PathInfo{}, connector.NewError(connector.ErrorCodePermissionDenied, "target path is outside allowed prefixes")
	}
	return info, nil
}

func (c *LocalFSConnector) buildNormalizedTarget(agentID string, info PathInfo) (connector.NormalizedTarget, error) {
	raw := c.rawObject(agentID, info, "")
	normalized, err := c.MapObject(context.Background(), raw)
	if err != nil {
		return connector.NormalizedTarget{}, err
	}
	return connector.NormalizedTarget{
		TargetType:        TargetTypeLocalPath,
		TargetRef:         info.NormalizedPath,
		TargetFingerprint: localTargetFingerprint(agentID, info.NormalizedPath),
		DisplayName:       normalized.DisplayName,
		ProviderMeta:      raw.ProviderMeta,
		RootObjectKey:     normalized.ObjectKey,
	}, nil
}
