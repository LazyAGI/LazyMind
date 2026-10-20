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
	addCloudTool(server, &mcp.Tool{Name: "knowledge.document.aggregate", Title: "Aggregate knowledge metadata", Description: "Bounded metadata statistics for selected knowledge bases. Rejects oversized scans and grouping instead of returning incomplete totals.", Annotations: readOnlyAnnotations()}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.AggregateKnowledgeDocumentsInput) (*mcp.CallToolResult, capability.AggregateKnowledgeDocumentsResult, error) {
		v, e := s.AggregateKnowledgeDocuments(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	addCloudTool(server, &mcp.Tool{Name: "knowledge.create", Title: "Create knowledge base", Description: "Create a knowledge base using the authenticated user's existing permissions. Never silently rename on conflict.", Annotations: writeAnnotations()}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.CreateKnowledgeRequest) (*mcp.CallToolResult, capability.ResourceOperationResult, error) {
		v, e := s.CreateKnowledge(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	addCloudTool(server, &mcp.Tool{Name: "knowledge.folder.create", Title: "Create logical folders", Description: "Create named logical folders in one library. Empty parent_id means library root.", Annotations: writeAnnotations()}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.CreateFoldersRequest) (*mcp.CallToolResult, capability.ResourceOperationResult, error) {
		v, e := s.CreateFolders(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	addCloudTool(server, &mcp.Tool{Name: "knowledge.resource.update_metadata", Title: "Update resource metadata", Description: "Update only supported metadata on existing resources. Does not create missing resources or replace originals.", Annotations: writeAnnotations()}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.UpdateMetadataRequest) (*mcp.CallToolResult, capability.ResourceOperationResult, error) {
		v, e := s.UpdateMetadata(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	addCloudTool(server, &mcp.Tool{Name: "knowledge.resource.move", Title: "Move resources", Description: "Move existing resources; each selected root tree is atomic. Cross-library moves logically remove the source and return new IDs. The calling client should confirm scope and impact of cross-library moves with the user.", Annotations: executionAnnotations(true)}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.MoveResourcesRequest) (*mcp.CallToolResult, capability.ResourceOperationResult, error) {
		v, e := s.Move(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	addCloudTool(server, &mcp.Tool{Name: "knowledge.resource.delete", Title: "Delete resources", Description: "Delete selected resources and invalidate active bindings. The calling client should confirm scope and impact with the user. Physical cleanup is reported separately.", Annotations: executionAnnotations(true)}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.DeleteResourcesRequest) (*mcp.CallToolResult, capability.ResourceOperationResult, error) {
		v, e := s.Delete(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	addCloudTool(server, &mcp.Tool{Name: "knowledge.document.upload", Title: "Import original documents", Description: "Persist originals and create documents from uploaded_file, host local_file, remote_file or fixed work_artifact versions. Host local paths must be transferred as bytes first. Uploaded does not imply parsed or ready.", Annotations: writeAnnotations()}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.UploadKnowledgeDocumentInput) (*mcp.CallToolResult, capability.ResourceOperationResult, error) {
		v, e := s.Upload(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
}

func newManagementServer(service *capability.Service) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "lazymind-knowledge", Version: "v1"}, nil)
	addKnowledgeManagementTools(server, service)
	addCloudTool(server, &mcp.Tool{Name: "knowledge.list", Description: "List accessible knowledge bases and allowed actions.", Annotations: readOnlyAnnotations()}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.ListKnowledgeInput) (*mcp.CallToolResult, capability.ListKnowledgeResult, error) {
		v, e := service.ListKnowledge(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	addCloudTool(server, &mcp.Tool{Name: "knowledge.document.list", Description: "List documents and logical folders across selected accessible libraries. path is the original upload relative directory.", Annotations: readOnlyAnnotations()}, func(ctx context.Context, r *mcp.CallToolRequest, in capability.ListKnowledgeDocumentsInput) (*mcp.CallToolResult, capability.ListKnowledgeDocumentsResult, error) {
		v, e := service.ListKnowledgeDocuments(ctx, invocation(ctx, r), in)
		return nil, v, e
	})
	return server
}
