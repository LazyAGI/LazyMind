package notion

import (
	"context"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type NotionConnector struct {
	auth AuthConnectionClient
	api  *Client
	temp TempObjectStore
}

func NewNotionConnector(auth AuthConnectionClient, api *Client) *NotionConnector {
	if api == nil {
		api = NewClient("", nil)
	}
	return &NotionConnector{auth: auth, api: api}
}

func (c *NotionConnector) UseTempObjectStore(temp TempObjectStore) {
	c.temp = temp
}

func (c *NotionConnector) Spec() connector.ConnectorSpec {
	return connector.ConnectorSpec{
		ConnectorType:            ConnectorType,
		DisplayName:              "Notion",
		TargetTypes:              []connector.TargetType{TargetTypePage, TargetTypeDatabase},
		SupportsSearch:           true,
		SupportsDelta:            false,
		SupportsRecursiveFetch:   false,
		SupportsDualRoleObject:   true,
		SupportsExportFormats:    []connector.ExportFormat{connector.ExportFormatMarkdown},
		DefaultVersionStrategy:   connector.VersionStrategyRevision,
		MaxPageSize:              100,
		RateLimitPolicy:          "provider",
		RequiresAuthConnectionID: true,
	}
}

func (c *NotionConnector) ValidateTarget(ctx context.Context, req connector.ValidateTargetRequest) (connector.NormalizedTarget, error) {
	if err := ctx.Err(); err != nil {
		return connector.NormalizedTarget{}, err
	}
	if err := connector.ValidateTargetConfig(c.Spec(), req); err != nil {
		return connector.NormalizedTarget{}, err
	}
	token, err := c.loadToken(ctx, req.AuthConnectionID, req.UserID)
	if err != nil {
		return connector.NormalizedTarget{}, err
	}
	object, err := c.probeTarget(ctx, token, req.TargetType, req.TargetRef)
	if err != nil {
		return connector.NormalizedTarget{}, err
	}
	raw := c.rawObject(req.AuthConnectionID, object)
	return connector.NormalizedTarget{
		TargetType:        req.TargetType,
		TargetRef:         object.ID,
		TargetFingerprint: versionFor(object),
		DisplayName:       raw.DisplayName,
		ProviderMeta:      raw.ProviderMeta,
		RootObjectKey:     raw.ObjectKey,
	}, nil
}

func (c *NotionConnector) ListChildren(ctx context.Context, req connector.ListChildrenRequest) (connector.RawObjectPage, error) {
	if err := ctx.Err(); err != nil {
		return connector.RawObjectPage{}, err
	}
	if err := validatePageSize(req.PageSize, c.Spec().MaxPageSize); err != nil {
		return connector.RawObjectPage{}, err
	}
	token, err := c.loadToken(ctx, req.AuthConnectionID, req.ProviderOptions.String("user_id"))
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	return c.listPage(ctx, token, req.TargetType, firstNonEmpty(req.NodeRef, req.TargetRef), req.Cursor, req.PageSize, req.AuthConnectionID, false)
}

func (c *NotionConnector) Search(ctx context.Context, req connector.SearchRequest) (connector.RawObjectPage, error) {
	if err := ctx.Err(); err != nil {
		return connector.RawObjectPage{}, err
	}
	if strings.TrimSpace(req.Keyword) == "" {
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeInvalidArgument, "keyword is required")
	}
	token, err := c.loadToken(ctx, req.AuthConnectionID, req.ProviderOptions.String("user_id"))
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	page, err := c.api.Search(ctx, token, req.Keyword, req.Cursor, pageSize(req.PageSize, c.Spec().MaxPageSize))
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	return c.rawObjectPage(req.AuthConnectionID, page, false), nil
}

func (c *NotionConnector) FetchPage(ctx context.Context, req connector.FetchPageRequest) (connector.RawObjectPage, error) {
	if err := ctx.Err(); err != nil {
		return connector.RawObjectPage{}, err
	}
	if req.AuthConnectionID == "" {
		return connector.RawObjectPage{}, connector.NewError(ErrorCodeAuthInvalid, "auth_connection_id is required")
	}
	if req.BindingGeneration <= 0 {
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeInvalidArgument, "binding_generation must be positive")
	}
	if err := validatePageSize(req.PageSize, c.Spec().MaxPageSize); err != nil {
		return connector.RawObjectPage{}, err
	}
	if req.ScopeType == connector.ScopeTypeDelta {
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeUnsupportedDelta, "notion delta fetch is not supported")
	}
	token, err := c.loadToken(ctx, req.AuthConnectionID, "")
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	targetRef := req.TargetRef
	if scoped := req.ScopeRef["node_ref"]; strings.TrimSpace(scoped) != "" {
		targetRef = scoped
	}
	includeRoot := strings.TrimSpace(req.Cursor) == "" && strings.TrimSpace(req.ScopeRef["node_ref"]) == ""
	return c.listPage(ctx, token, req.TargetType, targetRef, req.Cursor, req.PageSize, req.AuthConnectionID, includeRoot)
}

func (c *NotionConnector) ExportObject(ctx context.Context, req connector.ExportObjectRequest) (connector.ExportedObject, error) {
	if err := ctx.Err(); err != nil {
		return connector.ExportedObject{}, err
	}
	token, err := c.loadToken(ctx, req.ProviderMeta["auth_connection_id"], "")
	if err != nil {
		return connector.ExportedObject{}, err
	}
	kind := ObjectKind(req.ProviderMeta["kind"])
	objectID := firstNonEmpty(req.ProviderMeta["id"], req.ProviderMeta["page_id"], req.ProviderMeta["database_id"], req.ObjectKey)
	objectID = normalizeNotionID(objectID)
	if objectID == "" {
		return connector.ExportedObject{}, connector.NewError(connector.ErrorCodeInvalidArgument, "notion object id is required")
	}
	var content string
	switch kind {
	case ObjectKindDatabase:
		content, err = c.api.DatabaseToMarkdown(ctx, token, objectID)
	default:
		content, err = c.api.PageToMarkdown(ctx, token, objectID)
	}
	if err != nil {
		return connector.ExportedObject{}, err
	}
	exported := ExportedContent{
		Content:         []byte(content),
		MimeType:        "text/markdown",
		FileExtension:   ".md",
		SizeBytes:       int64(len(content)),
		ExportedVersion: req.SourceVersion,
	}
	return c.buildExportedObject(ctx, exported)
}

func (c *NotionConnector) MapObject(ctx context.Context, raw connector.RawObject) (connector.NormalizedSourceObject, error) {
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

func (c *NotionConnector) loadToken(ctx context.Context, authConnectionID, userID string) (string, error) {
	if strings.TrimSpace(authConnectionID) == "" {
		return "", connector.NewError(ErrorCodeAuthInvalid, "auth_connection_id is required")
	}
	if c.auth == nil {
		return "", connector.NewError(ErrorCodeAuthInvalid, "auth connection client is not configured")
	}
	token, err := c.auth.GetToken(ctx, tokenRequest(authConnectionID, userID))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return "", connector.NewError(ErrorCodeAuthInvalid, "access token is empty")
	}
	return strings.TrimSpace(token.AccessToken), nil
}

func (c *NotionConnector) probeTarget(ctx context.Context, token string, targetType connector.TargetType, targetRef string) (Object, error) {
	targetID := normalizeNotionID(targetRef)
	if targetID == "" {
		return Object{}, connector.NewError(connector.ErrorCodeInvalidTarget, "target_ref is required")
	}
	switch targetType {
	case TargetTypeDatabase:
		return c.api.GetDatabase(ctx, token, targetID)
	case TargetTypePage:
		return c.api.GetPage(ctx, token, targetID)
	default:
		return Object{}, connector.NewError(connector.ErrorCodeInvalidTarget, "target_type is not supported")
	}
}

func (c *NotionConnector) listPage(ctx context.Context, token string, targetType connector.TargetType, targetRef, cursor string, requestedPageSize int, authConnectionID string, includeRoot bool) (connector.RawObjectPage, error) {
	targetID := normalizeNotionID(targetRef)
	if targetID == "" {
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeInvalidTarget, "target_ref is required")
	}
	var page ObjectPage
	var root Object
	var err error
	switch targetType {
	case TargetTypeDatabase:
		root, err = c.api.GetDatabase(ctx, token, targetID)
		if err == nil {
			page, err = c.api.QueryDatabase(ctx, token, targetID, cursor, pageSize(requestedPageSize, c.Spec().MaxPageSize))
		}
	case TargetTypePage:
		root, err = c.api.GetPage(ctx, token, targetID)
		if err == nil {
			page, err = c.api.ListBlockChildren(ctx, token, targetID, cursor, pageSize(requestedPageSize, c.Spec().MaxPageSize))
		}
	default:
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeInvalidTarget, "target_type is not supported")
	}
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	rawPage := c.rawObjectPage(authConnectionID, page, true)
	if includeRoot {
		rawPage.Items = append([]connector.RawObject{c.rawObject(authConnectionID, root)}, rawPage.Items...)
	}
	return rawPage, nil
}
