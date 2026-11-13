package workflowmcp

import (
	"context"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/url"
)

type EditContextInput struct {
	SessionID string `json:"session_id" jsonschema:"required,Current completed workflow session"`
}
type EditItem struct {
	ListIndex   *int   `json:"list_index,omitempty" jsonschema:"Existing stable item ID from edit.context; omit only for new items or single slots"`
	ContentType string `json:"content_type,omitempty"`
	Value       any    `json:"value,omitempty"`
	LocalPath   string `json:"local_path,omitempty" jsonschema:"Updated file to upload; never place a local path in value"`
}
type EditCommitInput struct {
	SessionID    string                `json:"session_id" jsonschema:"required,Current completed workflow session"`
	RevisionID   string                `json:"revision_id" jsonschema:"required,Unchanged package revision from edit.context"`
	StateVersion int64                 `json:"state_version" jsonschema:"required,Unchanged version from edit.context"`
	Slots        map[string][]EditItem `json:"slots" jsonschema:"required,Full desired item order per included slot; preserve items by list_index without value, delete by omission, insert without list_index. Include all nonempty linked lists and the updated downloadable file."`
}

func registerEditTools(server *mcp.Server, client *Client) {
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.edit.context", Description: "Read the current completed artifact edit policy, original LazyMind Skills, ordered artifact references and optimistic version. Use for user-requested page edits, scoped regeneration, insertion, deletion or reordering. Do not start another Workflow or rewind a local edit.", Annotations: annotations(true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input EditContextInput) (*mcp.CallToolResult, map[string]any, error) {
		if input.SessionID == "" {
			return nil, nil, fmt.Errorf("session_id required")
		}
		var result map[string]any
		err := client.api.DoJSON(ctx, http.MethodGet, "/workflow-sessions/"+url.PathEscape(input.SessionID)+"/artifact-edit", nil, &result)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.edit.commit", Description: "Atomically save a user-requested completed artifact edit using LazyMind's shared revision mechanism. Validate changed pages and linked previews/notes/downloadable file first. Never bypass an AI modification dialog's confirmation. A version conflict requires rereading context, not blindly retrying.", Annotations: annotations(false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input EditCommitInput) (*mcp.CallToolResult, map[string]any, error) {
		slots, err := encodeEditSlots(input.Slots)
		if err != nil {
			return nil, nil, err
		}
		var result map[string]any
		err = client.api.DoJSON(ctx, http.MethodPost, "/workflow-sessions/"+url.PathEscape(input.SessionID)+"/artifact-edit", map[string]any{"revision_id": input.RevisionID, "state_version": input.StateVersion, "slots": slots}, &result)
		return nil, result, err
	})
}
func encodeEditSlots(slots map[string][]EditItem) (map[string][]map[string]any, error) {
	result := map[string][]map[string]any{}
	for slot, items := range slots {
		result[slot] = []map[string]any{}
		for _, item := range items {
			row := map[string]any{}
			if item.ListIndex != nil {
				row["list_index"] = *item.ListIndex
			}
			if item.Value != nil || item.LocalPath != "" {
				encoded, err := encodeOutput(Output{Slot: slot, ContentType: item.ContentType, Value: item.Value, LocalPath: item.LocalPath, Seq: 1})
				if err != nil {
					return nil, err
				}
				row["value"] = encoded["value"]
				row["content_type"] = encoded["content_type"]
			}
			result[slot] = append(result[slot], row)
		}
	}
	return result, nil
}
