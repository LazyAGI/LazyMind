package mcpadapter

import (
	"github.com/google/jsonschema-go/jsonschema"
	"lazymind/core/capability"
	"lazymind/core/learning"
	"reflect"
)

// The discriminator is executable protocol validation, not just a description
// that asks a model to avoid sending two different source locators.
func knowledgeInputSchema[In any]() (*jsonschema.Schema, error) {
	variant := func(kind string, fields ...string) *jsonschema.Schema {
		one := 1
		properties := map[string]*jsonschema.Schema{"kind": {Type: "string", Enum: []any{kind}, Description: "Resource type."}}
		required := []string{"kind"}
		for _, field := range fields {
			description := "Core library ID."
			if field == "folder_id" {
				description = "Folder ID in this library."
			} else if field == "document_id" {
				description = "Document ID in this library."
			}
			properties[field] = &jsonschema.Schema{Type: "string", MinLength: &one, Description: description}
			required = append(required, field)
		}
		return &jsonschema.Schema{Type: "object", Description: "Existing resource with Core IDs.", Properties: properties, Required: required, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
	}
	refs := &jsonschema.Schema{OneOf: []*jsonschema.Schema{
		variant("knowledge_base", "knowledge_base_id"), variant("folder", "knowledge_base_id", "folder_id"), variant("document", "knowledge_base_id", "document_id"),
	}}
	capabilityRef, err := jsonschema.For[learning.CapabilityRef](nil)
	if err != nil {
		return nil, err
	}
	for field, description := range map[string]string{
		"key":           "english_definition, chinese_definition, classical_definition, pinyin, general_translation, classical_translation, literary_appreciation.",
		"version":       "1, or 0 for current version; other versions fail.",
		"enabled":       "Enable this capability.",
		"display_order": "0 uses the one-based array position.",
		"settings": "Omitted/{}: capability defaults. Allowed keys: " +
			"cache_scope: document only for literary_appreciation; knowledge_base/document (default document) for classical_definition/classical_translation; user_global/knowledge_base/document (default user_global) otherwise. Context-sensitive caching may narrow user_global to document/library. " +
			"allow_llm_fallback: bool; default keeps provider pipeline. target_language: string fallback for reading requests. " +
			"output_language: auto/zh-Hans/en/zh-Hans+en; default capability language. " +
			"max_selection_length: number 1-10000; default no extra limit. " +
			"max_candidates_per_block: int 1-100, default 8. max_document_candidates: int 1-1000, default 20. Unknown keys fail.",
	} {
		capabilityRef.Properties[field].Description = description
	}
	options := &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[learning.CapabilityRef](): capabilityRef,
	}}
	changes, err := jsonschema.For[capability.ResourceMetadataChanges](options)
	if err != nil {
		return nil, err
	}
	metadataVariant := func(kind string, ref *jsonschema.Schema, fields ...string) *jsonschema.Schema {
		properties := map[string]*jsonschema.Schema{}
		for _, field := range fields {
			// Copy before specializing descriptions: shared fields also appear
			// in the other resource variants, with different name/tag rules.
			property := *changes.Properties[field]
			if kind == "knowledge_base" && field == "name" {
				property.Description = "1-100 characters: Chinese U+4E00-U+9FA5, A-Z/a-z, digits, hyphen, underscore or dot; no whitespace. Reserved names/conflicts fail."
			} else if kind == "knowledge_base" && field == "tags" {
				property.Description = "Trim/deduplicate nonblank tags; 1-10 must remain, max 20 characters each. Overflow or clearing all tags fails."
			} else if kind == "folder" && field == "tags" {
				property.Description = "Requires recursive=true and tag-only changes; descendant documents only. Trim/deduplicate nonblank tags, keep at most 10; [] clears with replace."
			}
			properties[field] = &property
		}
		min := 1
		return &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{
			"target": ref,
			"changes": {
				Type: "object", Description: "Changes for this resource.",
				Properties: properties, MinProperties: &min,
				AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
			},
		}, Required: []string{"target", "changes"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
	}
	metadata := &jsonschema.Schema{OneOf: []*jsonschema.Schema{
		metadataVariant("knowledge_base", refs.OneOf[0], "name", "description", "scene", "tags", "tag_mode", "profile_id", "algo_id", "processing_level", "capabilities"),
		metadataVariant("folder", refs.OneOf[1], "name", "tags", "tag_mode", "recursive"),
		metadataVariant("document", refs.OneOf[2], "name", "tags", "tag_mode"),
	}}
	options.TypeSchemas[reflect.TypeFor[capability.ResourceRef]()] = refs
	options.TypeSchemas[reflect.TypeFor[capability.ResourceMetadataItem]()] = metadata
	return jsonschema.For[In](options)
}
