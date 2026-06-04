package localfs

import (
	"context"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type LocalFSConnector struct {
	agent            AgentClient
	defaultAgentID   string
	publicRoot       string
	allowedPrefixes  []string
	recommendedRoots []string
	temp             TempObjectStore
	clock            func() time.Time
}

type Option func(*LocalFSConnector)

func NewLocalFSConnector(agent AgentClient, options ...Option) *LocalFSConnector {
	c := &LocalFSConnector{
		agent: agent,
		clock: time.Now,
	}
	for _, option := range options {
		option(c)
	}
	return c
}

func WithAllowedPrefixes(prefixes ...string) Option {
	return func(c *LocalFSConnector) {
		c.allowedPrefixes = cleanPrefixes(prefixes)
	}
}

func WithRecommendedRoots(paths ...string) Option {
	return func(c *LocalFSConnector) {
		c.recommendedRoots = cleanPrefixes(paths)
	}
}

func WithDefaultAgentID(agentID string) Option {
	return func(c *LocalFSConnector) {
		c.defaultAgentID = strings.TrimSpace(agentID)
	}
}

func WithPublicRoot(publicRoot string) Option {
	return func(c *LocalFSConnector) {
		if cleaned := cleanPath(publicRoot); cleaned != "." {
			c.publicRoot = cleaned
		}
	}
}

func WithTempObjectStore(temp TempObjectStore) Option {
	return func(c *LocalFSConnector) {
		c.temp = temp
	}
}

func (c *LocalFSConnector) Spec() connector.ConnectorSpec {
	return connector.ConnectorSpec{
		ConnectorType:          ConnectorType,
		DisplayName:            "Local Files",
		TargetTypes:            []connector.TargetType{TargetTypeLocalPath},
		SupportsSearch:         false,
		SupportsDelta:          false,
		SupportsRecursiveFetch: false,
		SupportsDualRoleObject: false,
		SupportsExportFormats:  []connector.ExportFormat{connector.ExportFormatOriginal},
		DefaultVersionStrategy: connector.VersionStrategyMTime,
		MaxPageSize:            100,
		RateLimitPolicy:        "agent",
		RequiresAgentID:        true,
	}
}

func (c *LocalFSConnector) ValidateTarget(ctx context.Context, req connector.ValidateTargetRequest) (connector.NormalizedTarget, error) {
	if err := ctx.Err(); err != nil {
		return connector.NormalizedTarget{}, err
	}
	req.AgentID = c.resolveAgentID(req.AgentID)
	req.TargetRef = c.publicPath(req.TargetRef)
	if err := c.validateTargetRequest(req); err != nil {
		return connector.NormalizedTarget{}, err
	}
	info, err := c.probeTarget(ctx, req)
	if err != nil {
		return connector.NormalizedTarget{}, err
	}
	return c.buildNormalizedTarget(req.AgentID, info)
}

func (c *LocalFSConnector) ListChildren(ctx context.Context, req connector.ListChildrenRequest) (connector.RawObjectPage, error) {
	if err := ctx.Err(); err != nil {
		return connector.RawObjectPage{}, err
	}
	req.AgentID = c.resolveAgentID(req.AgentID)
	if err := c.validateListRequest(req); err != nil {
		return connector.RawObjectPage{}, err
	}
	if isInitialRootRequest(req) {
		page, err := c.listInitialRoots(ctx, req)
		if err != nil {
			return connector.RawObjectPage{}, err
		}
		return c.virtualTargetPage(page), nil
	}
	nodePath, err := c.decodeNodeRef(req.TargetRef, req.NodeRef)
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	var page connector.RawObjectPage
	if req.ListMode == connector.ListModeAllCurrentLevel {
		page, err = c.listAllCurrentLevel(ctx, req, nodePath)
	} else {
		page, err = c.listOnePage(ctx, req, nodePath, req.Cursor)
	}
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	return c.virtualTargetPage(page), nil
}

func (c *LocalFSConnector) Search(context.Context, connector.SearchRequest) (connector.RawObjectPage, error) {
	return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeUnsupported, "local_fs search is not supported")
}

func (c *LocalFSConnector) FetchPage(ctx context.Context, req connector.FetchPageRequest) (connector.RawObjectPage, error) {
	if err := ctx.Err(); err != nil {
		return connector.RawObjectPage{}, err
	}
	if err := c.validateFetchRequest(req); err != nil {
		return connector.RawObjectPage{}, err
	}
	return c.fetchOnePage(ctx, req)
}

func (c *LocalFSConnector) ExportObject(ctx context.Context, req connector.ExportObjectRequest) (connector.ExportedObject, error) {
	if err := ctx.Err(); err != nil {
		return connector.ExportedObject{}, err
	}
	path, agentID, err := c.locateObject(req)
	if err != nil {
		return connector.ExportedObject{}, err
	}
	exported, err := c.exportToTempURI(ctx, agentID, path, req.SourceVersion)
	if err != nil {
		return connector.ExportedObject{}, err
	}
	if err := c.verifyExportedVersion(exported, req.SourceVersion); err != nil {
		return connector.ExportedObject{}, err
	}
	return c.buildExportedObject(exported), nil
}

func (c *LocalFSConnector) MapObject(ctx context.Context, raw connector.RawObject) (connector.NormalizedSourceObject, error) {
	if err := ctx.Err(); err != nil {
		return connector.NormalizedSourceObject{}, err
	}
	objectKey, err := c.buildObjectKey(raw)
	if err != nil {
		return connector.NormalizedSourceObject{}, err
	}
	return connector.NormalizedSourceObject{
		ObjectKey:       objectKey,
		ParentKey:       c.buildParentKey(raw),
		DisplayName:     displayName(raw.DisplayName, raw.ObjectRef),
		SearchName:      searchName(raw.SearchName, raw.DisplayName),
		ObjectType:      raw.ObjectType,
		IsDocument:      raw.IsDocument,
		IsContainer:     raw.IsContainer,
		HasChildren:     raw.HasChildren,
		SourceVersion:   raw.SourceVersion,
		SizeBytes:       raw.SizeBytes,
		MimeType:        raw.MimeType,
		FileExtension:   raw.FileExtension,
		ModifiedAt:      raw.ModifiedAt,
		DeletedAtSource: raw.DeletedAtSource,
		ProviderMeta:    cloneProviderMeta(raw.ProviderMeta),
	}, nil
}

func (c *LocalFSConnector) resolveAgentID(agentID string) string {
	if trimmed := strings.TrimSpace(agentID); trimmed != "" {
		return trimmed
	}
	return c.defaultAgentID
}
