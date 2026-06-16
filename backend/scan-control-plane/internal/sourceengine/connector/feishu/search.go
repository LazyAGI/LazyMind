package feishu

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

const feishuSearchRateLimitMaxAttempts = 4

func (c *FeishuConnector) search(ctx context.Context, req connector.SearchRequest) (connector.RawObjectPage, error) {
	if err := ctx.Err(); err != nil {
		return connector.RawObjectPage{}, err
	}
	keyword := strings.TrimSpace(req.Keyword)
	if keyword == "" {
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeInvalidArgument, "keyword is required")
	}
	if req.TargetType != "" && !isSupportedTargetType(req.TargetType) {
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeInvalidTarget, "target_type is not supported")
	}
	if c.auth == nil || c.api == nil {
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeInvalidArgument, "feishu clients are not configured")
	}
	if err := validatePageSize(req.PageSize, c.Spec().MaxPageSize); err != nil {
		return connector.RawObjectPage{}, err
	}
	token, err := c.loadToken(ctx, req.AuthConnectionID, req.ProviderOptions.String("user_id"))
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	page, err := c.currentLevelSearch(ctx, token.AccessToken, keyword, req)
	if err != nil {
		return connector.RawObjectPage{}, err
	}
	return c.buildRawObjectPage(req.AuthConnectionID, page, !page.HasMore), nil
}

func isWikiSearchRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	return strings.HasPrefix(ref, "wiki:") || strings.HasPrefix(ref, "feishu:wiki:")
}

type searchRoot struct {
	targetType connector.TargetType
	targetRef  string
	nodeRef    string
}

func (c *FeishuConnector) currentLevelSearch(ctx context.Context, token, keyword string, req connector.SearchRequest) (ObjectPage, error) {
	offset, err := parseCursor(req.Cursor)
	if err != nil {
		return ObjectPage{}, err
	}
	roots := searchRoots(req)
	matches := make([]Object, 0, req.PageSize)
	seenObjects := map[string]struct{}{}
	seenMatchCount := 0
	for len(roots) > 0 {
		if err := ctx.Err(); err != nil {
			return ObjectPage{}, err
		}
		root := roots[0]
		roots = roots[1:]
		cursor := ""
		for {
			page, err := c.listProviderPageForSearch(ctx, token, root, cursor, providerPageSize(root.targetType, root.nodeRef, c.Spec().MaxPageSize))
			if err != nil {
				return ObjectPage{}, err
			}
			for _, item := range page.Items {
				objectKey := objectKeyFor(item)
				if _, ok := seenObjects[objectKey]; ok {
					continue
				}
				seenObjects[objectKey] = struct{}{}
				if searchNameMatches(item, keyword) {
					seenMatchCount++
					if seenMatchCount > offset {
						matches = append(matches, item)
						if len(matches) > req.PageSize {
							return ObjectPage{Items: matches[:req.PageSize], HasMore: true, NextCursor: strconv.Itoa(offset + req.PageSize)}, nil
						}
					}
				}
			}
			if !page.HasMore {
				break
			}
			if strings.TrimSpace(page.NextCursor) == "" {
				return ObjectPage{}, connector.NewError(connector.ErrorCodeTransient, "feishu pagination cursor is empty")
			}
			cursor = page.NextCursor
		}
	}
	return ObjectPage{Items: matches}, nil
}

func searchRoots(req connector.SearchRequest) []searchRoot {
	ref := firstNonEmpty(req.NodeRef, req.TargetRef)
	switch req.TargetType {
	case TargetTypeDriveFolder:
		nodeRef := ref
		if nodeRef == "" {
			nodeRef = VirtualDriveRootRef
		}
		return []searchRoot{{targetType: TargetTypeDriveFolder, targetRef: req.TargetRef, nodeRef: nodeRef}}
	case TargetTypeWikiNode:
		nodeRef := ref
		if nodeRef == "" {
			nodeRef = VirtualWikiSpacesRef
		}
		return []searchRoot{{targetType: TargetTypeWikiNode, targetRef: req.TargetRef, nodeRef: nodeRef}}
	default:
		if isWikiSearchRef(ref) {
			return []searchRoot{{targetType: TargetTypeWikiNode, targetRef: req.TargetRef, nodeRef: ref}}
		}
		if ref != "" {
			return []searchRoot{{targetType: TargetTypeDriveFolder, targetRef: req.TargetRef, nodeRef: ref}}
		}
		return []searchRoot{
			{targetType: TargetTypeDriveFolder, nodeRef: VirtualDriveRootRef},
			{targetType: TargetTypeWikiNode, nodeRef: VirtualWikiSpacesRef},
		}
	}
}

func searchNameMatches(item Object, keyword string) bool {
	name := strings.ToLower(displayName(item.Name, item.Token))
	return strings.Contains(name, strings.ToLower(keyword))
}

func (c *FeishuConnector) listProviderPageForSearch(ctx context.Context, token string, root searchRoot, cursor string, pageSize int) (ObjectPage, error) {
	for attempt := 1; ; attempt++ {
		page, err := c.listProviderPage(ctx, token, root.targetType, root.targetRef, root.nodeRef, cursor, pageSize)
		if err == nil || !isFeishuRateLimitError(err) || attempt >= feishuSearchRateLimitMaxAttempts {
			return page, err
		}
		if err := sleepContext(ctx, c.searchRateLimitBackoff(attempt)); err != nil {
			return ObjectPage{}, err
		}
	}
}

func (c *FeishuConnector) searchRateLimitBackoff(attempt int) time.Duration {
	if c.searchRetryDelay != nil {
		return c.searchRetryDelay(attempt)
	}
	switch attempt {
	case 1:
		return 500 * time.Millisecond
	case 2:
		return time.Second
	default:
		return 2 * time.Second
	}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isFeishuRateLimitError(err error) bool {
	code, ok := connector.ErrorCodeOf(err)
	if !ok {
		return false
	}
	if code == connector.ErrorCodeRateLimited {
		return true
	}
	return code == connector.ErrorCodeTransient && isFeishuRateLimitMessage(err.Error())
}
