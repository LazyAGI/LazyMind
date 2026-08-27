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
	"workflow.step.resume",
	"workflow.step.submit",
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
		Description: "Read a published Workflow package, compiled graph, immutable revision and execution contract.", Annotations: readOnly},
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
		Description: "Read one immutable LazyMind Workflow input resource. Images are also returned as native MCP image content.", Annotations: readOnly},
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
		Description: "Create a durable Workflow session in the current external-Agent conversation. A prior completed, failed, or stopped session is archived atomically; if one is active or waiting, list and stop that current session before retrying. LazyMind pins the revision and owns all subsequent state and versions.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input StartInput) (*mcp.CallToolResult, StartResult, error) {
			value, err := client.Start(ctx, input)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.state", Title: "Read LazyMind Workflow state",
		Description: "Read authoritative Workflow readiness, attempts and completion state. Use this before choosing the next step.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input StateInput) (*mcp.CallToolResult, Projection, error) {
			value, err := client.State(ctx, input.SessionID)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.session.list", Title: "List external-Agent Workflow sessions",
		Description: "List durable Workflow sessions for the current external-Agent conversation. Use after an Agent restart to recover that conversation's session ID, then call workflow.state.", Annotations: readOnly},
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
		Description: "Resume a stopped Workflow session so its interrupted step can be begun again under Runtime rules. Safe to retry with the same command_id.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input SessionLifecycleInput) (*mcp.CallToolResult, SessionLifecycleResult, error) {
			value, err := client.ResumeSession(ctx, input.SessionID, input.CommandID)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.step.begin", Title: "Begin a LazyMind Workflow step",
		Description: "Reserve one currently ready step and return its immutable execution contract. Execute that contract with your native Agent tools, then call workflow.step.submit.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input BeginInput) (*mcp.CallToolResult, BeginResult, error) {
			value, err := client.Begin(ctx, input)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.step.resume", Title: "Resume a LazyMind Workflow step",
		Description: "Reclaim the same in-progress external execution after an Agent or connector restart and return the unchanged step contract.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input ResumeInput) (*mcp.CallToolResult, BeginResult, error) {
			value, err := client.Resume(ctx, input)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.step.submit", Title: "Submit a LazyMind Workflow step",
		Description: "Return the external Agent outcome and declared artifacts to LazyMind. LazyMind validates required outputs, versions artifacts and advances authoritative state.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input SubmitInput) (*mcp.CallToolResult, SubmitResult, error) {
			artifacts, err := encodeOutputs(input.Outputs)
			if err != nil {
				return nil, SubmitResult{}, err
			}
			value, err := client.Submit(ctx, input, artifacts)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.artifact.list", Title: "List LazyMind Workflow artifacts",
		Description: "List the selected artifact revisions currently owned by a Workflow session.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input ArtifactListInput) (*mcp.CallToolResult, any, error) {
			value, err := client.ListArtifacts(ctx, input.SessionID)
			return nil, ArtifactListResult{Artifacts: value}, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.artifact.get", Title: "Get a LazyMind Workflow artifact",
		Description: "Read one immutable artifact revision. Inline images are also returned as native MCP image content.", Annotations: readOnly},
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

func encodeOutputs(outputs []Output) ([]map[string]any, error) {
	values := make([]map[string]any, 0, len(outputs))
	nextSequence := make(map[string]int)
	for _, output := range outputs {
		slot := strings.TrimSpace(output.Slot)
		if slot == "" {
			return nil, errors.New("every output requires a slot")
		}
		if output.LocalPath != "" && output.Value != nil {
			return nil, fmt.Errorf("output %q must use either local_path or value, not both", output.Slot)
		}
		var value any
		contentType := strings.TrimSpace(output.ContentType)
		if output.LocalPath != "" {
			encoded, detected, err := encodeLocalFile(output.LocalPath, output.Caption)
			if err != nil {
				return nil, fmt.Errorf("output %q: %w", output.Slot, err)
			}
			value, contentType = encoded, detected
		} else {
			if output.Value == nil {
				return nil, fmt.Errorf("output %q requires local_path or value", output.Slot)
			}
			value = output.Value
			if contentType == "" {
				contentType = "application/json"
			}
		}
		seq := output.Seq
		if seq < 1 {
			seq = nextSequence[slot] + 1
		}
		if seq > nextSequence[slot] {
			nextSequence[slot] = seq
		}
		values = append(values, map[string]any{"slot": slot, "content_type": contentType, "value": value, "seq": seq})
	}
	return values, nil
}

type localFile struct {
	Name     string
	MIMEType string
	Size     int
	Hash     string
	Base64   string
}

func readLocalFile(path string) (localFile, error) {
	workspace, err := filepath.EvalSymlinks(mustAbs("."))
	if err != nil {
		return localFile{}, fmt.Errorf("resolve current workspace: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(mustAbs(path))
	if err != nil {
		return localFile{}, fmt.Errorf("resolve local_path: %w", err)
	}
	relative, err := filepath.Rel(workspace, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return localFile{}, errors.New("local_path must stay inside the current workspace")
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

func encodeLocalFile(path, caption string) (map[string]any, string, error) {
	file, err := readLocalFile(path)
	if err != nil {
		return nil, "", err
	}
	value := map[string]any{
		"storage": "inline_base64", "name": file.Name, "mime_type": file.MIMEType,
		"size": file.Size, "sha256": file.Hash, "content_base64": file.Base64,
	}
	if strings.TrimSpace(caption) != "" {
		value["caption"] = strings.TrimSpace(caption)
	}
	return value, file.MIMEType, nil
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
