package capability

import (
	"context"
	"slices"
	"strings"
)

type CreateKnowledgeRequest struct {
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Scene           string `json:"scene,omitempty"`
	ProcessingLevel string `json:"processing_level,omitempty"`
}
type CreateFoldersRequest struct {
	KnowledgeBaseID string   `json:"knowledge_base_id"`
	Folders         []string `json:"folders"`
	ParentID        string   `json:"parent_id,omitempty"`
}
type ResourceMetadataChanges struct {
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Scene       *string   `json:"scene,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
}
type ResourceMetadataItem struct {
	Target  ResourceRef             `json:"target"`
	Changes ResourceMetadataChanges `json:"changes"`
}
type UpdateMetadataRequest struct {
	Items []ResourceMetadataItem `json:"items"`
}
type MoveResourcesRequest struct {
	Sources     []ResourceRef `json:"sources"`
	Destination ResourceRef   `json:"destination"`
}
type DeleteResourcesRequest struct {
	Targets []ResourceRef `json:"targets"`
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
	EntryURL         string                   `json:"entry_url"`
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
	KnowledgeBaseID string                `json:"knowledge_base_id"`
	ParentID        string                `json:"parent_id,omitempty"`
	Files           []KnowledgeUploadItem `json:"files"`
}
type KnowledgeUploadItem struct {
	Source      KnowledgeUploadSource `json:"source"`
	DisplayName string                `json:"display_name,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
}
type KnowledgeUploadSource struct {
	Kind       string `json:"kind" jsonschema:"uploaded_file, local_file, remote_file or work_artifact"`
	FileRef    string `json:"file_ref,omitempty"`
	Path       string `json:"path,omitempty"`
	URL        string `json:"url,omitempty"`
	ArtifactID string `json:"artifact_id,omitempty"`
	RevisionID string `json:"revision_id,omitempty"`
}
type AggregateKnowledgeDocumentsInput struct {
	KnowledgeIDs    []string `json:"knowledge_ids"`
	FileTypes       []string `json:"file_types,omitempty"`
	DocumentStages  []string `json:"document_stages,omitempty"`
	DataSourceTypes []string `json:"data_source_types,omitempty"`
	Creators        []string `json:"creators,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	GroupBy         []string `json:"group_by,omitempty"`
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
