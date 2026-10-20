package coreadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"lazymind/core/capability"
	"lazymind/core/doc"
	"strings"
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
	v, e := m.resources.CreateKnowledge(ctx, m.operation(c), doc.CreateKnowledgeRequest{Name: r.Name, Description: r.Description, Scene: r.Scene, ProcessingLevel: r.ProcessingLevel})
	return mapOperation("knowledge.create", v, e)
}
func (m *KnowledgeManagement) CreateFolders(ctx context.Context, c capability.InvocationContext, r capability.CreateFoldersRequest) (capability.ResourceOperationResult, error) {
	v, e := m.resources.CreateFolders(ctx, m.operation(c), doc.CreateFoldersRequest{KnowledgeBaseID: r.KnowledgeBaseID, ParentID: r.ParentID, Folders: r.Folders})
	return mapOperation("knowledge.folder.create", v, e)
}
func (m *KnowledgeManagement) UpdateMetadata(ctx context.Context, c capability.InvocationContext, r capability.UpdateMetadataRequest) (capability.ResourceOperationResult, error) {
	items := make([]doc.ResourceMetadataItem, len(r.Items))
	for i, v := range r.Items {
		items[i] = doc.ResourceMetadataItem{Target: toCoreRef(v.Target), Changes: doc.ResourceMetadataChanges{Name: v.Changes.Name, Description: v.Changes.Description, Scene: v.Changes.Scene, Tags: v.Changes.Tags}}
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
	v, e := doc.AggregateMetadata(ctx, m.db, m.lazyDB, managementCaller(c), doc.AggregateDocumentsRequest{DatasetIDs: r.KnowledgeIDs, FileTypes: r.FileTypes, DocumentStages: r.DocumentStages, DataSourceTypes: r.DataSourceTypes, Creators: r.Creators, Tags: r.Tags, GroupBy: r.GroupBy}, doc.AggregateBudget{})
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
	const op = "knowledge.document.upload"
	if len(r.Files) == 0 || len(r.Files) > 20 || strings.TrimSpace(r.KnowledgeBaseID) == "" {
		return capability.ResourceOperationResult{}, capability.NewError(capability.InvalidArgument, op, "select a target library and one to twenty files", false, nil)
	}
	operation := m.operation(c)
	operation.InputDigest = doc.ResourceInputDigest(r)
	if saved, found, e := m.resources.ReplayOperation(ctx, operation, op, r); e != nil || found {
		return mapOperation(op, saved, e)
	}
	caller := managementCaller(c)
	if _, e := doc.ResolveResourceDataset(ctx, m.db, caller, r.KnowledgeBaseID, "UPLOAD"); e != nil {
		return capability.ResourceOperationResult{}, mapResourceError(op, e)
	}
	requests := make([]doc.ImportOriginalRequest, 0, len(r.Files))
	for i, f := range r.Files {
		var ref doc.OriginalRef
		var err error
		// A source is a discriminated union. Reject extra locators instead of silently
		// preferring one and importing different bytes than the caller selected.
		source := f.Source
		n := 0
		for _, v := range []string{source.FileRef, source.Path, source.URL, source.ArtifactID} {
			if v != "" {
				n++
			}
		}
		if n != 1 {
			return capability.ResourceOperationResult{}, capability.NewError(capability.InvalidArgument, op, fmt.Sprintf("file %d must have exactly one source locator", i), false, nil)
		}
		switch source.Kind {
		case "uploaded_file":
			if source.FileRef == "" {
				err = fmt.Errorf("file_ref required")
			} else {
				ref, err = m.originals.AcquireUploadedOriginal(ctx, caller, source.FileRef)
			}
		case "remote_file":
			if source.URL == "" {
				err = fmt.Errorf("url required")
			} else {
				ref, err = m.originals.AcquireRemoteOriginal(ctx, caller, doc.AcquireRemoteOriginalRequest{URL: source.URL, DisplayName: f.DisplayName})
			}
		case "work_artifact":
			if source.ArtifactID == "" || source.RevisionID == "" {
				err = fmt.Errorf("artifact_id and fixed revision_id required")
			} else {
				ref, err = m.originals.AcquireWorkOriginal(ctx, caller, source.ArtifactID, source.RevisionID)
			}
		case "local_file":
			return capability.ResourceOperationResult{}, capability.NewError(capability.Unsupported, op, "local_file requires host byte transfer; upload bytes then pass uploaded_file.file_ref", false, nil)
		default:
			err = fmt.Errorf("invalid source kind")
		}
		if err != nil {
			return capability.ResourceOperationResult{}, mapResourceError(op, err)
		}
		requests = append(requests, doc.ImportOriginalRequest{FileRef: ref.FileRef, DatasetID: r.KnowledgeBaseID, ParentID: r.ParentID, DisplayName: f.DisplayName, Tags: f.Tags})
	}
	v, e := m.resources.Upload(ctx, operation, requests)
	return mapOperation(op, v, e)
}

func (m *KnowledgeManagement) SetBeforeCommit(fn func(context.Context, capability.InvocationContext) error) {
	m.beforeCommit = fn
}
