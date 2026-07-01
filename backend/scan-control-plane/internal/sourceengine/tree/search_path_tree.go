package tree

import "strings"

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
	hasPath := false
	for _, match := range matches {
		path := searchNodePath(byKey, match)
		if len(path) == 0 {
			continue
		}
		if len(path) > 1 {
			hasPath = true
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
	if !hasPath || len(roots) == 0 {
		return matches
	}
	out := make([]TreeNode, 0, len(roots))
	for _, root := range roots {
		out = append(out, materializeSearchPathNode(root))
	}
	return out
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
