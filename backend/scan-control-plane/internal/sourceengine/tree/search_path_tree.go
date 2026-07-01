package tree

import "strings"

const (
	feishuConnectorType = "feishu"
	feishuDriveRootRef  = "feishu:drive:root"
	feishuWikiRootRef   = "feishu:wiki:spaces"
	feishuDriveRootKey  = feishuConnectorType + ":" + feishuDriveRootRef
	feishuWikiRootKey   = feishuConnectorType + ":" + feishuWikiRootRef
)

type searchPathNode struct {
	node      TreeNode
	children  []*searchPathNode
	childKeys map[string]struct{}
}

func buildSearchPathTree(allNodes, matches []TreeNode) []TreeNode {
	if len(allNodes) == 0 || len(matches) == 0 {
		return matches
	}
	byKey := make(map[string]TreeNode, len(allNodes))
	for _, node := range allNodes {
		key := treeNodeIdentity(node)
		if key == "" {
			continue
		}
		node.Children = nil
		byKey[key] = node
	}
	nodes := make(map[string]*searchPathNode, len(matches))
	rootKeys := map[string]struct{}{}
	roots := make([]*searchPathNode, 0, len(matches))
	for _, match := range matches {
		path := searchNodePath(byKey, match)
		if len(path) == 0 {
			continue
		}
		var parent *searchPathNode
		for _, node := range path {
			key := treeNodeIdentity(node)
			if key == "" {
				continue
			}
			current, ok := nodes[key]
			if !ok {
				node.Children = nil
				current = &searchPathNode{node: node}
				nodes[key] = current
			}
			if parent == nil {
				if _, ok := rootKeys[key]; !ok {
					rootKeys[key] = struct{}{}
					roots = append(roots, current)
				}
				parent = current
				continue
			}
			if parent.childKeys == nil {
				parent.childKeys = map[string]struct{}{}
			}
			if _, ok := parent.childKeys[key]; !ok {
				parent.childKeys[key] = struct{}{}
				parent.children = append(parent.children, current)
			}
			parent = current
		}
	}
	if len(roots) == 0 {
		return matches
	}
	out := make([]TreeNode, 0, len(roots))
	for _, root := range roots {
		out = append(out, materializeSearchPathNode(root))
	}
	return wrapFeishuSearchRoots(out)
}

func searchNodePath(byKey map[string]TreeNode, match TreeNode) []TreeNode {
	key := treeNodeIdentity(match)
	if key == "" {
		return nil
	}
	current := match
	if node, ok := byKey[key]; ok {
		current = node
	}
	reversed := make([]TreeNode, 0, 4)
	seen := map[string]struct{}{}
	for {
		currentKey := treeNodeIdentity(current)
		if currentKey == "" {
			break
		}
		if _, ok := seen[currentKey]; ok {
			break
		}
		seen[currentKey] = struct{}{}
		reversed = append(reversed, current)
		parentKey := strings.TrimSpace(current.ParentKey)
		if parentKey == "" || parentKey == currentKey {
			break
		}
		parent, ok := byKey[parentKey]
		if !ok {
			break
		}
		current = parent
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}

func materializeSearchPathNode(node *searchPathNode) TreeNode {
	out := node.node
	out.Children = nil
	if len(node.children) > 0 {
		out.HasChildren = true
		out.Children = make([]TreeNode, 0, len(node.children))
		for _, child := range node.children {
			out.Children = append(out.Children, materializeSearchPathNode(child))
		}
	}
	return out
}

func treeNodeIdentity(node TreeNode) string {
	if key := strings.TrimSpace(node.ObjectKey); key != "" {
		return key
	}
	return strings.TrimSpace(node.Key)
}

func wrapFeishuSearchRoots(nodes []TreeNode) []TreeNode {
	if len(nodes) == 0 {
		return nodes
	}
	var driveRoot *TreeNode
	var wikiRoot *TreeNode
	var driveChildren []TreeNode
	var wikiChildren []TreeNode
	var other []TreeNode
	for _, node := range nodes {
		switch feishuSearchRootKind(node) {
		case "drive_root":
			root := node
			driveRoot = &root
		case "wiki_root":
			root := node
			wikiRoot = &root
		case "drive":
			driveChildren = append(driveChildren, node)
		case "wiki":
			wikiChildren = append(wikiChildren, node)
		default:
			other = append(other, node)
		}
	}
	out := make([]TreeNode, 0, len(nodes)+2)
	if driveRoot != nil || len(driveChildren) > 0 {
		root := feishuVirtualRoot("drive", driveChildren)
		if driveRoot != nil {
			root = *driveRoot
		}
		root.Children = append(root.Children, driveChildren...)
		if len(root.Children) > 0 {
			root.HasChildren = true
		}
		out = append(out, root)
	}
	if wikiRoot != nil || len(wikiChildren) > 0 {
		root := feishuVirtualRoot("wiki", wikiChildren)
		if wikiRoot != nil {
			root = *wikiRoot
		}
		root.Children = append(root.Children, wikiChildren...)
		if len(root.Children) > 0 {
			root.HasChildren = true
		}
		out = append(out, root)
	}
	out = append(out, other...)
	if len(out) == len(other) {
		return nodes
	}
	return out
}

func feishuVirtualRoot(kind string, children []TreeNode) TreeNode {
	authConnectionID := feishuAuthConnectionID(children)
	if kind == "wiki" {
		return TreeNode{
			Key:           feishuWikiRootKey,
			NodeRef:       feishuWikiRootRef,
			DisplayName:   "Wiki",
			SearchName:    "wiki",
			ConnectorType: feishuConnectorType,
			TargetType:    "wiki_node",
			TargetRef:     feishuWikiRootRef,
			TreeKey:       feishuWikiRootKey,
			ObjectKey:     feishuWikiRootKey,
			IsContainer:   true,
			HasChildren:   len(children) > 0,
			Selectable:    true,
			ProviderMeta:  feishuVirtualRootMeta(feishuWikiRootRef, authConnectionID),
		}
	}
	return TreeNode{
		Key:           feishuDriveRootKey,
		NodeRef:       feishuDriveRootRef,
		DisplayName:   "Drive",
		SearchName:    "drive",
		ConnectorType: feishuConnectorType,
		TargetType:    "drive_folder",
		TargetRef:     feishuDriveRootRef,
		TreeKey:       feishuDriveRootKey,
		ObjectKey:     feishuDriveRootKey,
		IsContainer:   true,
		HasChildren:   len(children) > 0,
		Selectable:    true,
		ProviderMeta:  feishuVirtualRootMeta(feishuDriveRootRef, authConnectionID),
	}
}

func feishuVirtualRootMeta(token, authConnectionID string) map[string]any {
	meta := map[string]any{
		"kind":  "virtual_root",
		"token": token,
	}
	if authConnectionID != "" {
		meta["auth_connection_id"] = authConnectionID
	}
	return meta
}

func feishuAuthConnectionID(nodes []TreeNode) string {
	for _, node := range nodes {
		if value, ok := node.ProviderMeta["auth_connection_id"].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
		if value := feishuAuthConnectionID(node.Children); value != "" {
			return value
		}
	}
	return ""
}

func feishuSearchRootKind(node TreeNode) string {
	key := strings.TrimSpace(treeNodeIdentity(node))
	ref := strings.TrimSpace(firstNonEmptyTreeValue(node.TargetRef, node.NodeRef, node.TreeKey, node.Key))
	if key == feishuDriveRootKey || ref == feishuDriveRootRef {
		return "drive_root"
	}
	if key == feishuWikiRootKey || ref == feishuWikiRootRef {
		return "wiki_root"
	}
	if !isFeishuTreeNode(node) {
		return ""
	}
	if feishuNodeLooksWiki(node) {
		return "wiki"
	}
	if feishuNodeLooksDrive(node) {
		return "drive"
	}
	return ""
}

func isFeishuTreeNode(node TreeNode) bool {
	if strings.EqualFold(strings.TrimSpace(node.ConnectorType), feishuConnectorType) {
		return true
	}
	for _, value := range []string{node.Key, node.ObjectKey, node.TreeKey, node.TargetRef, node.NodeRef} {
		if strings.HasPrefix(strings.TrimSpace(value), feishuConnectorType+":") {
			return true
		}
	}
	return false
}

func feishuNodeLooksWiki(node TreeNode) bool {
	if kind, ok := node.ProviderMeta["kind"].(string); ok && strings.HasPrefix(kind, "wiki_") {
		return true
	}
	for _, value := range []string{node.Key, node.ObjectKey, node.TreeKey, node.TargetRef, node.NodeRef} {
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "feishu:wiki:") || strings.HasPrefix(value, "wiki:") {
			return true
		}
	}
	return false
}

func feishuNodeLooksDrive(node TreeNode) bool {
	if kind, ok := node.ProviderMeta["kind"].(string); ok && strings.HasPrefix(kind, "drive_") {
		return true
	}
	for _, value := range []string{node.Key, node.ObjectKey, node.TreeKey, node.TargetRef, node.NodeRef} {
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "feishu:drive:") || strings.HasPrefix(value, "drive:") {
			return true
		}
	}
	return false
}

func firstNonEmptyTreeValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
