package workflowmcp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type InputAnswer struct {
	SessionID    string `json:"session_id" jsonschema:"required,Workflow session with control.input_request"`
	ArtifactID   string `json:"input_artifact_id" jsonschema:"required,Exact control.input_request.artifact_id from workflow.state"`
	Revision     int    `json:"input_revision" jsonschema:"required,Exact control.input_request.revision"`
	StateVersion int64  `json:"expected_state_version" jsonschema:"required,State version returned with the questions"`
	AnswerText   string `json:"answer_text" jsonschema:"required,The user's actual answer to these questions; never invent an answer or reuse the launch request"`
	CommandID    string `json:"command_id" jsonschema:"required,Stable unique answer operation ID; reuse unchanged after an uncertain response"`
}

func registerInputAnswer(server *mcp.Server, client *Client) {
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.input.answer", Title: "Save the user's Workflow answers",
		Description: "Save the user's actual reply to control.input_request after asking the returned questions. Use its exact artifact, revision and state version, with a stable command_id. Never answer for the user. This records answers only: it does not approve reviews, resume a stopped run or start a step. Read workflow.state afterward and obey its admission. Automatic mode does not create these post-step questions.", Annotations: annotations(false)},
		func(ctx context.Context, _ *mcp.CallToolRequest, input InputAnswer) (*mcp.CallToolResult, map[string]any, error) {
			result, err := client.AnswerInput(ctx, input)
			return nil, result, err
		})
}

func (c *Client) AnswerInput(ctx context.Context, input InputAnswer) (map[string]any, error) {
	if strings.TrimSpace(input.SessionID) == "" || strings.TrimSpace(input.ArtifactID) == "" || input.Revision < 1 ||
		input.StateVersion < 0 || strings.TrimSpace(input.CommandID) == "" || strings.TrimSpace(input.AnswerText) == "" || len(input.AnswerText) > 16000 {
		return nil, errors.New("current question identity, state version, stable command_id and a user answer of 1–16000 bytes are required")
	}
	var result map[string]any
	err := c.api.DoJSON(ctx, http.MethodPost, "/workflow-sessions/"+url.PathEscape(input.SessionID)+"/control", map[string]any{
		"kind": "answer_input", "command_id": input.CommandID, "expected_state_version": input.StateVersion,
		"input_artifact_id": input.ArtifactID, "input_revision": input.Revision, "answer_text": input.AnswerText,
	}, &result)
	return result, err
}
