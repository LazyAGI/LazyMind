package tree

import (
	"context"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type RecommendedLocalPathRule struct {
	ProductID   string
	ProductName string
	Pattern     string
}

// RecommendedLocalPathRules is the maintained list of local directory shortcuts.
// Add a rule here to expose another product directory through the recommendations API.
var RecommendedLocalPathRules = []RecommendedLocalPathRule{
	{
		ProductID:   "cursor_rules",
		ProductName: "Cursor Rules",
		Pattern:     ".cursor/rules",
	},
	{
		ProductID:   "cursor_skills",
		ProductName: "Cursor Skills",
		Pattern:     ".cursor/skills",
	},
	{
		ProductID:   "agent_skills",
		ProductName: "Cursor / Codex Skills",
		Pattern:     ".agents/skills",
	},
	{
		ProductID:   "codex_skills",
		ProductName: "Codex Skills",
		Pattern:     ".codex/skills",
	},
	{
		ProductID:   "feishu_download",
		ProductName: "飞书下载目录",
		Pattern:     "Downloads/飞书",
	},
	{
		ProductID:   "feishu_download",
		ProductName: "飞书下载目录",
		Pattern:     "Downloads/Feishu",
	},
	{
		ProductID:   "feishu_download",
		ProductName: "Lark 下载目录",
		Pattern:     "Downloads/Lark",
	},
	{
		ProductID:   "feishu_documents",
		ProductName: "飞书文档目录",
		Pattern:     "Documents/飞书",
	},
	{
		ProductID:   "feishu_documents",
		ProductName: "飞书文档目录",
		Pattern:     "Documents/Feishu",
	},
	{
		ProductID:   "feishu_documents",
		ProductName: "Lark 文档目录",
		Pattern:     "Documents/Lark",
	},
	{
		ProductID:   "baidu_download",
		ProductName: "百度网盘下载目录",
		Pattern:     "Downloads/BaiduNetdiskDownload",
	},
	{
		ProductID:   "baidu_download",
		ProductName: "百度网盘下载目录",
		Pattern:     "BaiduNetdiskDownload",
	},
	{
		ProductID:   "baidu_download",
		ProductName: "百度网盘下载目录",
		Pattern:     "BaiduYunDownload",
	},
	{
		ProductID:   "baidu_download",
		ProductName: "百度网盘下载目录",
		Pattern:     "BaiduDownload",
	},
}

func (e *DefaultTargetTreeEngine) Recommend(ctx context.Context, req TargetTreeRecommendationRequest) (TreeNodePage, error) {
	return e.recommendLocalPaths(ctx, req, RecommendedLocalPathRules)
}

func (e *DefaultTargetTreeEngine) RecommendList(ctx context.Context, req TargetTreeRecommendationRequest) (TreeNodePage, error) {
	page, err := e.Recommend(ctx, req)
	if err != nil {
		return TreeNodePage{}, err
	}
	page.Items = flattenRecommendedTreeNodes(page.Items, RecommendedLocalPathRules)
	return page, nil
}

func (e *DefaultTargetTreeEngine) recommendLocalPaths(ctx context.Context, req TargetTreeRecommendationRequest, rules []RecommendedLocalPathRule) (TreeNodePage, error) {
	result := TreeNodePage{
		Items:         []TreeNode{},
		ListComplete:  true,
		SearchMode:    SearchModeCache,
		CacheStatus:   targetSearchCacheStatusComplete,
		CacheComplete: true,
	}
	if len(rules) == 0 {
		return result, nil
	}

	mergedRoots := make([]TreeNode, 0)
	for _, rule := range rules {
		pattern := strings.TrimSpace(rule.Pattern)
		if pattern == "" {
			continue
		}
		cursor := ""
		for {
			page, err := e.Search(ctx, TargetTreeSearchRequest{
				ConnectorType:   connector.ConnectorType("local_fs"),
				TargetType:      connector.TargetType("local_path"),
				Keyword:         pattern,
				AgentID:         req.AgentID,
				ProviderOptions: req.ProviderOptions,
				IncludeFiles:    false,
				ListMode:        ListModePage,
				PageSize:        e.limits.MaxPageSize,
				Cursor:          cursor,
			})
			if err != nil {
				return TreeNodePage{}, err
			}
			mergedRoots = mergeRecommendedTreeNodes(mergedRoots, page.Items)
			result.Truncated = result.Truncated || page.Truncated
			result.CacheBuilding = result.CacheBuilding || page.CacheBuilding
			result.CacheComplete = result.CacheComplete && page.CacheComplete
			if page.CacheError != "" && result.CacheError == "" {
				result.CacheError = page.CacheError
			}
			if page.CacheStatus == targetSearchCacheStatusFailed {
				result.CacheStatus = targetSearchCacheStatusFailed
			} else if page.CacheStatus == targetSearchCacheStatusBuilding &&
				result.CacheStatus != targetSearchCacheStatusFailed {
				result.CacheStatus = targetSearchCacheStatusBuilding
			}
			if !page.HasMore {
				break
			}
			if strings.TrimSpace(page.NextCursor) == "" || page.NextCursor == cursor {
				return TreeNodePage{}, NewError(ErrCodeInternal, "target cache pagination cursor did not advance")
			}
			cursor = page.NextCursor
		}
	}
	result.Items = mergedRoots
	return result, nil
}

func mergeRecommendedTreeNodes(existing, incoming []TreeNode) []TreeNode {
	roots := make([]*searchPathNode, 0, len(existing)+len(incoming))
	byKey := make(map[string]*searchPathNode, len(existing)+len(incoming))
	for _, node := range append(append([]TreeNode(nil), existing...), incoming...) {
		key := treeNodeIdentity(node)
		if key == "" {
			continue
		}
		root, ok := byKey[key]
		if !ok {
			base := node
			base.Children = nil
			root = &searchPathNode{node: base}
			byKey[key] = root
			roots = append(roots, root)
		}
		mergeRecommendedTreeChildren(root, node.Children)
	}

	out := make([]TreeNode, 0, len(roots))
	for _, root := range roots {
		out = append(out, materializeSearchPathNode(root))
	}
	return out
}

func mergeRecommendedTreeChildren(parent *searchPathNode, children []TreeNode) {
	if len(children) == 0 {
		return
	}
	if parent.childKeys == nil {
		parent.childKeys = map[string]struct{}{}
	}
	existing := make(map[string]*searchPathNode, len(parent.children))
	for _, child := range parent.children {
		existing[treeNodeIdentity(child.node)] = child
	}
	for _, childNode := range children {
		key := treeNodeIdentity(childNode)
		if key == "" {
			continue
		}
		child, ok := existing[key]
		if !ok {
			base := childNode
			base.Children = nil
			child = &searchPathNode{node: base}
			existing[key] = child
			parent.childKeys[key] = struct{}{}
			parent.children = append(parent.children, child)
		}
		mergeRecommendedTreeChildren(child, childNode.Children)
	}
}

func flattenRecommendedTreeNodes(nodes []TreeNode, rules []RecommendedLocalPathRule) []TreeNode {
	items := make([]TreeNode, 0)
	seen := make(map[string]struct{})
	var visit func([]TreeNode)
	visit = func(current []TreeNode) {
		for _, node := range current {
			for _, rule := range rules {
				pattern := strings.TrimSpace(rule.Pattern)
				if pattern == "" || !treeNodeSearchMatches(node, pattern) {
					continue
				}
				key := treeNodeIdentity(node)
				if _, ok := seen[key]; !ok && key != "" {
					seen[key] = struct{}{}
					item := node
					item.Children = nil
					items = append(items, item)
				}
				break
			}
			visit(node.Children)
		}
	}
	visit(nodes)
	return items
}
