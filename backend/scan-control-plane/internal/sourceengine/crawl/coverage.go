package crawl

import (
	"sort"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type coverageBuilder struct {
	coverage Coverage
	keys     map[string]struct{}
	subtrees map[string]struct{}
}

func newCoverageBuilder(scopeType connector.ScopeType, scopeRef connector.ScopeRef) *coverageBuilder {
	b := &coverageBuilder{
		coverage: Coverage{ScopeType: scopeType},
		keys:     make(map[string]struct{}),
		subtrees: make(map[string]struct{}),
	}
	switch scopeType {
	case connector.ScopeTypeFull:
		b.coverage.CoveredTargetRoot = true
	case connector.ScopeTypePartial:
		if objectKey := firstScopeValue(scopeRef, "object_key"); objectKey != "" {
			b.keys[objectKey] = struct{}{}
		}
		if subtreeRoot := firstScopeValue(scopeRef, "subtree_root", "root_object_key"); subtreeRoot != "" {
			b.subtrees[subtreeRoot] = struct{}{}
		}
	}
	return b
}

func (b *coverageBuilder) observePage(page connector.RawObjectPage) {
	for _, item := range page.Items {
		if item.ObjectKey == "" {
			continue
		}
		switch b.coverage.ScopeType {
		case connector.ScopeTypeDelta, connector.ScopeTypeWatchEvent:
			if item.DeletedAtSource != nil {
				b.keys[item.ObjectKey] = struct{}{}
			}
		default:
			b.keys[item.ObjectKey] = struct{}{}
		}
	}
	if page.Watermark != "" {
		b.coverage.Watermark = page.Watermark
	}
}

func (b *coverageBuilder) complete() Coverage {
	b.coverage.Complete = true
	b.coverage.CoveredObjectKeys = sortedKeys(b.keys)
	b.coverage.CoveredSubtrees = sortedKeys(b.subtrees)
	return b.coverage
}

func (b *coverageBuilder) incomplete(reason string) Coverage {
	b.coverage.Complete = false
	b.coverage.ExcludedReason = reason
	b.coverage.CoveredObjectKeys = sortedKeys(b.keys)
	b.coverage.CoveredSubtrees = sortedKeys(b.subtrees)
	return b.coverage
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func firstScopeValue(scopeRef connector.ScopeRef, keys ...string) string {
	for _, key := range keys {
		if scopeRef[key] != "" {
			return scopeRef[key]
		}
	}
	return ""
}
