package workflow

import (
	"encoding/base64"
	"fmt"
	"math"
	"sort"
	"strings"

	"lazymind/core/common/orm"
)

// Keep a fingerprint after large input bytes have been removed from task state.
func externalTaskRequestHash(body externalAgentWorkflowTaskRequest) string {
	body.IdempotencyKey = ""
	body.Skill.ZipBase64 = ""
	return sha256Hex(mustJSON(body))
}

func externalTaskInputDescriptors(task orm.ExternalAgentWorkflowTask) []map[string]string {
	request := decodeJSONMap(task.RequestJSON)
	inputs := []map[string]string{}
	bindings, _ := request["input_bindings"].(map[string]any)
	for id := range bindings {
		inputs = append(inputs, map[string]string{"material_id": id})
	}
	files, _ := request["input_files"].([]any)
	for _, raw := range files {
		file, _ := raw.(map[string]any)
		inputs = append(inputs, map[string]string{"material_id": stringFromMap(file, "material_id"), "name": stringFromMap(file, "name"), "mime_type": stringFromMap(file, "mime_type")})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i]["material_id"] < inputs[j]["material_id"] })
	return inputs
}

func externalTaskGenerationDescription(task orm.ExternalAgentWorkflowTask) string {
	lines := []string{
		"Create a reusable LazyMind Workflow from the supplied Skill for external Agent execution.",
		"The Workflow must stay task-agnostic: do not hard-code the current caller task, examples, search keywords, or one-off requirements into workflow.yaml, state.yml, scenario.md, or scripts.",
		"Each run receives the caller's concrete task as runtime user input. Make executable step prompts use {{user_input}} as the authoritative request to complete.",
		"At execution time LazyMind passes task_description as user_input and as the session intent. The Workflow must follow that runtime input when selecting keywords, filters, outputs, and final answer content.",
	}
	inputs := externalTaskInputDescriptors(task)
	if len(inputs) == 0 {
		return strings.Join(lines, "\n")
	}
	lines = append(lines,
		"The caller may supply these external input materials. Preserve their material_id values in the Workflow input declarations and bind consuming steps to them:",
		string(mustJSON(inputs)),
	)
	return strings.Join(lines, "\n")
}

func validateExternalTaskInputs(body externalAgentWorkflowTaskRequest) error {
	seen := make(map[string]bool)
	for id, raw := range body.InputBindings {
		binding, ok := raw.(map[string]any)
		if strings.TrimSpace(id) == "" || !ok || stringFromMap(binding, "resource_id") == "" {
			return fmt.Errorf("input_bindings[%q] requires an object with resource_id", id)
		}
		for field := range binding {
			if field != "resource_id" && field != "revision" && field != "content_hash" {
				return fmt.Errorf("input_bindings[%q]: unknown field %q", id, field)
			}
		}
		if rawRevision, exists := binding["revision"]; exists {
			revision, ok := rawRevision.(float64)
			if !ok || revision < 1 || math.Trunc(revision) != revision {
				return fmt.Errorf("input_bindings[%q].revision must be a positive integer", id)
			}
		}
		if hash, exists := binding["content_hash"]; exists {
			if _, ok := hash.(string); !ok {
				return fmt.Errorf("input_bindings[%q].content_hash must be a string", id)
			}
		}
		seen[id] = true
	}
	for i, file := range body.InputFiles {
		id := strings.TrimSpace(file.MaterialID)
		if id == "" || strings.TrimSpace(file.Name) == "" || strings.TrimSpace(file.MimeType) == "" {
			return fmt.Errorf("input_files[%d] requires material_id, name and mime_type", i)
		}
		if seen[id] {
			return fmt.Errorf("input_files[%d]: duplicate material_id %q", i, id)
		}
		seen[id] = true
		content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(file.ContentBase64))
		if err != nil || len(content) == 0 {
			return fmt.Errorf("input_files[%d].content_base64 must encode a non-empty file", i)
		}
		if file.ContentHash != "" && strings.TrimSpace(file.ContentHash) != "sha256:"+sha256Hex(content) {
			return fmt.Errorf("input_files[%d].content_hash does not match the file", i)
		}
	}
	return nil
}
