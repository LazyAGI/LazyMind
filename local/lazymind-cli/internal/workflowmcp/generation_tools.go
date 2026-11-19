package workflowmcp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GenerationClaimInput struct {
	ResultDirectory string `json:"result_directory,omitempty" jsonschema:"Existing absolute private directory. Save the exact generation request to a file for the model worker"`
	SessionID       string `json:"session_id" jsonschema:"required,Bound Workflow session"`
	JobID           string `json:"job_id" jsonschema:"required,Model generation job from the notification"`
	CommandID       string `json:"command_id" jsonschema:"required,Stable UUID for this worker. Reuse unchanged after a transport failure"`
}

type GenerationResponseInput struct {
	MessageFile string         `json:"message_file,omitempty" jsonschema:"Absolute Connector-local JSON file containing the completed worker's assistant message. Use either message or message_file"`
	SessionID   string         `json:"session_id" jsonschema:"required,Bound Workflow session"`
	JobID       string         `json:"job_id" jsonschema:"required,Claimed model generation job"`
	WorkerToken string         `json:"worker_token" jsonschema:"required,Unchanged token returned by generation.claim"`
	RequestHash string         `json:"request_hash" jsonschema:"required,Unchanged generation request hash"`
	Message     map[string]any `json:"message,omitempty" jsonschema:"OpenAI assistant message with role assistant and content or tool_calls. Execute only the supplied model request"`
	Error       string         `json:"error,omitempty" jsonschema:"Actual generation failure after the worker has ended"`
	Cancelled   bool           `json:"cancelled,omitempty" jsonschema:"Acknowledge only after this worker has stopped"`
}

func registerGenerationTools(server *mcp.Server, client *Client) {
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.generation.claim", Description: "Claim one admitted native LazyMind model request. At most two are admitted per Workflow session. Claim returned ready_job_ids too for up to two concurrent model workers. With status running, execute the exact generation_request messages and requested response format using the bound external Agent. Only generate an assistant message, including tool-call decisions when requested. LazyMind executes its own tools, validators and Workflow steps. Keep worker_token in the controller. With cancel_requested, acknowledge cancellation without generating. End for any other status.", Annotations: annotations(false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input GenerationClaimInput) (*mcp.CallToolResult, map[string]any, error) {
		if input.ResultDirectory != "" {
			if err := validateResultDirectory(input.ResultDirectory); err != nil {
				return nil, nil, err
			}
		}
		result, err := client.generation(ctx, input.SessionID, input.JobID, "claim", map[string]any{"command_id": input.CommandID})
		if request, ok := result["generation_request"].(map[string]any); err == nil && ok {
			messages, _ := request["messages"].([]any)
			result["generation_request_instructions"] = fmt.Sprintf("Read all %d messages in their original order, including earlier guidance, schemas and data. The last message may contain only task parameters or a repair error. Execute this job as a fresh model call using the complete request and its current response format. Use an independent native model worker when available. Generate a response for each current request and repair request. Return actual generation failures to LazyMind, then follow workflow.state and Core's retry admission.", len(messages))
		}
		if err == nil && input.ResultDirectory != "" && result["generation_request"] != nil {
			file, fileErr := saveJSONValue(input.ResultDirectory, result["generation_request"])
			if fileErr != nil {
				return nil, nil, fileErr
			}
			delete(result, "generation_request")
			result["generation_request_file"] = file
		}
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.generation.respond", Description: "Return the completed model worker's assistant message or actual failure to LazyMind. Preserve request_hash and worker_token. Identical retries are idempotent. Sending this releases the worker's reservation, so call only after the worker has ended. LazyMind resumes the original algorithm and may notify another model request. Do not publish artifacts, approve reviews or complete Workflow steps for a model request.", Annotations: annotations(false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input GenerationResponseInput) (*mcp.CallToolResult, map[string]any, error) {
		if input.MessageFile != "" {
			if input.Message != nil {
				return nil, nil, fmt.Errorf("use either message or message_file")
			}
			message, err := readArgumentFile(input.MessageFile)
			if err != nil {
				return nil, nil, err
			}
			input.Message = message
			input.MessageFile = ""
		}
		result, err := client.generation(ctx, input.SessionID, input.JobID, "respond", input)
		return nil, result, err
	})
}

func (c *Client) generation(ctx context.Context, session, job, action string, body any) (map[string]any, error) {
	if session == "" || job == "" {
		return nil, fmt.Errorf("session_id and job_id are required")
	}
	var result map[string]any
	err := c.api.DoJSON(ctx, http.MethodPost, "/workflow-sessions/"+url.PathEscape(session)+"/model-generations/"+url.PathEscape(job)+":"+action, body, &result)
	return result, err
}
