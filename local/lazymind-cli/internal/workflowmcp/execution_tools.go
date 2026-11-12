package workflowmcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/agentconnector/internal/coreapi"
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
		Description: "List tools declared by the current step, with input schemas, availability and reasons for missing capabilities. The controller uses the execution_handle to discover tools for itself or a worker.", Annotations: annotations(true)},
		func(ctx context.Context, _ *mcp.CallToolRequest, input ExecutionToolsInput) (*mcp.CallToolResult, map[string]any, error) {
			value, err := client.ListExecutionTools(ctx, input)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.tool.call", Title: "Call a tool for a Workflow execution",
		OutputSchema: map[string]any{"type": "object", "additionalProperties": true},
		Description:  "Call a declared tool using its listed name and schema. The controller supplies the execution_handle and a command_id stable across identical retries. arguments_file reads worker-prepared JSON locally. result_directory saves the full response and files for handoff. Correct arguments or configuration using the returned error code and message. Workflow publication and completion use their own endpoints.", Annotations: annotations(false)},
		func(ctx context.Context, _ *mcp.CallToolRequest, input ExecutionToolCallInput) (*mcp.CallToolResult, any, error) {
			value, err := client.CallExecutionTool(ctx, input)
			var failure *coreapi.Error
			if errors.As(err, &failure) {
				detail := map[string]any{"code": failure.Code, "message": failure.Message, "retryable": failure.Retryable}
				if failure.Upstream != nil {
					detail["upstream"] = failure.Upstream
				}
				payload := map[string]any{"error": detail}
				encoded, _ := json.Marshal(payload)
				return &mcp.CallToolResult{IsError: true, StructuredContent: payload,
					Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil, nil
			}
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
