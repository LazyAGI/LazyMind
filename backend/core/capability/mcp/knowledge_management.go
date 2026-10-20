package mcpadapter

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/core/capability"
)

var KnowledgeManagementTools = []string{"knowledge.list", "knowledge.document.list", "knowledge.document.aggregate", "knowledge.create", "knowledge.folder.create", "knowledge.resource.update_metadata", "knowledge.resource.move", "knowledge.resource.delete", "knowledge.document.upload"}

func addKnowledgeManagementTools(server *mcp.Server, s *capability.Service) {
	if !s.SupportsKnowledgeManagement() {
		return
	}
	addCloudTool(server, &mcp.Tool{Name: "knowledge.document.aggregate", Title: "Aggregate knowledge metadata", Description: "Count documents and sum stored sizes. Filters combine with AND. file_types/document_stages/data_source_types/creators/tags: trim, case-insensitive; omitted/[] means unrestricted. Scan limit 10000 before these metadata filters; max 1000 groups. Oversized scans/tag expansion fail with QUERY_TOO_LARGE, never partial totals.", Annotations: readOnlyAnnotations()}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.AggregateKnowledgeDocumentsInput) (*mcp.CallToolResult, capability.AggregateKnowledgeDocumentsResult, error) {
		v, e := s.AggregateKnowledgeDocuments(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	addCloudTool(server, &mcp.Tool{Name: "knowledge.create", Title: "Create knowledge base", Description: "Create a library with name and tags. Configuration defaults are in parameter descriptions. Report resource/processing states immediately; do not sleep or poll.", Annotations: writeAnnotations()}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.CreateKnowledgeRequest) (*mcp.CallToolResult, capability.ResourceOperationResult, error) {
		v, e := s.CreateKnowledge(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	addCloudTool(server, &mcp.Tool{Name: "knowledge.folder.create", Title: "Create logical folders", Description: "Create logical folders transactionally. Existing name conflicts fail; shared prefixes created in this batch are reused. Report results immediately; do not sleep or poll.", Annotations: writeAnnotations()}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.CreateFoldersRequest) (*mcp.CallToolResult, capability.ResourceOperationResult, error) {
		v, e := s.CreateFolders(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	addCloudTool(server, &mcp.Tool{Name: "knowledge.resource.update_metadata", Title: "Update resource metadata", Description: "Update existing metadata without creating resources or replacing originals. Report states and downstream sync errors immediately; do not sleep or poll.", Annotations: writeAnnotations()}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.UpdateMetadataRequest) (*mcp.CallToolResult, capability.ResourceOperationResult, error) {
		v, e := s.UpdateMetadata(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	addCloudTool(server, &mcp.Tool{Name: "knowledge.resource.move", Title: "Move resources", Description: "Move folders/documents; each independent tree is atomic, batches may partially succeed. Same-library IDs remain; cross-library moves assign new IDs and logically remove sources. Confirm cross-library scope/impact with the user. Report states/ID mappings immediately; do not sleep or poll.", Annotations: executionAnnotations(true)}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.MoveResourcesRequest) (*mcp.CallToolResult, capability.ResourceOperationResult, error) {
		v, e := s.Move(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	addCloudTool(server, &mcp.Tool{Name: "knowledge.resource.delete", Title: "Delete resources", Description: "Logically delete resources, invalidating active bindings. Whole-library deletion requires ownership. Confirm scope/impact with the user. Report resource_status, cleanup_status and per-item errors separately; do not sleep or poll for physical cleanup.", Annotations: executionAnnotations(true)}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.DeleteResourcesRequest) (*mcp.CallToolResult, capability.ResourceOperationResult, error) {
		v, e := s.Delete(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	addCloudTool(server, &mcp.Tool{Name: "knowledge.document.upload", Title: "Import original documents", Description: "Import originals from shared-filesystem paths; resolve attachments and download/export URLs or artifacts first. Uploaded does not mean parsed/ready. Report resource/processing states immediately; do not sleep, poll or re-upload to wait.", Annotations: writeAnnotations()}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.UploadKnowledgeDocumentInput) (*mcp.CallToolResult, capability.ResourceOperationResult, error) {
		v, e := s.Upload(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
}

func newManagementServer(service *capability.Service) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "lazymind-knowledge", Version: "v1"}, nil)
	addKnowledgeManagementTools(server, service)
	addCloudTool(server, &mcp.Tool{Name: "knowledge.list", Description: "List accessible library metadata and allowed actions; filters combine with AND. Returned IDs are Core IDs.", Annotations: readOnlyAnnotations()}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.ListKnowledgeInput) (*mcp.CallToolResult, capability.ListKnowledgeResult, error) {
		v, e := service.ListKnowledge(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	addCloudTool(server, &mcp.Tool{Name: "knowledge.document.list", Description: "List document/folder metadata across libraries with global pagination; filters combine with AND. logical_path is current folder placement. Use knowledge.search for parsed content.", Annotations: readOnlyAnnotations()}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.ListKnowledgeDocumentsInput) (*mcp.CallToolResult, capability.ListKnowledgeDocumentsResult, error) {
		v, e := service.ListKnowledgeDocuments(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	return server
}
