package workflowmcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxLocalArtifactBytes = 20 << 20

var ToolNames = []string{
	"workflow.list",
	"workflow.get",
	"workflow.input.import",
	"workflow.input.get",
	"workflow.start",
	"workflow.state",
	"workflow.session.list",
	"workflow.session.stop",
	"workflow.session.resume",
	"workflow.step.begin",
	"workflow.step.claim",
	"workflow.step.resume",
	"workflow.step.complete",
	"workflow.artifact.publish",
	"workflow.artifact.list",
	"workflow.artifact.get",
}

func IsReadOnlyTool(name string) bool {
	switch name {
	case "workflow.list", "workflow.get", "workflow.input.get", "workflow.state", "workflow.session.list",
		"workflow.artifact.list", "workflow.artifact.get":
		return true
	default:
		return false
	}
}

type GetInput struct {
	WorkflowID string `json:"workflow_id" jsonschema:"required,LazyMind Workflow identifier"`
	RevisionID string `json:"revision_id,omitempty" jsonschema:"Optional immutable revision identifier"`
}

type StateInput struct {
	SessionID string `json:"session_id" jsonschema:"required,Workflow session identifier"`
}

type SessionListInput struct {
	Status    string `json:"status,omitempty" jsonschema:"Optional exact status: active waiting stopped failed or completed"`
	PageSize  int    `json:"page_size,omitempty" jsonschema:"Optional page size from 1 to 100"`
	PageToken string `json:"page_token,omitempty" jsonschema:"Opaque continuation token returned by the previous page"`
}

type SessionLifecycleInput struct {
	SessionID string `json:"session_id" jsonschema:"required,Workflow session identifier"`
	CommandID string `json:"command_id,omitempty" jsonschema:"Stable retry key; generated when omitted"`
}

type InputImportInput struct {
	LocalPath string `json:"local_path" jsonschema:"required,File inside the current Agent workspace"`
}

type InputGetInput struct {
	ResourceID string `json:"resource_id" jsonschema:"required,Immutable LazyMind input resource identifier"`
}

type InputGetResult struct {
	Resource InputResource `json:"resource"`
	Rendered bool          `json:"rendered_as_mcp_content,omitempty"`
}

type ArtifactListInput struct {
	SessionID string `json:"session_id" jsonschema:"required,Workflow session identifier"`
}

type ArtifactListResult struct {
	Artifacts []Artifact `json:"artifacts"`
}

type ArtifactGetInput struct {
	ArtifactID string `json:"artifact_id" jsonschema:"required,Artifact revision identifier"`
}

type ArtifactGetResult struct {
	Artifact Artifact `json:"artifact"`
	Rendered bool     `json:"rendered_as_mcp_content,omitempty"`
}

func Register(server *mcp.Server, client *Client) {
	readOnly, write := annotations(true), annotations(false)
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.list", Title: "List LazyMind Workflows",
		Description: "List published LazyMind Workflows available to this user.", Annotations: readOnly,
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			value, err := client.List(ctx)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.get", Title: "Get a LazyMind Workflow",
		Description: "Read one published Workflow revision: identifiers plus declared tool_scripts as UTF-8 files. Omits compiled_graph, scenario, and yaml. Use when inspecting package scripts; step execution uses the contract returned by workflow.step.begin.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input GetInput) (*mcp.CallToolResult, map[string]any, error) {
			value, err := client.Get(ctx, input.WorkflowID, input.RevisionID)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.input.import", Title: "Import a Workflow input",
		Description: "Import one file from the current Agent workspace as an immutable LazyMind input resource. Use the returned binding under its material ID in workflow.start input_bindings.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input InputImportInput) (*mcp.CallToolResult, InputImportResult, error) {
			file, err := readLocalFile(input.LocalPath)
			if err != nil {
				return nil, InputImportResult{}, err
			}
			value, err := client.ImportInput(ctx, file.Name, file.MIMEType, file.Hash, file.Base64, int64(file.Size))
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.input.get", Title: "Get a Workflow input",
		Description: "Read one immutable LazyMind Workflow input resource by resource_id from the input binding. Images are also returned as native MCP image content.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input InputGetInput) (*mcp.CallToolResult, InputGetResult, error) {
			resource, err := client.GetInput(ctx, input.ResourceID)
			if err != nil {
				return nil, InputGetResult{}, err
			}
			content, rendered := inputContent(resource)
			if rendered {
				resource.ContentBase64 = ""
				return &mcp.CallToolResult{Content: content}, InputGetResult{Resource: resource, Rendered: true}, nil
			}
			return nil, InputGetResult{Resource: resource}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.start", Title: "Start a LazyMind Workflow",
		Description: "Create a durable Workflow session and pin its revision. Returns session_id, workflow_id, and revision_id. Next call workflow.step.begin for a ready step, including auto steps; start creates the session without launching steps. A prior terminal session in this same conversation is archived atomically; a conflicting active session must be handled explicitly. Other conversations are independent.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input StartInput) (*mcp.CallToolResult, StartResult, error) {
			value, err := client.Start(ctx, input)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.state", Title: "Read LazyMind Workflow state",
		Description: "Read authoritative Workflow readiness, attempts and completion state. With continuation=continue and admission.can_begin=true, call workflow.step.begin for a ready step, including human steps: human/requires_approval means review AFTER execution, not another confirmation before begin. With awaiting_executor, a leftover native attempt is still running; yield. With awaiting_user, yield for panel review.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input StateInput) (*mcp.CallToolResult, Projection, error) {
			value, err := client.State(ctx, input.SessionID)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.session.list", Title: "List external-Agent Workflow sessions",
		Description: "List this user's external-Agent Workflow sessions, scoped to the conversation when the host supplies its identity. Results may include other conversations: check the binding before controlling a run. Use after restart to recover a session ID, then read workflow.state.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input SessionListInput) (*mcp.CallToolResult, SessionPage, error) {
			value, err := client.ListSessions(ctx, input.Status, input.PageSize, input.PageToken)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.session.stop", Title: "Stop a LazyMind Workflow session",
		Description: "Stop one external-Agent Workflow session and interrupt its active hosted attempt. Safe to retry with the same command_id.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input SessionLifecycleInput) (*mcp.CallToolResult, SessionLifecycleResult, error) {
			value, err := client.StopSession(ctx, input.SessionID, input.CommandID)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.session.resume", Title: "Resume a stopped LazyMind Workflow session",
		Description: "Resume a stopped legacy Workflow session so its interrupted step can be begun again under Runtime rules. Controlled workflows require the user to select Resume in the authenticated workflow page; this tool cannot bypass that decision. Safe to retry with the same command_id.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input SessionLifecycleInput) (*mcp.CallToolResult, SessionLifecycleResult, error) {
			value, err := client.ResumeSession(ctx, input.SessionID, input.CommandID)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.step.begin", Title: "Begin a LazyMind Workflow step",
		Description: "Start one ready step, including auto steps, and return its contract and execution_handle. Human review occurs after a successful completion. If executor_host is lazymind, no handle is issued: only observe.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input BeginInput) (*mcp.CallToolResult, BeginResult, error) {
			value, err := client.Begin(ctx, input)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.step.claim", Title: "Claim a prepared Workflow execution",
		Description: "Inspect or claim an existing execution_id without creating another step. If an execution_handle is returned, execute the granted contract and complete with it. If executor_host is lazymind, only observe its attempt_status; no handle is issued.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input ResumeInput) (*mcp.CallToolResult, BeginResult, error) {
			value, err := client.Claim(ctx, input)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.step.resume", Title: "Resume a LazyMind Workflow step",
		Description: "Reclaim the same in-progress external execution after restart and return its contract with a new execution_handle. Execute that contract and complete with the new handle. If executor_host is lazymind this only reads status; it cannot take over a leftover native worker.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input ResumeInput) (*mcp.CallToolResult, BeginResult, error) {
			value, err := client.Resume(ctx, input)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.step.complete", Title: "Complete a LazyMind Workflow step",
		Description: "Finish an external step after all workflow.artifact.publish calls have succeeded. Pass the outcome and unchanged execution_handle; do not resend outputs. LazyMind checks saved required outputs and settles state. If the step requires human review, stop this turn and wait for the user.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input CompleteInput) (*mcp.CallToolResult, CompleteResult, error) {
			value, err := client.Complete(ctx, input)
			if err != nil {
				return nil, value, err
			}
			if AwaitingReview(value.State) {
				message := "Stop this turn. The submitted step requires user review"
				if value.State.InteractionURL != "" {
					message += " at " + value.State.InteractionURL
				}
				message += ". Do not call workflow.step.begin until the user asks to continue."
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: message}}}, value, nil
			}
			return nil, value, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.artifact.publish", Title: "Publish a Workflow artifact",
		Description: "Publish each save_artifact/save_artifacts result immediately (key becomes slot), while the step is running. Types: text, json, image, file, or file_list. Use local_path for files or value for inline results. Supply a positive seq unique per slot and reuse it unchanged on retry. Wait for publication acknowledgements before workflow.step.complete. Publication does not finish the step or trigger review.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input PublishInput) (*mcp.CallToolResult, map[string]any, error) {
			artifact, err := encodeOutput(input.Output)
			if err != nil {
				return nil, nil, err
			}
			value, err := client.Publish(ctx, input, artifact)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.artifact.list", Title: "List LazyMind Workflow artifacts",
		Description: "List the selected artifact revisions currently owned by a Workflow session.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input ArtifactListInput) (*mcp.CallToolResult, any, error) {
			value, err := client.ListArtifacts(ctx, input.SessionID)
			return nil, ArtifactListResult{Artifacts: value}, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.artifact.get", Title: "Get a LazyMind Workflow artifact",
		Description: "Read one immutable artifact revision by artifact_id from the step inputs or workflow.artifact.list; a slot key is not an artifact_id. Corresponds to get_artifact/read_artifact in step instructions. Inline images are also returned as native MCP image content.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input ArtifactGetInput) (*mcp.CallToolResult, any, error) {
			artifact, err := client.GetArtifact(ctx, input.ArtifactID)
			if err != nil {
				return nil, nil, err
			}
			content, rendered := artifactContent(artifact)
			if rendered {
				artifact.Value = nil
				return &mcp.CallToolResult{Content: content}, ArtifactGetResult{Artifact: artifact, Rendered: true}, nil
			}
			return nil, ArtifactGetResult{Artifact: artifact}, nil
		})
}

func annotations(readOnly bool) *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{ReadOnlyHint: readOnly, IdempotentHint: readOnly, DestructiveHint: &no, OpenWorldHint: &no}
}

func encodeOutput(output Output) (map[string]any, error) {
	slot := strings.TrimSpace(output.Slot)
	if slot == "" || output.Seq < 1 {
		return nil, errors.New("output requires a slot and stable positive seq")
	}
	if output.LocalPath != "" && output.Value != nil {
		return nil, fmt.Errorf("output %q must use either local_path or value, not both", slot)
	}
	value := output.Value
	if output.LocalPath != "" {
		var err error
		value, err = encodeLocalFile(output.LocalPath, output.Caption)
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", slot, err)
		}
	} else if value == nil {
		return nil, fmt.Errorf("output %q requires local_path or value", slot)
	}
	return map[string]any{"slot": slot, "content_type": slotContentType(output), "value": value, "seq": output.Seq}, nil
}

type localFile struct {
	Name     string
	MIMEType string
	Size     int
	Hash     string
	Base64   string
}

func slotContentType(output Output) string {
	if contentType := strings.TrimSpace(output.ContentType); contentType != "" {
		return contentType
	}
	if strings.TrimSpace(output.LocalPath) != "" {
		return "file"
	}
	return "text"
}

func readLocalFile(path string) (localFile, error) {
	return loadLocalFile(path, false)
}

func loadLocalFile(path string, allowAbsoluteOutside bool) (localFile, error) {
	workspace, err := filepath.EvalSymlinks(mustAbs("."))
	if err != nil {
		return localFile{}, fmt.Errorf("resolve current workspace: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(mustAbs(path))
	if err != nil {
		return localFile{}, fmt.Errorf("resolve local_path: %w", err)
	}
	if !filepath.IsAbs(path) || !allowAbsoluteOutside {
		relative, err := filepath.Rel(workspace, resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return localFile{}, errors.New("local_path must stay inside the current workspace")
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return localFile{}, err
	}
	if !info.Mode().IsRegular() {
		return localFile{}, errors.New("local_path must be a regular file")
	}
	if info.Size() > maxLocalArtifactBytes {
		return localFile{}, errors.New("local file exceeds 20 MiB")
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return localFile{}, err
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(resolved)))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	sum := sha256.Sum256(data)
	return localFile{Name: filepath.Base(resolved), MIMEType: contentType, Size: len(data),
		Hash: "sha256:" + hex.EncodeToString(sum[:]), Base64: base64.StdEncoding.EncodeToString(data)}, nil
}

func encodeLocalFile(path, caption string) (map[string]any, error) {
	file, err := loadLocalFile(path, true)
	if err != nil {
		return nil, err
	}
	value := map[string]any{
		"storage": "inline_base64", "name": file.Name, "mime_type": file.MIMEType,
		"size": file.Size, "sha256": file.Hash, "content_base64": file.Base64,
	}
	if strings.TrimSpace(caption) != "" {
		value["caption"] = strings.TrimSpace(caption)
	}
	return value, nil
}

func mustAbs(path string) string {
	value, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return value
}

func artifactContent(artifact Artifact) ([]mcp.Content, bool) {
	value, ok := artifact.Value.(map[string]any)
	if !ok || value["storage"] != "inline_base64" {
		return nil, false
	}
	encoded, _ := value["content_base64"].(string)
	mimeType, _ := value["mime_type"].(string)
	if encoded == "" || !strings.HasPrefix(mimeType, "image/") {
		return nil, false
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false
	}
	metadata, _ := json.Marshal(map[string]any{
		"artifact_id": artifact.ID, "slot": artifact.Slot, "revision": artifact.Revision,
		"name": value["name"], "mime_type": mimeType, "size": value["size"], "sha256": value["sha256"],
	})
	return []mcp.Content{&mcp.TextContent{Text: string(metadata)}, &mcp.ImageContent{Data: data, MIMEType: mimeType}}, true
}

func inputContent(resource InputResource) ([]mcp.Content, bool) {
	if resource.ContentBase64 == "" || !strings.HasPrefix(resource.MIMEType, "image/") {
		return nil, false
	}
	data, err := base64.StdEncoding.DecodeString(resource.ContentBase64)
	if err != nil {
		return nil, false
	}
	metadata, _ := json.Marshal(map[string]any{"resource_id": resource.ResourceID, "name": resource.Name,
		"mime_type": resource.MIMEType, "size": resource.Size, "content_hash": resource.ContentHash, "revision": resource.Revision})
	return []mcp.Content{&mcp.TextContent{Text: string(metadata)}, &mcp.ImageContent{Data: data, MIMEType: resource.MIMEType}}, true
}
