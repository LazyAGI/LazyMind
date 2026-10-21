package coreadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"lazymind/core/capability"
	"lazymind/core/doc"
)

type KnowledgeManagement struct {
	db, lazyDB   *gorm.DB
	resources    *doc.ResourceService
	originals    *doc.OriginalService
	commitGuard  func(context.Context, *gorm.DB, capability.InvocationContext) error
	beforeCommit func(context.Context, capability.InvocationContext) error
}

func NewKnowledgeManagement(db, lazyDB *gorm.DB, resources *doc.ResourceService, originals *doc.OriginalService) *KnowledgeManagement {
	return &KnowledgeManagement{db: db, lazyDB: lazyDB, resources: resources, originals: originals}
}
func managementCaller(c capability.InvocationContext) doc.DatasetCatalogCaller {
	p := make([]string, 0, len(c.Principal.Permissions))
	for v := range c.Principal.Permissions {
		p = append(p, v)
	}
	return doc.DatasetCatalogCaller{UserID: c.Principal.UserID, TenantID: c.Principal.TenantID, Permissions: p}
}
func managementContext(c capability.InvocationContext) doc.ResourceOperationContext {
	return doc.ResourceOperationContext{Caller: managementCaller(c), OperationKey: c.OperationKey, ExpectedSnapshot: c.ExpectedSnapshot, AllowedDatasetIDs: c.AllowedDatasetIDs}
}
func (m *KnowledgeManagement) SetCommitGuard(fn func(context.Context, *gorm.DB, capability.InvocationContext) error) {
	m.commitGuard = fn
}
func (m *KnowledgeManagement) operation(c capability.InvocationContext) doc.ResourceOperationContext {
	op := managementContext(c)
	if c.ExecutionKey != "" {
		op.ValidateBeforeCommit = func(ctx context.Context) error {
			if m.beforeCommit == nil {
				return fmt.Errorf("knowledge execution guard unavailable")
			}
			return m.beforeCommit(ctx, c)
		}
		op.ValidateExecution = func(ctx context.Context, tx *gorm.DB) error {
			if m.commitGuard == nil {
				return fmt.Errorf("knowledge execution guard unavailable")
			}
			return m.commitGuard(ctx, tx, c)
		}
	}
	return op
}
func toCoreRef(r capability.ResourceRef) doc.ResourceRef {
	return doc.ResourceRef{Kind: r.Kind, KnowledgeBaseID: r.KnowledgeBaseID, FolderID: r.FolderID, DocumentID: r.DocumentID}
}
func toCoreRefs(refs []capability.ResourceRef) []doc.ResourceRef {
	out := make([]doc.ResourceRef, len(refs))
	for i, r := range refs {
		out[i] = toCoreRef(r)
	}
	return out
}

// The application and Core types intentionally share a wire contract; Core
// keeps persistence and policy types out of the protocol-facing package.
func mapOperation(op string, r doc.ResourceOperationResult, e error) (capability.ResourceOperationResult, error) {
	if e != nil {
		return capability.ResourceOperationResult{}, mapResourceError(op, e)
	}
	raw, e := json.Marshal(r)
	if e != nil {
		return capability.ResourceOperationResult{}, e
	}
	var out capability.ResourceOperationResult
	e = json.Unmarshal(raw, &out)
	return out, e
}
func (m *KnowledgeManagement) CreateKnowledge(ctx context.Context, c capability.InvocationContext, r capability.CreateKnowledgeRequest) (capability.ResourceOperationResult, error) {
	v, e := m.resources.CreateKnowledge(ctx, m.operation(c), doc.CreateKnowledgeRequest{Name: r.Name, Description: r.Description, Scene: r.Scene, ProcessingLevel: r.ProcessingLevel, Tags: r.Tags, ProfileID: r.ProfileID, AlgoID: r.AlgoID, Capabilities: r.Capabilities})
	return mapOperation("knowledge.create", v, e)
}
func (m *KnowledgeManagement) CreateFolders(ctx context.Context, c capability.InvocationContext, r capability.CreateFoldersRequest) (capability.ResourceOperationResult, error) {
	v, e := m.resources.CreateFolders(ctx, m.operation(c), doc.CreateFoldersRequest{KnowledgeBaseID: r.KnowledgeBaseID, ParentID: r.ParentID, Folders: r.Folders})
	return mapOperation("knowledge.folder.create", v, e)
}
func (m *KnowledgeManagement) UpdateMetadata(ctx context.Context, c capability.InvocationContext, r capability.UpdateMetadataRequest) (capability.ResourceOperationResult, error) {
	items := make([]doc.ResourceMetadataItem, len(r.Items))
	for i, v := range r.Items {
		items[i] = doc.ResourceMetadataItem{Target: toCoreRef(v.Target), Changes: doc.ResourceMetadataChanges{Name: v.Changes.Name, Description: v.Changes.Description, Scene: v.Changes.Scene, Tags: v.Changes.Tags, ProfileID: v.Changes.ProfileID, AlgoID: v.Changes.AlgoID, ProcessingLevel: v.Changes.ProcessingLevel, Capabilities: v.Changes.Capabilities, TagMode: v.Changes.TagMode, Recursive: v.Changes.Recursive}}
	}
	v, e := m.resources.UpdateMetadata(ctx, m.operation(c), doc.UpdateMetadataRequest{Items: items})
	return mapOperation("knowledge.resource.update_metadata", v, e)
}
func (m *KnowledgeManagement) Move(ctx context.Context, c capability.InvocationContext, r capability.MoveResourcesRequest) (capability.ResourceOperationResult, error) {
	v, e := m.resources.Move(ctx, m.operation(c), doc.MoveResourcesRequest{Sources: toCoreRefs(r.Sources), Destination: toCoreRef(r.Destination)})
	return mapOperation("knowledge.resource.move", v, e)
}
func (m *KnowledgeManagement) Delete(ctx context.Context, c capability.InvocationContext, r capability.DeleteResourcesRequest) (capability.ResourceOperationResult, error) {
	v, e := m.resources.Delete(ctx, m.operation(c), doc.DeleteResourcesRequest{Targets: toCoreRefs(r.Targets)})
	return mapOperation("knowledge.resource.delete", v, e)
}
func (m *KnowledgeManagement) Aggregate(ctx context.Context, c capability.InvocationContext, r capability.AggregateKnowledgeDocumentsInput) (capability.AggregateKnowledgeDocumentsResult, error) {
	v, e := doc.AggregateMetadata(ctx, m.db, m.lazyDB, managementCaller(c), doc.AggregateDocumentsRequest{ResourceIDs: r.ResourceIDs, ParentID: r.ParentID, Recursive: r.Recursive, Match: r.Match, Name: r.Name, Path: r.Path, DatasetIDs: r.KnowledgeIDs, FileTypes: r.FileTypes, DocumentStages: r.DocumentStages, DataSourceTypes: r.DataSourceTypes, Creators: r.Creators, Tags: r.Tags, GroupBy: r.GroupBy}, doc.AggregateBudget{})
	if e != nil {
		return capability.AggregateKnowledgeDocumentsResult{}, mapResourceError("knowledge.document.aggregate", e)
	}
	groups := make([]capability.KnowledgeAggregateGroup, len(v.Groups))
	for i, g := range v.Groups {
		groups[i] = capability.KnowledgeAggregateGroup{Key: g.Key, Count: g.Count, Size: g.Size}
	}
	return capability.AggregateKnowledgeDocumentsResult{TotalCount: v.TotalCount, TotalSize: v.TotalSize, Groups: groups}, nil
}
func (m *KnowledgeManagement) Upload(ctx context.Context, c capability.InvocationContext, r capability.UploadKnowledgeDocumentInput) (capability.ResourceOperationResult, error) {
	files := make([]doc.LocalUploadItem, len(r.Files))
	for i, f := range r.Files {
		files[i] = doc.LocalUploadItem{Path: f.Path, DisplayName: f.DisplayName, Tags: f.Tags}
	}
	v, e := m.resources.UploadLocalBatch(ctx, m.operation(c), doc.LocalUploadRequest{KnowledgeBaseID: r.KnowledgeBaseID, ParentID: r.ParentID, Files: files})
	return mapOperation("knowledge.document.upload", v, e)
}

func (m *KnowledgeManagement) SetBeforeCommit(fn func(context.Context, capability.InvocationContext) error) {
	m.beforeCommit = fn
}

func (m *KnowledgeManagement) Reparse(ctx context.Context, c capability.InvocationContext, r capability.ReparseKnowledgeDocumentsInput) (capability.ResourceOperationResult, error) {
	v, e := m.resources.Reparse(ctx, m.operation(c), doc.ReparseDocumentsRequest{KnowledgeBaseID: r.KnowledgeBaseID, DocumentIDs: r.DocumentIDs})
	return mapOperation("knowledge.document.reparse", v, e)
}
