package tree

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

const (
	targetSearchCacheListRetries = 4
	targetSearchCacheRetryDelay  = 2 * time.Second
	targetSearchCacheProgressLog = 30 * time.Second
)

type targetCacheListScope struct {
	targetType connector.TargetType
	targetRef  string
	nodeRef    string
}

func (e *DefaultTargetTreeEngine) searchCachedTargets(ctx context.Context, conn connector.SourceConnector, req TargetTreeSearchRequest) (TreeNodePage, error) {
	pageSize := normalizePageSize(req.PageSize, e.limitForConnector(conn.Spec()))
	snapshot := e.cache.snapshot(ctx, req)
	if err := ctx.Err(); err != nil {
		return TreeNodePage{}, err
	}
	page, err := paginateCachedTargetNodes(snapshot.nodes, req.Keyword, req.IncludeFiles, pageSize, req.Cursor)
	if err != nil {
		return TreeNodePage{}, err
	}
	page.CacheStatus = snapshot.status
	page.CacheBuilding = snapshot.building
	page.CacheComplete = snapshot.complete
	page.Truncated = snapshot.truncated
	page.CacheError = snapshot.lastError
	return page, nil
}

func (e *DefaultTargetTreeEngine) buildAndSearchCachedTargets(ctx context.Context, conn connector.SourceConnector, req TargetTreeSearchRequest) (TreeNodePage, error) {
	pageSize := normalizePageSize(req.PageSize, e.limitForConnector(conn.Spec()))
	snapshot := e.cache.buildIfUnlocked(ctx, conn, req, e.buildTargetSearchCache)
	if snapshot.status == targetSearchCacheStatusFailed && strings.TrimSpace(snapshot.lastError) != "" {
		return TreeNodePage{}, NewError(ErrCodeInternal, "target search cache build failed: "+snapshot.lastError)
	}
	page, err := paginateCachedTargetNodes(snapshot.nodes, req.Keyword, req.IncludeFiles, pageSize, req.Cursor)
	if err != nil {
		return TreeNodePage{}, err
	}
	page.CacheStatus = snapshot.status
	page.CacheBuilding = snapshot.building
	page.CacheComplete = snapshot.complete
	page.Truncated = snapshot.truncated
	page.CacheError = snapshot.lastError
	return page, nil
}

func (e *DefaultTargetTreeEngine) Prewarm(ctx context.Context, req TargetTreeSearchRequest) error {
	conn, err := e.registry.Get(req.ConnectorType)
	if err != nil {
		return mapConnectorError(err)
	}
	if !conn.Spec().SupportsSearch {
		return nil
	}
	snapshot := e.cache.buildIfUnlocked(ctx, conn, req, e.buildTargetSearchCache)
	if snapshot.status == targetSearchCacheStatusFailed && strings.TrimSpace(snapshot.lastError) != "" {
		return NewError(ErrCodeInternal, "target search cache prewarm failed: "+snapshot.lastError)
	}
	return nil
}

func (e *DefaultTargetTreeEngine) buildTargetSearchCache(ctx context.Context, conn connector.SourceConnector, req TargetTreeSearchRequest) ([]TreeNode, bool, error) {
	queue := initialTargetCacheQueue(req)
	seenScopes := map[string]struct{}{}
	seenNodes := map[string]struct{}{}
	nodes := make([]TreeNode, 0)
	truncated := false
	pageSize := e.limitForConnector(conn.Spec()).MaxPageSize
	listCalls := 0
	listDelay := targetSearchCacheListDelay(req, e.cache.delay)
	startedAt := time.Now()
	nextProgressLog := startedAt.Add(targetSearchCacheProgressLog)
	fmt.Fprintf(os.Stdout, "target search cache build start connector=%s auth_connection_id=%s page_size=%d max_items=%d delay=%s\n", req.ConnectorType, req.AuthConnectionID, pageSize, e.cache.maxItems, listDelay)
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nodes, truncated, err
		}
		scope := queue[0]
		queue = queue[1:]
		scopeKey := targetCacheScopeKey(scope)
		if _, ok := seenScopes[scopeKey]; ok {
			continue
		}
		seenScopes[scopeKey] = struct{}{}
		cursor := ""
		for {
			if listCalls > 0 && listDelay > 0 {
				if err := sleepTargetCache(ctx, listDelay); err != nil {
					return nodes, truncated, err
				}
			}
			listCalls++
			rawPage, err := listTargetCacheChildrenWithRetry(ctx, conn, connector.ListChildrenRequest{
				TargetType:       scope.targetType,
				TargetRef:        scope.targetRef,
				NodeRef:          scope.nodeRef,
				ListMode:         connector.ListModePage,
				Cursor:           cursor,
				PageSize:         pageSize,
				AgentID:          req.AgentID,
				AuthConnectionID: req.AuthConnectionID,
				ProviderOptions:  connector.ProviderOptions(req.ProviderOptions),
			})
			if err != nil {
				return nodes, truncated, mapConnectorError(err)
			}
			if time.Now().After(nextProgressLog) {
				fmt.Fprintf(os.Stdout, "target search cache build progress connector=%s auth_connection_id=%s list_calls=%d nodes=%d queue=%d scopes=%d target_type=%s target_ref=%q node_ref=%q cursor=%q has_more=%t next_cursor=%q elapsed=%s\n", req.ConnectorType, req.AuthConnectionID, listCalls, len(nodes), len(queue), len(seenScopes), scope.targetType, scope.targetRef, scope.nodeRef, cursor, rawPage.HasMore, rawPage.NextCursor, time.Since(startedAt).Truncate(time.Second))
				nextProgressLog = time.Now().Add(targetSearchCacheProgressLog)
			}
			for _, raw := range rawPage.Items {
				normalized, err := conn.MapObject(ctx, raw)
				if err != nil {
					return nodes, truncated, mapConnectorError(err)
				}
				node := targetNode(req.ConnectorType, raw, normalized)
				if _, ok := seenNodes[node.ObjectKey]; ok {
					continue
				}
				seenNodes[node.ObjectKey] = struct{}{}
				nodes = append(nodes, node)
				if len(nodes) >= e.cache.maxItems {
					truncated = true
					return nodes, truncated, nil
				}
				if isTargetDirectoryNode(raw, normalized) {
					child := targetCacheChildScope(raw, scope)
					if child.targetType != "" || child.targetRef != "" || child.nodeRef != "" {
						queue = append(queue, child)
					}
				}
			}
			if !rawPage.HasMore {
				break
			}
			if rawPage.NextCursor == "" {
				fmt.Fprintf(os.Stdout, "target search cache build pagination_error connector=%s auth_connection_id=%s reason=empty_cursor list_calls=%d nodes=%d target_type=%s target_ref=%q node_ref=%q cursor=%q elapsed=%s\n", req.ConnectorType, req.AuthConnectionID, listCalls, len(nodes), scope.targetType, scope.targetRef, scope.nodeRef, cursor, time.Since(startedAt).Truncate(time.Second))
				return nodes, truncated, NewError(ErrCodeInternal, "target cache pagination cursor is empty")
			}
			if rawPage.NextCursor == cursor {
				fmt.Fprintf(os.Stdout, "target search cache build pagination_error connector=%s auth_connection_id=%s reason=cursor_not_advanced list_calls=%d nodes=%d target_type=%s target_ref=%q node_ref=%q cursor=%q elapsed=%s\n", req.ConnectorType, req.AuthConnectionID, listCalls, len(nodes), scope.targetType, scope.targetRef, scope.nodeRef, cursor, time.Since(startedAt).Truncate(time.Second))
				return nodes, truncated, NewError(ErrCodeInternal, "target cache pagination cursor did not advance")
			}
			cursor = rawPage.NextCursor
		}
	}
	return nodes, truncated, nil
}

func initialTargetCacheQueue(req TargetTreeSearchRequest) []targetCacheListScope {
	if targetSearchHasCurrentLevel(req) {
		return []targetCacheListScope{{
			targetType: req.TargetType,
			targetRef:  req.TargetRef,
			nodeRef:    req.NodeRef,
		}}
	}
	return []targetCacheListScope{{}}
}

func targetSearchCacheListDelay(req TargetTreeSearchRequest, delay time.Duration) time.Duration {
	if strings.EqualFold(strings.TrimSpace(string(req.ConnectorType)), "feishu") {
		return delay
	}
	return 0
}

func listTargetCacheChildrenWithRetry(ctx context.Context, conn connector.SourceConnector, req connector.ListChildrenRequest) (connector.RawObjectPage, error) {
	var lastErr error
	for attempt := 0; attempt <= targetSearchCacheListRetries; attempt++ {
		page, err := conn.ListChildren(ctx, req)
		if err == nil {
			return page, nil
		}
		lastErr = err
		if attempt == targetSearchCacheListRetries || !isTargetCacheRetriableListError(err) {
			return connector.RawObjectPage{}, err
		}
		delay := targetSearchCacheRetryDelay << attempt
		fmt.Fprintf(os.Stdout, "target search cache list retry auth_connection_id=%s attempt=%d delay=%s target_type=%s target_ref=%q node_ref=%q cursor=%q error=%q\n", req.AuthConnectionID, attempt+1, delay, req.TargetType, req.TargetRef, req.NodeRef, req.Cursor, err.Error())
		if err := sleepTargetCache(ctx, delay); err != nil {
			return connector.RawObjectPage{}, err
		}
	}
	return connector.RawObjectPage{}, lastErr
}

func isTargetCacheRetriableListError(err error) bool {
	code, ok := connector.ErrorCodeOf(err)
	if ok && code == connector.ErrorCodeRateLimited {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "request trigger frequency limit") ||
		strings.Contains(message, "rate_limited")
}

func targetCacheChildScope(raw connector.RawObject, parent targetCacheListScope) targetCacheListScope {
	targetType := raw.BindingTargetType
	if targetType == "" {
		targetType = parent.targetType
	}
	targetRef := raw.BindingTargetRef
	if targetRef == "" {
		targetRef = parent.targetRef
	}
	return targetCacheListScope{
		targetType: targetType,
		targetRef:  targetRef,
		nodeRef:    raw.ObjectRef,
	}
}

func targetCacheScopeKey(scope targetCacheListScope) string {
	return string(scope.targetType) + "\x00" + scope.targetRef + "\x00" + scope.nodeRef
}

func sleepTargetCache(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
