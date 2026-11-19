package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/core/capability"
)

type failingExternalTool struct {
	capability.ExternalCapabilityExecutor
}

func (failingExternalTool) InvokeExternalTool(context.Context, capability.InvocationContext, capability.InvokeExternalToolInput) (capability.InvokeExternalToolResult, error) {
	err := capability.NewError(capability.ProviderRejected, "tool.call", "Provider rejected image generation (HTTP 451)", false, errors.New("private cause"))
	err.Upstream = &capability.UpstreamError{HTTPStatus: 451, Code: "provider_policy", RequestID: "request-123"}
	return capability.InvokeExternalToolResult{}, err
}

func TestDirectMCPToolPreservesStructuredProviderFailure(t *testing.T) {
	ports := &mcpFakePorts{}
	service, err := capability.NewService(capability.Dependencies{
		Skills: ports, Knowledge: ports, Documents: ports, Search: ports,
		Cloud: ports, Vocabulary: ports, External: failingExternalTool{},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := newServer(service)
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			ctx = context.WithValue(ctx, trustedInvocationKey{}, capability.InvocationContext{
				Principal:     capability.Principal{UserID: "owner", Permissions: capability.NewPermissionSet(capability.RequiredPermission)},
				ExternalAgent: "codex",
			})
			return next(ctx, method, request)
		}
	})
	a, b := mcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "error-test", Version: "1"}, nil).Connect(t.Context(), b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "tool.call", Arguments: map[string]any{"tool_id": "builtin:image_generator"}})
	if err != nil || !result.IsError {
		t.Fatalf("provider failure became success or protocol error: %+v %v", result, err)
	}
	for _, representation := range []any{result.StructuredContent, result.Content} {
		body, _ := json.Marshal(representation)
		for _, expected := range []string{"PROVIDER_REJECTED", "451", "request-123", "provider_policy", "retryable"} {
			if !strings.Contains(string(body), expected) {
				t.Fatalf("MCP lost %s: %s", expected, body)
			}
		}
		if strings.Contains(string(body), "private") {
			t.Fatalf("MCP exposed a private cause: %s", body)
		}
	}
}
