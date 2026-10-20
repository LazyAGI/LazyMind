package capability

import (
	"context"
	"lazymind/core/learning"
	"slices"
	"strings"
)

type CreateKnowledgeRequest struct {
	Name            string                   `json:"name" jsonschema:"1-100 characters: Chinese U+4E00-U+9FA5, A-Z/a-z, digits, hyphen, underscore or dot; no whitespace. Reserved names/conflicts fail."`
	Description     string                   `json:"description,omitempty" jsonschema:"Max 300 characters; default empty."`
	Scene           string                   `json:"scene,omitempty" jsonschema:"general, academic_papers, chinese_modern, chinese_classical, english_learning, legal, technical, historical, custom; paper_collection aliases academic_papers. Omitted/empty: general."`
	ProcessingLevel string                   `json:"processing_level,omitempty" jsonschema:"stored (original), parsed (text), chunked (chunks), indexed (vectors). Omitted/empty: stored if parsing is disabled; indexed if required embedding models are ready; otherwise chunked. Unavailable explicit levels fail."`
	Tags            []string                 `json:"tags" jsonschema:"After trimming, dropping blanks and deduplicating: 1-10 tags, each max 20 characters."`
	ProfileID       string                   `json:"profile_id,omitempty" jsonschema:"Known accessible custom preset ID; requires scene=custom. Omitted/empty: no preset; custom then requires explicit capabilities."`
	AlgoID          string                   `json:"algo_id,omitempty" jsonschema:"Omitted/blank: general_algo. Otherwise a known deployment algorithm ID; unavailable IDs fail."`
	Capabilities    []learning.CapabilityRef `json:"capabilities,omitempty" jsonschema:"Omitted/null: scene/preset defaults. A nonempty array replaces defaults; keys must be unique. custom without profile_id requires this."`
}
type CreateFoldersRequest struct {
	KnowledgeBaseID string   `json:"knowledge_base_id" jsonschema:"Core library ID with upload permission."`
	Folders         []string `json:"folders" jsonschema:"Nonempty relative paths, e.g. papers/2026. Slash separates segments; no leading slash, backslash, colon, empty/dot/dot-dot segments. Each segment: max 512 bytes, no surrounding whitespace. Input and expanded counts obey deployment limits."`
	ParentID        string   `json:"parent_id,omitempty" jsonschema:"Folder ID in this library; omitted/empty: root."`
}
type ResourceMetadataChanges struct {
	Name            *string                   `json:"name,omitempty" jsonschema:"Trimmed nonempty name, max 512 bytes; no slash/backslash. Name conflicts fail."`
	Description     *string                   `json:"description,omitempty" jsonschema:"Max 300 characters; empty clears."`
	Scene           *string                   `json:"scene,omitempty" jsonschema:"general, academic_papers, chinese_modern, chinese_classical, english_learning, legal, technical, historical, custom; paper_collection aliases academic_papers. Empty string is invalid. Changing scene clears the old preset and reapplies defaults unless profile_id/capabilities override."`
	Tags            *[]string                 `json:"tags,omitempty" jsonschema:"Trim/deduplicate nonblank tags; keep at most 10. [] clears with replace."`
	ProfileID       *string                   `json:"profile_id,omitempty" jsonschema:"Known accessible preset ID; nonempty requires effective scene=custom. Empty clears preset. Reapplies defaults unless capabilities override."`
	AlgoID          *string                   `json:"algo_id,omitempty" jsonschema:"Blank: general_algo; otherwise a known available deployment algorithm ID."`
	ProcessingLevel *string                   `json:"processing_level,omitempty" jsonschema:"stored (original), parsed (text), chunked (chunks), indexed (vectors). Empty string is invalid; unavailable levels fail."`
	Capabilities    *[]learning.CapabilityRef `json:"capabilities,omitempty" jsonschema:"Nonempty unique-key references replacing defaults. Omission retains configuration unless scene/profile changes; editing only this preserves scene."`
	TagMode         string                    `json:"tag_mode,omitempty" jsonschema:"replace (default, including empty) or append; applies only with tags."`
	Recursive       bool                      `json:"recursive,omitempty" jsonschema:"Default false; true required for folder tag updates. Descendant documents only; no rename in the same item."`
}
type ResourceMetadataItem struct {
	Target  ResourceRef             `json:"target" jsonschema:"Existing resource to update."`
	Changes ResourceMetadataChanges `json:"changes" jsonschema:"Kind-specific changes; see items for omission rules."`
}
type UpdateMetadataRequest struct {
	Items []ResourceMetadataItem `json:"items" jsonschema:"Nonempty batch within deployment limits; at least one actual change per item. Omit or null to keep values, except scene/preset changes reapply defaults. Reading configuration changes require ownership. Only fields allowed for each resource kind may be changed."`
}
type MoveResourcesRequest struct {
	Sources     []ResourceRef `json:"sources" jsonschema:"Nonempty folder/document references with source write permission. Includes descendants; overlapping roots are normalized. Deployment resource limits apply."`
	Destination ResourceRef   `json:"destination" jsonschema:"Folder or knowledge_base (root) with upload permission. No self/descendant destination or name conflicts. Cross-library moves require calibrated byte/resource/time budgets."`
}
type DeleteResourcesRequest struct {
	Targets []ResourceRef `json:"targets" jsonschema:"Nonempty resource references with write permission. Libraries/folders include descendants; overlaps are normalized. Deployment resource limits apply."`
}
type ResourceOperationSummary struct {
	Unconfirmed int `json:"unconfirmed"`
	Total       int `json:"total"`
	Succeeded   int `json:"succeeded"`
	Failed      int `json:"failed"`
	Skipped     int `json:"skipped"`
}
type ResourceOperationItem struct {
	ResourceRef      ResourceRef              `json:"resource_ref"`
	PreviousRef      *ResourceRef             `json:"previous_ref,omitempty"`
	Name             string                   `json:"name"`
	ParentID         string                   `json:"parent_id"`
	ResourceStatus   string                   `json:"resource_status"`
	ProcessingStatus string                   `json:"processing_status,omitempty"`
	CleanupStatus    string                   `json:"cleanup_status,omitempty"`
	DocumentCount    int                      `json:"document_count,omitempty"`
	FolderCount      int                      `json:"folder_count,omitempty"`
	IDMapping        map[string]string        `json:"id_mapping,omitempty"`
	Before           map[string]any           `json:"before,omitempty"`
	After            map[string]any           `json:"after,omitempty"`
	EntryURL         string                   `json:"-"`
	Error            *KnowledgeOperationError `json:"error,omitempty"`
}
type ResourceOperationResult struct {
	Action  string                   `json:"action"`
	Status  string                   `json:"status"`
	Summary ResourceOperationSummary `json:"summary"`
	Items   []ResourceOperationItem  `json:"items"`
}
type KnowledgeOperationError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *KnowledgeOperationError) Error() string { return e.Message }

type UploadKnowledgeDocumentInput struct {
	KnowledgeBaseID string                `json:"knowledge_base_id" jsonschema:"Core library ID with upload permission."`
	ParentID        string                `json:"parent_id,omitempty" jsonschema:"Folder ID in this library; omitted/empty: root."`
	Files           []KnowledgeUploadItem `json:"files" jsonschema:"1-20 nonempty regular files, max 500 MiB each and 1 GiB total; deployment resource limits also apply."`
}
type KnowledgeUploadItem struct {
	Path        string   `json:"path" jsonschema:"Absolute shared-filesystem path readable by the MCP server; not URL/file_ref/attachment/artifact ID. Extensions: pdf, docx, doc, ppt, pptx, pptm, jpg, jpeg, png, gif, bmp, webp, tiff, tif, ipynb, epub, md, mbox, csv, xls, xlsx, mp3, mp4, txt, xml, json, jsonl, yaml, yml, html, htm, py. Content must match format; parser availability varies."`
	DisplayName string   `json:"display_name,omitempty" jsonschema:"Omitted/empty: source basename. Trimmed plain filename; not dot/dot-dot; no slash, backslash or NUL. Name conflicts fail."`
	Tags        []string `json:"tags,omitempty" jsonschema:"Default []; trim/deduplicate nonblank values, keep at most 10. Library tags are not inherited or changed."`
}
type AggregateKnowledgeDocumentsInput struct {
	ResourceIDs     []string `json:"resource_ids,omitempty" jsonschema:"Document IDs within selected libraries; omitted/[]: unrestricted."`
	ParentID        *string  `json:"parent_id,omitempty" jsonschema:"Omitted/null: whole library; empty: root; otherwise folder ID. Supplying this requires one library."`
	Recursive       *bool    `json:"recursive,omitempty" jsonschema:"Default true: all descendants of parent_id; false: direct children. Ignored without parent_id."`
	Match           string   `json:"match,omitempty" jsonschema:"contains (default): case-insensitive literal substring; exact: stored name/path equality."`
	Name            string   `json:"name,omitempty" jsonschema:"Display-name filter using match; omitted/empty: unrestricted."`
	Path            string   `json:"path,omitempty" jsonschema:"Upload relative_path filter using match, not host/logical-folder path; omitted/empty: unrestricted."`
	KnowledgeIDs    []string `json:"knowledge_ids" jsonschema:"1-20 readable Core library IDs, max 256 bytes each; no implicit all-library scan."`
	FileTypes       []string `json:"file_types,omitempty" jsonschema:"File-type filter, e.g. PDF, DOCX."`
	DocumentStages  []string `json:"document_stages,omitempty" jsonschema:"Stored processing-stage filter; pipeline-dependent values."`
	DataSourceTypes []string `json:"data_source_types,omitempty" jsonschema:"Stored source-type filter, e.g. LOCAL_FILE; deployment-dependent values."`
	Creators        []string `json:"creators,omitempty" jsonschema:"Creator values from document metadata."`
	Tags            []string `json:"tags,omitempty" jsonschema:"Every supplied tag must match; other metadata filters accept any supplied value."`
	GroupBy         []string `json:"group_by,omitempty" jsonschema:"Up to 3 distinct dimensions: dataset_id, dataset_display, file_type, document_stage, data_source_type, creator, tag. Omitted/[]: totals only. Multi-tag documents count in each tag group, once in totals; duplicate/unknown dimensions fail."`
}
type KnowledgeAggregateGroup struct {
	Key   map[string]string `json:"key"`
	Count int64             `json:"count"`
	Size  int64             `json:"size"`
}
type AggregateKnowledgeDocumentsResult struct {
	TotalCount int64                     `json:"total_count"`
	TotalSize  int64                     `json:"total_size"`
	Groups     []KnowledgeAggregateGroup `json:"groups"`
}
type KnowledgeManager interface {
	CreateKnowledge(context.Context, InvocationContext, CreateKnowledgeRequest) (ResourceOperationResult, error)
	CreateFolders(context.Context, InvocationContext, CreateFoldersRequest) (ResourceOperationResult, error)
	UpdateMetadata(context.Context, InvocationContext, UpdateMetadataRequest) (ResourceOperationResult, error)
	Move(context.Context, InvocationContext, MoveResourcesRequest) (ResourceOperationResult, error)
	Delete(context.Context, InvocationContext, DeleteResourcesRequest) (ResourceOperationResult, error)
	Upload(context.Context, InvocationContext, UploadKnowledgeDocumentInput) (ResourceOperationResult, error)
	Aggregate(context.Context, InvocationContext, AggregateKnowledgeDocumentsInput) (AggregateKnowledgeDocumentsResult, error)
}

func (s *Service) SupportsKnowledgeManagement() bool { return s != nil && s.management != nil }
func (s *Service) validateManagement(call InvocationContext, action string) error {
	if strings.TrimSpace(call.Principal.UserID) == "" {
		return NewError(Unauthenticated, action, "authenticated user required", false, nil)
	}
	if !call.Principal.Permissions.Has("document.write") && !call.Principal.Permissions.Has("*") {
		return NewError(PermissionDenied, action, "document.write permission required", false, nil)
	}
	if s.management == nil {
		return NewError(Unsupported, action, "knowledge management is not enabled", false, nil)
	}
	return nil
}
func (s *Service) CreateKnowledge(ctx context.Context, call InvocationContext, input CreateKnowledgeRequest) (ResourceOperationResult, error) {
	if err := s.validateManagement(call, "knowledge.create"); err != nil {
		return ResourceOperationResult{}, err
	}
	return s.management.CreateKnowledge(ctx, call, input)
}
func (s *Service) CreateFolders(ctx context.Context, call InvocationContext, input CreateFoldersRequest) (ResourceOperationResult, error) {
	if err := s.validateManagement(call, "knowledge.folder.create"); err != nil {
		return ResourceOperationResult{}, err
	}
	return s.management.CreateFolders(ctx, call, input)
}
func (s *Service) UpdateMetadata(ctx context.Context, call InvocationContext, input UpdateMetadataRequest) (ResourceOperationResult, error) {
	if err := s.validateManagement(call, "knowledge.resource.update_metadata"); err != nil {
		return ResourceOperationResult{}, err
	}
	return s.management.UpdateMetadata(ctx, call, input)
}
func (s *Service) Move(ctx context.Context, call InvocationContext, input MoveResourcesRequest) (ResourceOperationResult, error) {
	if err := s.validateManagement(call, "knowledge.resource.move"); err != nil {
		return ResourceOperationResult{}, err
	}
	return s.management.Move(ctx, call, input)
}
func (s *Service) Delete(ctx context.Context, call InvocationContext, input DeleteResourcesRequest) (ResourceOperationResult, error) {
	if err := s.validateManagement(call, "knowledge.resource.delete"); err != nil {
		return ResourceOperationResult{}, err
	}
	return s.management.Delete(ctx, call, input)
}
func (s *Service) Upload(ctx context.Context, call InvocationContext, input UploadKnowledgeDocumentInput) (ResourceOperationResult, error) {
	if err := s.validateManagement(call, "knowledge.document.upload"); err != nil {
		return ResourceOperationResult{}, err
	}
	return s.management.Upload(ctx, call, input)
}
func (s *Service) AggregateKnowledgeDocuments(ctx context.Context, call InvocationContext, input AggregateKnowledgeDocumentsInput) (AggregateKnowledgeDocumentsResult, error) {
	const op = "knowledge.document.aggregate"
	if err := validateCaller(call, op); err != nil {
		return AggregateKnowledgeDocumentsResult{}, err
	}
	if s.management == nil {
		return AggregateKnowledgeDocumentsResult{}, NewError(Unsupported, op, "knowledge management is not enabled", false, nil)
	}
	ids, err := normalizeKnowledgeIDs(input.KnowledgeIDs, op)
	if err != nil || len(ids) == 0 {
		if err == nil {
			err = NewError(InvalidArgument, op, "knowledge_ids required", false, nil)
		}
		return AggregateKnowledgeDocumentsResult{}, err
	}
	if call.AllowedDatasetIDs != nil {
		for _, id := range ids {
			if !slices.Contains(call.AllowedDatasetIDs, id) {
				return AggregateKnowledgeDocumentsResult{}, NewError(PermissionDenied, op, "library outside execution scope", false, nil)
			}
		}
	}
	input.KnowledgeIDs = ids
	return s.management.Aggregate(ctx, call, input)
}
