package store

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// ParseInputBindings accepts the legacy single reference and ordered lists.
// Each list position has its own command identity, including repeated resources.
func ParseInputBindings(values map[string]any, commandID string) ([]InputBinding, error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []InputBinding{}
	for _, material := range keys {
		if strings.TrimSpace(material) == "" {
			return nil, fmt.Errorf("input_bindings requires a nonempty material ID")
		}
		items, list := values[material].([]any)
		if !list {
			items = []any{values[material]}
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("input_bindings[%q] must not be an empty list", material)
		}
		for index, raw := range items {
			value, ok := raw.(map[string]any)
			resourceID, _ := value["resource_id"].(string)
			if !ok || strings.TrimSpace(resourceID) == "" {
				return nil, fmt.Errorf("input_bindings[%q][%d] requires an object with resource_id", material, index)
			}
			for field := range value {
				if field != "resource_id" && field != "revision" && field != "content_hash" {
					return nil, fmt.Errorf("input_bindings[%q]: unknown field %q", material, field)
				}
			}
			var revision int64
			if rawRevision, exists := value["revision"]; exists {
				number, ok := rawRevision.(float64)
				if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < 1 || number >= math.MaxInt64 || math.Trunc(number) != number {
					return nil, fmt.Errorf("input_bindings[%q].revision must be a positive integer", material)
				}
				revision = int64(number)
			}
			hash := ""
			if rawHash, exists := value["content_hash"]; exists {
				var ok bool
				hash, ok = rawHash.(string)
				if !ok || strings.TrimSpace(hash) == "" {
					return nil, fmt.Errorf("input_bindings[%q].content_hash must be a nonempty string", material)
				}
			}
			itemCommand := commandID
			if list {
				itemCommand = fmt.Sprintf("%s:item:%08d", commandID, index)
			}
			out = append(out, InputBinding{MaterialID: material, ResourceType: "input_resource", ResourceID: resourceID, ResourceRevision: revision, ContentHash: hash, CreatedByCommandID: itemCommand})
		}
	}
	return out, nil
}

// ResolveInputBindings validates ownership and pins omitted revision/hash fields.
func (r *Repository) ResolveInputBindings(ctx context.Context, owner string, values map[string]any, commandID string) ([]InputBinding, error) {
	bindings, err := ParseInputBindings(values, commandID)
	if err != nil {
		return nil, err
	}
	for index := range bindings {
		binding := &bindings[index]
		resource, err := r.GetInputResource(ctx, owner, binding.ResourceID)
		if err != nil {
			return nil, fmt.Errorf("input_bindings[%q]: resource is unavailable to this user: %w", binding.MaterialID, err)
		}
		if (binding.ResourceRevision != 0 && binding.ResourceRevision != resource.Revision) || (binding.ContentHash != "" && binding.ContentHash != resource.ContentHash) {
			return nil, fmt.Errorf("input_bindings[%q]: resource revision or content_hash mismatch", binding.MaterialID)
		}
		binding.ResourceRevision, binding.ContentHash = resource.Revision, resource.ContentHash
	}
	return bindings, nil
}
