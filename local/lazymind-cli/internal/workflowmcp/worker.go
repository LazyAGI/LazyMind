package workflowmcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
)

type WorkerGrantInput struct {
	SessionID       string `json:"session_id" jsonschema:"required,Current Workflow session"`
	ExecutionID     string `json:"execution_id" jsonschema:"required,Current external attempt"`
	ExecutionHandle string `json:"execution_handle" jsonschema:"required,Controller handle; never pass this handle to a worker"`
	CommandID       string `json:"command_id" jsonschema:"required,Stable grant request identity"`
	ResultDirectory string `json:"result_directory" jsonschema:"required,Existing absolute private Connector-local runtime directory, never a business artifact directory"`
}

type workerConnection struct {
	SessionID    string `json:"session_id"`
	ExecutionID  string `json:"execution_id"`
	WorkerHandle string `json:"worker_handle"`
}

func registerWorkerGrant(server *mcp.Server, client *Client) {
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.worker.grant", Title: "Create a step-tool worker connection",
		Description: "Optional, controller-only: create restricted authorization for direct SubAgent tool calls when the host supports an isolated worker connection. Otherwise keep the SubAgent and proxy declared tool calls through the controller. Saves the credential to a private runtime file, never the business contract or artifacts. Configure a worker-only MCP server with lazymind mcp workflow-worker <grant_file>. That server exposes only tool discovery and calls, never review, step control or publication. Do not claim connection inheritance without testing.", Annotations: annotations(false)},
		func(ctx context.Context, _ *mcp.CallToolRequest, input WorkerGrantInput) (*mcp.CallToolResult, map[string]any, error) {
			path, err := executionToolPath(input.SessionID, input.ExecutionID, input.ExecutionHandle, "grant")
			if err != nil {
				return nil, nil, err
			}
			if input.CommandID == "" {
				return nil, nil, errors.New("command_id is required")
			}
			if err := validateResultDirectory(input.ResultDirectory); err != nil {
				return nil, nil, err
			}
			path = path[:len(path)-len("tools:grant")] + "workers:grant"
			var value workerConnection
			err = client.api.DoJSON(ctx, http.MethodPost, path, map[string]any{"execution_handle": input.ExecutionHandle, "command_id": input.CommandID}, &value)
			if err != nil {
				return nil, nil, err
			}
			if value.WorkerHandle == "" {
				return nil, nil, errors.New("Core returned no worker authorization")
			}
			value.SessionID, value.ExecutionID = input.SessionID, input.ExecutionID
			directory, err := os.MkdirTemp(input.ResultDirectory, "workflow-worker-")
			if err != nil {
				return nil, nil, err
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, nil, err
			}
			file := filepath.Join(directory, "connection.json")
			if err := os.WriteFile(file, raw, 0600); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"grant_file": file, "scope": "step_tools", "command": "lazymind", "args": []string{"mcp", "workflow-worker", file}}, nil
		})
}

type workerToolCall struct {
	ToolName        string         `json:"tool_name" jsonschema:"required,Exact tool returned by workflow.tool.list"`
	Arguments       map[string]any `json:"arguments,omitempty" jsonschema:"Tool arguments, mutually exclusive with arguments_file"`
	ArgumentsFile   string         `json:"arguments_file,omitempty" jsonschema:"Connector-local JSON parameter file"`
	ResultDirectory string         `json:"result_directory,omitempty" jsonschema:"Connector-local directory for full results and actual files"`
	CommandID       string         `json:"command_id" jsonschema:"required,Stable identity for identical retries"`
}

// RegisterToolWorker deliberately does not call Register or proxy upstream MCP.
// The model cannot replace the session, attempt, credential, or tool namespace.
func RegisterToolWorker(server *mcp.Server, client *Client, connection workerConnection) {
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.tool.list", Description: "Discover the current step's authorized tools and their schemas.", Annotations: annotations(true)},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, map[string]any, error) {
			result, err := client.ListExecutionTools(ctx, ExecutionToolsInput{SessionID: connection.SessionID, ExecutionID: connection.ExecutionID, ExecutionHandle: connection.WorkerHandle})
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.tool.call", OutputSchema: map[string]any{"type": "object", "additionalProperties": true}, Description: "Directly execute a declared business tool. No approval, step control or publication authority.", Annotations: annotations(false)},
		func(ctx context.Context, _ *mcp.CallToolRequest, input workerToolCall) (*mcp.CallToolResult, any, error) {
			result, err := client.CallExecutionTool(ctx, ExecutionToolCallInput{SessionID: connection.SessionID, ExecutionID: connection.ExecutionID,
				ExecutionHandle: connection.WorkerHandle, ToolName: input.ToolName, Arguments: input.Arguments,
				ArgumentsFile: input.ArgumentsFile, ResultDirectory: input.ResultDirectory, CommandID: input.CommandID})
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
			return nil, result, err
		})
}

func RunToolWorker(ctx context.Context, store *credentials.Store, grantFile string) error {
	if !filepath.IsAbs(grantFile) {
		return errors.New("worker grant file must be an absolute private runtime path")
	}
	info, err := os.Lstat(grantFile)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8192 || info.Mode().Perm()&0077 != 0 {
		return errors.New("worker grant must be a private regular file of at most 8 KiB")
	}
	raw, err := os.ReadFile(grantFile)
	if err != nil {
		return err
	}
	var connection workerConnection
	if json.Unmarshal(raw, &connection) != nil || connection.SessionID == "" || connection.ExecutionID == "" || connection.WorkerHandle == "" {
		return errors.New("invalid worker connection")
	}
	api, err := coreapi.New(store)
	if err != nil {
		return err
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "lazymind-step-worker", Version: "v1"}, &mcp.ServerOptions{
		Instructions: "Read the complete pinned business contract. Directly discover and call its authorized tools. Return candidate files to the controller. You cannot approve reviews, begin/complete steps or publish artifacts. Never reveal connection credentials.",
	})
	RegisterToolWorker(server, &Client{api: api}, connection)
	return server.Run(ctx, &mcp.StdioTransport{})
}
