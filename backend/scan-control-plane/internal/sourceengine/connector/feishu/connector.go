package feishu

import (
	"context"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type FeishuConnector struct {
	auth AuthConnectionClient
	api  FeishuClient
	temp TempObjectStore
}

func NewFeishuConnector(auth AuthConnectionClient, api FeishuClient) *FeishuConnector {
	return &FeishuConnector{auth: auth, api: api}
}

func (c *FeishuConnector) UseTempObjectStore(temp TempObjectStore) {
	c.temp = temp
}

func (c *FeishuConnector) Spec() connector.ConnectorSpec {
	return connector.ConnectorSpec{
		ConnectorType:            ConnectorType,
		DisplayName:              "Feishu",
		TargetTypes:              []connector.TargetType{TargetTypeDriveFolder, TargetTypeWikiNode},
		SupportsSearch:           true,
		SupportsDelta:            false,
		SupportsRecursiveFetch:   false,
		SupportsDualRoleObject:   true,
		SupportsExportFormats:    []connector.ExportFormat{connector.ExportFormatOriginal, connector.ExportFormatMarkdown},
		DefaultVersionStrategy:   connector.VersionStrategyRevision,
		MaxPageSize:              100,
		RateLimitPolicy:          "provider",
		RequiresAuthConnectionID: true,
	}
}

func (c *FeishuConnector) ValidateTarget(ctx context.Context, req connector.ValidateTargetRequest) (connector.NormalizedTarget, error) {
	if err := ctx.Err(); err != nil {
		return connector.NormalizedTarget{}, err
	}
	if err := c.validateTargetRequest(req); err != nil {
		return connector.NormalizedTarget{}, err
	}
	token, err := c.loadToken(ctx, req.AuthConnectionID, req.UserID)
	if err != nil {
		return connector.NormalizedTarget{}, err
	}
	object, err := c.probeTarget(ctx, token.AccessToken, req)
	if err != nil {
		return connector.NormalizedTarget{}, err
	}
	return c.buildNormalizedTarget(req, object)
}

func (c *FeishuConnector) ListChildren(ctx context.Context, req connector.ListChildrenRequest) (connector.RawObjectPage, error) {
	if err := ctx.Err(); err != nil {
		return connector.RawObjectPage{}, err
	}
	if err := c.validateListRequest(req); err != nil {
		return connector.RawObjectPage{}, err
	}
	token, err := c.tokenForList(ctx, req)
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	if req.ListMode == connector.ListModeAllCurrentLevel {
		return c.listAllCurrentLevel(ctx, token.AccessToken, req)
	}
	return c.listOnePage(ctx, token.AccessToken, req, req.Cursor)
}

func (c *FeishuConnector) tokenForList(ctx context.Context, req connector.ListChildrenRequest) (Token, error) {
	if isInitialRootRequest(req) {
		return Token{}, nil
	}
	return c.loadToken(ctx, req.AuthConnectionID, req.ProviderOptions.String("user_id"))
}

func (c *FeishuConnector) Search(ctx context.Context, req connector.SearchRequest) (connector.RawObjectPage, error) {
	return c.search(ctx, req)
}

func (c *FeishuConnector) FetchPage(ctx context.Context, req connector.FetchPageRequest) (connector.RawObjectPage, error) {
	if err := ctx.Err(); err != nil {
		return connector.RawObjectPage{}, err
	}
	if err := c.validateFetchRequest(req); err != nil {
		return connector.RawObjectPage{}, err
	}
	token, err := c.loadToken(ctx, req.AuthConnectionID, "")
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	return c.fetchOnePage(ctx, token.AccessToken, req)
}

func (c *FeishuConnector) ExportObject(ctx context.Context, req connector.ExportObjectRequest) (connector.ExportedObject, error) {
	if err := ctx.Err(); err != nil {
		return connector.ExportedObject{}, err
	}
	token, err := c.loadToken(ctx, req.ProviderMeta["auth_connection_id"], "")
	if err != nil {
		return connector.ExportedObject{}, err
	}
	target, err := c.locateObject(req)
	if err != nil {
		return connector.ExportedObject{}, err
	}
	exported, err := c.exportToTempURI(ctx, token.AccessToken, target, req)
	if err != nil {
		return connector.ExportedObject{}, err
	}
	exported, err = c.ensureScanTempURI(ctx, exported)
	if err != nil {
		return connector.ExportedObject{}, err
	}
	if err := c.verifyExportedVersion(exported, req.SourceVersion); err != nil {
		return connector.ExportedObject{}, err
	}
	return c.buildExportedObject(exported), nil
}

func (c *FeishuConnector) MapObject(ctx context.Context, raw connector.RawObject) (connector.NormalizedSourceObject, error) {
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
