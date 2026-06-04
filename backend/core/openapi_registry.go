package main

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"lazymind/core/chat"
	"lazymind/core/doc"
	"lazymind/core/evalset"
	"lazymind/core/modelprovider"
	"lazymind/core/wordgroup"
)

type schemaSource struct {
	Type   any
	Ref    string
	Inline map[string]any
}

type openAPIBody struct {
	Required    bool
	ContentType string
	Schema      schemaSource
}

type openAPIResponse struct {
	Description string
	ContentType string
	Schema      schemaSource
}

type openAPIOperation struct {
	Method      string
	Path        string
	Summary     string
	Description string
	Tags        []string
	PathParams  any
	QueryParams any
	Headers     any
	RequestBody *openAPIBody
	Responses   map[int]openAPIResponse
}

type schemaBuilder struct {
	components map[string]any
	seen       map[reflect.Type]string
}

func newSchemaBuilder() *schemaBuilder {
	return &schemaBuilder{
		components: map[string]any{},
		seen:       map[reflect.Type]string{},
	}
}

func operationRegistryOpenAPISpec() map[string]any {
	builder := newSchemaBuilder()
	paths := map[string]any{}
	for _, op := range registeredCoreOperations() {
		pathItem, _ := paths[op.Path].(map[string]any)
		if pathItem == nil {
			pathItem = map[string]any{}
			paths[op.Path] = pathItem
		}
		pathItem[strings.ToLower(op.Method)] = op.toOpenAPI(builder)
	}
	return map[string]any{
		"components": map[string]any{
			"schemas": builder.components,
		},
		"paths": paths,
	}
}

func (op openAPIOperation) toOpenAPI(builder *schemaBuilder) map[string]any {
	result := map[string]any{
		"summary": op.Summary,
	}
	if strings.TrimSpace(op.Description) != "" {
		result["description"] = op.Description
	}
	if len(op.Tags) > 0 {
		result["tags"] = op.Tags
	}

	params := make([]map[string]any, 0)
	params = append(params, buildStructParameters(op.PathParams, "path", builder)...)
	params = append(params, buildStructParameters(op.QueryParams, "query", builder)...)
	params = append(params, buildStructParameters(op.Headers, "header", builder)...)
	if len(params) > 0 {
		items := make([]any, 0, len(params))
		for _, item := range params {
			items = append(items, item)
		}
		result["parameters"] = items
	}

	if op.RequestBody != nil {
		contentType := op.RequestBody.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		result["requestBody"] = map[string]any{
			"required": op.RequestBody.Required,
			"content": map[string]any{
				contentType: map[string]any{
					"schema": builder.schemaFromSource(op.RequestBody.Schema),
				},
			},
		}
	}

	responses := map[string]any{}
	for _, code := range sortedStatusCodes(op.Responses) {
		resp := op.Responses[code]
		description := resp.Description
		if description == "" {
			description = httpStatusText(code)
		}
		contentType := resp.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		entry := map[string]any{"description": description}
		if schema := builder.schemaFromSource(resp.Schema); schema != nil {
			entry["content"] = map[string]any{
				contentType: map[string]any{"schema": schema},
			}
		}
		responses[fmt.Sprintf("%d", code)] = entry
	}
	if len(responses) == 0 {
		responses["200"] = map[string]any{"description": "OK"}
	}
	result["responses"] = responses
	return result
}

func buildStructParameters(v any, location string, builder *schemaBuilder) []map[string]any {
	if v == nil {
		return nil
	}
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	params := make([]map[string]any, 0)
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name, ok := field.Tag.Lookup(location)
		if !ok || strings.TrimSpace(name) == "" || name == "-" {
			continue
		}
		schema := builder.schemaForType(field.Type)
		if schema == nil {
			continue
		}
		required := location == "path" || field.Tag.Get("required") == "true"
		params = append(params, map[string]any{
			"name":     name,
			"in":       location,
			"required": required,
			"schema":   schema,
		})
	}
	return params
}

func (b *schemaBuilder) schemaFromSource(source schemaSource) map[string]any {
	if source.Inline != nil {
		return source.Inline
	}
	if source.Ref != "" {
		return refSchema(source.Ref)
	}
	if source.Type == nil {
		return nil
	}
	return b.schemaForType(reflect.TypeOf(source.Type))
}

func (b *schemaBuilder) schemaForType(t reflect.Type) map[string]any {
	if t == nil {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if schema := inlineSpecialSchema(t); schema != nil {
		return schema
	}
	if isPrimitiveKind(t.Kind()) || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map || t.Kind() == reflect.Interface {
		return b.inlineSchemaForType(t)
	}
	if t.Kind() == reflect.Struct {
		name := schemaNameForType(t)
		if existing, ok := b.seen[t]; ok {
			return refSchema(existing)
		}
		b.seen[t] = name
		b.components[name] = b.inlineSchemaForType(t)
		return refSchema(name)
	}
	return map[string]any{"type": "string"}
}

func (b *schemaBuilder) inlineSchemaForType(t reflect.Type) map[string]any {
	if t == nil {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if schema := inlineSpecialSchema(t); schema != nil {
		return schema
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		schema := map[string]any{"type": "integer"}
		if t.Kind() == reflect.Int64 {
			schema["format"] = "int64"
		}
		return schema
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		schema := map[string]any{"type": "integer", "minimum": 0}
		if t.Kind() == reflect.Uint64 {
			schema["format"] = "int64"
		}
		return schema
	case reflect.Float32:
		return map[string]any{"type": "number", "format": "float"}
	case reflect.Float64:
		return map[string]any{"type": "number", "format": "double"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": b.schemaForType(t.Elem())}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return obj()
		}
		return map[string]any{"type": "object", "additionalProperties": b.schemaForType(t.Elem())}
	case reflect.Interface:
		return obj()
	case reflect.Struct:
		properties := map[string]any{}
		required := make([]string, 0)
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			name, omitEmpty, skip := jsonFieldName(field)
			if skip {
				continue
			}
			properties[name] = b.schemaForType(field.Type)
			if field.Tag.Get("required") == "true" || (!omitEmpty && !isOptionalField(field.Type)) {
				required = append(required, name)
			}
		}
		sort.Strings(required)
		result := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			result["required"] = required
		}
		return result
	default:
		return map[string]any{"type": "string"}
	}
}

func inlineSpecialSchema(t reflect.Type) map[string]any {
	if t.PkgPath() == "time" && t.Name() == "Time" {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	return nil
}

func jsonFieldName(field reflect.StructField) (name string, omitEmpty bool, skip bool) {
	jsonTag := field.Tag.Get("json")
	if jsonTag == "-" {
		return "", false, true
	}
	if jsonTag == "" {
		return lowerCamel(field.Name), false, false
	}
	parts := strings.Split(jsonTag, ",")
	name = strings.TrimSpace(parts[0])
	if name == "" {
		name = lowerCamel(field.Name)
	}
	for _, part := range parts[1:] {
		if strings.TrimSpace(part) == "omitempty" {
			omitEmpty = true
		}
	}
	return name, omitEmpty, false
}

func lowerCamel(v string) string {
	if v == "" {
		return v
	}
	return strings.ToLower(v[:1]) + v[1:]
}

func isOptionalField(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		return true
	}
	switch t.Kind() {
	case reflect.Map, reflect.Slice, reflect.Interface:
		return true
	default:
		return false
	}
}

func isPrimitiveKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func schemaNameForType(t reflect.Type) string {
	if name := t.Name(); name != "" {
		return name
	}
	return strings.ReplaceAll(t.String(), ".", "_")
}

func sortedStatusCodes(responses map[int]openAPIResponse) []int {
	codes := make([]int, 0, len(responses))
	for code := range responses {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	return codes
}

func httpStatusText(code int) string {
	switch code {
	case 200:
		return "OK"
	case 201:
		return "Created"
	case 204:
		return "No Content"
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 500:
		return "Internal Server Error"
	default:
		return "Response"
	}
}

type datasetPathParams struct {
	Dataset string `path:"dataset"`
}

type documentPathParams struct {
	Dataset  string `path:"dataset"`
	Document string `path:"document"`
}

type taskPathParams struct {
	Dataset string `path:"dataset"`
	Task    string `path:"task"`
}

type uploadPathParams struct {
	Dataset  string `path:"dataset"`
	UploadID string `path:"upload_id"`
}

type exportConversationFilePathParams struct {
	FileID string `path:"file_id"`
}

type agentFileContentOpenAPIRequest struct {
	Path string `json:"path"`
}

type agentFileContentOpenAPIResponse struct {
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Content  string `json:"content"`
	FileSize int64  `json:"file_size"`
}

type agentThreadListQueryParams struct {
	PageSize  int32  `query:"page_size"`
	PageToken string `query:"page_token"`
}

type agentThreadOpenAPIResponse struct {
	ThreadID      string         `json:"thread_id"`
	CurrentTaskID string         `json:"current_task_id,omitempty"`
	Status        string         `json:"status"`
	ThreadPayload map[string]any `json:"thread_payload,omitempty"`
	CreatedAt     string         `json:"created_at"`
	UpdatedAt     string         `json:"updated_at"`
}

type agentThreadListOpenAPIResponse struct {
	Threads       []agentThreadOpenAPIResponse `json:"threads"`
	TotalSize     int64                        `json:"total_size"`
	NextPageToken string                       `json:"next_page_token"`
}

type skillPathParams struct {
	SkillID string `path:"skill_id"`
}

type datasetQueryParams struct {
	PageToken string   `query:"page_token"`
	PageSize  int32    `query:"page_size"`
	OrderBy   string   `query:"order_by"`
	Keyword   string   `query:"keyword"`
	Tags      []string `query:"tags"`
}

type createDatasetQueryParams struct {
	DatasetID string `query:"dataset_id"`
}

type listDocumentsQueryParams struct {
	PageToken string `query:"page_token"`
	PageSize  int32  `query:"page_size"`
}

type listWordGroupsQueryParams struct {
	PageToken string `query:"page_token"`
	PageSize  int32  `query:"page_size"`
}

type listUserModelProvidersQueryParams struct {
	Category        string `query:"category"`
	ExcludeCategory string `query:"exclude_category"`
	Keyword         string `query:"keyword"`
}

type checkModelProviderOpenAPIRequest struct {
	ProviderName string `json:"provider_name"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	DryRun       bool   `json:"dry_run"`
}

type modelProviderGroupPathParams struct {
	ModelProviderID string `path:"model_provider_id"`
}

type modelProviderGroupByIDPathParams struct {
	ModelProviderID string `path:"model_provider_id"`
	GroupID         string `path:"group_id"`
}

type updateModelProviderGroupOpenAPIRequest struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key,omitempty"`
	Verify  bool   `json:"verify"`
}

type createModelProviderGroupOpenAPIRequest struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key,omitempty"`
	Verify  bool   `json:"verify"`
}

type createModelProviderGroupOpenAPIResponse struct {
	ID                  string                                `json:"id"`
	UserModelProviderID string                                `json:"user_model_provider_id"`
	Name                string                                `json:"name"`
	BaseURL             string                                `json:"base_url"`
	Check               *modelprovider.CheckModelProviderData `json:"check,omitempty"`
}

type deleteModelProviderGroupOpenAPIResponse struct {
	ID string `json:"id"`
}

type addModelProviderGroupModelOpenAPIRequest struct {
	Name      string `json:"name"`
	ModelType string `json:"model_type"`
}

type addModelProviderGroupModelOpenAPIResponse struct {
	ID                       string `json:"id"`
	UserModelProviderID      string `json:"user_model_provider_id"`
	UserModelProviderGroupID string `json:"user_model_provider_group_id"`
	Name                     string `json:"name"`
	ModelType                string `json:"model_type"`
	ProviderName             string `json:"provider_name"`
	GroupName                string `json:"group_name"`
	BaseURL                  string `json:"base_url"`
	IsDefault                bool   `json:"is_default"`
}

type listModelProviderGroupModelsOpenAPIItem struct {
	ID                       string `json:"id"`
	UserModelProviderID      string `json:"user_model_provider_id"`
	UserModelProviderGroupID string `json:"user_model_provider_group_id"`
	Name                     string `json:"name"`
	ModelType                string `json:"model_type"`
	ProviderName             string `json:"provider_name"`
	GroupName                string `json:"group_name"`
	BaseURL                  string `json:"base_url"`
	IsDefault                bool   `json:"is_default"`
}

type listModelProviderGroupModelsOpenAPIResponse struct {
	Models []listModelProviderGroupModelsOpenAPIItem `json:"models"`
}

type listUserModelsByModelTypeQueryParams struct {
	ModelType string `query:"model_type"`
}

type selectedModelOpenAPIItem struct {
	ModelKey                 string `json:"model_key"`
	ModelID                  string `json:"model_id"`
	UserModelProviderID      string `json:"user_model_provider_id"`
	UserModelProviderGroupID string `json:"user_model_provider_group_id"`
	Name                     string `json:"name"`
	ProviderName             string `json:"provider_name"`
	GroupName                string `json:"group_name"`
	BaseURL                  string `json:"base_url"`
}

type listSelectedModelsOpenAPIResponse struct {
	Selections []selectedModelOpenAPIItem `json:"selections"`
}

type setSelectedModelOpenAPIItem struct {
	ModelKey string `json:"model_key"`
	ModelID  string `json:"model_id"`
}

type setSelectedModelsOpenAPIRequest struct {
	Selections []setSelectedModelOpenAPIItem `json:"selections"`
}

type modelProviderGroupModelPathParams struct {
	ModelProviderID string `path:"model_provider_id"`
	GroupID         string `path:"group_id"`
	ModelID         string `path:"model_id"`
}

type deleteModelProviderGroupModelOpenAPIResponse struct {
	ID string `json:"id"`
}

type verifiedProviderQueryParams struct {
	Category string `query:"category"`
}

type verifiedProviderGroupOpenAPIItem struct {
	GroupID             string `json:"group_id"`
	UserModelProviderID string `json:"user_model_provider_id"`
	ProviderName        string `json:"provider_name"`
	GroupName           string `json:"group_name"`
	BaseURL             string `json:"base_url"`
	Category            string `json:"category"`
}

type verifiedProviderOpenAPIResponse struct {
	Ready        bool   `json:"ready"`
	Source       string `json:"source,omitempty"`
	SharedByName string `json:"shared_by_name,omitempty"`
	SharedByID   string `json:"shared_by_id,omitempty"`
	ProviderName string `json:"provider_name,omitempty"`
	GroupName    string `json:"group_name,omitempty"`
}

type verifiedProviderGroupsOpenAPIResponse struct {
	Groups []verifiedProviderGroupOpenAPIItem `json:"groups"`
}

type setSelectedProviderOpenAPIRequest struct {
	Selections []setSelectedProviderOpenAPIItem `json:"selections"`
}

type setSelectedProviderOpenAPIItem struct {
	Category string `json:"category"`
	GroupID  string `json:"group_id"`
}

type selectedProviderOpenAPIItem struct {
	Category            string `json:"category"`
	GroupID             string `json:"group_id"`
	UserModelProviderID string `json:"user_model_provider_id"`
	ProviderName        string `json:"provider_name"`
	GroupName           string `json:"group_name"`
	BaseURL             string `json:"base_url"`
	Share               bool   `json:"share"`
}

type selectedProvidersOpenAPIResponse struct {
	Selections []selectedProviderOpenAPIItem `json:"selections"`
}

type setSharedProviderOpenAPIRequest struct {
	GroupID string `json:"group_id"`
	Share   bool   `json:"share"`
}

type userModelProviderOpenAPIItem struct {
	ID                     string   `json:"id"`
	DefaultModelProviderID string   `json:"default_model_provider_id"`
	Name                   string   `json:"name"`
	Description            string   `json:"description"`
	BaseURL                string   `json:"base_url"`
	Category               string   `json:"category"`
	IsConfigured           bool     `json:"is_configured"`
	Capabilities           []string `json:"capabilities"`
}

type listUserModelProvidersOpenAPIResponse struct {
	Providers []userModelProviderOpenAPIItem `json:"providers"`
}

type listModelProviderGroupsOpenAPIItem struct {
	ID                  string `json:"id"`
	UserModelProviderID string `json:"user_model_provider_id"`
	Name                string `json:"name"`
	BaseURL             string `json:"base_url"`
	APIKey              string `json:"api_key"`
}

type listModelProviderGroupsOpenAPIResponse struct {
	Groups []listModelProviderGroupsOpenAPIItem `json:"groups"`
}

type listTasksQueryParams struct {
	PageToken   string `query:"page_token"`
	PageSize    int32  `query:"page_size"`
	TaskState   string `query:"task_state"`
	TaskType    string `query:"task_type"`
	DocumentID  string `query:"document_id"`
	DocumentPID string `query:"document_pid"`
}

type skillGenerateOpenAPIRequest struct {
	SuggestionIDs []string `json:"suggestion_ids"`
	UserInstruct  string   `json:"user_instruct"`
}

type skillGenerateOpenAPIResponse struct {
	DraftStatus        string `json:"draft_status"`
	DraftSourceVersion int64  `json:"draft_source_version"`
	DraftPath          string `json:"draft_path"`
	Outdated           bool   `json:"outdated"`
}

type skillDraftPreviewOpenAPIResponse struct {
	SkillID            string `json:"skill_id"`
	DraftStatus        string `json:"draft_status"`
	DraftSourceVersion int64  `json:"draft_source_version"`
	CurrentContent     string `json:"current_content"`
	DraftContent       string `json:"draft_content"`
	Diff               string `json:"diff"`
	Outdated           bool   `json:"outdated"`
}

type suggestionIDPathParams struct {
	ID string `path:"id"`
}

type shareItemPathParams struct {
	ShareItemID string `path:"share_item_id"`
}

type suggestionListQueryParams struct {
	Page         int32  `query:"page"`
	PageSize     int32  `query:"page_size"`
	EvolutionID  string `query:"evolution_id"`
	ResourceType string `query:"resource_type"`
	ResourceKey  string `query:"resource_key"`
	Keyword      string `query:"keyword"`
}

type skillListQueryParams struct {
	Keyword  string   `query:"keyword"`
	Category string   `query:"category"`
	Tags     []string `query:"tags"`
	Page     int32    `query:"page"`
	PageSize int32    `query:"page_size"`
}

type shareListQueryParams struct {
	Status   string `query:"status"`
	Page     int32  `query:"page"`
	PageSize int32  `query:"page_size"`
}

type suggestionPayloadOpenAPIRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Reason  string `json:"reason,omitempty"`
}

type suggestionBatchReviewOpenAPIRequest struct {
	IDs []string `json:"ids"`
}

type recordedSuggestionOpenAPIResponse struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	InvalidReason string `json:"invalid_reason,omitempty"`
}

type recordedSuggestionListOpenAPIResponse struct {
	Items []recordedSuggestionOpenAPIResponse `json:"items"`
}

type suggestionItemOpenAPIResponse struct {
	ID              string  `json:"id"`
	UserID          string  `json:"user_id"`
	ResourceType    string  `json:"resource_type"`
	ResourceKey     string  `json:"resource_key"`
	Category        string  `json:"category"`
	ParentSkillName string  `json:"parent_skill_name"`
	SkillName       string  `json:"skill_name"`
	FileExt         string  `json:"file_ext"`
	RelativePath    string  `json:"relative_path"`
	Action          string  `json:"action"`
	SessionID       string  `json:"session_id"`
	Title           string  `json:"title"`
	Content         string  `json:"content"`
	Reason          string  `json:"reason"`
	FullContent     string  `json:"full_content"`
	Status          string  `json:"status"`
	InvalidReason   string  `json:"invalid_reason"`
	ReviewerID      string  `json:"reviewer_id"`
	ReviewerName    string  `json:"reviewer_name"`
	ReviewedAt      *string `json:"reviewed_at,omitempty"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
	Outdated        bool    `json:"outdated"`
}

type suggestionListOpenAPIResponse struct {
	Items    []suggestionItemOpenAPIResponse `json:"items"`
	Page     int32                           `json:"page"`
	PageSize int32                           `json:"page_size"`
	Total    int64                           `json:"total"`
}

type suggestionBatchReviewOpenAPIResponse struct {
	Items []suggestionItemOpenAPIResponse `json:"items"`
}

type skillChildCreateOpenAPIRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Content     string   `json:"content"`
	FileExt     string   `json:"file_ext,omitempty"`
	AutoEvo     *bool    `json:"auto_evo,omitempty"`
}

type skillCreateManagedOpenAPIRequest struct {
	Name            string                           `json:"name"`
	Description     string                           `json:"description,omitempty"`
	Category        string                           `json:"category,omitempty"`
	ParentSkillID   string                           `json:"parent_skill_id,omitempty"`
	ParentSkillName string                           `json:"parent_skill_name,omitempty"`
	Tags            []string                         `json:"tags,omitempty"`
	Content         string                           `json:"content"`
	FileExt         string                           `json:"file_ext,omitempty"`
	AutoEvo         *bool                            `json:"auto_evo,omitempty"`
	IsEnabled       *bool                            `json:"is_enabled,omitempty"`
	Children        []skillChildCreateOpenAPIRequest `json:"children,omitempty"`
}

type skillUpdateManagedOpenAPIRequest struct {
	Name            *string  `json:"name,omitempty"`
	Description     *string  `json:"description,omitempty"`
	Category        *string  `json:"category,omitempty"`
	ParentSkillID   *string  `json:"parent_skill_id,omitempty"`
	ParentSkillName *string  `json:"parent_skill_name,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Content         *string  `json:"content,omitempty"`
	FileExt         *string  `json:"file_ext,omitempty"`
	AutoEvo         *bool    `json:"auto_evo,omitempty"`
	IsEnabled       *bool    `json:"is_enabled,omitempty"`
}

type skillListChildOpenAPIResponse struct {
	SkillID                     string `json:"skill_id"`
	Name                        string `json:"name"`
	Description                 string `json:"description"`
	FileExt                     string `json:"file_ext"`
	AutoEvo                     bool   `json:"auto_evo"`
	AutoEvoApplyStatus          string `json:"auto_evo_apply_status"`
	AutoEvoGeneration           int64  `json:"auto_evo_generation"`
	AutoEvoError                string `json:"auto_evo_error"`
	IsEnabled                   bool   `json:"is_enabled"`
	UpdateStatus                string `json:"update_status"`
	HasPendingReviewSuggestions bool   `json:"has_pending_review_suggestions"`
	SuggestionStatus            string `json:"suggestion_status"`
	NodeType                    string `json:"node_type"`
	ParentID                    string `json:"parent_id"`
	ParentSkillID               string `json:"parent_skill_id"`
	ParentSkillName             string `json:"parent_skill_name"`
}

type skillListItemOpenAPIResponse struct {
	SkillID                     string                          `json:"skill_id"`
	Name                        string                          `json:"name"`
	Description                 string                          `json:"description"`
	Category                    string                          `json:"category"`
	Tags                        []string                        `json:"tags"`
	AutoEvo                     bool                            `json:"auto_evo"`
	AutoEvoApplyStatus          string                          `json:"auto_evo_apply_status"`
	AutoEvoGeneration           int64                           `json:"auto_evo_generation"`
	AutoEvoError                string                          `json:"auto_evo_error"`
	IsEnabled                   bool                            `json:"is_enabled"`
	UpdateStatus                string                          `json:"update_status"`
	HasPendingReviewSuggestions bool                            `json:"has_pending_review_suggestions"`
	SuggestionStatus            string                          `json:"suggestion_status"`
	NodeType                    string                          `json:"node_type"`
	Children                    []skillListChildOpenAPIResponse `json:"children"`
}

type skillListOpenAPIResponse struct {
	Items    []skillListItemOpenAPIResponse `json:"items"`
	Page     int32                          `json:"page"`
	PageSize int32                          `json:"page_size"`
	Total    int32                          `json:"total"`
}

type skillDetailChildOpenAPIResponse struct {
	SkillID                     string `json:"skill_id"`
	Name                        string `json:"name"`
	Description                 string `json:"description"`
	FileExt                     string `json:"file_ext"`
	AutoEvo                     bool   `json:"auto_evo"`
	AutoEvoApplyStatus          string `json:"auto_evo_apply_status"`
	AutoEvoGeneration           int64  `json:"auto_evo_generation"`
	AutoEvoError                string `json:"auto_evo_error"`
	IsEnabled                   bool   `json:"is_enabled"`
	UpdateStatus                string `json:"update_status"`
	HasPendingReviewSuggestions bool   `json:"has_pending_review_suggestions"`
	SuggestionStatus            string `json:"suggestion_status"`
	NodeType                    string `json:"node_type"`
	ParentID                    string `json:"parent_id"`
	ParentSkillID               string `json:"parent_skill_id"`
	ParentSkillName             string `json:"parent_skill_name"`
	Content                     string `json:"content"`
}

type skillDetailOpenAPIResponse struct {
	SkillID                     string                            `json:"skill_id"`
	Name                        string                            `json:"name"`
	Description                 string                            `json:"description"`
	Category                    string                            `json:"category"`
	Tags                        []string                          `json:"tags"`
	AutoEvo                     bool                              `json:"auto_evo"`
	AutoEvoApplyStatus          string                            `json:"auto_evo_apply_status"`
	AutoEvoGeneration           int64                             `json:"auto_evo_generation"`
	AutoEvoError                string                            `json:"auto_evo_error"`
	IsEnabled                   bool                              `json:"is_enabled"`
	UpdateStatus                string                            `json:"update_status"`
	HasPendingReviewSuggestions bool                              `json:"has_pending_review_suggestions"`
	SuggestionStatus            string                            `json:"suggestion_status"`
	NodeType                    string                            `json:"node_type"`
	ParentID                    string                            `json:"parent_id"`
	ParentSkillID               string                            `json:"parent_skill_id"`
	ParentSkillName             string                            `json:"parent_skill_name"`
	Content                     string                            `json:"content"`
	FileExt                     string                            `json:"file_ext"`
	Children                    []skillDetailChildOpenAPIResponse `json:"children"`
}

type skillDeleteOpenAPIResponse struct {
	Deleted bool `json:"deleted"`
}

type skillDiscardOpenAPIResponse struct {
	Discarded bool `json:"discarded"`
}

type shareSkillOpenAPIRequest struct {
	TargetUserIDs  []string `json:"target_user_ids,omitempty"`
	TargetGroupIDs []string `json:"target_group_ids,omitempty"`
	Message        string   `json:"message,omitempty"`
}

type skillShareCreateItemOpenAPIResponse struct {
	ID                 string  `json:"id"`
	ShareTaskID        string  `json:"share_task_id"`
	TargetUserID       string  `json:"target_user_id"`
	TargetUserName     string  `json:"target_user_name"`
	Status             string  `json:"status"`
	TargetRelativeRoot string  `json:"target_relative_root,omitempty"`
	AcceptedAt         *string `json:"accepted_at,omitempty"`
	RejectedAt         *string `json:"rejected_at,omitempty"`
	TargetRootSkillID  string  `json:"target_root_skill_id,omitempty"`
	ErrorMessage       string  `json:"error_message,omitempty"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
}

type skillShareCreateOpenAPIResponse struct {
	ShareTaskID string                                `json:"share_task_id"`
	Items       []skillShareCreateItemOpenAPIResponse `json:"items"`
}

type skillShareTargetStatusSummaryOpenAPIResponse struct {
	PendingAccept int64 `json:"pending_accept"`
	Completed     int64 `json:"completed"`
	Rejected      int64 `json:"rejected"`
	Failed        int64 `json:"failed"`
}

type skillShareTargetItemOpenAPIResponse struct {
	TargetUserID      string  `json:"target_user_id"`
	TargetUserName    string  `json:"target_user_name"`
	Status            string  `json:"status"`
	ShareItemID       string  `json:"share_item_id"`
	ShareTaskID       string  `json:"share_task_id"`
	Message           string  `json:"message"`
	AcceptedAt        *string `json:"accepted_at,omitempty"`
	RejectedAt        *string `json:"rejected_at,omitempty"`
	TargetRootSkillID string  `json:"target_root_skill_id,omitempty"`
	ErrorMessage      string  `json:"error_message,omitempty"`
	SharedAt          string  `json:"shared_at"`
	UpdatedAt         string  `json:"updated_at"`
}

type skillShareTargetsOpenAPIResponse struct {
	SkillID       string                                       `json:"skill_id"`
	StatusSummary skillShareTargetStatusSummaryOpenAPIResponse `json:"status_summary"`
	Items         []skillShareTargetItemOpenAPIResponse        `json:"items"`
	Page          int32                                        `json:"page"`
	PageSize      int32                                        `json:"page_size"`
	Total         int64                                        `json:"total"`
}

type skillShareListItemOpenAPIResponse struct {
	ShareItemID           string  `json:"share_item_id"`
	ShareTaskID           string  `json:"share_task_id"`
	Status                string  `json:"status"`
	SourceUserID          string  `json:"source_user_id"`
	SourceUserName        string  `json:"source_user_name"`
	TargetUserID          string  `json:"target_user_id"`
	TargetUserName        string  `json:"target_user_name"`
	SourceSkillID         string  `json:"source_skill_id"`
	SourceCategory        string  `json:"source_category"`
	SourceParentSkillName string  `json:"source_parent_skill_name"`
	Message               string  `json:"message"`
	AcceptedAt            *string `json:"accepted_at,omitempty"`
	RejectedAt            *string `json:"rejected_at,omitempty"`
	TargetRootSkillID     string  `json:"target_root_skill_id,omitempty"`
	ErrorMessage          string  `json:"error_message,omitempty"`
	CreatedAt             string  `json:"created_at"`
	UpdatedAt             string  `json:"updated_at"`
}

type skillShareListOpenAPIResponse struct {
	Items    []skillShareListItemOpenAPIResponse `json:"items"`
	Page     int32                               `json:"page"`
	PageSize int32                               `json:"page_size"`
	Total    int64                               `json:"total"`
}

type skillShareDetailOpenAPIResponse struct {
	ShareItemID string                     `json:"share_item_id"`
	Status      string                     `json:"status"`
	Message     string                     `json:"message"`
	Source      skillDetailOpenAPIResponse `json:"source"`
}

type skillShareAcceptOpenAPIResponse struct {
	Accepted          bool   `json:"accepted"`
	TargetRootSkillID string `json:"target_root_skill_id"`
}

type skillShareRejectOpenAPIResponse struct {
	Rejected bool `json:"rejected"`
}

type systemSuggestionOpenAPIRequest struct {
	SessionID   string                            `json:"session_id"`
	Suggestions []suggestionPayloadOpenAPIRequest `json:"suggestions"`
}

type managedStateUpsertOpenAPIRequest struct {
	Content string `json:"content"`
	AutoEvo *bool  `json:"auto_evo,omitempty"`
}

type managedStateOpenAPIResponse struct {
	ResourceID                  string `json:"resource_id"`
	ResourceType                string `json:"resource_type"`
	Title                       string `json:"title"`
	Content                     string `json:"content"`
	ContentSummary              string `json:"content_summary"`
	HasPendingReviewSuggestions bool   `json:"has_pending_review_suggestions"`
	SuggestionStatus            string `json:"suggestion_status"`
	AutoEvo                     bool   `json:"auto_evo"`
	AutoEvoApplyStatus          string `json:"auto_evo_apply_status"`
	AutoEvoGeneration           int64  `json:"auto_evo_generation"`
	AutoEvoError                string `json:"auto_evo_error"`
}

type managedStateListOpenAPIResponse struct {
	Items []managedStateOpenAPIResponse `json:"items"`
}

type personalizationSettingOpenAPIRequest struct {
	Enabled bool `json:"enabled"`
}

type personalizationSettingOpenAPIResponse struct {
	Enabled bool `json:"enabled"`
}

type systemGenerateOpenAPIResponse struct {
	DraftStatus        string   `json:"draft_status"`
	DraftSourceVersion int64    `json:"draft_source_version"`
	DraftContent       string   `json:"draft_content"`
	SuggestionIDs      []string `json:"suggestion_ids"`
}

type systemDraftPreviewOpenAPIResponse struct {
	DraftStatus        string `json:"draft_status"`
	DraftSourceVersion int64  `json:"draft_source_version"`
	CurrentContent     string `json:"current_content"`
	DraftContent       string `json:"draft_content"`
	Diff               string `json:"diff"`
}

type systemConfirmOpenAPIResponse struct {
	Content string `json:"content"`
	Version int64  `json:"version"`
}

type systemDiscardOpenAPIResponse struct {
	Discarded bool `json:"discarded"`
}

type internalSkillSuggestionOpenAPIRequest struct {
	SessionID   string                            `json:"session_id"`
	ID          string                            `json:"id,omitempty"`
	SkillID     string                            `json:"skill_id,omitempty"`
	Category    string                            `json:"category,omitempty"`
	SkillName   string                            `json:"skill_name,omitempty"`
	Suggestions []suggestionPayloadOpenAPIRequest `json:"suggestions"`
}

type internalSkillCreateOpenAPIRequest struct {
	SessionID string `json:"session_id"`
	Category  string `json:"category"`
	SkillName string `json:"skill_name"`
	Content   string `json:"content"`
}

type internalSkillRemoveOpenAPIRequest struct {
	ID        string `json:"id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Category  string `json:"category,omitempty"`
	SkillName string `json:"skill_name,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type evalSetImportPreviewOpenAPIRequest struct {
	File     string `json:"file" required:"true"`
	FileType string `json:"file_type,omitempty"`
}

func registeredCoreOperations() []openAPIOperation {
	jsonBodyOf := func(v any, required bool) *openAPIBody {
		return &openAPIBody{Required: required, ContentType: "application/json", Schema: schemaSource{Type: v}}
	}
	multipartBodyOf := func(v any, required bool) *openAPIBody {
		return &openAPIBody{Required: required, ContentType: "multipart/form-data", Schema: schemaSource{Type: v}}
	}
	resp := func(description string, v any) openAPIResponse {
		return openAPIResponse{Description: description, ContentType: "application/json", Schema: schemaSource{Type: v}}
	}
	refResp := func(description, name string) openAPIResponse {
		return openAPIResponse{Description: description, ContentType: "application/json", Schema: schemaSource{Ref: name}}
	}
	return []openAPIOperation{
		{
			Method:      "GET",
			Path:        "/datasets",
			Summary:     "Dataset list",
			Tags:        []string{"datasets"},
			QueryParams: datasetQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Dataset list", doc.ListDatasetsResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/datasets",
			Summary:     "Create dataset",
			Tags:        []string{"datasets"},
			QueryParams: createDatasetQueryParams{},
			RequestBody: jsonBodyOf(doc.Dataset{}, false),
			Responses:   map[int]openAPIResponse{200: resp("Created dataset", doc.Dataset{})},
		},
		{
			Method:     "GET",
			Path:       "/datasets/{dataset}",
			Summary:    "Get dataset",
			Tags:       []string{"datasets"},
			PathParams: datasetPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Dataset details", doc.Dataset{})},
		},
		{
			Method:      "PATCH",
			Path:        "/datasets/{dataset}",
			Summary:     "Update dataset",
			Tags:        []string{"datasets"},
			PathParams:  datasetPathParams{},
			RequestBody: jsonBodyOf(doc.Dataset{}, false),
			Responses:   map[int]openAPIResponse{200: resp("Updated dataset", doc.Dataset{})},
		},
		{
			Method:     "DELETE",
			Path:       "/datasets/{dataset}",
			Summary:    "Delete dataset",
			Tags:       []string{"datasets"},
			PathParams: datasetPathParams{},
			Responses:  map[int]openAPIResponse{200: refResp("Deleted successfully", "EmptyObject")},
		},
		{
			Method:      "POST",
			Path:        "/datasets/{dataset}:setDefault",
			Summary:     "Set as default dataset",
			Tags:        []string{"datasets"},
			PathParams:  datasetPathParams{},
			RequestBody: jsonBodyOf(doc.SetDefaultDatasetRequest{}, true),
			Responses:   map[int]openAPIResponse{200: refResp("Set successfully", "EmptyObject")},
		},
		{
			Method:      "POST",
			Path:        "/datasets/{dataset}:unsetDefault",
			Summary:     "Unset default dataset",
			Tags:        []string{"datasets"},
			PathParams:  datasetPathParams{},
			RequestBody: jsonBodyOf(doc.UnsetDefaultDatasetRequest{}, true),
			Responses:   map[int]openAPIResponse{200: refResp("Unset successfully", "EmptyObject")},
		},
		{
			Method:      "GET",
			Path:        "/eval-sets",
			Summary:     "List eval sets",
			Tags:        []string{"eval-sets"},
			QueryParams: evalset.ListEvalSetsQuery{},
			Responses:   map[int]openAPIResponse{200: resp("Eval set list", evalset.ListEvalSetsResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/eval-sets",
			Summary:     "Create eval set",
			Tags:        []string{"eval-sets"},
			RequestBody: jsonBodyOf(evalset.CreateEvalSetRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created eval set", evalset.EvalSetResponse{})},
		},
		{
			Method:    "GET",
			Path:      "/eval-sets/datasets",
			Summary:   "List eval set dataset options",
			Tags:      []string{"eval-sets"},
			Responses: map[int]openAPIResponse{200: resp("Dataset options", evalset.DatasetOptionsResponse{})},
		},
		{
			Method:    "GET",
			Path:      "/eval-sets/question-types",
			Summary:   "List eval set question type options",
			Tags:      []string{"eval-sets"},
			Responses: map[int]openAPIResponse{200: resp("Question type options", evalset.QuestionTypeOptionsResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/eval-set-import-templates/{file_type}",
			Summary:    "Download eval set import template",
			Tags:       []string{"eval-set-imports"},
			PathParams: evalset.ImportTemplatePathParams{},
			Responses: map[int]openAPIResponse{200: {
				Description: "Import template file",
				ContentType: "application/octet-stream",
				Schema: schemaSource{Inline: map[string]any{
					"type":   "string",
					"format": "binary",
				}},
			}},
		},
		{
			Method:      "POST",
			Path:        "/eval-sets/imports:preview",
			Summary:     "Preview eval set import",
			Tags:        []string{"eval-set-imports"},
			RequestBody: multipartBodyOf(evalSetImportPreviewOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Eval set import preview", evalset.ImportPreviewResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/eval-sets:import",
			Summary:     "Create eval set by import",
			Tags:        []string{"eval-set-imports"},
			RequestBody: jsonBodyOf(evalset.CreateEvalSetByImportRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created eval set import task", evalset.CreateEvalSetByImportResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/eval-set-import-tasks/{task_id}",
			Summary:    "Get eval set import task",
			Tags:       []string{"eval-set-imports"},
			PathParams: evalset.EvalSetImportTaskPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Eval set import task", evalset.EvalSetImportTaskResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/eval-sets/{eval_set_id}/items",
			Summary:     "List eval set items",
			Tags:        []string{"eval-set-items"},
			PathParams:  evalset.EvalSetPathParams{},
			QueryParams: evalset.ListEvalSetItemsQuery{},
			Responses:   map[int]openAPIResponse{200: resp("Eval set item list", evalset.ListEvalSetItemsResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/eval-sets/{eval_set_id}/imports",
			Summary:     "Append eval set import",
			Tags:        []string{"eval-set-imports"},
			PathParams:  evalset.EvalSetPathParams{},
			RequestBody: jsonBodyOf(evalset.AppendEvalSetImportRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Appended eval set import task", evalset.AppendEvalSetImportResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/eval-sets/{eval_set_id}/items",
			Summary:     "Create eval set item",
			Tags:        []string{"eval-set-items"},
			PathParams:  evalset.EvalSetPathParams{},
			RequestBody: jsonBodyOf(evalset.CreateEvalSetItemRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created eval set item", evalset.EvalSetItemResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/eval-sets/{eval_set_id}/items:batchDelete",
			Summary:     "Batch delete eval set items",
			Tags:        []string{"eval-set-items"},
			PathParams:  evalset.EvalSetPathParams{},
			RequestBody: jsonBodyOf(evalset.BatchDeleteEvalSetItemsRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Deleted eval set items", evalset.BatchDeleteEvalSetItemsResponse{})},
		},
		{
			Method:      "PATCH",
			Path:        "/eval-sets/{eval_set_id}/items/{item_id}",
			Summary:     "Update eval set item",
			Tags:        []string{"eval-set-items"},
			PathParams:  evalset.EvalSetItemPathParams{},
			RequestBody: jsonBodyOf(evalset.UpdateEvalSetItemRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Updated eval set item", evalset.EvalSetItemResponse{})},
		},
		{
			Method:     "DELETE",
			Path:       "/eval-sets/{eval_set_id}/items/{item_id}",
			Summary:    "Delete eval set item",
			Tags:       []string{"eval-set-items"},
			PathParams: evalset.EvalSetItemPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Deleted eval set item", evalset.DeleteEvalSetItemResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/eval-sets/{eval_set_id}",
			Summary:    "Get eval set",
			Tags:       []string{"eval-sets"},
			PathParams: evalset.EvalSetPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Eval set details", evalset.EvalSetResponse{})},
		},
		{
			Method:      "PATCH",
			Path:        "/eval-sets/{eval_set_id}",
			Summary:     "Update eval set",
			Tags:        []string{"eval-sets"},
			PathParams:  evalset.EvalSetPathParams{},
			RequestBody: jsonBodyOf(evalset.UpdateEvalSetRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Updated eval set", evalset.EvalSetResponse{})},
		},
		{
			Method:     "DELETE",
			Path:       "/eval-sets/{eval_set_id}",
			Summary:    "Delete eval set",
			Tags:       []string{"eval-sets"},
			PathParams: evalset.EvalSetPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Deleted eval set", evalset.DeleteEvalSetResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/datasets/{dataset}/documents",
			Summary:     "Document list",
			Tags:        []string{"documents"},
			PathParams:  datasetPathParams{},
			QueryParams: listDocumentsQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Document list", doc.ListDocumentsResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/datasets/{dataset}/documents",
			Summary:     "Create document",
			Tags:        []string{"documents"},
			PathParams:  datasetPathParams{},
			RequestBody: jsonBodyOf(doc.Doc{}, false),
			Responses:   map[int]openAPIResponse{200: resp("Created document", doc.Doc{})},
		},
		{
			Method:     "GET",
			Path:       "/datasets/{dataset}/documents/{document}",
			Summary:    "Get document",
			Tags:       []string{"documents"},
			PathParams: documentPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Document details", doc.Doc{})},
		},
		{
			Method:      "PATCH",
			Path:        "/datasets/{dataset}/documents/{document}",
			Summary:     "Update document",
			Tags:        []string{"documents"},
			PathParams:  documentPathParams{},
			RequestBody: jsonBodyOf(doc.Doc{}, false),
			Responses:   map[int]openAPIResponse{200: resp("Updated document", doc.Doc{})},
		},
		{
			Method:     "DELETE",
			Path:       "/datasets/{dataset}/documents/{document}",
			Summary:    "Delete document",
			Tags:       []string{"documents"},
			PathParams: documentPathParams{},
			Responses:  map[int]openAPIResponse{200: refResp("Deleted successfully", "EmptyObject")},
		},
		{
			Method:      "POST",
			Path:        "/datasets/{dataset}/documents:search",
			Summary:     "Search documents",
			Tags:        []string{"documents"},
			PathParams:  datasetPathParams{},
			RequestBody: jsonBodyOf(doc.SearchDocumentsRequest{}, false),
			Responses:   map[int]openAPIResponse{200: resp("Document search results", doc.ListDocumentsResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/documents:listByDatasets",
			Summary:     "List documents by datasets",
			Tags:        []string{"documents"},
			RequestBody: jsonBodyOf(doc.ListDatasetDocumentsRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Dataset document list", doc.ListDocumentsResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/documents:search",
			Summary:     "textSearch documents",
			Tags:        []string{"documents"},
			RequestBody: jsonBodyOf(doc.SearchDocumentsRequest{}, false),
			Responses:   map[int]openAPIResponse{200: resp("textDocument search results", doc.ListDocumentsResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/datasets/{dataset}:batchDelete",
			Summary:     "BatchDelete document",
			Tags:        []string{"documents"},
			PathParams:  datasetPathParams{},
			RequestBody: jsonBodyOf(doc.BatchDeleteDocumentRequest{}, true),
			Responses:   map[int]openAPIResponse{200: refResp("Deleted successfully", "EmptyObject")},
		},
		{
			Method:      "GET",
			Path:        "/datasets/{dataset}/tasks",
			Summary:     "Task list",
			Tags:        []string{"tasks"},
			PathParams:  datasetPathParams{},
			QueryParams: listTasksQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Task list", doc.ListTasksResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/datasets/{dataset}/tasks",
			Summary:     "Create task",
			Tags:        []string{"tasks"},
			PathParams:  datasetPathParams{},
			RequestBody: jsonBodyOf(doc.CreateTaskRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created task", doc.CreateTasksResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/datasets/{dataset}/tasks:search",
			Summary:     "Search tasks by task ID",
			Tags:        []string{"tasks"},
			PathParams:  datasetPathParams{},
			RequestBody: jsonBodyOf(doc.SearchTasksRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Task search results", doc.ListTasksResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/datasets/{dataset}/tasks/{task}",
			Summary:    "Get task",
			Tags:       []string{"tasks"},
			PathParams: taskPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Task details", doc.TaskResponse{})},
		},
		{
			Method:     "DELETE",
			Path:       "/datasets/{dataset}/tasks/{task}",
			Summary:    "Delete task",
			Tags:       []string{"tasks"},
			PathParams: taskPathParams{},
			Responses:  map[int]openAPIResponse{200: refResp("Deleted successfully", "EmptyObject")},
		},
		{
			Method:      "POST",
			Path:        "/datasets/{dataset}/tasks:start",
			Summary:     "Start task",
			Tags:        []string{"tasks"},
			PathParams:  datasetPathParams{},
			RequestBody: jsonBodyOf(doc.StartTaskRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Start result", doc.StartTasksResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/datasets/{dataset}/tasks/{task}:resume",
			Summary:     "Resume task",
			Tags:        []string{"tasks"},
			PathParams:  taskPathParams{},
			RequestBody: jsonBodyOf(doc.ResumeTaskRequest{}, false),
			Responses:   map[int]openAPIResponse{200: resp("Resume result", doc.StartTasksResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/datasets/{dataset}/tasks/{task}:suspend",
			Summary:     "Suspend task",
			Tags:        []string{"tasks"},
			PathParams:  taskPathParams{},
			RequestBody: jsonBodyOf(doc.SuspendJobRequest{}, true),
			Responses:   map[int]openAPIResponse{200: refResp("Suspended successfully", "EmptyObject")},
		},
		{
			Method:      "POST",
			Path:        "/datasets/{dataset}/uploads:initUpload",
			Summary:     "Initialize dataset upload",
			Tags:        []string{"tasks"},
			PathParams:  datasetPathParams{},
			RequestBody: jsonBodyOf(doc.InitUploadRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Upload initialization result", doc.InitUploadResponse{})},
		},
		{
			Method:  "PUT",
			Path:    "/datasets/{dataset}/uploads/{upload_id}/parts/{part_number}",
			Summary: "UploadDatasettext",
			Tags:    []string{"tasks"},
			PathParams: struct {
				Dataset    string `path:"dataset"`
				UploadID   string `path:"upload_id"`
				PartNumber string `path:"part_number"`
			}{},
			RequestBody: &openAPIBody{Required: true, ContentType: "application/octet-stream", Schema: schemaSource{Inline: map[string]any{"type": "string", "format": "binary"}}},
			Responses:   map[int]openAPIResponse{200: refResp("Part upload result", "UploadPartResponse")},
		},
		{
			Method:      "POST",
			Path:        "/datasets/{dataset}/uploads/{upload_id}:complete",
			Summary:     "Complete upload",
			Tags:        []string{"tasks"},
			PathParams:  uploadPathParams{},
			RequestBody: jsonBodyOf(doc.CompleteUploadRequest{}, false),
			Responses:   map[int]openAPIResponse{200: resp("Complete uploadtext", doc.CompleteUploadResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/datasets/{dataset}/uploads/{upload_id}:abort",
			Summary:     "Abort upload",
			Tags:        []string{"tasks"},
			PathParams:  uploadPathParams{},
			RequestBody: jsonBodyOf(doc.AbortUploadRequest{}, false),
			Responses:   map[int]openAPIResponse{200: refResp("Abort uploadtext", "AbortUploadResponse")},
		},
		{
			Method:      "POST",
			Path:        "/temp/uploads:initUpload",
			Summary:     "Initialize temp multipart upload",
			Tags:        []string{"uploads"},
			RequestBody: jsonBodyOf(doc.InitUploadRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Upload initialization result", doc.InitUploadResponse{})},
		},
		{
			Method:  "PUT",
			Path:    "/temp/uploads/{upload_id}/parts/{part_number}",
			Summary: "Upload temp filetext",
			Tags:    []string{"uploads"},
			PathParams: struct {
				UploadID   string `path:"upload_id"`
				PartNumber string `path:"part_number"`
			}{},
			RequestBody: &openAPIBody{Required: true, ContentType: "application/octet-stream", Schema: schemaSource{Inline: map[string]any{"type": "string", "format": "binary"}}},
			Responses:   map[int]openAPIResponse{200: refResp("Part upload result", "UploadPartResponse")},
		},
		{
			Method:  "POST",
			Path:    "/temp/uploads/{upload_id}:complete",
			Summary: "textUpload",
			Tags:    []string{"uploads"},
			PathParams: struct {
				UploadID string `path:"upload_id"`
			}{},
			RequestBody: jsonBodyOf(doc.CompleteUploadRequest{}, false),
			Responses:   map[int]openAPIResponse{200: resp("Complete uploadtext", doc.CompleteUploadResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/evolution/suggestions",
			Summary:     "List evolution suggestions",
			Description: "Use evolution_id=<resource_type>:<resource_id> for a single-parameter resource filter. resource_type and resource_key remain available as optional compatibility filters.",
			Tags:        []string{"evolution"},
			QueryParams: suggestionListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Suggestion list", suggestionListOpenAPIResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/evolution/suggestions/{id}",
			Summary:    "Get evolution suggestion",
			Tags:       []string{"evolution"},
			PathParams: suggestionIDPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Suggestion details", suggestionItemOpenAPIResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/evolution/suggestions/{id}:approve",
			Summary:    "Approve evolution suggestion",
			Tags:       []string{"evolution"},
			PathParams: suggestionIDPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Approved suggestion", suggestionItemOpenAPIResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/evolution/suggestions/{id}:reject",
			Summary:    "Reject evolution suggestion",
			Tags:       []string{"evolution"},
			PathParams: suggestionIDPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Rejected suggestion", suggestionItemOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/evolution/suggestions:batchApprove",
			Summary:     "Batch approve evolution suggestions",
			Description: "Sets every listed suggestion to accepted regardless of its current status, as long as the suggestion exists.",
			Tags:        []string{"evolution"},
			RequestBody: jsonBodyOf(suggestionBatchReviewOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Approved suggestions", suggestionBatchReviewOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/evolution/suggestions:batchReject",
			Summary:     "Batch reject evolution suggestions",
			Description: "Sets every listed suggestion to rejected regardless of its current status, as long as the suggestion exists.",
			Tags:        []string{"evolution"},
			RequestBody: jsonBodyOf(suggestionBatchReviewOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Rejected suggestions", suggestionBatchReviewOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skills",
			Summary:     "List skills",
			Tags:        []string{"skills"},
			QueryParams: skillListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Skill list", skillListOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skills",
			Summary:     "Create managed skill",
			Tags:        []string{"skills"},
			RequestBody: jsonBodyOf(skillCreateManagedOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created skill", skillDetailOpenAPIResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/skills/{skill_id}",
			Summary:    "Get skill details",
			Tags:       []string{"skills"},
			PathParams: skillPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Skill details", skillDetailOpenAPIResponse{})},
		},
		{
			Method:      "PATCH",
			Path:        "/skills/{skill_id}",
			Summary:     "Update managed skill",
			Tags:        []string{"skills"},
			PathParams:  skillPathParams{},
			RequestBody: jsonBodyOf(skillUpdateManagedOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Updated skill", skillDetailOpenAPIResponse{})},
		},
		{
			Method:     "DELETE",
			Path:       "/skills/{skill_id}",
			Summary:    "Delete managed skill",
			Tags:       []string{"skills"},
			PathParams: skillPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Deleted skill", skillDeleteOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skills/{skill_id}:generate",
			Summary:     "Generate skill draft",
			Tags:        []string{"skills"},
			PathParams:  skillPathParams{},
			RequestBody: jsonBodyOf(skillGenerateOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Generated skill draft", skillGenerateOpenAPIResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/skills/{skill_id}:draft-preview",
			Summary:    "Preview skill draft diff",
			Tags:       []string{"skills"},
			PathParams: skillPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Skill draft preview", skillDraftPreviewOpenAPIResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/skills/{skill_id}:confirm",
			Summary:    "Confirm skill draft",
			Tags:       []string{"skills"},
			PathParams: skillPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Confirmed skill", skillDetailOpenAPIResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/skills/{skill_id}:discard",
			Summary:    "Discard skill draft",
			Tags:       []string{"skills"},
			PathParams: skillPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Discarded skill draft", skillDiscardOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skills/{skill_id}:share",
			Summary:     "Share skill",
			Tags:        []string{"skill-shares"},
			PathParams:  skillPathParams{},
			RequestBody: jsonBodyOf(shareSkillOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created share task", skillShareCreateOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skills/{skill_id}:shares",
			Summary:     "List latest skill share status by target user",
			Tags:        []string{"skill-shares"},
			PathParams:  skillPathParams{},
			QueryParams: shareListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Skill share targets", skillShareTargetsOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skill-shares/incoming",
			Summary:     "List incoming skill shares",
			Tags:        []string{"skill-shares"},
			QueryParams: shareListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Incoming share list", skillShareListOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skill-shares/outgoing",
			Summary:     "List outgoing skill shares",
			Tags:        []string{"skill-shares"},
			QueryParams: shareListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Outgoing share list", skillShareListOpenAPIResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/skill-shares/{share_item_id}",
			Summary:    "Get skill share item",
			Tags:       []string{"skill-shares"},
			PathParams: shareItemPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Skill share detail", skillShareDetailOpenAPIResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/skill-shares/{share_item_id}:accept",
			Summary:    "Accept skill share",
			Tags:       []string{"skill-shares"},
			PathParams: shareItemPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Accepted share", skillShareAcceptOpenAPIResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/skill-shares/{share_item_id}:reject",
			Summary:    "Reject skill share",
			Tags:       []string{"skill-shares"},
			PathParams: shareItemPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Rejected share", skillShareRejectOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skill/suggestion",
			Summary:     "Create skill suggestions",
			Tags:        []string{"skill-evolution"},
			RequestBody: jsonBodyOf(internalSkillSuggestionOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created skill suggestions", recordedSuggestionListOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skill/create",
			Summary:     "Create skill directly from internal request",
			Tags:        []string{"skill-evolution"},
			RequestBody: jsonBodyOf(internalSkillCreateOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created skill", skillDetailOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skill/remove",
			Summary:     "Delete skill by ID",
			Tags:        []string{"skill-evolution"},
			RequestBody: jsonBodyOf(internalSkillRemoveOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created remove suggestion", recordedSuggestionListOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/memory/suggestion",
			Summary:     "Create memory suggestions",
			Tags:        []string{"memory"},
			RequestBody: jsonBodyOf(systemSuggestionOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created memory suggestions", recordedSuggestionListOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/model_providers",
			Summary:     "List user model providers",
			Description: "Per-user model provider list. Missing catalog rows are synced from default_model_providers on each request. Query parameter category filters by provider category (default model when category and exclude_category are both omitted). Query parameter exclude_category excludes a category (e.g. exclude_category=model returns ocr and search providers). Query parameter keyword filters by provider name (SQL LIKE).",
			Tags:        []string{"model_providers"},
			QueryParams: listUserModelProvidersQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("User model provider list", listUserModelProvidersOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/model_providers:with_groups",
			Summary:     "List user model providers that have groups",
			Description: "Returns user_model_providers for the current user that have at least one non-deleted row in user_model_provider_groups. The current user identity is injected by the auth gateway from the token. Same response shape as GET /model_providers.",
			Tags:        []string{"model_providers"},
			Responses:   map[int]openAPIResponse{200: resp("User model providers with groups", listUserModelProvidersOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/model_providers/{model_provider_id}/groups/{group_id}:check",
			Summary:     "Check model provider connectivity",
			Description: "Validates credentials. Model providers are proxied to the algorithm check endpoint; OCR cloud services use the same provider API/key request shape as the OCR readers. The current user identity is injected by the auth gateway from the token.",
			Tags:        []string{"model_providers"},
			RequestBody: jsonBodyOf(checkModelProviderOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("data: success and message from provider check", modelprovider.CheckModelProviderData{})},
		},
		{
			Method:      "GET",
			Path:        "/model_providers/features",
			Summary:     "Get model feature flags",
			Description: "Returns feature flags derived from the algorithm service runtime_models.yaml. Result is permanently cached after the first successful fetch. image_embed_enabled is true when a cross_modal_embed role is configured.",
			Tags:        []string{"model_providers"},
			Responses:   map[int]openAPIResponse{200: resp("Feature flags", modelprovider.ModelFeaturesResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/model_providers/models",
			Summary:     "List current user's models by model_type",
			Description: "Requires query model_type (e.g. llm, embedding). Returns all non-deleted user_model_provider_group_models for the current user with that model_type across all providers and groups. Ordered by user_model_provider_id, group id, then name. Same items as GET .../groups/{group_id}/models.",
			Tags:        []string{"model_providers"},
			QueryParams: listUserModelsByModelTypeQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Models list", listModelProviderGroupModelsOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/model_providers/selected_models",
			Summary:     "Get selected models by model_type",
			Description: "Returns the current user's selected model for each model_type.",
			Tags:        []string{"model_providers"},
			Responses:   map[int]openAPIResponse{200: resp("Selected models", listSelectedModelsOpenAPIResponse{})},
		},
		{
			Method:      "PUT",
			Path:        "/model_providers/selected_models",
			Summary:     "Save selected models by model_type",
			Description: "Upserts selected model rows for the current user. Each selection requires model_type and model_id. model_id must belong to the current user and model_type must match the model row.",
			Tags:        []string{"model_providers"},
			RequestBody: jsonBodyOf(setSelectedModelsOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Saved selected models", listSelectedModelsOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/model_providers/{model_provider_id}/groups",
			Summary:     "List model provider connection groups",
			Description: "Lists non-deleted groups for the user model provider. model_provider_id is the id from GET /model_providers. Each item includes api_key.",
			Tags:        []string{"model_providers"},
			PathParams:  modelProviderGroupPathParams{},
			Responses:   map[int]openAPIResponse{200: resp("Group list", listModelProviderGroupsOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/model_providers/{model_provider_id}/groups",
			Summary:     "Create model provider connection group",
			Description: "Creates a group (name, base_url, optional api_key) under the given user model provider. OCR cloud services validate the submitted API key against the provider API before saving. The api_key is not returned in the response body.",
			Tags:        []string{"model_providers"},
			PathParams:  modelProviderGroupPathParams{},
			RequestBody: jsonBodyOf(createModelProviderGroupOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created group", createModelProviderGroupOpenAPIResponse{})},
		},
		{
			Method:      "PATCH",
			Path:        "/model_providers/{model_provider_id}/groups/{group_id}",
			Summary:     "Update model provider connection group",
			Description: "Updates name, base_url, and optionally api_key for a group. OCR cloud services validate the effective API key against the provider API before saving. Omit api_key or send an empty string to keep the existing API key. The api_key is not returned in the response body.",
			Tags:        []string{"model_providers"},
			PathParams:  modelProviderGroupByIDPathParams{},
			RequestBody: jsonBodyOf(updateModelProviderGroupOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Updated group", createModelProviderGroupOpenAPIResponse{})},
		},
		{
			Method:      "DELETE",
			Path:        "/model_providers/{model_provider_id}/groups/{group_id}",
			Summary:     "Delete model provider connection group",
			Description: "Soft-deletes the group and its user_model_provider_group_models rows. The current user identity is injected by the auth gateway from the token.",
			Tags:        []string{"model_providers"},
			PathParams:  modelProviderGroupByIDPathParams{},
			Responses:   map[int]openAPIResponse{200: resp("Deleted group", deleteModelProviderGroupOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/model_providers/{model_provider_id}/groups/{group_id}/models",
			Summary:     "List models under a connection group",
			Description: "Lists non-deleted user_model_provider_group_models for the group. Each item includes is_default (true when copied from default_models seeding; false for user-added models).",
			Tags:        []string{"model_providers"},
			PathParams:  modelProviderGroupByIDPathParams{},
			Responses:   map[int]openAPIResponse{200: resp("Group models list", listModelProviderGroupModelsOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/model_providers/{model_provider_id}/groups/{group_id}/models",
			Summary:     "Add custom model under a connection group",
			Description: "Creates a user_model_provider_group_models row with is_default false (custom model name and model_type). Name must be unique within the group among active rows. provider_name and base_url are taken from the user provider and group. Response group_name is user_model_provider_groups.name (not stored on the model row).",
			Tags:        []string{"model_providers"},
			PathParams:  modelProviderGroupByIDPathParams{},
			RequestBody: jsonBodyOf(addModelProviderGroupModelOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created group model", addModelProviderGroupModelOpenAPIResponse{})},
		},
		{
			Method:      "DELETE",
			Path:        "/model_providers/{model_provider_id}/groups/{group_id}/models/{model_id}",
			Summary:     "Delete model under a connection group",
			Description: "Soft-deletes one user_model_provider_group_models row. The current user identity is injected by the auth gateway from the token.",
			Tags:        []string{"model_providers"},
			PathParams:  modelProviderGroupModelPathParams{},
			Responses:   map[int]openAPIResponse{200: resp("Deleted group model", deleteModelProviderGroupModelOpenAPIResponse{})},
		},
		{
			Method:    "GET",
			Path:      "/personalization-items",
			Summary:   "List managed memory and preference items",
			Tags:      []string{"personalization"},
			Responses: map[int]openAPIResponse{200: resp("Managed personalization items", managedStateListOpenAPIResponse{})},
		},
		{
			Method:    "GET",
			Path:      "/personalization-setting",
			Summary:   "Get personalization setting",
			Tags:      []string{"personalization"},
			Responses: map[int]openAPIResponse{200: resp("Personalization setting", personalizationSettingOpenAPIResponse{})},
		},
		{
			Method:      "PUT",
			Path:        "/personalization-setting",
			Summary:     "Set personalization setting",
			Tags:        []string{"personalization"},
			RequestBody: jsonBodyOf(personalizationSettingOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Updated personalization setting", personalizationSettingOpenAPIResponse{})},
		},
		{
			Method:      "PUT",
			Path:        "/memory",
			Summary:     "Upsert managed memory",
			Tags:        []string{"memory"},
			RequestBody: jsonBodyOf(managedStateUpsertOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Managed memory item", managedStateOpenAPIResponse{})},
		},
		{
			Method:    "GET",
			Path:      "/memory:draft-preview",
			Summary:   "Preview memory draft diff",
			Tags:      []string{"memory"},
			Responses: map[int]openAPIResponse{200: resp("Memory draft preview", systemDraftPreviewOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/memory:generate",
			Summary:     "Generate memory draft",
			Tags:        []string{"memory"},
			RequestBody: jsonBodyOf(skillGenerateOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Generated memory draft", systemGenerateOpenAPIResponse{})},
		},
		{
			Method:    "POST",
			Path:      "/memory:confirm",
			Summary:   "Confirm memory draft",
			Tags:      []string{"memory"},
			Responses: map[int]openAPIResponse{200: resp("Confirmed memory draft", systemConfirmOpenAPIResponse{})},
		},
		{
			Method:    "POST",
			Path:      "/memory:discard",
			Summary:   "Discard memory draft",
			Tags:      []string{"memory"},
			Responses: map[int]openAPIResponse{200: resp("Discarded memory draft", systemDiscardOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/user_preference/suggestion",
			Summary:     "Create user preference suggestions",
			Tags:        []string{"preferences"},
			RequestBody: jsonBodyOf(systemSuggestionOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created user preference suggestions", recordedSuggestionListOpenAPIResponse{})},
		},
		{
			Method:      "PUT",
			Path:        "/user-preference",
			Summary:     "Upsert managed user preference",
			Tags:        []string{"preferences"},
			RequestBody: jsonBodyOf(managedStateUpsertOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Managed user preference item", managedStateOpenAPIResponse{})},
		},
		{
			Method:    "GET",
			Path:      "/user-preference:draft-preview",
			Summary:   "Preview user preference draft diff",
			Tags:      []string{"preferences"},
			Responses: map[int]openAPIResponse{200: resp("User preference draft preview", systemDraftPreviewOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/user-preference:generate",
			Summary:     "Generate user preference draft",
			Tags:        []string{"preferences"},
			RequestBody: jsonBodyOf(skillGenerateOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Generated user preference draft", systemGenerateOpenAPIResponse{})},
		},
		{
			Method:    "POST",
			Path:      "/user-preference:confirm",
			Summary:   "Confirm user preference draft",
			Tags:      []string{"preferences"},
			Responses: map[int]openAPIResponse{200: resp("Confirmed user preference draft", systemConfirmOpenAPIResponse{})},
		},
		{
			Method:    "POST",
			Path:      "/user-preference:discard",
			Summary:   "Discard user preference draft",
			Tags:      []string{"preferences"},
			Responses: map[int]openAPIResponse{200: resp("Discarded user preference draft", systemDiscardOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/conversation:export",
			Summary:     "Export conversations",
			Tags:        []string{"conversations"},
			RequestBody: jsonBodyOf(chat.ExportConversationsRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Export conversation files", chat.ExportConversationsResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/conversation:export/files/{file_id}",
			Summary:    "Download exported conversation file",
			Tags:       []string{"conversations"},
			PathParams: exportConversationFilePathParams{},
			Responses:  map[int]openAPIResponse{200: {Description: "Exported conversation file", ContentType: "application/octet-stream", Schema: schemaSource{Inline: map[string]any{"type": "string", "format": "binary"}}}},
		},
		{
			Method:      "GET",
			Path:        "/agent/threads",
			Summary:     "List agent threads",
			Description: "List the current user's agent threads. Use thread_id from this response to load thread details or history.",
			Tags:        []string{"agent"},
			QueryParams: agentThreadListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Agent thread list", agentThreadListOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/agent/files:content",
			Summary:     "Read agent result file content",
			Description: "Read a local agent result file by path and return its text content. Use JSON body to avoid URL path escaping issues.",
			Tags:        []string{"agent"},
			RequestBody: jsonBodyOf(agentFileContentOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Agent result file content", agentFileContentOpenAPIResponse{})},
		},
		{
			Method:  "POST",
			Path:    "/temp/uploads/{upload_id}:abort",
			Summary: "AborttextUpload",
			Tags:    []string{"uploads"},
			PathParams: struct {
				UploadID string `path:"upload_id"`
			}{},
			RequestBody: jsonBodyOf(doc.AbortUploadRequest{}, false),
			Responses:   map[int]openAPIResponse{200: refResp("Abort uploadtext", "AbortUploadResponse")},
		},
		{
			Method:      "POST",
			Path:        "/word_group:checkExists",
			Summary:     "Check which words already exist",
			Tags:        []string{"word_group"},
			RequestBody: jsonBodyOf(wordgroup.CheckWordsExistRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Existing words among term and aliases", wordgroup.CheckWordsExistResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/word_group:update",
			Summary:     "Update word group (term, description, lock, replace aliases)",
			Tags:        []string{"word_group"},
			RequestBody: jsonBodyOf(wordgroup.UpdateWordGroupRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Updated word group", wordgroup.CreateWordGroupResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/word_group:search",
			Summary:     "Search word groups by keyword and optional source (paginated list)",
			Tags:        []string{"word_group"},
			RequestBody: jsonBodyOf(wordgroup.SearchWordGroupsRequest{}, true),
			Responses: map[int]openAPIResponse{
				200: resp("Word group search results", wordgroup.ListWordGroupsResponse{}),
			},
		},
		{
			Method:      "GET",
			Path:        "/word_group",
			Summary:     "List word groups (term row updated_at DESC)",
			Tags:        []string{"word_group"},
			QueryParams: listWordGroupsQueryParams{},
			Responses: map[int]openAPIResponse{
				200: resp("Word group list", wordgroup.ListWordGroupsResponse{}),
			},
		},
		{
			Method:  "GET",
			Path:    "/word_group/{group_id}",
			Summary: "Get word group detail by group_id",
			Tags:    []string{"word_group"},
			PathParams: struct {
				GroupID string `path:"group_id"`
			}{},
			Responses: map[int]openAPIResponse{200: resp("Word group detail", wordgroup.CreateWordGroupResponse{})},
		},
		{
			Method:  "DELETE",
			Path:    "/word_group/{group_id}",
			Summary: "Delete word group by group_id",
			Tags:    []string{"word_group"},
			PathParams: struct {
				GroupID string `path:"group_id"`
			}{},
			Responses: map[int]openAPIResponse{200: resp("Deleted word group", wordgroup.DeleteWordGroupResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/word_group:batchDelete",
			Summary:     "Batch soft-delete word groups by group_ids",
			Tags:        []string{"word_group"},
			RequestBody: jsonBodyOf(wordgroup.BatchDeleteWordGroupsRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Batch deleted word groups", wordgroup.BatchDeleteWordGroupsResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/word_group:merge",
			Summary:     "Merge word groups: soft-delete merged groups' words, recreate master group from term, aliases, description",
			Tags:        []string{"word_group"},
			RequestBody: jsonBodyOf(wordgroup.MergeWordGroupsRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Merged word group", wordgroup.CreateWordGroupResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/word_group_conflict:mergeAndAddWord",
			Summary:     "Merge word groups from merges list, add word into group_ids, resolve conflict",
			Tags:        []string{"word_group"},
			RequestBody: jsonBodyOf(wordgroup.MergeAndAddWordRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Merged word groups with added word (one item per merge batch)", wordgroup.MergeAndAddWordResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/word_group",
			Summary:     "Create word group",
			Tags:        []string{"word_group"},
			RequestBody: jsonBodyOf(wordgroup.CreateWordGroupRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created word group", wordgroup.CreateWordGroupResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/word_group_conflict",
			Summary:     "List pending word group conflicts (updated_at DESC)",
			Tags:        []string{"word_group"},
			QueryParams: listWordGroupsQueryParams{},
			Responses: map[int]openAPIResponse{
				200: resp("Word group conflict list", wordgroup.ListWordGroupConflictsResponse{}),
			},
		},
		{
			Method:      "POST",
			Path:        "/word_group_conflict:addToGroup",
			Summary:     "Add conflict word to selected groups",
			Tags:        []string{"word_group"},
			RequestBody: jsonBodyOf(wordgroup.AddWordGroupConflictToGroupsRequest{}, true),
			Responses: map[int]openAPIResponse{
				200: resp("Conflict word add-to-group result", wordgroup.AddWordGroupConflictToGroupsResponse{}),
			},
		},
		{
			Method:      "POST",
			Path:        "/word_group_conflict:createGroup",
			Summary:     "Create word group from conflict and optionally add conflict word to existing groups",
			Description: "Creates a new word group (term, aliases, description). If group_ids is non-empty, inserts the conflict word as alias into each existing group (skips duplicates). Soft-deletes the conflict row by id.",
			Tags:        []string{"word_group"},
			RequestBody: jsonBodyOf(wordgroup.CreateWordGroupFromConflictRequest{}, true),
			Responses: map[int]openAPIResponse{
				200: resp("Created word group from conflict", wordgroup.CreateWordGroupFromConflictResponse{}),
			},
		},
		{
			Method:  "DELETE",
			Path:    "/word_group_conflict/{id}",
			Summary: "Soft-delete a word group conflict by id",
			Tags:    []string{"word_group"},
			PathParams: struct {
				ID string `path:"id"`
			}{},
			Responses: map[int]openAPIResponse{
				200: resp("Deleted word group conflict", wordgroup.DeleteWordGroupConflictResponse{}),
			},
		},
		{
			Method:      "POST",
			Path:        "/inner/word_group:apply",
			Summary:     "Internal: apply word-group actions in batch (algorithm → core)",
			Tags:        []string{"word_group"},
			RequestBody: jsonBodyOf(wordgroup.ApplyWordGroupActionRequest{}, true),
			Responses: map[int]openAPIResponse{
				200: resp("Per-item apply results", wordgroup.ApplyWordGroupActionBatchResponse{}),
			},
		},
		{
			Method:      "GET",
			Path:        "/model_providers/verified",
			Summary:     "Check whether a provider category is ready",
			Description: "Checks the current user's selected provider for the given category first, then falls back to a shared provider selection. This endpoint does not return selectable group details.",
			Tags:        []string{"model_providers"},
			QueryParams: verifiedProviderQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Provider ready state", verifiedProviderOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/model_providers/provider_groups",
			Summary:     "List verified provider groups for the current user",
			Description: "Lists verified provider groups owned by the current user for the given non-model category (for example ocr or search). Shared provider groups are intentionally excluded from this selectable list.",
			Tags:        []string{"model_providers"},
			QueryParams: verifiedProviderQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Current user's verified provider groups", verifiedProviderGroupsOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/model_providers/selected_providers",
			Summary:     "Get selected provider groups (OCR, search, etc.)",
			Description: "Returns the current user's selected provider group for each non-model category.",
			Tags:        []string{"model_providers"},
			Responses:   map[int]openAPIResponse{200: resp("Selected providers", selectedProvidersOpenAPIResponse{})},
		},
		{
			Method:      "PUT",
			Path:        "/model_providers/selected_providers",
			Summary:     "Set selected provider group for a category",
			Description: "Upserts selected provider groups by category. Request shape mirrors selected_models: selections contains category and group_id. Send an empty group_id to clear a category selection.",
			Tags:        []string{"model_providers"},
			RequestBody: jsonBodyOf(setSelectedProviderOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Saved selected providers", selectedProvidersOpenAPIResponse{})},
		},
		{
			Method:      "PUT",
			Path:        "/model_providers/selected_providers/share",
			Summary:     "Set shared provider group for a category",
			Description: "Sets or clears the share flag for a selected provider row. Only one share=true row is allowed per category. Protected by document.write permission.",
			Tags:        []string{"model_providers"},
			RequestBody: jsonBodyOf(setSharedProviderOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: refResp("Updated share flag", "EmptyObject")},
		},
	}
}
