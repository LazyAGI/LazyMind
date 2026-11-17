package workflowmcp

import (
	"context"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/url"
)

type ActionJobInput struct {
	SessionID string `json:"session_id" jsonschema:"required,Workflow session identifier"`
	JobID     string `json:"job_id" jsonschema:"required,Artifact edit identifier from host notification"`
}
type ActionResponseInput struct {
	SessionID   string `json:"session_id" jsonschema:"required,Workflow session identifier"`
	JobID       string `json:"job_id" jsonschema:"required,Artifact edit identifier"`
	Version     int64  `json:"version" jsonschema:"required,Unchanged job version"`
	RequestHash string `json:"request_hash" jsonschema:"required,Unchanged generation_request.request_hash"`
	Text        string `json:"text" jsonschema:"required,Model response to the exact LazyMind generation request"`
}

func registerActionTools(server *mcp.Server, client *Client) {
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.action.get", Description: "Read a queued artifact edit. Execute its exact LazyMind generation_request in the external agent, using a native worker when available. This is not a workflow step and does not grant permission to commit. Stop when ready or cancelled; the user confirms in LazyMind.", Annotations: annotations(true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input ActionJobInput) (*mcp.CallToolResult, map[string]any, error) {
		result, err := client.actionJob(ctx, input.SessionID, input.JobID, nil)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.action.cancel", Description: "Cancel a queued or ready AI preview when the user explicitly cancels it. This does not alter the published artifact.", Annotations: annotations(false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input ActionJobInput) (*mcp.CallToolResult, map[string]any, error) {
		result, err := client.actionJob(ctx, input.SessionID, input.JobID, map[string]any{"cancel": true})
		return nil, result, err
	})

	mcp.AddTool(server, &mcp.Tool{Name: "workflow.action.respond", Description: "Return the external agent response to the shared LazyMind artifact-action planner. Preserve version and request_hash. Identical retries are idempotent. If another generation_request is returned, process it; when ready, end the turn and let the user confirm the preview. Never call step.begin for an artifact edit.", Annotations: annotations(false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input ActionResponseInput) (*mcp.CallToolResult, map[string]any, error) {
		result, err := client.actionJob(ctx, input.SessionID, input.JobID, input)
		return nil, result, err
	})
}
func (c *Client) actionJob(ctx context.Context, session, id string, body any) (map[string]any, error) {
	if session == "" || id == "" {
		return nil, fmt.Errorf("session_id and job_id are required")
	}
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	var result map[string]any
	err := c.api.DoJSON(ctx, method, "/workflow-sessions/"+url.PathEscape(session)+"/artifact-actions/"+url.PathEscape(id), body, &result)
	// A ready preview contains large HTML/font data. The UI fetches it directly;
	// the agent needs only lifecycle metadata, never an opaque commit capability.
	if result["status"] == "ready" {
		result = map[string]any{"status": "ready", "job_id": id, "version": result["version"], "agent_instruction": "END this turn. The user confirms the preview in LazyMind."}
	}
	return result, err
}
