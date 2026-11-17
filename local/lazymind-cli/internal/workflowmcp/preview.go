package workflowmcp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type PreviewInput struct {
	SessionID       string  `json:"session_id" jsonschema:"required,Workflow session"`
	ExecutionID     string  `json:"execution_id" jsonschema:"required,Current external execution"`
	ExecutionHandle string  `json:"execution_handle" jsonschema:"required,Root controller handle; never pass to workers"`
	Slot            string  `json:"slot" jsonschema:"required,Current paper step public document slot e.g. write_outline_document"`
	Seq             int     `json:"seq" jsonschema:"required,Positive increasing snapshot sequence for this execution lease; retry identical snapshots with the same seq"`
	Text            *string `json:"text,omitempty" jsonschema:"Cumulative public Markdown generated so far; mutually exclusive with text_file"`
	TextFile        string  `json:"text_file,omitempty" jsonschema:"Absolute Connector-local UTF-8 file holding the cumulative public Markdown snapshot, at most 512 KiB"`
	State           string  `json:"state" jsonschema:"required,streaming while generating; ended when the draft is ready; aborted to discard its preview"`
}

func registerPreview(server *mcp.Server, client *Client) {
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.artifact.preview", Title: "Update a paper document preview",
		Description: "Show public paper Markdown in the embedded workflow panel during generation. Send cumulative snapshots as sections are written, including literature review, outline, manuscript and revisions; do not wait for the complete output. Controller-only; proxy worker snapshot files without passing credentials. A preview never publishes artifacts, completes steps or approves reviews. Use ended when generation finishes and aborted when discarding a draft. Final signed artifacts still require workflow.artifact.publish.", Annotations: annotations(false)},
		func(ctx context.Context, _ *mcp.CallToolRequest, input PreviewInput) (*mcp.CallToolResult, map[string]any, error) {
			value, err := client.Preview(ctx, input)
			return nil, value, err
		})
}

func (c *Client) Preview(ctx context.Context, input PreviewInput) (map[string]any, error) {
	if strings.TrimSpace(input.SessionID) == "" || strings.TrimSpace(input.ExecutionID) == "" ||
		strings.TrimSpace(input.ExecutionHandle) == "" || input.Seq < 1 || input.Slot == "" {
		return nil, errors.New("session_id, execution_id, execution_handle, slot and positive seq are required")
	}
	if input.TextFile != "" {
		if input.Text != nil || !filepath.IsAbs(input.TextFile) {
			return nil, errors.New("use either text or an absolute text_file")
		}
		file, err := os.Open(input.TextFile)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		// Read a bounded snapshot even when a worker is still updating the file.
		data, err := io.ReadAll(io.LimitReader(file, (512<<10)+1))
		if err != nil {
			return nil, err
		}
		text := string(data)
		input.Text = &text
	}
	if input.State != "streaming" && input.State != "ended" && input.State != "aborted" {
		return nil, errors.New("state must be streaming, ended or aborted")
	}
	if input.State == "aborted" && input.Text == nil {
		text := ""
		input.Text = &text
	}
	if input.Text == nil || len(*input.Text) > 512<<10 || !utf8.ValidString(*input.Text) {
		return nil, errors.New("preview requires UTF-8 text of at most 512 KiB")
	}
	path := "/workflow-sessions/" + url.PathEscape(input.SessionID) + "/hosted-attempts/" + url.PathEscape(input.ExecutionID) + "/preview"
	var result map[string]any
	err := c.api.DoJSON(ctx, http.MethodPost, path, map[string]any{"execution_handle": input.ExecutionHandle,
		"slot": input.Slot, "seq": input.Seq, "text": *input.Text, "state": input.State}, &result)
	return result, err
}
