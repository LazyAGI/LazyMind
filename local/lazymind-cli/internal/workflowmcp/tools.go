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
	"workflow.action.get", "workflow.action.respond", "workflow.action.cancel", "workflow.edit.context", "workflow.edit.commit",
	"workflow.list",
	"workflow.get",
	"workflow.input.import",
	"workflow.input.get",
	"workflow.input.answer",
	"workflow.start",
	"workflow.state",
	"workflow.session.list",
	"workflow.session.stop",
	"workflow.session.resume",
	"workflow.step.begin",
	"workflow.step.claim",
	"workflow.step.resume",
	"workflow.step.complete",
	"workflow.tool.list",
	"workflow.tool.call",
	"workflow.artifact.publish",
	"workflow.artifact.list",
	"workflow.artifact.get",
}

func IsReadOnlyTool(name string) bool {
	switch name {
	case "workflow.action.get", "workflow.edit.context", "workflow.list", "workflow.get", "workflow.input.get", "workflow.state", "workflow.session.list",
		"workflow.artifact.list", "workflow.artifact.get", "workflow.tool.list":
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
	LocalPath string `json:"local_path" jsonschema:"required,Absolute regular-file path on the Connector machine; resolve or download host attachments first"`
}

type InputGetInput struct {
	ResultDirectory string `json:"result_directory,omitempty" jsonschema:"Existing absolute Connector-local directory; save full resource JSON privately and return file references instead of source content"`
	ResourceID      string `json:"resource_id" jsonschema:"required,Immutable LazyMind input resource identifier"`
}

type InputGetResult struct {
	ResourceFile map[string]any `json:"resource_file,omitempty"`
	Resource     InputResource  `json:"resource"`
	Rendered     bool           `json:"rendered_as_mcp_content,omitempty"`
}

type ArtifactListInput struct {
	MetadataOnly bool   `json:"metadata_only,omitempty" jsonschema:"Return artifact identifiers and revision metadata without artifact values; use for controller routing and final inventory"`
	SessionID    string `json:"session_id" jsonschema:"required,Workflow session identifier"`
}

type ArtifactListResult struct {
	Artifacts []Artifact `json:"artifacts"`
}

type ArtifactGetInput struct {
	ResultDirectory string `json:"result_directory,omitempty" jsonschema:"Existing absolute Connector-local directory; save full artifact JSON privately and return file references for the executing worker"`
	ArtifactID      string `json:"artifact_id" jsonschema:"required,Artifact revision identifier"`
}

type ArtifactGetResult struct {
	ArtifactFile map[string]any `json:"artifact_file,omitempty"`
	Artifact     Artifact       `json:"artifact"`
	Rendered     bool           `json:"rendered_as_mcp_content,omitempty"`
}

const workflowStartDescription = "Start a revision-bound Workflow with request_context, known clarification_values and declared file inputs. Get input IDs and types from workflow.get. Files use absolute Connector-local paths or existing resource references and are bound before creation. needs_input returns questions and preparation guidance. started returns session_id and state. Reuse idempotency_key for identical retries and use a new key after changing answers or inputs. The host displays the panel for the bound conversation after session creation."

func Register(server *mcp.Server, client *Client) {
	readOnly, write := annotations(true), annotations(false)
	registerExecutionTools(server, client)
	registerActionTools(server, client)
	registerEditTools(server, client)
	registerInputAnswer(server, client)
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.list", Title: "List LazyMind Workflows",
		Description: "List published Workflows with names, descriptions, selection guidance and revision IDs. Use workflow.get for startup requirements.", Annotations: readOnly,
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			value, err := client.List(ctx)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.get", Title: "Get a LazyMind Workflow",
		Description: "Read a published Workflow revision and its startup_contract. Use exact input_materials IDs and clarification_fields when starting. The current step contract is returned by workflow.step.begin/claim.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input GetInput) (*mcp.CallToolResult, map[string]any, error) {
			value, err := client.Get(ctx, input.WorkflowID, input.RevisionID)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.input.import", Title: "Import a Workflow input",
		Description: "Import an absolute Connector-local file as an immutable resource. Returns a resource_id for startup bindings or declared tool inputs; import alone does not attach it to a run. Resolve host attachments or downloads to readable files first. For starting with files, workflow.start inputs imports and binds them together.", Annotations: write},
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
			if input.ResultDirectory != "" {
				ref, err := saveJSONValue(input.ResultDirectory, InputGetResult{Resource: resource})
				return nil, InputGetResult{ResourceFile: ref}, err
			}
			content, rendered := inputContent(resource)
			if rendered {
				resource.ContentBase64 = ""
				return &mcp.CallToolResult{Content: content}, InputGetResult{Resource: resource, Rendered: true}, nil
			}
			return nil, InputGetResult{Resource: resource}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.start", Title: "Start a LazyMind Workflow",
		Description: workflowStartDescription, Annotations: write},
		func(ctx context.Context, request *mcp.CallToolRequest, input StartInput) (*mcp.CallToolResult, StartResult, error) {
			if client.ResolveDriver != nil && client.RequireHostBinding {
				var err error
				input.DriverSessionID, err = client.ResolveDriver(ctx, request, input.DriverSessionID)
				if err != nil {
					return nil, StartResult{}, err
				}
			}
			value, err := client.Start(ctx, input)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.state", Title: "Read LazyMind Workflow state",
		Description: "Read authoritative Workflow state, ready/retryable/rewindable steps, admission and current action guidance. Follow the returned agent_instruction. User input and review gates are decided by Core.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input StateInput) (*mcp.CallToolResult, Projection, error) {
			value, err := client.State(ctx, input.SessionID)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.session.list", Title: "List external-Agent Workflow sessions",
		Description: "Recover Workflow session IDs after restart. Verify the returned conversation binding, then read workflow.state for the selected run.", Annotations: readOnly},
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
		Description: "Resume a stopped Workflow using the same control rules as the Workflow panel. Human review remains required when pending. A returned execution_id is already scheduled: use workflow.step.claim to inspect or claim it, never begin a duplicate. Otherwise read workflow.state for the next action. Safe to retry with the same command_id.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input SessionLifecycleInput) (*mcp.CallToolResult, SessionLifecycleResult, error) {
			value, err := client.ResumeSession(ctx, input.SessionID, input.CommandID)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.step.begin", Title: "Begin a LazyMind Workflow step",
		Description: "Begin a returned ready, retryable or rewindable step when admission.can_begin is true and receive its pinned contract and execution_handle. Reuse command_id with identical arguments after an uncertain result. User-requested retries use retry_origin=user. Native execution returns executor_host=lazymind without a handle. contract_directory writes the complete contract to a local handoff file. Legacy native wrapper names describe operations to fulfill with host capabilities or declared tools.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input BeginInput) (*mcp.CallToolResult, BeginResult, error) {
			value, err := withContractFile(input.ContractDirectory, func() (BeginResult, error) { return client.Begin(ctx, input) })
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.step.claim", Title: "Claim a prepared Workflow execution",
		Description: "Claim an existing execution_id and receive its contract and handle. Use this for an already scheduled execution. Native execution returns status without a handle.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input ResumeInput) (*mcp.CallToolResult, BeginResult, error) {
			value, err := withContractFile(input.ContractDirectory, func() (BeginResult, error) { return client.Claim(ctx, input) })
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.step.resume", Title: "Resume a LazyMind Workflow step",
		Description: "Recover an in-progress external execution after restart or lease expiry. Returns the same execution with a new handle. Use that handle for subsequent calls and reuse saved results. Native execution returns status only.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input ResumeInput) (*mcp.CallToolResult, BeginResult, error) {
			value, err := withContractFile(input.ContractDirectory, func() (BeginResult, error) { return client.Resume(ctx, input) })
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.step.complete", Title: "Complete a LazyMind Workflow step",
		Description: "Complete an external step after all required artifact publications are acknowledged. Send outcome, verification summary and the unchanged handle. executor_ref may identify a real executor or trace. Follow the returned state and review guidance. If state_unavailable is true, refresh workflow.state before continuing.", Annotations: write},
		func(ctx context.Context, _ *mcp.CallToolRequest, input CompleteInput) (*mcp.CallToolResult, CompleteResult, error) {
			value, err := client.Complete(ctx, input)
			if err != nil {
				return nil, value, err
			}
			if AwaitingReview(value.State) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: value.State.AgentInstruction}}}, value, nil
			}
			return nil, value, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.artifact.publish", Title: "Publish a Workflow artifact",
		Description: "Publish one output to a declared slot. For legacy save_artifact/save_artifacts instructions, key maps to slot. Use local_path for binary files, value_file for text/JSON files, or value inline. Use a stable positive seq per slot and reuse it with the same payload on retry. Follow the contract publication order and await acknowledgements before completing the step. Publication alone does not complete a step.", Annotations: write},
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
			if input.MetadataOnly {
				for i := range value {
					value[i].Value = nil
				}
			}
			return nil, ArtifactListResult{Artifacts: value}, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "workflow.artifact.get", Title: "Get a LazyMind Workflow artifact",
		Description: "Read an immutable artifact by artifact_id from step inputs or workflow.artifact.list. A slot key must first be resolved to an artifact ID. Images are returned as MCP image content. result_directory saves files locally for handoff.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input ArtifactGetInput) (*mcp.CallToolResult, any, error) {
			artifact, err := client.GetArtifact(ctx, input.ArtifactID)
			if err != nil {
				return nil, nil, err
			}
			if input.ResultDirectory != "" {
				ref, err := saveJSONValue(input.ResultDirectory, ArtifactGetResult{Artifact: artifact})
				return nil, ArtifactGetResult{ArtifactFile: ref}, err
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
	if output.ValueFile != "" {
		if output.Value != nil || output.LocalPath != "" || (output.ContentType != "text" && output.ContentType != "json") {
			return nil, errors.New("value_file requires text or json content_type and cannot be combined with value or local_path")
		}
		raw, err := readValueFile(output.ValueFile)
		if err != nil {
			return nil, err
		}
		if output.ContentType == "text" {
			output.Value = string(raw)
		} else if json.Unmarshal(raw, &output.Value) != nil {
			return nil, errors.New("value_file is not valid JSON")
		}
	}
	slot := strings.TrimSpace(output.Slot)
	if slot == "" || output.Seq < 1 {
		return nil, errors.New("output requires a slot and stable positive seq")
	}
	if output.LocalPath != "" && output.Value != nil {
		return nil, fmt.Errorf("output %q must use either local_path or value, not both", slot)
	}
	if nested, ok := output.Value.(map[string]any); ok && nested["local_path"] != nil {
		return nil, fmt.Errorf("output %q: local_path must be at output.local_path, not inside value; otherwise the file is not uploaded", slot)
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
	return readLocalInputFile(path)
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
