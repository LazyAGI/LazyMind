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
	"skill.get",
	"skill.list",
}

type Bridge struct {
	api                 *coreapi.Client
	connectorInstanceID string
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
	return &Bridge{api: api, connectorInstanceID: instanceID}, nil
}

func (b *Bridge) Endpoint(ctx context.Context) (string, error) {
	return b.api.MCPURL(ctx)
}

func (b *Bridge) Connect(ctx context.Context) (*mcp.ClientSession, []*mcp.Tool, string, error) {
	endpoint, err := b.Endpoint(ctx)
	if err != nil {
		return nil, nil, "", err
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
		return nil, nil, endpoint, fmt.Errorf("connect LazyMind MCP at %s: %w", endpoint, err)
	}
	tools, err := listAllTools(ctx, session)
	if err != nil {
		_ = session.Close()
		return nil, nil, endpoint, fmt.Errorf("list LazyMind MCP tools: %w", err)
	}
	if missing := missingRequiredTools(tools); len(missing) > 0 {
		_ = session.Close()
		return nil, nil, endpoint, fmt.Errorf("LazyMind MCP is missing required tools: %s", strings.Join(missing, ", "))
	}
	return session, tools, endpoint, nil
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
	upstream, tools, _, err := b.Connect(ctx)
	if err != nil {
		return err
	}
	defer upstream.Close()

	server := mcp.NewServer(&mcp.Implementation{Name: "lazymind", Version: "v2"}, &mcp.ServerOptions{
		Logger: discardLogger(), Instructions: "When the user chooses a LazyMind Workflow, call workflow.start, then repeatedly call workflow.step.begin, execute the returned immutable step_contract with your native Agent tools, and call workflow.step.submit. Resolve referenced step inputs with workflow.input.get or workflow.artifact.get according to source_type. If the Agent loses a session ID after restart, call workflow.session.list; if only an in-progress execution was interrupted, call workflow.step.resume with the same execution_id. Use workflow.session.stop/resume only for the whole session lifecycle. Never skip Workflow steps or report completion until workflow.state says completed. LazyMind owns Workflow state, artifacts, lineage and versions; submit generated workspace files through the local_path output field. After completion call workflow.artifact.list and use the returned LazyMind-managed URL for user-facing file links; never expose local_path or file:// URLs.",
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
			return upstream.CallTool(callCtx, &mcp.CallToolParams{
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
	workflowmcp.Register(server, workflowClient)
	for _, name := range workflowmcp.ToolNames {
		readOnlyTools[name] = workflowmcp.IsReadOnlyTool(name)
	}
	server.AddReceivingMiddleware(invocationMiddleware(b.api, b.connectorInstanceID, readOnlyTools))
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
