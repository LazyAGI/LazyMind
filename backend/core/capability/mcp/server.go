package mcpadapter

import (
	"context"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/core/capability"
)

const DefaultMaxRequestBodyBytes = 64 << 10

type HandlerConfig struct {
	Verifier            auth.TokenVerifier
	MaxRequestBodyBytes int64
}

func NewHandler(service *capability.Service, config HandlerConfig) (http.Handler, error) {
	if service == nil {
		return nil, capability.NewError(capability.Internal, "mcp.server.new", "capability service is required", false, nil)
	}
	if config.Verifier == nil {
		return nil, capability.NewError(capability.Internal, "mcp.server.new", "bearer token verifier is required", false, nil)
	}
	maxBody := config.MaxRequestBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxRequestBodyBytes
	}
	server := newServer(service)
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		MaxRequestBodyBytes:          maxBody,
		PropagateRequestCancellation: true,
	})
	requireToken := auth.RequireBearerToken(config.Verifier, &auth.RequireBearerTokenOptions{
		Scopes:                 []string{capability.RequiredPermission},
		AllowMissingExpiration: true,
	})
	return requireToken(transport), nil
}

func newServer(service *capability.Service) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "lazymind-capabilities", Version: "v1"}, nil)
	annotations := readOnlyAnnotations()
	mcp.AddTool(server, &mcp.Tool{
		Name: "skill.list", Title: "List LazyMind skills",
		Description: "List the authenticated user's enabled, committed LazyMind skills.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.ListSkillsInput) (*mcp.CallToolResult, capability.ListSkillsResult, error) {
		result, err := service.ListSkills(ctx, invocation(request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "skill.get", Title: "Get a LazyMind skill",
		Description: "Get one enabled LazyMind skill and optionally its committed SKILL.md content.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.GetSkillInput) (*mcp.CallToolResult, capability.GetSkillResult, error) {
		result, err := service.GetSkill(ctx, invocation(request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "knowledge.list", Title: "List LazyMind knowledge bases",
		Description: "List knowledge bases visible to the authenticated LazyMind user.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.ListKnowledgeInput) (*mcp.CallToolResult, capability.ListKnowledgeResult, error) {
		result, err := service.ListKnowledge(ctx, invocation(request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "knowledge.document.list", Title: "List LazyMind knowledge documents",
		Description: "List documents in one knowledge base visible to the authenticated LazyMind user.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.ListKnowledgeDocumentsInput) (*mcp.CallToolResult, capability.ListKnowledgeDocumentsResult, error) {
		result, err := service.ListKnowledgeDocuments(ctx, invocation(request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "knowledge.document.get", Title: "Get a LazyMind knowledge document",
		Description: "Read one accessible knowledge document, with optional safe text content or parsed chunks.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.GetKnowledgeDocumentInput) (*mcp.CallToolResult, capability.GetKnowledgeDocumentResult, error) {
		result, err := service.GetKnowledgeDocument(ctx, invocation(request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "knowledge.search", Title: "Search LazyMind knowledge",
		Description: "Retrieve matching document chunks with LazyMind's existing knowledge retrieval; this tool returns hits and does not generate an answer.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.SearchKnowledgeInput) (*mcp.CallToolResult, capability.SearchKnowledgeResult, error) {
		result, err := service.SearchKnowledge(ctx, invocation(request), input)
		return nil, result, err
	})
	return server
}

func invocation(request *mcp.CallToolRequest) capability.InvocationContext {
	call := capability.InvocationContext{}
	if request == nil {
		return call
	}
	if request.Extra != nil {
		if info := request.Extra.TokenInfo; info != nil {
			call.Principal.UserID = strings.TrimSpace(info.UserID)
			call.Principal.Permissions = capability.NewPermissionSet(info.Scopes...)
			if info.Extra != nil {
				call.Principal.TenantID, _ = info.Extra[extraTenantID].(string)
			}
		}
	}
	return call
}

func readOnlyAnnotations() *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{
		ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: &no, OpenWorldHint: &no,
	}
}
