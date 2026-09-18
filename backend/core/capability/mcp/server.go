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

type requestIdentity struct {
	agent        string
	invocationID string
}

type requestIdentityKey struct{}

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
	authorized := requireToken(transport)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := requestIdentity{
			agent:        strings.ToLower(strings.TrimSpace(r.Header.Get("X-LazyMind-Agent-Provider"))),
			invocationID: strings.TrimSpace(r.Header.Get("X-LazyMind-Invocation-Id")),
		}
		ctx := context.WithValue(r.Context(), requestIdentityKey{}, identity)
		authorized.ServeHTTP(w, r.WithContext(ctx))
	}), nil
}

func newServer(service *capability.Service) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "lazymind-capabilities", Version: "v1"}, nil)
	annotations := readOnlyAnnotations()
	mcp.AddTool(server, &mcp.Tool{
		Name: "skill.list", Title: "List LazyMind skills",
		Description: "List the authenticated user's enabled, committed LazyMind skills.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.ListSkillsInput) (*mcp.CallToolResult, capability.ListSkillsResult, error) {
		result, err := service.ListSkills(ctx, invocation(ctx, request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "skill.get", Title: "Get a LazyMind skill",
		Description: "Get one enabled LazyMind skill and optionally its committed SKILL.md content.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.GetSkillInput) (*mcp.CallToolResult, capability.GetSkillResult, error) {
		result, err := service.GetSkill(ctx, invocation(ctx, request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "knowledge.list", Title: "List LazyMind knowledge bases",
		Description: "List knowledge bases visible to the authenticated LazyMind user.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.ListKnowledgeInput) (*mcp.CallToolResult, capability.ListKnowledgeResult, error) {
		result, err := service.ListKnowledge(ctx, invocation(ctx, request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "knowledge.document.list", Title: "List LazyMind knowledge documents",
		Description: "List documents in one knowledge base visible to the authenticated LazyMind user.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.ListKnowledgeDocumentsInput) (*mcp.CallToolResult, capability.ListKnowledgeDocumentsResult, error) {
		result, err := service.ListKnowledgeDocuments(ctx, invocation(ctx, request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "knowledge.document.get", Title: "Get a LazyMind knowledge document",
		Description: "Read one accessible knowledge document, with optional safe text content or parsed chunks.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.GetKnowledgeDocumentInput) (*mcp.CallToolResult, capability.GetKnowledgeDocumentResult, error) {
		result, err := service.GetKnowledgeDocument(ctx, invocation(ctx, request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "knowledge.search", Title: "Search LazyMind knowledge",
		Description: "Retrieve matching document chunks with LazyMind's existing knowledge retrieval; this tool returns hits and does not generate an answer.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.SearchKnowledgeInput) (*mcp.CallToolResult, capability.SearchKnowledgeResult, error) {
		result, err := service.SearchKnowledge(ctx, invocation(ctx, request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "cloud_document.list", Title: "List connected cloud accounts",
		Description: "List the authenticated user's Feishu accounts enabled for LazyMind conversations.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.ListCloudDocumentsInput) (*mcp.CallToolResult, capability.ListCloudDocumentsResult, error) {
		result, err := service.ListCloudDocuments(ctx, invocation(ctx, request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "cloud_document.get", Title: "Browse connected cloud documents",
		Description: "Browse one online page of files or folders through an authorized LazyMind cloud account. This never creates a scan source or starts synchronization.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.GetCloudDocumentInput) (*mcp.CallToolResult, capability.GetCloudDocumentResult, error) {
		result, err := service.GetCloudDocument(ctx, invocation(ctx, request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "cloud_document.search", Title: "Search connected cloud documents",
		Description: "Search file and folder titles online through an authorized LazyMind cloud account; it does not use or refresh a scan index.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.SearchCloudDocumentsInput) (*mcp.CallToolResult, capability.SearchCloudDocumentsResult, error) {
		result, err := service.SearchCloudDocuments(ctx, invocation(ctx, request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "vocabulary.wordbook.list", Title: "List vocabulary wordbooks",
		Description: "List wordbooks from the user's currently selected vocabulary backend.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.ListVocabularyWordbooksInput) (*mcp.CallToolResult, capability.ListVocabularyWordbooksResult, error) {
		result, err := service.ListVocabularyWordbooks(ctx, invocation(ctx, request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "vocabulary.word.list", Title: "List vocabulary words",
		Description: "List words in a selected wordbook or deck. Set due_only=true whenever the user asks which words need review; this uses the authoritative scheduler and must not be inferred from state or review_count.", Annotations: annotations,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.ListVocabularyWordsInput) (*mcp.CallToolResult, capability.ListVocabularyWordsResult, error) {
		result, err := service.ListVocabularyWords(ctx, invocation(ctx, request), input)
		return nil, result, err
	})
	if service.HasExternalCapabilities() {
		mcp.AddTool(server, &mcp.Tool{
			Name: "model.list", Title: "List authorized LazyMind models",
			Description: "List only configured, verified models explicitly authorized for this external Agent. Credentials and provider endpoints are never returned.", Annotations: annotations,
		}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.ListExternalModelsInput) (*mcp.CallToolResult, capability.ListExternalModelsResult, error) {
			result, err := service.ListExternalModels(ctx, invocation(ctx, request), input)
			return nil, result, err
		})
		mcp.AddTool(server, &mcp.Tool{
			Name: "model.chat", Title: "Call an authorized LazyMind model",
			Description: "Run a text chat completion through a model explicitly authorized for this external Agent. LazyMind validates access and proxies the request without exposing credentials.", Annotations: executionAnnotations(false),
		}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.InvokeExternalModelInput) (*mcp.CallToolResult, capability.InvokeExternalModelResult, error) {
			result, err := service.InvokeExternalModel(ctx, invocation(ctx, request), input)
			return nil, result, err
		})
		mcp.AddTool(server, &mcp.Tool{
			Name: "tool.list", Title: "List authorized LazyMind tools",
			Description: "List available tools for this Agent. Tools are allowed by default unless the user explicitly disables access. Stored service credentials are never returned.", Annotations: annotations,
		}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.ListExternalToolsInput) (*mcp.CallToolResult, capability.ListExternalToolsResult, error) {
			result, err := service.ListExternalTools(ctx, invocation(ctx, request), input)
			return nil, result, err
		})
		mcp.AddTool(server, &mcp.Tool{
			Name: "tool.call", Title: "Call an authorized LazyMind tool",
			// Some MCP clients (including DSH) reject boolean property schemas.
			// An empty object accepts the same arbitrary JSON values as true.
			OutputSchema: map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"result": map[string]any{}},
				"required":   []string{"result"},
			},
			Description: "Execute an available tool through LazyMind after checking ownership, current availability, and the Agent's explicit opt-outs.", Annotations: executionAnnotations(true),
		}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.InvokeExternalToolInput) (*mcp.CallToolResult, capability.InvokeExternalToolResult, error) {
			result, err := service.InvokeExternalTool(ctx, invocation(ctx, request), input)
			return nil, result, err
		})
	}
	return server
}

func invocation(ctx context.Context, request *mcp.CallToolRequest) capability.InvocationContext {
	call := capability.InvocationContext{}
	if identity, ok := ctx.Value(requestIdentityKey{}).(requestIdentity); ok {
		call.ExternalAgent = identity.agent
		call.InvocationID = identity.invocationID
	}
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

func executionAnnotations(destructive bool) *mcp.ToolAnnotations {
	yes := true
	return &mcp.ToolAnnotations{
		ReadOnlyHint: false, IdempotentHint: false, DestructiveHint: &destructive, OpenWorldHint: &yes,
	}
}

func readOnlyAnnotations() *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{
		ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: &no, OpenWorldHint: &no,
	}
}

func writeAnnotations() *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false, DestructiveHint: &no, OpenWorldHint: &no}
}
