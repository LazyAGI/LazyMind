package main

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"lazymind/core/chat"
	"lazymind/core/datasource"
	"lazymind/core/doc"
	"lazymind/core/evalset"
	"lazymind/core/mcp"
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
		param := map[string]any{
			"name":     name,
			"in":       location,
			"required": required,
			"schema":   schema,
		}
		if values := openAPIEnumValues(field.Tag.Get("enum")); len(values) > 0 {
			schema["enum"] = values
		}
		if description := strings.TrimSpace(field.Tag.Get("desc")); description != "" {
			param["description"] = description
		}
		params = append(params, param)
	}
	return params
}

func openAPIEnumValues(raw string) []any {
	parts := strings.Split(raw, ",")
	values := make([]any, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
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
			propertySchema := b.schemaForType(field.Type)
			if description := strings.TrimSpace(field.Tag.Get("desc")); description != "" {
				propertySchema["description"] = description
			}
			properties[name] = propertySchema
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

type databaseConnectionPathParams struct {
	Connection string `path:"connection"`
}

type deleteDatabaseConnectionOpenAPIResponse struct {
	Deleted bool `json:"deleted"`
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

type toolPathParams struct {
	ToolName string `path:"tool_name"`
}

type toolListQueryParams struct {
	Keyword  string `query:"keyword"`
	Page     int32  `query:"page"`
	PageSize int32  `query:"page_size"`
}

type mcpServerListQueryParams struct {
	Keyword  string `query:"keyword"`
	Page     int32  `query:"page"`
	PageSize int32  `query:"page_size"`
}

type mcpServerPathParams struct {
	ID string `path:"id"`
}

type mcpDeleteServerOpenAPIResponse struct {
	ID string `json:"id"`
}

type toolMethodOpenAPIResponse struct {
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
}

type toolGroupOpenAPIResponse struct {
	Name        string                      `json:"name"`
	Label       string                      `json:"label,omitempty"`
	Description string                      `json:"description,omitempty"`
	Methods     []toolMethodOpenAPIResponse `json:"methods,omitempty"`
	CanDisable  bool                        `json:"can_disable"`
	Active      bool                        `json:"active"`
	Disabled    bool                        `json:"disabled"`
}

type toolListOpenAPIResponse struct {
	ToolGroups []toolGroupOpenAPIResponse `json:"tool_groups"`
	Page       int32                      `json:"page"`
	PageSize   int32                      `json:"page_size"`
	Total      int32                      `json:"total"`
}

type toolStateOpenAPIResponse struct {
	Name     string `json:"name"`
	Disabled bool   `json:"disabled"`
}

type agentThreadPathParams struct {
	ThreadID string `path:"thread_id"`
}

type agentThreadGatePathParams struct {
	ThreadID string `path:"thread_id"`
	Step     string `path:"step"`
	Version  int32  `path:"version"`
}

type agentThreadGateVersionPathParams struct {
	ThreadID string `path:"thread_id"`
	Version  int32  `path:"version"`
}

type agentThreadTracePathParams struct {
	ThreadID string `path:"thread_id"`
	TraceID  string `path:"trace_id"`
}

type agentThreadTraceCompareQueryParams struct {
	A string `query:"a" required:"true"`
	B string `query:"b" required:"true"`
}

type agentThreadEvalBadCasesQueryParams struct {
	PageSize    int32  `query:"page_size"`
	PageToken   string `query:"page_token"`
	Keyword     string `query:"keyword"`
	FailureType string `query:"failure_type"`
}

type agentThreadABTestCaseDetailsQueryParams struct {
	PageSize  int32  `query:"page_size"`
	PageToken string `query:"page_token"`
	Keyword   string `query:"keyword"`
	Outcome   string `query:"outcome"`
}

type agentThreadEventsQueryParams struct {
	StepID string `query:"step_id"`
}

type agentThreadEventTraceQueryParams struct {
	StepID string `query:"step_id" required:"true"`
}

type agentThreadListQueryParams struct {
	PageSize  int32  `query:"page_size"`
	PageToken string `query:"page_token"`
}

type agentCandidateListQueryParams struct {
	ThreadID  string `query:"thread_id" required:"true"`
	Status    string `query:"status"`
	PageSize  int32  `query:"page_size"`
	PageToken string `query:"page_token"`
}

type agentCandidatePathParams struct {
	CandidateID string `path:"candidate_id:.*"`
}

type agentRouterAlgorithmPathParams struct {
	AlgorithmID string `path:"algorithm_id"`
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

type resourceUpdateTaskPathParams struct {
	TaskID string `path:"task_id"`
}

type reviewResultPathParams struct {
	ReviewResultID string `path:"review_result_id"`
}

type resourceVersionPathParams struct {
	VersionID string `path:"version_id"`
}

type resourceUpdateTaskListQueryParams struct {
	Page         int32  `query:"page"`
	PageSize     int32  `query:"page_size"`
	Status       string `query:"status"`
	ResourceType string `query:"resource_type"`
	TaskType     string `query:"task_type"`
}

type skillReviewTaskListQueryParams struct {
	Page      int32  `query:"page"`
	PageSize  int32  `query:"page_size"`
	Status    string `query:"status"`
	RequestID string `query:"requestid"`
}

type skillReviewResultListQueryParams struct {
	Page         int32  `query:"page"`
	PageSize     int32  `query:"page_size"`
	ReviewStatus string `query:"review_status"`
	Type         string `query:"type"`
	SkillName    string `query:"skill_name"`
	RequestID    string `query:"requestid"`
}

type memoryReviewResultListQueryParams struct {
	Page         int32  `query:"page"`
	PageSize     int32  `query:"page_size"`
	ReviewStatus string `query:"review_status"`
	Target       string `query:"target"`
}

type resourceVersionListQueryParams struct {
	Page         int32  `query:"page"`
	PageSize     int32  `query:"page_size"`
	ResourceType string `query:"resource_type"`
	ResourceID   string `query:"resource_id"`
}

type resourceUpdateTaskOpenAPIResponse struct {
	ID             string  `json:"id"`
	TaskType       string  `json:"task_type"`
	ResourceType   string  `json:"resource_type"`
	UserID         string  `json:"user_id"`
	ResourceID     string  `json:"resource_id"`
	TriggerType    string  `json:"trigger_type"`
	TriggerID      string  `json:"trigger_id"`
	Status         string  `json:"status"`
	ReviewResultID string  `json:"review_result_id,omitempty"`
	ResultID       string  `json:"result_id,omitempty"`
	ErrorCode      string  `json:"error_code,omitempty"`
	ErrorMessage   string  `json:"error_message,omitempty"`
	AttemptCount   int32   `json:"attempt_count"`
	NextRunAt      string  `json:"next_run_at"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	StartedAt      *string `json:"started_at,omitempty"`
	FinishedAt     *string `json:"finished_at,omitempty"`
}

type resourceUpdateTaskListOpenAPIResponse struct {
	Items    []resourceUpdateTaskOpenAPIResponse `json:"items"`
	Page     int32                               `json:"page"`
	PageSize int32                               `json:"page_size"`
	Total    int64                               `json:"total"`
}

type skillReviewResultOpenAPIResponse struct {
	ID             string                         `json:"id"`
	SkillName      string                         `json:"skill_name"`
	Type           string                         `json:"type"`
	ReviewStatus   string                         `json:"review_status"`
	UserID         string                         `json:"userid"`
	RequestID      string                         `json:"requestid"`
	SkillContent   string                         `json:"skill_content,omitempty"`
	CurrentContent string                         `json:"current_content,omitempty"`
	Diff           string                         `json:"diff,omitempty"`
	DiffEntryLines []diffEntryLineOpenAPIResponse `json:"diffEntryLines,omitempty"`
	Summary        string                         `json:"summary"`
	Time           string                         `json:"time"`
}

type skillReviewResultListOpenAPIResponse struct {
	Items    []skillReviewResultOpenAPIResponse `json:"items"`
	Page     int32                              `json:"page"`
	PageSize int32                              `json:"page_size"`
	Total    int64                              `json:"total"`
}

type skillReviewSummaryOpenAPIResponse struct {
	QualifiedSessionCount int32                              `json:"qualified_session_count"`
	UserTurnCount         int32                              `json:"user_turn_count"`
	ToolCallCount         int32                              `json:"tool_call_count"`
	MinUserTurns          int32                              `json:"min_user_turns"`
	MinToolTurns          int32                              `json:"min_tool_turns"`
	QuantityThreshold     int32                              `json:"quantity_threshold"`
	WindowStart           string                             `json:"window_start"`
	WindowEnd             string                             `json:"window_end"`
	RunningTask           *resourceUpdateTaskOpenAPIResponse `json:"running_task,omitempty"`
	RunningRequestID      string                             `json:"running_requestid,omitempty"`
}

type skillReviewRunOpenAPIResponse struct {
	Task      resourceUpdateTaskOpenAPIResponse `json:"task"`
	Summary   skillReviewSummaryOpenAPIResponse `json:"summary"`
	RequestID string                            `json:"requestid"`
}

type skillReviewTaskStatusOpenAPIResponse struct {
	Task        resourceUpdateTaskOpenAPIResponse `json:"task"`
	RequestID   string                            `json:"requestid"`
	Status      string                            `json:"status"`
	RunStatus   string                            `json:"run_status,omitempty"`
	ResultCount int64                             `json:"result_count"`
}

type skillReviewTaskListOpenAPIResponse struct {
	Items    []skillReviewTaskStatusOpenAPIResponse `json:"items"`
	Page     int32                                  `json:"page"`
	PageSize int32                                  `json:"page_size"`
	Total    int64                                  `json:"total"`
}

type skillOrganizeOpenAPIRequest struct {
	RequestID   string   `json:"requestid"`
	Skills      []string `json:"skills"`
	ArtifactDir string   `json:"artifact_dir,omitempty"`
}

type skillOrganizeOpenAPIResponse struct {
	Status    string `json:"status"`
	RequestID string `json:"requestid"`
	TaskID    string `json:"taskid"`
}

type memoryReviewResultOpenAPIResponse struct {
	ID             string         `json:"id"`
	UserID         string         `json:"user_id"`
	Target         string         `json:"target"`
	SessionID      string         `json:"session_id"`
	SourceContent  string         `json:"source_content"`
	Content        string         `json:"content"`
	CurrentContent string         `json:"current_content,omitempty"`
	Diff           string         `json:"diff,omitempty"`
	Operations     map[string]any `json:"operations,omitempty"`
	State          string         `json:"state"`
	ReviewStatus   string         `json:"review_status"`
	Time           string         `json:"time"`
}

type memoryReviewResultListOpenAPIResponse struct {
	Items    []memoryReviewResultOpenAPIResponse `json:"items"`
	Page     int32                               `json:"page"`
	PageSize int32                               `json:"page_size"`
	Total    int64                               `json:"total"`
}

type resourceVersionOpenAPIResponse struct {
	ID            string `json:"id"`
	ResourceType  string `json:"resource_type"`
	ResourceID    string `json:"resource_id"`
	UserID        string `json:"user_id"`
	ChangeSource  string `json:"change_source"`
	FromVersion   int64  `json:"from_version"`
	ToVersion     int64  `json:"to_version"`
	SourceRefType string `json:"source_ref_type"`
	SourceRefID   string `json:"source_ref_id"`
	BeforeContent string `json:"before_content"`
	AfterContent  string `json:"after_content"`
	Diff          string `json:"diff"`
	CreatedAt     string `json:"created_at"`
}

type resourceVersionListOpenAPIResponse struct {
	Items    []resourceVersionOpenAPIResponse `json:"items"`
	Page     int32                            `json:"page"`
	PageSize int32                            `json:"page_size"`
	Total    int64                            `json:"total"`
}

type latestVersionChangeOpenAPIResponse struct {
	ChangeSource  string `json:"change_source"`
	SourceRefType string `json:"source_ref_type"`
	SourceRefID   string `json:"source_ref_id"`
	ChangedAt     string `json:"changed_at"`
}

type skillGenerateOpenAPIRequest struct {
	UserInstruct string `json:"user_instruct"`
}

type skillGenerateOpenAPIResponse struct {
	DraftStatus        string `json:"draft_status"`
	DraftSourceVersion int64  `json:"draft_source_version"`
	DraftPath          string `json:"draft_path"`
	Outdated           bool   `json:"outdated"`
}

type skillDraftPreviewOpenAPIResponse struct {
	SkillID            string `json:"skill_id"`
	ReviewResultID     string `json:"review_result_id"`
	ReviewStatus       string `json:"review_status"`
	DraftStatus        string `json:"draft_status"`
	DraftSourceVersion int64  `json:"draft_source_version"`
	Diff               string `json:"diff"`
	Outdated           bool   `json:"outdated"`
}

type shareItemPathParams struct {
	ShareItemID string `path:"share_item_id"`
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

type skillSourceOpenAPIRequest struct {
	Type     string `json:"type" desc:"Source type: uploaded_zip or url."`
	UploadID string `json:"upload_id,omitempty" desc:"Completed upload id when type is uploaded_zip."`
	URL      string `json:"url,omitempty" desc:"ZIP URL when type is url."`
}

type skillCreateManagedOpenAPIRequest struct {
	Name        string                    `json:"name"`
	Category    string                    `json:"category"`
	Source      skillSourceOpenAPIRequest `json:"source"`
	Description string                    `json:"description,omitempty"`
	Tags        []string                  `json:"tags,omitempty"`
	AutoEvo     *bool                     `json:"auto_evo,omitempty"`
	IsEnabled   *bool                     `json:"is_enabled,omitempty"`
}

type skillUpdateManagedOpenAPIRequest struct {
	Name        *string                    `json:"name,omitempty" desc:"Optional. Rename the directory skill."`
	Category    *string                    `json:"category,omitempty" desc:"Optional. Move the skill to another category."`
	Description *string                    `json:"description,omitempty" desc:"Optional. Replace product metadata description; SKILL.md is not rewritten."`
	Tags        []string                   `json:"tags,omitempty" desc:"Optional. Replace tags; omit to keep tags unchanged."`
	AutoEvo     *bool                      `json:"auto_evo,omitempty" desc:"Optional. Enable or disable automatic evolution."`
	IsEnabled   *bool                      `json:"is_enabled,omitempty" desc:"Optional. Enable or disable the skill."`
	Source      *skillSourceOpenAPIRequest `json:"source,omitempty" desc:"Optional. Replace the whole skill directory from an uploaded ZIP or URL."`
}

type skillDraftSummaryOpenAPIResponse struct {
	HasUncommittedDraft bool   `json:"has_uncommitted_draft"`
	TaskID              string `json:"task_id,omitempty"`
	Version             int64  `json:"version"`
}

type skillListItemOpenAPIResponse struct {
	ID                  string                              `json:"id"`
	SkillID             string                              `json:"skill_id"`
	Name                string                              `json:"name"`
	SkillName           string                              `json:"skill_name,omitempty"`
	Description         string                              `json:"description"`
	Category            string                              `json:"category"`
	Tags                []string                            `json:"tags"`
	HeadRevisionID      string                              `json:"head_revision_id"`
	FileContent         string                              `json:"file_content,omitempty"`
	Draft               skillDraftSummaryOpenAPIResponse    `json:"draft"`
	LatestVersionChange *latestVersionChangeOpenAPIResponse `json:"latest_version_change,omitempty"`
}

type skillListOpenAPIResponse struct {
	Items    []skillListItemOpenAPIResponse `json:"items"`
	Page     int32                          `json:"page"`
	PageSize int32                          `json:"page_size"`
	Total    int32                          `json:"total"`
}

type skillTagsOpenAPIResponse struct {
	Tags []string `json:"tags"`
}

type skillCategoriesOpenAPIResponse struct {
	Categories []string `json:"categories"`
}

type skillDetailOpenAPIResponse struct {
	ID                  string                              `json:"id"`
	SkillID             string                              `json:"skill_id"`
	Name                string                              `json:"name"`
	SkillName           string                              `json:"skill_name,omitempty"`
	Description         string                              `json:"description"`
	Category            string                              `json:"category"`
	Tags                []string                            `json:"tags"`
	HeadRevisionID      string                              `json:"head_revision_id"`
	FileContent         string                              `json:"file_content,omitempty"`
	Draft               skillDraftSummaryOpenAPIResponse    `json:"draft"`
	LatestVersionChange *latestVersionChangeOpenAPIResponse `json:"latest_version_change,omitempty"`
}

type skillWriteOpenAPIResponse struct {
	SkillID        string `json:"skill_id"`
	HeadRevisionID string `json:"head_revision_id,omitempty"`
}

type skillFileQueryParams struct {
	Path string `query:"path" required:"true"`
}

type skillFSQueryParams struct {
	Path string `query:"path"`
}

type skillRevisionPathParams struct {
	SkillID    string `path:"skill_id"`
	RevisionID string `path:"revision_id"`
}

type builtinSkillPathParams struct {
	BuiltinSkillUID string `path:"builtin_skill_uid"`
}

type skillTreeNodeOpenAPIResponse struct {
	Name     string                         `json:"name"`
	Path     string                         `json:"path"`
	Type     string                         `json:"type"`
	Children []skillTreeNodeOpenAPIResponse `json:"children,omitempty"`
	BlobHash string                         `json:"blob_hash,omitempty"`
	Size     int64                          `json:"size,omitempty"`
	Mime     string                         `json:"mime,omitempty"`
	FileType string                         `json:"file_type,omitempty"`
	Binary   bool                           `json:"binary,omitempty"`
}

type skillFileOpenAPIResponse struct {
	Path        string `json:"path"`
	Content     string `json:"content,omitempty"`
	Binary      bool   `json:"binary"`
	DownloadURL string `json:"download_url,omitempty"`
	Mime        string `json:"mime,omitempty"`
	FileType    string `json:"file_type,omitempty"`
	BlobHash    string `json:"blob_hash,omitempty"`
}

type skillFSListOpenAPIResponse struct {
	Items []skillTreeNodeOpenAPIResponse `json:"items"`
}

type skillExistsOpenAPIResponse struct {
	Exists bool `json:"exists"`
}

type skillDraftStateOpenAPIResponse struct {
	HasUncommittedDraft bool   `json:"has_uncommitted_draft"`
	DraftVersion        int64  `json:"draft_version"`
	BaseRevisionID      string `json:"base_revision_id,omitempty"`
	TaskID              string `json:"task_id,omitempty"`
	ConversationID      string `json:"conversation_id,omitempty"`
}

type skillDraftStatusOpenAPIResponse struct {
	BaseRevisionID      string `json:"base_revision_id,omitempty"`
	TaskID              string `json:"task_id,omitempty"`
	ConversationID      string `json:"conversation_id,omitempty"`
	DraftVersion        int64  `json:"draft_version"`
	HasUncommittedDraft bool   `json:"has_uncommitted_draft"`
	OverlayCount        int64  `json:"overlay_count"`
}

type skillDraftWriteTextOpenAPIRequest struct {
	Path                 string `json:"path"`
	Content              string `json:"content"`
	ExpectedDraftVersion int64  `json:"expected_draft_version"`
}

type skillDraftUploadOpenAPIRequest struct {
	Path                 string `json:"path"`
	UploadID             string `json:"upload_id"`
	ExpectedDraftVersion int64  `json:"expected_draft_version"`
}

type skillDraftMkdirOpenAPIRequest struct {
	Path                 string `json:"path"`
	ExpectedDraftVersion int64  `json:"expected_draft_version"`
}

type skillDraftDeleteOpenAPIRequest struct {
	Path                 string `json:"path,omitempty"`
	Recursive            bool   `json:"recursive,omitempty"`
	ExpectedDraftVersion int64  `json:"expected_draft_version,omitempty"`
}

type skillDraftMoveOpenAPIRequest struct {
	From                 string `json:"from"`
	To                   string `json:"to"`
	ExpectedDraftVersion int64  `json:"expected_draft_version"`
}

type skillDraftMutationOpenAPIResponse struct {
	DraftVersion int64  `json:"draft_version"`
	BlobHash     string `json:"blob_hash,omitempty"`
}

type skillCommitOpenAPIRequest struct {
	DraftVersion int64 `json:"draft_version"`
}

type skillCommitOpenAPIResponse struct {
	RevisionID string `json:"revision_id"`
	RevisionNo int64  `json:"revision_no"`
}

type skillRevisionOpenAPIResponse struct {
	ID               string `json:"id"`
	RevisionID       string `json:"revision_id"`
	SkillID          string `json:"skill_id"`
	ParentRevisionID string `json:"parent_revision_id,omitempty"`
	RevisionNo       int64  `json:"revision_no"`
	TreeHash         string `json:"tree_hash"`
	Message          string `json:"message,omitempty"`
	ChangeSource     string `json:"change_source"`
	CreatedBy        string `json:"created_by,omitempty"`
	CreatedAt        string `json:"created_at"`
	FileContent      string `json:"file_content,omitempty"`
}

type skillRevisionListOpenAPIResponse struct {
	Items []skillRevisionOpenAPIResponse `json:"items"`
}

type skillRollbackOpenAPIRequest struct {
	TargetRevisionID string `json:"target_revision_id,omitempty"`
	RevisionID       string `json:"revision_id,omitempty"`
}

type skillRollbackWarningOpenAPIResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type skillRollbackDiffFileOpenAPIResponse struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type skillRollbackDiffTreeOpenAPIResponse struct {
	Files []skillRollbackDiffFileOpenAPIResponse `json:"files"`
}

type skillRollbackPreviewOpenAPIResponse struct {
	TreeDiff skillRollbackDiffTreeOpenAPIResponse  `json:"tree_diff"`
	Warnings []skillRollbackWarningOpenAPIResponse `json:"warnings"`
}

type skillRollbackOpenAPIResponse struct {
	HeadRevisionID string `json:"head_revision_id"`
	RevisionNo     int64  `json:"revision_no"`
}

type diffRefOpenAPIRequest struct {
	Type       string `json:"type"`
	SkillID    string `json:"skill_id,omitempty"`
	RevisionID string `json:"revision_id,omitempty"`
	UploadID   string `json:"upload_id,omitempty"`
}

type diffOpenAPIRequest struct {
	Old          diffRefOpenAPIRequest `json:"old"`
	New          diffRefOpenAPIRequest `json:"new"`
	Path         string                `json:"path,omitempty"`
	ContextLines int                   `json:"context_lines,omitempty"`
	Mode         string                `json:"mode,omitempty"`
	OldStart     int                   `json:"old_start,omitempty"`
	NewStart     int                   `json:"new_start,omitempty"`
	Lines        int                   `json:"lines,omitempty"`
}

type diffEntryLineOpenAPIResponse struct {
	Type                    string `json:"type"`
	Text                    string `json:"text"`
	HTML                    string `json:"html,omitempty"`
	OldLine                 int    `json:"oldLine,omitempty"`
	NewLine                 int    `json:"newLine,omitempty"`
	DisplayNoNewLineWarning bool   `json:"displayNoNewLineWarning,omitempty"`
}

type diffFileOpenAPIResponse struct {
	Path           string                         `json:"path"`
	Type           string                         `json:"type"`
	Status         string                         `json:"status"`
	Binary         bool                           `json:"binary"`
	TooLarge       bool                           `json:"too_large"`
	CacheWritten   bool                           `json:"cache_written"`
	DiffEntryLines []diffEntryLineOpenAPIResponse `json:"diffEntryLines"`
}

type diffTreeOpenAPIResponse struct {
	UserID       string                    `json:"user_id,omitempty"`
	Files        []diffFileOpenAPIResponse `json:"files"`
	CacheWritten bool                      `json:"cache_written"`
}

type remoteFSListQueryParams struct {
	UserID string `query:"user_id" required:"true" desc:"Required. Target user id used to resolve skills owned by the user."`
	Path   string `query:"path" required:"true" desc:"RemoteFS path. Use skills for categories, skills/<category> for package list, or skills/<category>/<skill_name>[/rel_path] for package content."`
	TaskID string `query:"task_id,omitempty" desc:"Optional for skills root/category list; required when path enters a package. Prefix review_ reads/writes existing draft, org_ is skill organization, other values are skill_editor session ids."`
}

type remoteFSQueryParams struct {
	UserID   string `query:"user_id" required:"true" desc:"Required. Target user id used to resolve skills owned by the user."`
	Path     string `query:"path" required:"true" desc:"RemoteFS package path: skills/<category>/<skill_name>[/rel_path]."`
	TaskID   string `query:"task_id" required:"true" desc:"Required package content task id. Prefix review_ reads/writes existing draft, org_ is skill organization, other values are skill_editor session ids."`
	Encoding string `query:"encoding,omitempty" enum:"raw,base64" desc:"Optional content encoding for GET /remote-fs/content."`
}

type remoteFSTaskQueryParams struct {
	UserID string `query:"user_id" required:"true" desc:"Required. Target user id used to resolve skills owned by the user."`
	TaskID string `query:"task_id" required:"true" desc:"Required mutation task id. Prefix review_ writes existing draft, org_ is skill organization, other values are skill_editor session ids."`
}

type remoteFSUserQueryParams struct {
	UserID string `query:"user_id" required:"true" desc:"Required. Target user id used to resolve skills owned by the user."`
}

type remoteFSDeleteQueryParams struct {
	UserID    string `query:"user_id" required:"true" desc:"Required. Target user id used to resolve skills owned by the user."`
	Path      string `query:"path" required:"true" desc:"RemoteFS path to delete."`
	TaskID    string `query:"task_id,omitempty" desc:"Required for package-internal delete; not required for confirmed permanent package purge."`
	Permanent bool   `query:"permanent,omitempty" desc:"Required true for package root physical purge."`
	Confirm   bool   `query:"confirm,omitempty" desc:"Required true for package root physical purge."`
}

type remoteFSDirOpenAPIRequest struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}

type remoteFSCopyOpenAPIRequest struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Overwrite bool   `json:"overwrite"`
}

type remoteFSMoveOpenAPIRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type remoteFSTrashOpenAPIRequest struct {
	Path string `json:"path"`
}

type remoteFSItemOpenAPIResponse struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Type     string `json:"type"`
	Size     int64  `json:"size,omitempty"`
	Mime     string `json:"mime,omitempty"`
	FileType string `json:"file_type,omitempty"`
	Binary   bool   `json:"binary,omitempty"`
}

type remoteFSListOpenAPIResponse struct {
	Items []remoteFSItemOpenAPIResponse `json:"items"`
}

type remoteFSInfoOpenAPIResponse struct {
	Path     string `json:"path"`
	Type     string `json:"type"`
	Size     int64  `json:"size,omitempty"`
	Mime     string `json:"mime,omitempty"`
	FileType string `json:"file_type,omitempty"`
	Binary   bool   `json:"binary,omitempty"`
}

type remoteFSExistsOpenAPIResponse struct {
	Exists bool `json:"exists"`
}

type remoteFSBase64ContentOpenAPIResponse struct {
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

type okOpenAPIResponse struct {
	OK bool `json:"ok"`
}

type marketItemPathParams struct {
	MarketItemID string `path:"market_item_id"`
}

type marketInstallOpenAPIRequest struct {
	MarketItemID string `json:"market_item_id,omitempty"`
}

type marketInstallOpenAPIResponse struct {
	SkillID string `json:"skill_id"`
}

type marketPublishOpenAPIRequest struct {
	Name     string                    `json:"name"`
	Category string                    `json:"category"`
	Source   skillSourceOpenAPIRequest `json:"source"`
}

type marketPublishOpenAPIResponse struct {
	MarketItemID  string `json:"market_item_id"`
	SourceSkillID string `json:"source_skill_id"`
}

type marketEditOpenAPIRequest struct {
	Name        *string                    `json:"name,omitempty"`
	Category    *string                    `json:"category,omitempty"`
	Description *string                    `json:"description,omitempty"`
	Source      *skillSourceOpenAPIRequest `json:"source,omitempty"`
	VersionNote *string                    `json:"version_note,omitempty"`
}

type marketItemOpenAPIResponse struct {
	ID            string                      `json:"id,omitempty"`
	MarketItemID  string                      `json:"market_item_id"`
	SourceSkillID string                      `json:"source_skill_id,omitempty"`
	Status        string                      `json:"status,omitempty"`
	Icon          string                      `json:"icon,omitempty"`
	SortOrder     int                         `json:"sort_order,omitempty"`
	VersionNote   string                      `json:"version_note,omitempty"`
	PublishedAt   string                      `json:"published_at,omitempty"`
	CreatedAt     string                      `json:"created_at,omitempty"`
	UpdatedAt     string                      `json:"updated_at,omitempty"`
	Source        *skillDetailOpenAPIResponse `json:"source,omitempty"`
}

type marketListOpenAPIResponse struct {
	Items    []marketItemOpenAPIResponse `json:"items"`
	Page     int32                       `json:"page"`
	PageSize int32                       `json:"page_size"`
	Total    int32                       `json:"total"`
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
	ShareItemID       string  `json:"share_item_id"`
	ShareTaskID       string  `json:"share_task_id"`
	Status            string  `json:"status"`
	SourceUserID      string  `json:"source_user_id"`
	SourceUserName    string  `json:"source_user_name"`
	TargetUserID      string  `json:"target_user_id"`
	TargetUserName    string  `json:"target_user_name"`
	SourceSkillID     string  `json:"source_skill_id"`
	SourceCategory    string  `json:"source_category"`
	Message           string  `json:"message"`
	AcceptedAt        *string `json:"accepted_at,omitempty"`
	RejectedAt        *string `json:"rejected_at,omitempty"`
	TargetRootSkillID string  `json:"target_root_skill_id,omitempty"`
	ErrorMessage      string  `json:"error_message,omitempty"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
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

type memoryUpsertOpenAPIRequest struct {
	Content string `json:"content,omitempty"`
	AutoEvo *bool  `json:"auto_evo,omitempty"`
}

type managedStateUpsertOpenAPIRequest struct {
	Content       string `json:"content,omitempty"`
	AgentPersona  string `json:"agent_persona,omitempty"`
	PreferredName string `json:"preferred_name,omitempty"`
	ResponseStyle string `json:"response_style,omitempty"`
	AutoEvo       *bool  `json:"auto_evo,omitempty"`
}

type managedStateOpenAPIResponse struct {
	ResourceID             string                              `json:"resource_id"`
	ResourceType           string                              `json:"resource_type"`
	Title                  string                              `json:"title"`
	Content                string                              `json:"content"`
	AgentPersona           *string                             `json:"agent_persona,omitempty"`
	PreferredName          *string                             `json:"preferred_name,omitempty"`
	ResponseStyle          *string                             `json:"response_style,omitempty"`
	ContentSummary         string                              `json:"content_summary"`
	Version                int64                               `json:"version"`
	LatestVersionChange    *latestVersionChangeOpenAPIResponse `json:"latest_version_change"`
	HasPendingReviewResult bool                                `json:"has_pending_review_result"`
	ReviewStatus           string                              `json:"review_status"`
	AutoEvo                bool                                `json:"auto_evo"`
	AutoEvoApplyStatus     string                              `json:"auto_evo_apply_status"`
	AutoEvoGeneration      int64                               `json:"auto_evo_generation"`
	AutoEvoError           string                              `json:"auto_evo_error"`
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

type userUIPreferencesPatchOpenAPIRequest struct {
	ChatPreferenceNoticeDismissed *bool `json:"chat_preference_notice_dismissed,omitempty"`
	DeveloperModeActive           *bool `json:"developer_mode_active,omitempty"`
}

type userUIPreferencesOpenAPIResponse struct {
	ChatPreferenceNoticeDismissed bool   `json:"chat_preference_notice_dismissed"`
	DeveloperModeActive           bool   `json:"developer_mode_active"`
	UserPreferenceConfigured      bool   `json:"user_preference_configured"`
	UpdatedAt                     string `json:"updated_at"`
}

type localFSChatSettingOpenAPIRequest struct {
	Enabled bool `json:"enabled"`
}

type localFSChatSettingOpenAPIResponse struct {
	Enabled bool `json:"enabled"`
}

type systemGenerateOpenAPIResponse struct {
	DraftStatus        string `json:"draft_status"`
	DraftSourceVersion int64  `json:"draft_source_version"`
	DraftContent       string `json:"draft_content"`
}

type systemDraftPreviewOpenAPIResponse struct {
	ReviewResultID     string `json:"review_result_id"`
	ReviewStatus       string `json:"review_status"`
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

type internalSkillCreateOpenAPIRequest struct {
	SessionID string `json:"session_id"`
	Category  string `json:"category"`
	SkillName string `json:"skill_name"`
	Content   string `json:"content"`
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
	rawResp := func(description string) openAPIResponse {
		return openAPIResponse{Description: description, ContentType: "application/octet-stream", Schema: schemaSource{Inline: map[string]any{"type": "string", "format": "binary"}}}
	}
	refResp := func(description, name string) openAPIResponse {
		return openAPIResponse{Description: description, ContentType: "application/json", Schema: schemaSource{Ref: name}}
	}
	evoObjectSchema := map[string]any{
		"type":                 "object",
		"additionalProperties": true,
	}
	evoJSONBody := func(required bool) *openAPIBody {
		return &openAPIBody{Required: required, ContentType: "application/json", Schema: schemaSource{Inline: evoObjectSchema}}
	}
	evoJSONResp := func(description string) openAPIResponse {
		return openAPIResponse{Description: description, ContentType: "application/json", Schema: schemaSource{Inline: evoObjectSchema}}
	}
	evoStreamResp := openAPIResponse{
		Description: "Evo event stream",
		ContentType: "text/event-stream",
		Schema: schemaSource{Inline: map[string]any{
			"type": "string",
		}},
	}
	evoDownloadResp := openAPIResponse{
		Description: "Evo download",
		ContentType: "application/octet-stream",
		Schema: schemaSource{Inline: map[string]any{
			"type":   "string",
			"format": "binary",
		}},
	}
	evoGateContentResp := openAPIResponse{
		Description: "Evo gate content",
		ContentType: "application/json",
		Schema: schemaSource{Inline: map[string]any{
			"type":                 "object",
			"additionalProperties": true,
		}},
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
			Method:    "GET",
			Path:      "/data-sources/local-fs-chat-setting",
			Summary:   "Get local filesystem chat setting",
			Tags:      []string{"data-sources"},
			Responses: map[int]openAPIResponse{200: resp("Local filesystem chat setting", localFSChatSettingOpenAPIResponse{})},
		},
		{
			Method:      "PUT",
			Path:        "/data-sources/local-fs-chat-setting",
			Summary:     "Update local filesystem chat setting",
			Tags:        []string{"data-sources"},
			RequestBody: jsonBodyOf(localFSChatSettingOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Updated local filesystem chat setting", localFSChatSettingOpenAPIResponse{})},
		},
		{
			Method:    "GET",
			Path:      "/data-sources/database-connections",
			Summary:   "List database connections",
			Tags:      []string{"data-sources"},
			Responses: map[int]openAPIResponse{200: resp("Database connection list", datasource.ListDatabaseConnectionsResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/data-sources/database-connections",
			Summary:     "Create database connection",
			Tags:        []string{"data-sources"},
			RequestBody: jsonBodyOf(datasource.DatabaseConnectionRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created database connection", datasource.DatabaseConnectionResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/data-sources/database-connections/{connection}",
			Summary:    "Get database connection",
			Tags:       []string{"data-sources"},
			PathParams: databaseConnectionPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Database connection", datasource.DatabaseConnectionResponse{})},
		},
		{
			Method:      "PATCH",
			Path:        "/data-sources/database-connections/{connection}",
			Summary:     "Update database connection",
			Tags:        []string{"data-sources"},
			PathParams:  databaseConnectionPathParams{},
			RequestBody: jsonBodyOf(datasource.UpdateDatabaseConnectionRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Updated database connection", datasource.DatabaseConnectionResponse{})},
		},
		{
			Method:     "DELETE",
			Path:       "/data-sources/database-connections/{connection}",
			Summary:    "Delete database connection",
			Tags:       []string{"data-sources"},
			PathParams: databaseConnectionPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Deleted database connection", deleteDatabaseConnectionOpenAPIResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/data-sources/database-connections/{connection}:check",
			Summary:    "Check database connection",
			Tags:       []string{"data-sources"},
			PathParams: databaseConnectionPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Database connection check result", datasource.CheckDatabaseConnectionResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/data-sources/database-connections/{connection}:secret",
			Summary:    "Get database connection secret",
			Tags:       []string{"data-sources"},
			PathParams: databaseConnectionPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Database connection secret", datasource.DatabaseConnectionSecretResponse{})},
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
			Method:     "GET",
			Path:       "/eval-sets/{eval_set_id}/question-types",
			Summary:    "List eval set question types",
			Tags:       []string{"eval-set-items"},
			PathParams: evalset.EvalSetPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Question type options", evalset.QuestionTypeOptionsResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/eval-sets/{eval_set_id}/items:invalidReferences",
			Summary:     "List eval set items with invalid references",
			Tags:        []string{"eval-set-items"},
			PathParams:  evalset.EvalSetPathParams{},
			QueryParams: evalset.ListEvalSetItemsQuery{},
			Responses:   map[int]openAPIResponse{200: resp("Invalid reference eval set item list", evalset.ListEvalSetItemsResponse{})},
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
			Path:        "/system-query/documents:aggregate",
			Summary:     "Aggregate documents",
			Tags:        []string{"documents"},
			RequestBody: jsonBodyOf(doc.AggregateDocumentsRequest{}, false),
			Responses:   map[int]openAPIResponse{200: resp("Document aggregate results", doc.AggregateDocumentsResponse{})},
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
			Path:        "/evolution/tasks",
			Summary:     "List resource update tasks",
			Description: "Lists background resource update tasks for the current user.",
			Tags:        []string{"evolution"},
			QueryParams: resourceUpdateTaskListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Resource update task list", resourceUpdateTaskListOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/evolution/tasks/{task_id}",
			Summary:     "Get resource update task",
			Description: "Gets one background resource update task for the current user.",
			Tags:        []string{"evolution"},
			PathParams:  resourceUpdateTaskPathParams{},
			Responses:   map[int]openAPIResponse{200: resp("Resource update task", resourceUpdateTaskOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skill-review:summary",
			Summary:     "Get skill review summary",
			Description: "Returns the current review window and depositable conversation count for manual skill review.",
			Tags:        []string{"skill-review"},
			Responses:   map[int]openAPIResponse{200: resp("Skill review summary", skillReviewSummaryOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skill-review:run",
			Summary:     "Run manual skill review",
			Description: "Creates a manual skill review task for the current review window when at least one conversation is depositable.",
			Tags:        []string{"skill-review"},
			Responses:   map[int]openAPIResponse{200: resp("Manual skill review task", skillReviewRunOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skill-review/tasks",
			Summary:     "List skill review tasks",
			Description: "Lists manual skill review tasks for the current user using the algorithm run status when available.",
			Tags:        []string{"skill-review"},
			QueryParams: skillReviewTaskListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Manual skill review task list", skillReviewTaskListOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skill-review-results",
			Summary:     "List skill review results",
			Description: "Lists skill draft review results for the current user.",
			Tags:        []string{"skill-review-results"},
			QueryParams: skillReviewResultListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Skill review result list", skillReviewResultListOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skill-review-results/{review_result_id}",
			Summary:     "Get skill review result",
			Description: "Gets one skill draft review result for the current user.",
			Tags:        []string{"skill-review-results"},
			PathParams:  reviewResultPathParams{},
			Responses:   map[int]openAPIResponse{200: resp("Skill review result", skillReviewResultOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skill-review-results/{review_result_id}:accept",
			Summary:     "Accept skill review result",
			Description: "Synchronously accepts a pending skill draft review result.",
			Tags:        []string{"skill-review-results"},
			PathParams:  reviewResultPathParams{},
			Responses:   map[int]openAPIResponse{200: resp("Accepted skill review result", skillReviewResultOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skill-review-results/{review_result_id}:reject",
			Summary:     "Reject skill review result",
			Description: "Synchronously rejects a pending skill draft review result.",
			Tags:        []string{"skill-review-results"},
			PathParams:  reviewResultPathParams{},
			Responses:   map[int]openAPIResponse{200: resp("Rejected skill review result", skillReviewResultOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/memory-review-results",
			Summary:     "List memory review results",
			Description: "Lists memory and user preference draft review results for the current user.",
			Tags:        []string{"memory-review-results"},
			QueryParams: memoryReviewResultListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Memory review result list", memoryReviewResultListOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/memory-review-results/{review_result_id}",
			Summary:     "Get memory review result",
			Description: "Gets one memory or user preference draft review result for the current user.",
			Tags:        []string{"memory-review-results"},
			PathParams:  reviewResultPathParams{},
			Responses:   map[int]openAPIResponse{200: resp("Memory review result", memoryReviewResultOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/memory-review-results/{review_result_id}:accept",
			Summary:     "Accept memory review result",
			Description: "Synchronously accepts a pending memory or user preference draft review result.",
			Tags:        []string{"memory-review-results"},
			PathParams:  reviewResultPathParams{},
			Responses:   map[int]openAPIResponse{200: resp("Accepted memory review result", memoryReviewResultOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/memory-review-results/{review_result_id}:reject",
			Summary:     "Reject memory review result",
			Description: "Synchronously rejects a pending memory or user preference draft review result.",
			Tags:        []string{"memory-review-results"},
			PathParams:  reviewResultPathParams{},
			Responses:   map[int]openAPIResponse{200: resp("Rejected memory review result", memoryReviewResultOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/resource-versions",
			Summary:     "List resource versions",
			Description: "Lists content version history for skills, memory, and user preferences for the current user.",
			Tags:        []string{"resource-versions"},
			QueryParams: resourceVersionListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Resource version list", resourceVersionListOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/resource-versions/{version_id}",
			Summary:     "Get resource version",
			Description: "Gets one content version history entry for the current user.",
			Tags:        []string{"resource-versions"},
			PathParams:  resourceVersionPathParams{},
			Responses:   map[int]openAPIResponse{200: resp("Resource version", resourceVersionOpenAPIResponse{})},
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
			Method:    "GET",
			Path:      "/skills/tags",
			Summary:   "List skill tags",
			Tags:      []string{"skills"},
			Responses: map[int]openAPIResponse{200: resp("Skill tag list", skillTagsOpenAPIResponse{})},
		},
		{
			Method:    "GET",
			Path:      "/skills/categories",
			Summary:   "List skill categories",
			Tags:      []string{"skills"},
			Responses: map[int]openAPIResponse{200: resp("Skill category list", skillCategoriesOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skill_organize",
			Summary:     "Submit skill organize task",
			Description: "Submits a skill organize task for current user's SkillV2 files. The task runs asynchronously in the algorithm service.",
			Tags:        []string{"skills"},
			RequestBody: jsonBodyOf(skillOrganizeOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Skill organize task accepted", skillOrganizeOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skills",
			Summary:     "Create directory skill",
			Description: "Creates one directory-based skill from an uploaded ZIP or URL. The package must contain SKILL.md; description is product metadata and is not written into SKILL.md front matter.",
			Tags:        []string{"skills"},
			RequestBody: jsonBodyOf(skillCreateManagedOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created skill", skillWriteOpenAPIResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/builtin-skills/{builtin_skill_uid}:enable",
			Summary:    "Enable builtin directory skill",
			Tags:       []string{"skills"},
			PathParams: builtinSkillPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Enabled builtin skill", skillDetailOpenAPIResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/skills/{skill_id}",
			Summary:    "Get directory skill details",
			Tags:       []string{"skills"},
			PathParams: skillPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Skill details", skillDetailOpenAPIResponse{})},
		},
		{
			Method:      "PATCH",
			Path:        "/skills/{skill_id}",
			Summary:     "Update directory skill",
			Description: "Partially updates skill metadata. When source is present, the whole directory is replaced from an uploaded ZIP or URL after draft conflict checks.",
			Tags:        []string{"skills"},
			PathParams:  skillPathParams{},
			RequestBody: jsonBodyOf(skillUpdateManagedOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Updated skill", skillWriteOpenAPIResponse{})},
		},
		{
			Method:     "DELETE",
			Path:       "/skills/{skill_id}",
			Summary:    "Delete directory skill",
			Tags:       []string{"skills"},
			PathParams: skillPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Deleted skill", skillDeleteOpenAPIResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/skills/{skill_id}/tree",
			Summary:    "Get skill tree",
			Tags:       []string{"skills"},
			PathParams: skillPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Skill tree", skillTreeNodeOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skills/{skill_id}/file",
			Summary:     "Read skill file",
			Tags:        []string{"skills"},
			PathParams:  skillPathParams{},
			QueryParams: skillFileQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Skill file", skillFileOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skills/{skill_id}/fs/list",
			Summary:     "List skill directory entries",
			Tags:        []string{"skill-fs"},
			PathParams:  skillPathParams{},
			QueryParams: skillFSQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Skill directory entries", skillFSListOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skills/{skill_id}/fs/info",
			Summary:     "Get skill path info",
			Tags:        []string{"skill-fs"},
			PathParams:  skillPathParams{},
			QueryParams: skillFileQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Skill path info", skillTreeNodeOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skills/{skill_id}/fs/exists",
			Summary:     "Check skill path exists",
			Tags:        []string{"skill-fs"},
			PathParams:  skillPathParams{},
			QueryParams: skillFileQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Skill path exists", skillExistsOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skills/{skill_id}/fs/content",
			Summary:     "Read skill file content",
			Tags:        []string{"skill-fs"},
			PathParams:  skillPathParams{},
			QueryParams: skillFileQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Skill file content", skillFileOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skills/{skill_id}/fs/download",
			Summary:     "Download skill file",
			Tags:        []string{"skill-fs"},
			PathParams:  skillPathParams{},
			QueryParams: skillFileQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Skill file download", skillFileOpenAPIResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/skills/{skill_id}/draft/exists",
			Summary:    "Check skill draft exists",
			Tags:       []string{"skill-drafts"},
			PathParams: skillPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Skill draft state", skillDraftStateOpenAPIResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/skills/{skill_id}/draft/status",
			Summary:    "Get skill draft status",
			Tags:       []string{"skill-drafts"},
			PathParams: skillPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Skill draft status", skillDraftStatusOpenAPIResponse{})},
		},
		{
			Method:      "PUT",
			Path:        "/skills/{skill_id}/draft/fs/text",
			Summary:     "Write text file to skill draft",
			Tags:        []string{"skill-drafts"},
			PathParams:  skillPathParams{},
			RequestBody: jsonBodyOf(skillDraftWriteTextOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Skill draft mutation", skillDraftMutationOpenAPIResponse{})},
		},
		{
			Method:      "PUT",
			Path:        "/skills/{skill_id}/draft/fs/upload",
			Summary:     "Write uploaded file to skill draft",
			Tags:        []string{"skill-drafts"},
			PathParams:  skillPathParams{},
			RequestBody: jsonBodyOf(skillDraftUploadOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Skill draft mutation", skillDraftMutationOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skills/{skill_id}/draft/fs/dir",
			Summary:     "Create directory in skill draft",
			Tags:        []string{"skill-drafts"},
			PathParams:  skillPathParams{},
			RequestBody: jsonBodyOf(skillDraftMkdirOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Skill draft mutation", skillDraftMutationOpenAPIResponse{})},
		},
		{
			Method:      "DELETE",
			Path:        "/skills/{skill_id}/draft/fs/path",
			Summary:     "Delete path from skill draft",
			Tags:        []string{"skill-drafts"},
			PathParams:  skillPathParams{},
			QueryParams: skillFSQueryParams{},
			RequestBody: jsonBodyOf(skillDraftDeleteOpenAPIRequest{}, false),
			Responses:   map[int]openAPIResponse{200: resp("Skill draft mutation", skillDraftMutationOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skills/{skill_id}/draft/fs/move",
			Summary:     "Move path in skill draft",
			Tags:        []string{"skill-drafts"},
			PathParams:  skillPathParams{},
			RequestBody: jsonBodyOf(skillDraftMoveOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Skill draft mutation", skillDraftMutationOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skills/{skill_id}/commit",
			Summary:     "Commit skill draft",
			Tags:        []string{"skill-revisions"},
			PathParams:  skillPathParams{},
			RequestBody: jsonBodyOf(skillCommitOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Committed skill draft", skillCommitOpenAPIResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/skills/{skill_id}/revisions",
			Summary:    "List skill revisions",
			Tags:       []string{"skill-revisions"},
			PathParams: skillPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Skill revisions", skillRevisionListOpenAPIResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/skills/{skill_id}/revisions/{revision_id}",
			Summary:    "Get skill revision",
			Tags:       []string{"skill-revisions"},
			PathParams: skillRevisionPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Skill revision", skillRevisionOpenAPIResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/skills/{skill_id}/revisions/{revision_id}/tree",
			Summary:    "Get skill revision tree",
			Tags:       []string{"skill-revisions"},
			PathParams: skillRevisionPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Skill revision tree", skillTreeNodeOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skills/{skill_id}/revisions/{revision_id}/file",
			Summary:     "Read skill revision file",
			Tags:        []string{"skill-revisions"},
			PathParams:  skillRevisionPathParams{},
			QueryParams: skillFileQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Skill revision file", skillFileOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skills/{skill_id}/rollback/preview",
			Summary:     "Preview skill rollback",
			Tags:        []string{"skill-revisions"},
			PathParams:  skillPathParams{},
			RequestBody: jsonBodyOf(skillRollbackOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Skill rollback preview", skillRollbackPreviewOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skills/{skill_id}/rollback",
			Summary:     "Rollback skill",
			Tags:        []string{"skill-revisions"},
			PathParams:  skillPathParams{},
			RequestBody: jsonBodyOf(skillRollbackOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Rolled back skill", skillRollbackOpenAPIResponse{})},
		},
		{
			Method:     "DELETE",
			Path:       "/skills/{skill_id}/revisions/{revision_id}",
			Summary:    "Delete skill revision",
			Tags:       []string{"skill-revisions"},
			PathParams: skillRevisionPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Deleted skill revision", skillDeleteOpenAPIResponse{})},
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
			Path:        "/skill-diff/tree",
			Summary:     "Compare skill trees",
			Tags:        []string{"skill-diff"},
			RequestBody: jsonBodyOf(diffOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Skill tree diff", diffTreeOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skill-diff/file",
			Summary:     "Compare skill file",
			Tags:        []string{"skill-diff"},
			RequestBody: jsonBodyOf(diffOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Skill file diff", diffFileOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/skill-market",
			Summary:     "List published market skills",
			Tags:        []string{"skill-market"},
			QueryParams: skillListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Market skill list", marketListOpenAPIResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/skill-market/{market_item_id}",
			Summary:    "Get market skill details",
			Tags:       []string{"skill-market"},
			PathParams: marketItemPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Market skill item", marketItemOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skill-market:install",
			Summary:     "Install skill from market",
			Tags:        []string{"skill-market"},
			RequestBody: jsonBodyOf(marketInstallOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Installed market skill", marketInstallOpenAPIResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/skill-market/{market_item_id}:install",
			Summary:    "Install skill from market item",
			Tags:       []string{"skill-market"},
			PathParams: marketItemPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Installed market skill", marketInstallOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/admin/skill-market",
			Summary:     "Publish market skill item",
			Tags:        []string{"skill-market"},
			RequestBody: jsonBodyOf(marketPublishOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Published market skill", marketPublishOpenAPIResponse{})},
		},
		{
			Method:      "PATCH",
			Path:        "/admin/skill-market/{market_item_id}",
			Summary:     "Edit market skill item",
			Tags:        []string{"skill-market"},
			PathParams:  marketItemPathParams{},
			RequestBody: jsonBodyOf(marketEditOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Edited market skill", marketItemOpenAPIResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/admin/skill-market/{market_item_id}:offline",
			Summary:    "Unpublish market skill item",
			Tags:       []string{"skill-market"},
			PathParams: marketItemPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Unpublished market skill", marketItemOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skill-market/admin/items",
			Summary:     "Publish market skill item",
			Tags:        []string{"skill-market"},
			RequestBody: jsonBodyOf(marketPublishOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Published market skill", marketPublishOpenAPIResponse{})},
		},
		{
			Method:      "PATCH",
			Path:        "/skill-market/admin/items/{market_item_id}",
			Summary:     "Edit market skill item",
			Tags:        []string{"skill-market"},
			PathParams:  marketItemPathParams{},
			RequestBody: jsonBodyOf(marketEditOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Edited market skill", marketItemOpenAPIResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/skill-market/admin/items/{market_item_id}:unpublish",
			Summary:    "Unpublish market skill item",
			Tags:       []string{"skill-market"},
			PathParams: marketItemPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Unpublished market skill", marketItemOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/skill/create",
			Summary:     "Create skill directly from internal request",
			Tags:        []string{"skill-evolution"},
			RequestBody: jsonBodyOf(internalSkillCreateOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created skill", skillWriteOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/remote-fs/list",
			Summary:     "List remote skill filesystem path",
			Description: "List skills root/category without task_id, or list package content with task-aware view selection.",
			Tags:        []string{"remote-fs"},
			QueryParams: remoteFSListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Remote filesystem list", remoteFSListOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/remote-fs/info",
			Summary:     "Get remote skill filesystem path info",
			Description: "Package paths require task_id so RemoteFS can choose publish view, current task draft view, or review draft view.",
			Tags:        []string{"remote-fs"},
			QueryParams: remoteFSQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Remote filesystem info", remoteFSInfoOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/remote-fs/exists",
			Summary:     "Check remote skill filesystem path exists",
			Description: "Package paths require task_id so RemoteFS can choose publish view, current task draft view, or review draft view.",
			Tags:        []string{"remote-fs"},
			QueryParams: remoteFSQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Remote filesystem exists", remoteFSExistsOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/remote-fs/content",
			Summary:     "Read remote skill filesystem content",
			Description: "Reads package content using task_id semantics: review_ sees existing draft, org_ and editor session ids see publish unless they own the current draft.",
			Tags:        []string{"remote-fs"},
			QueryParams: remoteFSQueryParams{},
			Responses:   map[int]openAPIResponse{200: rawResp("Remote filesystem raw content")},
		},
		{
			Method:      "PUT",
			Path:        "/remote-fs/content",
			Summary:     "Write remote skill filesystem content",
			Description: "Writes raw body bytes into the task draft. review_ may write an existing draft without changing draft ownership; org_ and editor session ids conflict with another task draft.",
			Tags:        []string{"remote-fs"},
			QueryParams: remoteFSQueryParams{},
			RequestBody: &openAPIBody{Required: true, ContentType: "application/octet-stream", Schema: schemaSource{Inline: map[string]any{"type": "string", "format": "binary"}}},
			Responses:   map[int]openAPIResponse{200: resp("Remote filesystem write result", okOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/remote-fs/dir",
			Summary:     "Create remote skill filesystem directory or empty package",
			Tags:        []string{"remote-fs"},
			QueryParams: remoteFSTaskQueryParams{},
			RequestBody: jsonBodyOf(remoteFSDirOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Remote filesystem mkdir result", okOpenAPIResponse{})},
		},
		{
			Method:      "DELETE",
			Path:        "/remote-fs/path",
			Summary:     "Delete remote skill filesystem path",
			Tags:        []string{"remote-fs"},
			QueryParams: remoteFSDeleteQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Remote filesystem delete result", okOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/remote-fs/copy",
			Summary:     "Copy remote skill filesystem path",
			Tags:        []string{"remote-fs"},
			QueryParams: remoteFSTaskQueryParams{},
			RequestBody: jsonBodyOf(remoteFSCopyOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Remote filesystem copy result", okOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/remote-fs/move",
			Summary:     "Move remote skill filesystem path",
			Tags:        []string{"remote-fs"},
			QueryParams: remoteFSTaskQueryParams{},
			RequestBody: jsonBodyOf(remoteFSMoveOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Remote filesystem move result", okOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/remote-fs/trash",
			Summary:     "Trash remote skill package",
			Tags:        []string{"remote-fs"},
			QueryParams: remoteFSUserQueryParams{},
			RequestBody: jsonBodyOf(remoteFSTrashOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Remote filesystem trash result", okOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/model_providers",
			Summary:     "List user model providers",
			Description: "Per-user model provider list. Missing catalog rows are synced from default_model_providers on each request. Query parameter category filters by provider category (default model when category and exclude_category are both omitted). Query parameter exclude_category excludes a category (e.g. exclude_category=model returns ocr and search providers). Query parameter keyword filters by provider name case-insensitively.",
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
			Method:    "GET",
			Path:      "/user/ui-preferences",
			Summary:   "Get current user's UI preferences",
			Tags:      []string{"user"},
			Responses: map[int]openAPIResponse{200: resp("Current user's UI preferences", userUIPreferencesOpenAPIResponse{})},
		},
		{
			Method:      "PATCH",
			Path:        "/user/ui-preferences",
			Summary:     "Partially update current user's UI preferences",
			Description: "Partial update. Every field inside the request body is optional; send only fields that should change.",
			Tags:        []string{"user"},
			RequestBody: jsonBodyOf(userUIPreferencesPatchOpenAPIRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Updated current user's UI preferences", userUIPreferencesOpenAPIResponse{})},
		},
		{
			Method:      "PUT",
			Path:        "/memory",
			Summary:     "Upsert managed memory",
			Tags:        []string{"memory"},
			RequestBody: jsonBodyOf(memoryUpsertOpenAPIRequest{}, true),
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
			Method:      "GET",
			Path:        "/tools",
			Summary:     "Tool list",
			Tags:        []string{"tools"},
			QueryParams: toolListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Tool list", toolListOpenAPIResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/tools/{tool_name}:disable",
			Summary:    "Disable tool",
			Tags:       []string{"tools"},
			PathParams: toolPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Tool disabled", toolStateOpenAPIResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/tools/{tool_name}:enable",
			Summary:    "Enable tool",
			Tags:       []string{"tools"},
			PathParams: toolPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Tool enabled", toolStateOpenAPIResponse{})},
		},
		{
			Method:      "GET",
			Path:        "/mcp_servers",
			Summary:     "List MCP servers",
			Tags:        []string{"mcp_servers"},
			QueryParams: mcpServerListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("MCP server list", mcp.ListServersResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/mcp_servers",
			Summary:     "Create MCP server",
			Tags:        []string{"mcp_servers"},
			RequestBody: jsonBodyOf(mcp.CreateServerRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Created MCP server", mcp.ServerResponse{})},
		},
		{
			Method:     "GET",
			Path:       "/mcp_servers/{id}",
			Summary:    "Get MCP server",
			Tags:       []string{"mcp_servers"},
			PathParams: mcpServerPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("MCP server", mcp.ServerResponse{})},
		},
		{
			Method:      "PATCH",
			Path:        "/mcp_servers/{id}",
			Summary:     "Update MCP server",
			Tags:        []string{"mcp_servers"},
			PathParams:  mcpServerPathParams{},
			RequestBody: jsonBodyOf(mcp.UpdateServerRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Updated MCP server", mcp.ServerResponse{})},
		},
		{
			Method:     "DELETE",
			Path:       "/mcp_servers/{id}",
			Summary:    "Delete MCP server",
			Tags:       []string{"mcp_servers"},
			PathParams: mcpServerPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Deleted MCP server", mcpDeleteServerOpenAPIResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/mcp_servers/{id}:check",
			Summary:    "Check MCP server",
			Tags:       []string{"mcp_servers"},
			PathParams: mcpServerPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("MCP server check result", mcp.CheckResponse{})},
		},
		{
			Method:     "POST",
			Path:       "/mcp_servers/{id}:discover",
			Summary:    "Discover MCP server tools",
			Tags:       []string{"mcp_servers"},
			PathParams: mcpServerPathParams{},
			Responses:  map[int]openAPIResponse{200: resp("Discovered MCP tools", mcp.DiscoverResponse{})},
		},
		{
			Method:      "PUT",
			Path:        "/mcp_servers/{id}/tools",
			Summary:     "Update MCP server tools",
			Tags:        []string{"mcp_servers"},
			PathParams:  mcpServerPathParams{},
			RequestBody: jsonBodyOf(mcp.UpdateToolsRequest{}, true),
			Responses:   map[int]openAPIResponse{200: resp("Updated MCP server tools", mcp.ServerResponse{})},
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
			Description: "List the current user's Core thread index entries. Core refreshes status from Evo when available.",
			Tags:        []string{"agent"},
			QueryParams: agentThreadListQueryParams{},
			Responses:   map[int]openAPIResponse{200: resp("Agent thread list", agentThreadListOpenAPIResponse{})},
		},
		{
			Method:      "POST",
			Path:        "/agent/threads",
			Summary:     "Create agent thread",
			Description: "Creates an Evo thread and stores only the local thread index and active-thread lock needed by Core.",
			Tags:        []string{"agent"},
			RequestBody: evoJSONBody(true),
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Created agent thread")},
		},
		{
			Method:      "GET",
			Path:        "/agent/threads/{thread_id}/events:stream",
			Summary:     "Stream agent thread events",
			Description: "Proxies Evo GET /threads/{thread_id}/events:stream.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadPathParams{},
			QueryParams: agentThreadEventsQueryParams{},
			Responses:   map[int]openAPIResponse{200: evoStreamResp},
		},
		{
			Method:      "GET",
			Path:        "/agent/threads/{thread_id}/event-trace:stream",
			Summary:     "Stream agent thread event trace",
			Description: "Proxies Evo GET /threads/{thread_id}/event-trace:stream.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadPathParams{},
			QueryParams: agentThreadEventTraceQueryParams{},
			Responses:   map[int]openAPIResponse{200: evoStreamResp},
		},
		{
			Method:      "GET",
			Path:        "/agent/threads/{thread_id}/steps",
			Summary:     "List agent thread steps",
			Description: "Proxies Evo GET /threads/{thread_id}/steps. Core does not read or write step detail rows for this endpoint.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadPathParams{},
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo thread steps")},
		},
		{
			Method:      "GET",
			Path:        "/agent/threads/{thread_id}/gates",
			Summary:     "List agent thread gates",
			Description: "Proxies Evo GET /threads/{thread_id}/gates.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadPathParams{},
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo gate list")},
		},
		{
			Method:      "GET",
			Path:        "/agent/threads/{thread_id}/gates/{step}/versions/{version}:download",
			Summary:     "Download agent thread gate version",
			Description: "Proxies Evo GET /threads/{thread_id}/gates/{step}/versions/{version}:download.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadGatePathParams{},
			QueryParams: struct {
				Format string `query:"format" enum:"json"`
			}{},
			Responses: map[int]openAPIResponse{200: evoDownloadResp},
		},
		{
			Method:      "GET",
			Path:        "/agent/threads/{thread_id}/gates/{step}/versions/{version}",
			Summary:     "Get agent thread gate version",
			Description: "Proxies Evo GET /threads/{thread_id}/gates/{step}/versions/{version}.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadGatePathParams{},
			Responses:   map[int]openAPIResponse{200: evoGateContentResp},
		},
		{
			Method:      "GET",
			Path:        "/agent/threads/{thread_id}/gates/eval/versions/{version}/bad-cases",
			Summary:     "List eval bad cases for a gate version",
			Description: "Proxies Evo GET /threads/{thread_id}/gates/eval/versions/{version}/bad-cases.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadGateVersionPathParams{},
			QueryParams: agentThreadEvalBadCasesQueryParams{},
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo eval bad case page")},
		},
		{
			Method:      "GET",
			Path:        "/agent/threads/{thread_id}/gates/abtest/versions/{version}/case-details",
			Summary:     "List AB test case details for a gate version",
			Description: "Proxies Evo GET /threads/{thread_id}/gates/abtest/versions/{version}/case-details.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadGateVersionPathParams{},
			QueryParams: agentThreadABTestCaseDetailsQueryParams{},
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo AB test case detail page")},
		},
		{
			Method:      "GET",
			Path:        "/agent/threads/{thread_id}/results/traces:compare",
			Summary:     "Compare agent traces",
			Description: "Proxies Evo GET /threads/{thread_id}/results/traces:compare.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadPathParams{},
			QueryParams: agentThreadTraceCompareQueryParams{},
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo trace comparison")},
		},
		{
			Method:      "GET",
			Path:        "/agent/threads/{thread_id}/results/traces/{trace_id}",
			Summary:     "Get agent trace detail",
			Description: "Proxies Evo GET /threads/{thread_id}/results/traces/{trace_id}.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadTracePathParams{},
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo trace detail")},
		},
		{
			Method:      "GET",
			Path:        "/agent/threads/{thread_id}",
			Summary:     "Get agent thread",
			Description: "Returns the current user's local thread index entry with status refreshed from Evo when available.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadPathParams{},
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Agent thread")},
		},
		{
			Method:      "DELETE",
			Path:        "/agent/threads/{thread_id}",
			Summary:     "Delete agent thread",
			Description: "Deletes the Evo thread when present and removes Core's local thread index and active-thread row.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadPathParams{},
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Deleted agent thread")},
		},
		{
			Method:      "GET",
			Path:        "/agent/threads/{thread_id}/messages",
			Summary:     "List agent thread messages",
			Description: "Proxies Evo GET /threads/{thread_id}/messages.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadPathParams{},
			QueryParams: struct {
				PageSize  int32  `query:"page_size"`
				PageToken string `query:"page_token"`
			}{},
			Responses: map[int]openAPIResponse{200: evoJSONResp("Evo thread messages")},
		},
		{
			Method:      "POST",
			Path:        "/agent/threads/{thread_id}/messages",
			Summary:     "Send agent thread message",
			Description: "Proxies Evo POST /threads/{thread_id}/messages.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadPathParams{},
			RequestBody: evoJSONBody(true),
			Responses:   map[int]openAPIResponse{200: evoStreamResp},
		},
		{
			Method:      "POST",
			Path:        "/agent/threads/{thread_id}/start",
			Summary:     "Start agent thread",
			Description: "Proxies Evo start and updates Core's local thread status and active-thread lock.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadPathParams{},
			RequestBody: evoJSONBody(false),
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo command response")},
		},
		{
			Method:      "POST",
			Path:        "/agent/threads/{thread_id}/pause",
			Summary:     "Pause agent thread",
			Description: "Proxies Evo pause and updates Core's local thread status.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadPathParams{},
			RequestBody: evoJSONBody(false),
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo command response")},
		},
		{
			Method:      "POST",
			Path:        "/agent/threads/{thread_id}/cancel",
			Summary:     "Cancel agent thread",
			Description: "Proxies Evo cancel and releases Core's active-thread lock for the thread.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadPathParams{},
			RequestBody: evoJSONBody(false),
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo command response")},
		},
		{
			Method:      "POST",
			Path:        "/agent/threads/{thread_id}/retry",
			Summary:     "Retry agent thread",
			Description: "Proxies Evo retry and updates Core's local thread status and active-thread lock.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadPathParams{},
			RequestBody: evoJSONBody(false),
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo command response")},
		},
		{
			Method:      "POST",
			Path:        "/agent/threads/{thread_id}/continue",
			Summary:     "Continue agent thread",
			Description: "Proxies Evo continue and updates Core's local thread status and active-thread lock.",
			Tags:        []string{"agent"},
			PathParams:  agentThreadPathParams{},
			RequestBody: evoJSONBody(false),
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo command response")},
		},
		{
			Method:      "GET",
			Path:        "/agent/candidates",
			Summary:     "List Evo candidates",
			Description: "Proxies Evo GET /candidates for a current-user thread. The thread_id query parameter is required by Core for ownership enforcement.",
			Tags:        []string{"agent"},
			QueryParams: agentCandidateListQueryParams{},
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo candidate list")},
		},
		{
			Method:      "GET",
			Path:        "/agent/candidates/{candidate_id:.*}",
			Summary:     "Get Evo candidate",
			Description: "Proxies Evo GET /candidates/{candidate_id} after validating the thread_id prefix belongs to the current user.",
			Tags:        []string{"agent"},
			PathParams:  agentCandidatePathParams{},
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo candidate")},
		},
		{
			Method:      "GET",
			Path:        "/agent/router/status",
			Summary:     "Get Evo router status",
			Description: "Proxies Evo GET /router/status.",
			Tags:        []string{"agent"},
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo router status")},
		},
		{
			Method:      "GET",
			Path:        "/agent/router/algorithms",
			Summary:     "List Evo router algorithms",
			Description: "Proxies Evo GET /router/algorithms.",
			Tags:        []string{"agent"},
			QueryParams: struct {
				ThreadID       string `query:"thread_id"`
				AlgorithmID    string `query:"algorithm_id"`
				Status         string `query:"status"`
				RouterAdminURL string `query:"router_admin_url"`
				RouterChatURL  string `query:"router_chat_url"`
			}{},
			Responses: map[int]openAPIResponse{200: evoJSONResp("Evo router algorithms")},
		},
		{
			Method:      "POST",
			Path:        "/agent/router/algorithms",
			Summary:     "Register Evo router algorithm",
			Description: "Proxies Evo POST /router/algorithms.",
			Tags:        []string{"agent"},
			RequestBody: evoJSONBody(true),
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo router algorithm")},
		},
		{
			Method:      "POST",
			Path:        "/agent/router/algorithms/{algorithm_id}:action",
			Summary:     "Run Evo router algorithm action",
			Description: "Proxies Evo POST /router/algorithms/{algorithm_id}:action.",
			Tags:        []string{"agent"},
			PathParams:  agentRouterAlgorithmPathParams{},
			RequestBody: evoJSONBody(true),
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo router algorithm action")},
		},
		{
			Method:      "GET",
			Path:        "/agent/router/ab-strategy",
			Summary:     "Get Evo router AB strategy",
			Description: "Proxies Evo GET /router/ab-strategy.",
			Tags:        []string{"agent"},
			QueryParams: struct {
				RouterAdminURL string `query:"router_admin_url"`
				RouterChatURL  string `query:"router_chat_url"`
			}{},
			Responses: map[int]openAPIResponse{200: evoJSONResp("Evo router AB strategy")},
		},
		{
			Method:      "PUT",
			Path:        "/agent/router/ab-strategy",
			Summary:     "Update Evo router AB strategy",
			Description: "Proxies Evo PUT /router/ab-strategy.",
			Tags:        []string{"agent"},
			RequestBody: evoJSONBody(true),
			Responses:   map[int]openAPIResponse{200: evoJSONResp("Evo router AB strategy")},
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
