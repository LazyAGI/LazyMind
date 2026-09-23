package feishu

import (
	"context"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func (c *FeishuConnector) validateTargetRequest(req connector.ValidateTargetRequest) error {
	if err := connector.ValidateTargetConfig(c.Spec(), req); err != nil {
		return err
	}
	if c.auth == nil || c.api == nil {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "feishu clients are not configured")
	}
	return nil
}

func (c *FeishuConnector) loadToken(ctx context.Context, authConnectionID, userID string) (Token, error) {
	return c.loadTokenRequest(ctx, TokenRequest{AuthConnectionID: authConnectionID, UserID: userID})
}

func (c *FeishuConnector) loadTokenRequest(ctx context.Context, request TokenRequest) (Token, error) {
	authConnectionID := strings.TrimSpace(request.AuthConnectionID)
	if strings.TrimSpace(authConnectionID) == "" {
		return Token{}, connector.NewError(ErrorCodeAuthInvalid, "auth_connection_id is required")
	}
	request.AuthConnectionID = authConnectionID
	token, err := c.auth.GetToken(ctx, request)
	if err != nil {
		return Token{}, err
	}
	if provider := strings.ToLower(strings.TrimSpace(token.Provider)); provider != "" && provider != string(ConnectorType) {
		return Token{}, connector.NewError(ErrorCodeAuthInvalid, "Provider Token Resolver returned a mismatched provider")
	}
	if status := strings.ToUpper(strings.TrimSpace(token.Status)); status != "" && status != "ACTIVE" {
		return Token{}, connector.NewError(ErrorCodeAuthInvalid, "Provider Connection requires reauthorization")
	}
	if !validFeishuUserSubject(token) {
		return Token{}, connector.NewError(ErrorCodeAuthInvalid, "Feishu personal documents require a user token")
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return Token{}, connector.NewError(ErrorCodeAuthInvalid, "access token is empty")
	}
	return token, nil
}

func feishuTokenRequest(authConnectionID, userID, sourceID, bindingID, capability string, options connector.ProviderOptions) TokenRequest {
	if strings.TrimSpace(sourceID) == "" {
		sourceID = options.String("source_id")
	}
	if strings.TrimSpace(bindingID) == "" {
		bindingID = options.String("binding_id")
	}
	contextMode := TokenContextMode("source_binding")
	if strings.TrimSpace(sourceID) == "" && strings.TrimSpace(bindingID) == "" && capability == "datasource.browse" {
		contextMode = TokenContextMode("pre_binding_browse")
	}
	return TokenRequest{
		AuthConnectionID:   strings.TrimSpace(authConnectionID),
		UserID:             strings.TrimSpace(userID),
		TenantID:           options.String("tenant_id"),
		SourceID:           strings.TrimSpace(sourceID),
		BindingID:          strings.TrimSpace(bindingID),
		ContextMode:        contextMode,
		Consumer:           "datasource",
		RequiredCapability: capability,
	}
}

func (c *FeishuConnector) probeTarget(ctx context.Context, token string, req connector.ValidateTargetRequest) (Object, error) {
	switch req.TargetType {
	case TargetTypeDriveFolder:
		folderToken := driveFolderToken(req.TargetRef)
		if folderToken == "" || folderToken == "root" {
			return c.api.GetDriveRoot(ctx, token)
		}
		return c.api.GetDriveFolder(ctx, token, folderToken)
	case TargetTypeWikiNode:
		if strings.TrimSpace(req.TargetRef) == VirtualWikiSpacesRef {
			return Object{
				Kind:        ObjectKindVirtualRoot,
				Token:       VirtualWikiSpacesRef,
				Name:        "Wiki",
				IsContainer: true,
				HasChildren: true,
				Revision:    "virtual-wiki",
			}, nil
		}
		if nodeToken := looseWikiNodeToken(req.TargetRef); nodeToken != "" {
			return c.api.GetWikiNode(ctx, token, "", nodeToken)
		}
		spaceID, nodeToken, isSpace, err := parseWikiTarget(req.TargetRef)
		if err != nil {
			return Object{}, err
		}
		if isSpace {
			return c.getWikiSpace(ctx, token, spaceID)
		}
		return c.api.GetWikiNode(ctx, token, spaceID, nodeToken)
	default:
		return Object{}, connector.NewError(connector.ErrorCodeInvalidTarget, "target_type is not supported")
	}
}

func (c *FeishuConnector) getWikiSpace(ctx context.Context, token, spaceID string) (Object, error) {
	cursor := ""
	pageSize := min(c.Spec().MaxPageSize, 50)
	for {
		page, err := c.api.ListWikiSpaces(ctx, token, cursor, pageSize)
		if err != nil {
			return Object{}, err
		}
		for _, item := range page.Items {
			if item.SpaceID == spaceID || wikiSpaceIDFromRef(item.Token) == spaceID {
				return item, nil
			}
		}
		if !page.HasMore {
			return Object{}, connector.NewError(connector.ErrorCodeNotFound, "wiki space is not found")
		}
		if page.NextCursor == "" {
			return Object{}, connector.NewError(connector.ErrorCodeTransient, "wiki spaces pagination cursor is empty")
		}
		cursor = page.NextCursor
	}
}

func (c *FeishuConnector) buildNormalizedTarget(req connector.ValidateTargetRequest, object Object) (connector.NormalizedTarget, error) {
	raw := c.rawObject(req.AuthConnectionID, object)
	normalized, err := c.MapObject(context.Background(), raw)
	if err != nil {
		return connector.NormalizedTarget{}, err
	}
	return connector.NormalizedTarget{
		TargetType:        req.TargetType,
		TargetRef:         targetRefFor(object),
		TargetFingerprint: targetFingerprint(object),
		DisplayName:       normalized.DisplayName,
		ProviderMeta:      raw.ProviderMeta,
		RootObjectKey:     normalized.ObjectKey,
	}, nil
}
