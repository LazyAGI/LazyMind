package workflowtool

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/executor"
)

func acceptsPDFResource(name string) bool {
	return name == "pdf_read_text" || name == "pdf_extract_images"
}

// The public schema accepts an immutable input reference. Bytes stay between
// Core and the audited adapter, rather than entering the agent's context.
func withFileResourceSchema(tool Tool) Tool {
	if tool.Source != "package" || !acceptsPDFResource(tool.Name) || tool.InputSchema == nil {
		return tool
	}
	properties, ok := tool.InputSchema["properties"].(map[string]any)
	if !ok {
		return tool
	}
	properties["resource_ref"] = map[string]any{"type": "string", "pattern": "^resource:.+",
		"description": "Use resource:<source_id> from this step's pinned file inputs. Core resolves bytes and verifies the owner, revision and hash."}
	remaining := []string{}
	switch required := tool.InputSchema["required"].(type) {
	case []any:
		for _, key := range required {
			if name, ok := key.(string); ok && name != "content_base64" {
				remaining = append(remaining, name)
			}
		}
	case []string:
		for _, name := range required {
			if name != "content_base64" {
				remaining = append(remaining, name)
			}
		}
	}
	if len(remaining) > 0 {
		tool.InputSchema["required"] = remaining
	} else {
		delete(tool.InputSchema, "required")
	}
	tool.InputSchema["oneOf"] = []any{
		map[string]any{"required": []string{"resource_ref"}},
		map[string]any{"required": []string{"content_base64"}},
	}
	return tool
}

func pinnedFileInput(value any, id string) map[string]any {
	switch item := value.(type) {
	case map[string]any:
		if item["source_type"] == "input_resource" && item["source_id"] == id {
			return item
		}
	case []any:
		for _, nested := range item {
			if bound, ok := nested.(map[string]any); ok && bound["source_type"] == "input_resource" && bound["source_id"] == id {
				return bound
			}
		}
	case []map[string]any:
		for _, bound := range item {
			if bound["source_type"] == "input_resource" && bound["source_id"] == id {
				return bound
			}
		}
	}
	return nil
}

func (s *Service) resolveFileArguments(ctx context.Context, owner string, contract executor.AttemptContext, name string, arguments map[string]any) (map[string]any, error) {
	if !acceptsPDFResource(name) {
		return arguments, nil
	}
	raw, present := arguments["resource_ref"]
	if !present {
		return arguments, nil
	}
	ref, ok := raw.(string)
	_, hasBytes := arguments["content_base64"]
	if !ok || !strings.HasPrefix(ref, "resource:") || len(ref) > 256 || hasBytes {
		return nil, &ToolError{Code: "INVALID_ARGUMENT", Message: "Supply either resource_ref (resource:<id>) or content_base64, never both"}
	}
	id := strings.TrimPrefix(ref, "resource:")
	var bound map[string]any
	for _, value := range contract.Inputs {
		if bound = pinnedFileInput(value, id); bound != nil {
			break
		}
	}
	if id == "" || bound == nil {
		return nil, &ToolError{Code: "PERMISSION_DENIED", Message: "The file must be a pinned input of the current step"}
	}
	var resource orm.WorkflowInputResource
	if err := s.DB.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, owner).Take(&resource).Error; err != nil {
		return nil, &ToolError{Code: "PERMISSION_DENIED", Message: "The input file is unavailable to this user"}
	}
	if len(resource.Content) == 0 || len(resource.Content) > 24<<20 || resource.Size != int64(len(resource.Content)) {
		return nil, &ToolError{Code: "INVALID_ARGUMENT", Message: "The PDF input is empty or exceeds 24 MiB"}
	}
	hash := fmt.Sprintf("sha256:%x", sha256.Sum256(resource.Content))
	if bound["source_revision"] != strconv.FormatInt(resource.Revision, 10) || bound["content_hash"] != hash || resource.ContentHash != hash {
		return nil, &ToolError{Code: "INPUT_VERSION_CHANGED", Message: "The file no longer matches its pinned revision and content hash"}
	}
	result := make(map[string]any, len(arguments)+1)
	for key, value := range arguments {
		if key != "resource_ref" {
			result[key] = value
		}
	}
	result["content_base64"] = base64.StdEncoding.EncodeToString(resource.Content)
	result["filename"] = resource.Name
	return result, nil
}
