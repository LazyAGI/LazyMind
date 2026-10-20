package mcpadapter

import (
	"github.com/google/jsonschema-go/jsonschema"
	"lazymind/core/capability"
	"reflect"
)

// The discriminator is executable protocol validation, not just a description
// that asks a model to avoid sending two different source locators.
func knowledgeInputSchema[In any]() (*jsonschema.Schema, error) {
	variant := func(kind string, fields ...string) *jsonschema.Schema {
		one := 1
		properties := map[string]*jsonschema.Schema{"kind": {Type: "string", Enum: []any{kind}}}
		required := []string{"kind"}
		for _, field := range fields {
			properties[field] = &jsonschema.Schema{Type: "string", MinLength: &one}
			required = append(required, field)
		}
		return &jsonschema.Schema{Type: "object", Properties: properties, Required: required, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
	}
	refs := &jsonschema.Schema{OneOf: []*jsonschema.Schema{
		variant("knowledge_base", "knowledge_base_id"), variant("folder", "knowledge_base_id", "folder_id"), variant("document", "knowledge_base_id", "document_id"),
	}}
	sources := &jsonschema.Schema{OneOf: []*jsonschema.Schema{
		variant("uploaded_file", "file_ref"), variant("local_file", "path"), variant("remote_file", "url"), variant("work_artifact", "artifact_id", "revision_id"),
	}}
	return jsonschema.For[In](&jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[capability.ResourceRef](): refs, reflect.TypeFor[capability.KnowledgeUploadSource](): sources,
	}})
}
