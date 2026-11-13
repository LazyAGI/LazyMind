package workflowmcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// InputSource names one local file or an existing immutable input resource.
// Arrays preserve the caller's order, including repeated resources.
type InputSource struct {
	LocalPath   string `json:"local_path,omitempty" jsonschema:"Absolute regular-file path on the Connector machine; mutually exclusive with resource_id. Resolve or download host attachments first."`
	ResourceID  string `json:"resource_id,omitempty" jsonschema:"Existing LazyMind input resource; mutually exclusive with local_path"`
	Revision    *int64 `json:"revision,omitempty" jsonschema:"Optional positive resource revision; only with resource_id"`
	ContentHash string `json:"content_hash,omitempty" jsonschema:"Optional resource content_hash; only with resource_id"`
}

func (c *Client) prepareStartInputs(ctx context.Context, sources map[string][]InputSource, existing map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return existing, nil
	}
	materials := make([]string, 0, len(sources))
	for material := range sources {
		materials = append(materials, material)
	}
	sort.Strings(materials)
	// Validate every source before uploading anything. File bytes are read one
	// at a time below; a failure can leave reusable resources, never a new run.
	for _, material := range materials {
		if strings.TrimSpace(material) == "" || material != strings.TrimSpace(material) {
			return nil, fmt.Errorf("inputs requires a nonempty material ID without surrounding whitespace")
		}
		if _, duplicate := existing[material]; duplicate {
			return nil, fmt.Errorf("material %q must use either inputs or input_bindings, not both", material)
		}
		if len(sources[material]) == 0 {
			return nil, fmt.Errorf("inputs[%q] must not be empty", material)
		}
		for index, source := range sources[material] {
			label := fmt.Sprintf("inputs[%q][%d]", material, index)
			if (source.LocalPath == "") == (source.ResourceID == "") {
				return nil, fmt.Errorf("%s requires exactly one of local_path or resource_id", label)
			}
			if source.LocalPath != "" {
				if source.Revision != nil || source.ContentHash != "" {
					return nil, fmt.Errorf("%s: revision and content_hash are only valid with resource_id", label)
				}
				file, err := openLocalInputFile(source.LocalPath)
				if err != nil {
					return nil, fmt.Errorf("%s (%s): %w", label, source.LocalPath, err)
				}
				if err := file.Close(); err != nil {
					return nil, fmt.Errorf("%s: close local_path: %w", label, err)
				}
			} else if strings.TrimSpace(source.ResourceID) == "" || source.Revision != nil && *source.Revision < 1 || source.ContentHash != "" && strings.TrimSpace(source.ContentHash) == "" {
				return nil, fmt.Errorf("%s requires a nonempty resource_id and a positive revision when supplied", label)
			}
		}
	}
	bindings := make(map[string]any, len(existing)+len(sources))
	for material, value := range existing {
		bindings[material] = value
	}
	for _, material := range materials {
		items := make([]any, 0, len(sources[material]))
		for index, source := range sources[material] {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if source.LocalPath != "" {
				file, err := readLocalInputFile(source.LocalPath)
				if err != nil {
					return nil, fmt.Errorf("inputs[%q][%d] (%s): %w", material, index, source.LocalPath, err)
				}
				imported, err := c.ImportInput(ctx, file.Name, file.MIMEType, file.Hash, file.Base64, int64(file.Size))
				if err != nil {
					return nil, fmt.Errorf("inputs[%q][%d] (%s): import failed: %w", material, index, file.Name, err)
				}
				items = append(items, imported.Binding)
				continue
			}
			binding := map[string]any{"resource_id": source.ResourceID}
			if source.Revision != nil {
				binding["revision"] = *source.Revision
			}
			if source.ContentHash != "" {
				binding["content_hash"] = source.ContentHash
			}
			items = append(items, binding)
		}
		bindings[material] = items
	}
	return bindings, nil
}
