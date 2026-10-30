package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/agentconnector/internal/codexhost"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowmcp"
)

var requiredTools = []string{
	"cloud_document.get",
	"cloud_document.list",
	"cloud_document.search",
	"knowledge.document.get",
	"knowledge.document.list",
	"knowledge.list",
	"knowledge.search",
	"model.chat",
	"model.list",
	"skill.get",
	"skill.list",
	"tool.call",
	"tool.list",
}

type Bridge struct {
	home                string
	api                 *coreapi.Client
	connectorInstanceID string
	sourceProvider      string
}

type ProbeResult struct {
	Endpoint string   `json:"endpoint"`
	Tools    []string `json:"tools"`
}

func New(store *credentials.Store) (*Bridge, error) {
	api, err := coreapi.New(store)
	if err != nil {
		return nil, err
	}
	instanceID, err := newInvocationID("connector-")
	if err != nil {
		return nil, err
	}
	return &Bridge{
		api: api, connectorInstanceID: instanceID, home: store.Directory(),
		sourceProvider: strings.ToLower(strings.TrimSpace(connectorconfig.Value("LAZYMIND_AGENT_PROVIDER"))),
	}, nil
}

func (b *Bridge) Endpoint(ctx context.Context) (string, error) {
	return b.api.MCPURL(ctx)
}

func (b *Bridge) Connect(ctx context.Context) (*mcp.ClientSession, []*mcp.Tool, string, error) {
	endpoint, err := b.Endpoint(ctx)
	if err != nil {
		return nil, nil, "", wrapMCPStartupError(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "lazymind-agent-bridge", Version: "v1"}, &mcp.ClientOptions{
		Logger: discardLogger(),
	})
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           b.api.HTTPClient(),
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return nil, nil, endpoint, wrapMCPStartupError(fmt.Errorf("connect LazyMind MCP at %s: %w", endpoint, err))
	}
	tools, err := listAllTools(ctx, session)
	if err != nil {
		_ = session.Close()
		return nil, nil, endpoint, wrapMCPStartupError(fmt.Errorf("list LazyMind MCP tools: %w", err))
	}
	if missing := missingRequiredTools(tools); len(missing) > 0 {
		_ = session.Close()
		return nil, nil, endpoint, fmt.Errorf("LazyMind MCP is missing required tools: %s", strings.Join(missing, ", "))
	}
	return session, tools, endpoint, nil
}

// wrapMCPStartupError keeps auth failures visible when DSH hosts mcp proxy.
func wrapMCPStartupError(err error) error {
	if err == nil {
		return nil
	}
	if credentials.IsAuthenticationRequired(err) || strings.Contains(err.Error(), "not logged in to LazyMind") {
		return fmt.Errorf("%w", err)
	}
	return err
}

func (b *Bridge) Probe(ctx context.Context) (ProbeResult, error) {
	session, tools, endpoint, err := b.Connect(ctx)
	if err != nil {
		return ProbeResult{}, err
	}
	defer session.Close()
	names := make([]string, 0, len(tools)+len(workflowmcp.ToolNames))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	names = append(names, workflowmcp.ToolNames...)
	sort.Strings(names)
	return ProbeResult{Endpoint: endpoint, Tools: names}, nil
}

func (b *Bridge) RunStdio(ctx context.Context) error {
	upstream, tools, endpoint, err := b.Connect(ctx)
	if err != nil {
		return err
	}
	scope, err := b.api.AccountScope()
	if err != nil {
		_ = upstream.Close()
		return err
	}
	connection := &liveConnection{bridge: b, scope: scope, endpoint: endpoint, session: upstream}
	defer connection.close()
	webBase, err := b.api.ServerURL(ctx)
	if err != nil {
		return err
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "lazymind", Version: "v2"}, &mcp.ServerOptions{
		Logger: discardLogger(), Instructions: workflowInstructions + " Do not call workflow.step.begin until admission.can_begin is true." + " For document access, honor the user's explicit choice of tool and account. Otherwise prefer the Agent's already available native document tools, using LazyMind as a fallback when those tools are unavailable or lack the required capability. Never switch accounts to bypass a permission denial. Use cloud_document.list to find an authorized source, cloud_document.get/search to discover documents, and cloud_document.read with the returned source_id and read_locator when that tool is advertised. Follow returned pagination and version fields; report warnings and unsupported formats. If read is absent, do not treat directory metadata as document content. For knowledge.document.upload, pass absolute regular-file paths readable by the MCP service on the shared filesystem. Resolve attachments, download URLs and export artifacts first. Report returned operation states immediately; do not sleep or poll for background processing. For already ingested documents use knowledge.document.list with optional name/path filters, knowledge.document.get with include_chunks, or knowledge.search. Do not request raw binary content as text. On connection or authorization errors, show the returned connection guidance instead of retrying with another account." + fmt.Sprintf(" Resolve relative authorization action URLs against the configured LazyMind web base %q (preserving its path prefix); present a clickable URL. After authorization, return to the original Agent and retry the same source_id.", webBase),
	})
	readOnlyTools := make(map[string]bool, len(tools)+len(workflowmcp.ToolNames))
	for _, publishedTool := range tools {
		tool := publishedTool
		readOnlyTools[tool.Name] = tool.Annotations != nil && tool.Annotations.ReadOnlyHint
		server.AddTool(tool, func(callCtx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if request == nil || request.Params == nil {
				return nil, errors.New("missing tool call parameters")
			}
			var arguments any = map[string]any{}
			if len(request.Params.Arguments) > 0 {
				if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
					return nil, fmt.Errorf("decode tool arguments: %w", err)
				}
			}
			return connection.call(callCtx, &mcp.CallToolParams{
				Meta:           request.Params.Meta,
				Name:           request.Params.Name,
				Arguments:      arguments,
				InputResponses: request.Params.InputResponses,
				RequestState:   request.Params.RequestState,
			})
		})
	}
	workflowClient, err := workflowmcp.NewClient(b.api, workflowmcp.StartOrigin{
		ConversationID: os.Getenv("LAZYMIND_CONVERSATION_ID"),
		ExternalRef:    os.Getenv("LAZYMIND_EXTERNAL_REF"),
	})
	if err != nil {
		return err
	}
	workflowClient.HostProvider = b.sourceProvider
	workflowClient.RequireHostBinding = (b.sourceProvider == "deepseek-harness" || b.sourceProvider == "codex" || b.sourceProvider == "workbuddy") && connectorconfig.Value("LAZYMIND_WORKFLOW_HOST_CONTROL") == "1"
	if b.sourceProvider == "codex" && workflowClient.RequireHostBinding {
		workflowClient.BindController = b.bindCodexController
		workflowClient.ResolveDriver = func(_ context.Context, request *mcp.CallToolRequest, fallback string) (string, error) {
			return workflowmcp.CodexDriver(request, fallback)
		}
		workflowClient.EnsureHostReady = func(ctx context.Context) error { return codexhost.EnsureConnection(ctx, b.home) }
	}
	if b.sourceProvider == "workbuddy" && workflowClient.RequireHostBinding {
		workflowClient.BindController = b.bindWorkBuddyController
		workflowClient.ResolveDriver = func(ctx context.Context, request *mcp.CallToolRequest, _ string) (string, error) {
			if request == nil || request.Params == nil {
				return "", errors.New("native workflow.start request unavailable")
			}
			return b.resolveWorkBuddyDriver(ctx, request.Params.Arguments)
		}
	}
	workflowmcp.Register(server, workflowClient)
	if b.sourceProvider == "codex" && workflowClient.RequireHostBinding {
		server.AddReceivingMiddleware(b.codexRecoveryMiddleware)
	}
	for _, name := range workflowmcp.ToolNames {
		readOnlyTools[name] = workflowmcp.IsReadOnlyTool(name)
	}
	server.AddReceivingMiddleware(invocationMiddleware(
		b.api, b.connectorInstanceID, b.sourceProvider, readOnlyTools,
	))
	server.AddReceivingMiddleware(accountGuard(b.api.AccountScope, scope))
	err = server.Run(ctx, &mcp.StdioTransport{})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func listAllTools(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Tool, error) {
	var tools []*mcp.Tool
	cursor := ""
	for {
		page, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		tools = append(tools, page.Tools...)
		if page.NextCursor == "" {
			return tools, nil
		}
		cursor = page.NextCursor
	}
}

func missingRequiredTools(tools []*mcp.Tool) []string {
	present := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if tool != nil {
			present[tool.Name] = struct{}{}
		}
	}
	var missing []string
	for _, name := range requiredTools {
		if _, ok := present[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
