package workflowmcp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ExecutionToolsInput struct {
	SessionID       string `json:"session_id" jsonschema:"required,Workflow session identifier"`
	ExecutionID     string `json:"execution_id" jsonschema:"required,Current external execution identifier"`
	ExecutionHandle string `json:"execution_handle" jsonschema:"required,Unchanged execution handle held by the root controller; never forward to a worker"`
}

type ExecutionToolCallInput struct {
	SessionID       string         `json:"session_id" jsonschema:"required,Workflow session identifier"`
	ExecutionID     string         `json:"execution_id" jsonschema:"required,Current external execution identifier"`
	ExecutionHandle string         `json:"execution_handle" jsonschema:"required,Unchanged execution handle held by the root controller; never forward to a worker"`
	ToolName        string         `json:"tool_name" jsonschema:"required,Exact name returned by workflow.tool.list"`
	Source          string         `json:"source,omitempty" jsonschema:"Optional package or global source; must match the pinned contract declaration"`
	Arguments       map[string]any `json:"arguments,omitempty" jsonschema:"Inline tool arguments; mutually exclusive with arguments_file"`
	ArgumentsFile   string         `json:"arguments_file,omitempty" jsonschema:"Absolute Connector-local UTF-8 JSON object file containing tool arguments; mutually exclusive with arguments. Let the worker prepare it from files instead of reading HTML or notes into controller context"`
	ResultDirectory string         `json:"result_directory,omitempty" jsonschema:"Existing absolute Connector-local directory. Save full tool response and decoded files into a new private subdirectory; return only paths hashes sizes and brief status, not generated prompts HTML or base64"`
	CommandID       string         `json:"command_id" jsonschema:"required,Stable unique operation ID; reuse unchanged with identical arguments after an uncertain result"`
}

func registerExecutionTools(server *mcp.Server, client *Client) {
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.tool.list", Title: "List tools for a Workflow execution",
		Description: "List tools explicitly allowed by this active execution's pinned external contract, including schemas and availability. The bound controller holds the execution_handle and may execute directly or proxy optional worker requests; workers do not control Workflow state. Missing capabilities must be configured in LazyMind before retrying.", Annotations: annotations(true)},
		func(ctx context.Context, _ *mcp.CallToolRequest, input ExecutionToolsInput) (*mcp.CallToolResult, map[string]any, error) {
			value, err := client.ListExecutionTools(ctx, input)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.tool.call", Title: "Call a tool for a Workflow execution",
		Description: "The bound controller executes one tool explicitly allowed by the pinned external contract, directly or on behalf of an optional worker. Use the listed name and schema, unchanged execution_handle, and a stable command_id for unchanged retries. Use arguments_file for a worker-prepared JSON argument file and result_directory to return only local file references and brief status. Connector transport paths stay local; upstream arguments still follow the declared tool schema. This does not publish or complete a step. Do not send credentials or arbitrary server filesystem paths. If unavailable, show the LazyMind configuration guidance and yield; do not replace the workflow or silently use a different capability.", Annotations: annotations(false)},
		func(ctx context.Context, _ *mcp.CallToolRequest, input ExecutionToolCallInput) (*mcp.CallToolResult, map[string]any, error) {
			value, err := client.CallExecutionTool(ctx, input)
			return nil, value, err
		})
}

func executionToolPath(session, execution, handle, action string) (string, error) {
	if strings.TrimSpace(session) == "" || strings.TrimSpace(execution) == "" || strings.TrimSpace(handle) == "" {
		return "", errors.New("session_id, execution_id and execution_handle are required")
	}
	return "/workflow-sessions/" + url.PathEscape(session) + "/hosted-attempts/" + url.PathEscape(execution) + "/tools:" + action, nil
}

func (c *Client) ListExecutionTools(ctx context.Context, input ExecutionToolsInput) (map[string]any, error) {
	path, err := executionToolPath(input.SessionID, input.ExecutionID, input.ExecutionHandle, "list")
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = c.api.DoJSON(ctx, http.MethodPost, path, map[string]any{"execution_handle": input.ExecutionHandle}, &result)
	return result, err
}

func (c *Client) CallExecutionTool(ctx context.Context, input ExecutionToolCallInput) (map[string]any, error) {
	path, err := executionToolPath(input.SessionID, input.ExecutionID, input.ExecutionHandle, "call")
	if err != nil {
		return nil, err
	}
	if input.ArgumentsFile != "" {
		if input.Arguments != nil {
			return nil, errors.New("use either arguments or arguments_file, not both")
		}
		input.Arguments, err = readArgumentFile(input.ArgumentsFile)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(input.ToolName) == "" || strings.TrimSpace(input.CommandID) == "" || input.Arguments == nil {
		return nil, errors.New("tool_name, arguments and a stable command_id are required")
	}
	if input.Source != "" && input.Source != "package" && input.Source != "global" {
		return nil, errors.New("source must be package or global")
	}
	if input.ResultDirectory != "" {
		if err := validateResultDirectory(input.ResultDirectory); err != nil {
			return nil, err
		}
	}
	payload := map[string]any{"execution_handle": input.ExecutionHandle, "tool_name": input.ToolName,
		"arguments": input.Arguments, "command_id": input.CommandID}
	if input.Source != "" {
		payload["source"] = input.Source
	}
	var result map[string]any
	err = c.api.DoJSON(ctx, http.MethodPost, path, payload, &result)
	if err == nil && input.ResultDirectory != "" {
		return saveExecutionResult(input.ResultDirectory, result)
	}
	return result, err
}
