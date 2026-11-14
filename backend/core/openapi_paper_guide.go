package main

import (
	"lazymind/core/paperguide"
	"reflect"
	"strings"
	"time"
)

// These schemas are projected from the public DTOs, so a new response field
// cannot silently disappear from generated clients. Domain/ORM rows are never
// reflected here: credentials, filesystem paths and queue payloads stay private.
func paperGuideOpenAPISchemas() map[string]any {
	schemas := map[string]any{}
	var schema func(reflect.Type) map[string]any
	schema = func(t reflect.Type) map[string]any {
		if t.Kind() == reflect.Pointer {
			inner := schema(t.Elem())
			return map[string]any{"allOf": []any{inner}, "nullable": true}
		}
		if t == reflect.TypeOf(time.Time{}) {
			return map[string]any{"type": "string", "format": "date-time"}
		}
		switch t.Kind() {
		case reflect.String:
			return strSchema()
		case reflect.Bool:
			return boolSchema()
		case reflect.Int, reflect.Int64:
			return map[string]any{"type": "integer", "format": "int64"}
		case reflect.Float64:
			return map[string]any{"type": "number"}
		case reflect.Slice, reflect.Array:
			return array(schema(t.Elem()))
		case reflect.Struct:
			name := "PaperGuide" + strings.TrimPrefix(t.Name(), "Guide")
			if t.Name() == "" {
				name = "PaperGuideEvidenceResponse"
			}
			if schemas[name] != nil {
				return refSchema(name)
			}
			properties := map[string]any{}
			required := []string{}
			schemas[name] = map[string]any{}
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				tag := f.Tag.Get("json")
				if tag == "-" {
					continue
				}
				if f.Anonymous {
					embedded := schema(f.Type)
					ref, _ := embedded["$ref"].(string)
					parent, _ := schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
					if props, ok := parent["properties"].(map[string]any); ok {
						for k, v := range props {
							properties[k] = v
						}
					}
					if req, ok := parent["required"].([]string); ok {
						required = append(required, req...)
					}
					continue
				}
				key := strings.Split(tag, ",")[0]
				if key == "" {
					key = f.Name
				}
				properties[key] = schema(f.Type)
				if !strings.Contains(tag, "omitempty") {
					required = append(required, key)
				}
			}
			schemas[name] = map[string]any{"type": "object", "properties": properties, "required": required}
			return refSchema(name)
		}
		return map[string]any{"type": "object"}
	}
	roots := map[string]any{"PaperGuideStatus": paperguide.StatusResponse{}, "PaperGuideRevision": paperguide.RevisionResponse{}, "PaperGuideGenerateRequest": paperguide.GenerateRequest{}, "PaperGuideProgressRequest": paperguide.ProgressRequest{}, "PaperGuideSelectionRequest": paperguide.SelectionRequest{}, "PaperGuideSelectionContext": paperguide.SelectionContext{}, "PaperGuideRevisionView": paperguide.RevisionView{}, "PaperGuideEvidenceResponse": struct {
		Evidence      paperguide.GuideEvidence `json:"evidence"`
		SourceCurrent bool                     `json:"source_current"`
	}{}}
	for name, value := range roots {
		r := schema(reflect.TypeOf(value))
		ref, _ := r["$ref"].(string)
		schemas[name] = schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
	}
	schemas["PaperGuideCancellation"] = obj(prop("canceled", boolSchema()))
	return schemas
}
func paperGuideOpenAPIPaths() map[string]any {
	paths := map[string]any{}
	add := func(method, suffix, request, result, summary string) {
		parameters := []any{map[string]any{"name": "language", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"zh", "en"}, "default": "zh"}}}
		if suffix == "/paper-guide/revisions/{revision}" {
			parameters = append(parameters, map[string]any{"name": "after_version", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 0}})
		}
		op := map[string]any{"summary": summary, "parameters": parameters, "responses": map[string]any{"200": response(200, "Authorized personal result", obj(prop("data", refSchema(result)))), "400": response(400, "Invalid input", refSchema("ErrorResponse")), "403": response(403, "Generation disabled", refSchema("ErrorResponse")), "404": response(404, "Resource missing or permission revoked", refSchema("ErrorResponse")), "409": response(409, "Source changed, preparation required, or conflicting run", refSchema("ErrorResponse")), "422": response(422, "Model unavailable, budget exhausted, context too long, invalid evidence, or insufficient material", refSchema("ErrorResponse"))}}
		if request != "" {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": refSchema(request)}}}
		}
		if suffix == "/paper-guide:generate" {
			op["parameters"] = []any{}
			op["responses"].(map[string]any)["202"] = response(202, "Queued durable revision; model_ref only affects this run", obj(prop("data", refSchema(result))))
		}
		if suffix == "/paper-guide" {
			op["responses"].(map[string]any)["304"] = map[string]any{"description": "ETag unchanged"}
			op["description"] = "Lightweight status with ETag. Published and candidate revisions are separate; disabled generation does not prevent authorized reading."
		}
		paths["/datasets/{dataset}/documents/{document}"+suffix] = map[string]any{method: op}
	}
	add("get", "/paper-guide", "", "PaperGuideStatus", "Read personal guide status")
	add("post", "/paper-guide:generate", "PaperGuideGenerateRequest", "PaperGuideRevisionView", "Generate or explicitly regenerate a guide")
	add("get", "/paper-guide/revisions/{revision}", "", "PaperGuideRevision", "Read published sections, optionally after a change version")
	add("get", "/paper-guide/revisions/{revision}/evidence/{evidence}", "", "PaperGuideEvidenceResponse", "Read authorized original evidence; stale source coordinates cannot highlight the new PDF")
	add("post", "/paper-guide/runs/{run}:cancel", "", "PaperGuideCancellation", "Cancel future calls; retain published sections")
	add("post", "/paper-guide/runs/{run}:resume", "", "PaperGuideStatus", "Resume with original model and remaining persisted budget")
	add("put", "/paper-guide/progress", "PaperGuideProgressRequest", "PaperGuideProgressRequest", "Save independent reading position; offset is a Unicode rune offset")
	add("post", "/paper-guide/revisions/{revision}:selection-context", "PaperGuideSelectionRequest", "PaperGuideSelectionContext", "Resolve a published block selection; start/end are half-open Unicode rune offsets")
	return paths
}
