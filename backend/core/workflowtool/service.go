// Package workflowtool exposes tools scoped to one external Workflow attempt.
// It stores invocation receipts, not model decisions or generated content.
package workflowtool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/capability"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/externalcapability"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/executor"
	workflowstore "lazymind/core/workflow/store"
)

const maxPackageBytes = 64 << 20
const maxResultBytes = 32 << 20
const maxToolExecutionDuration = 5 * time.Minute
const unresolvedToolGracePeriod = time.Minute

type Tool struct {
	Name        string         `json:"name"`
	Source      string         `json:"source"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Available   bool           `json:"available"`
	Reason      string         `json:"reason,omitempty"`
}

type CallInput struct {
	ExecutionHandle string         `json:"execution_handle"`
	ToolName        string         `json:"tool_name"`
	Source          string         `json:"source,omitempty"`
	Arguments       map[string]any `json:"arguments"`
	CommandID       string         `json:"command_id"`
}

type ToolError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *ToolError) Error() string { return e.Code + ": " + e.Message }

type Result struct {
	Result any        `json:"result,omitempty"`
	Error  *ToolError `json:"error,omitempty"`
}

type Service struct {
	DB         *gorm.DB
	HTTPClient *http.Client
	Endpoint   string
}

func (s *Service) client() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return &http.Client{Timeout: maxToolExecutionDuration}
}

func unavailable(message string) error {
	return &ToolError{Code: "CAPABILITY_UNAVAILABLE", Message: message + "; configure the corresponding feature in LazyMind, then retry the affected step"}
}

// Capability errors expose a deliberate public message. Preserve actionable
// input/permission guidance, but never serialize internal causes or details.
func publicToolError(err error) *ToolError {
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		return toolErr
	}
	var capabilityErr *capability.Error
	if errors.As(err, &capabilityErr) {
		switch capabilityErr.Code {
		case capability.InvalidArgument, capability.Unauthenticated, capability.PermissionDenied,
			capability.NotFound, capability.DeadlineExceeded, capability.Unavailable,
			capability.ResultTooLarge, capability.Unsupported, capability.Conflict, capability.Internal:
			return &ToolError{Code: string(capabilityErr.Code), Message: capabilityErr.Message, Retryable: capabilityErr.Retryable}
		}
	}
	return &ToolError{Code: "CAPABILITY_UNAVAILABLE", Message: "Tool execution failed; check the corresponding configuration and dependency in LazyMind, then retry the affected step"}
}

func (s *Service) invocation(ctx context.Context, owner, sessionID, attemptID string) (capability.InvocationContext, error) {
	var session orm.WorkflowSession
	if err := s.DB.WithContext(ctx).Where("id = ? AND create_user_id = ?", sessionID, owner).First(&session).Error; err != nil {
		return capability.InvocationContext{}, err
	}
	binding, err := controlstore.DecodeBinding(session)
	if err != nil {
		return capability.InvocationContext{}, err
	}
	provider, err := externalcapability.NormalizeAgent(binding.Provider)
	if err != nil {
		return capability.InvocationContext{}, controlstore.Reject("BINDING_REQUIRED", "a supported external Agent binding is required to call global tools")
	}
	return capability.InvocationContext{Principal: capability.Principal{UserID: owner}, ExternalAgent: provider,
		ConversationID: session.ConversationID, ExecutionKey: attemptID}, nil
}

func (s *Service) List(ctx context.Context, owner, sessionID, attemptID, handle string) ([]Tool, error) {
	contract, err := executor.AuthorizeExternalExecution(ctx, s.DB, owner, sessionID, attemptID, handle)
	if err != nil {
		return nil, err
	}
	result := []Tool{}
	if contract.Execution == nil {
		return result, nil
	}
	var packageTools map[string]Tool
	var globalTools map[string]capability.ExternalToolSummary
	var packageErr, globalErr error
	for _, declared := range contract.Execution.Tools {
		item := Tool{Name: declared.Name, Source: declared.Source}
		switch declared.Source {
		case "package":
			if adapter, ok := academicTool(declared.Name); ok {
				item = adapter
				if err := s.validateAcademicBinding(ctx, contract); err != nil {
					item.Available, item.Reason = false, err.Error()
				}
				break
			}
			if adapter, ok := knowledgeTool(declared.Name); ok {
				item = adapter
				if err := s.validateKnowledgeBinding(ctx, contract); err != nil {
					item.Available, item.Reason = false, err.Error()
				}
				break
			}
			if packageTools == nil {
				packageTools = map[string]Tool{}
				var response struct {
					Tools []Tool `json:"tools"`
				}
				packageErr = s.packageRequest(ctx, contract, "list", "", nil, &response)
				for _, tool := range response.Tools {
					packageTools[tool.Name] = tool
				}
			}
			if tool, ok := packageTools[declared.Name]; ok && packageErr == nil {
				item.Description, item.InputSchema, item.Available, item.Reason = tool.Description, tool.InputSchema, tool.Available, tool.Reason
			} else {
				item.Reason = "Package tool is unavailable; check its adapter and dependencies in LazyMind"
			}
		case "global":
			if globalTools == nil {
				globalTools = map[string]capability.ExternalToolSummary{}
				var call capability.InvocationContext
				call, globalErr = s.invocation(ctx, owner, sessionID, attemptID)
				if globalErr == nil {
					var catalog capability.ListExternalToolsResult
					catalog, globalErr = externalcapability.New(s.DB, s.client()).ListExternalTools(ctx, call)
					for _, tool := range catalog.Items {
						globalTools[tool.ID] = tool
					}
				}
			}
			if tool, ok := globalTools[globalID(declared.Name)]; ok && globalErr == nil {
				item.Description, item.InputSchema, item.Available = tool.Description, tool.InputSchema, true
			} else if declared.Name == "builtin:multimodal" && globalErr == nil {
				item.Reason = "Configure a verified VLM or vision-enabled main model in LazyMind, enable multimodal for the connected Agent, then retry"
			} else {
				item.Reason = "Configure and enable this capability for the connected Agent in LazyMind, then retry"
			}
		}
		result = append(result, withFileResourceSchema(item))
	}
	if _, err = executor.AuthorizeExternalExecution(ctx, s.DB, owner, sessionID, attemptID, handle); err != nil {
		return nil, err
	}
	return result, nil
}

func globalID(name string) string {
	// Only the documented built-in aliases are expanded. Opaque IDs (including
	// custom tools) must reach the capability service without reinterpretation.
	switch name {
	case "image_generator", "image_editor", "video_generator", "speech_to_text", "text_to_speech":
		return "builtin:" + name
	}
	return name
}

// Call reserves an immutable request before executing. A lost response never
// causes a second paid/media call: the same command either replays its receipt
// or reports an unresolved invocation. No database lock spans tool execution.
func (s *Service) Call(ctx context.Context, owner, sessionID, attemptID string, input CallInput) (Result, error) {
	if len(strings.TrimSpace(input.CommandID)) < 1 || len(input.CommandID) > 256 || input.ToolName == "" {
		return Result{}, controlstore.Reject("INVALID_TOOL_CALL", "tool_name and a stable command_id of at most 256 characters are required")
	}
	contract, declared, err := executor.AuthorizeExternalToolCall(ctx, s.DB, owner, sessionID, attemptID, input.ExecutionHandle, input.ToolName)
	if err != nil {
		return Result{}, err
	}
	source := declared.Source
	if input.Source != "" && input.Source != source {
		return Result{}, controlstore.Reject("EXTERNAL_TOOL_UNDECLARED", "tool source does not match the pinned contract")
	}
	canonical, err := json.Marshal(map[string]any{"source": source, "tool_name": input.ToolName, "arguments": input.Arguments, "revision": contract.WorkflowRevision})
	if err != nil {
		return Result{}, err
	}
	commandID := "workflow-tool:" + controlstore.Hash([]byte(attemptID+":"+input.CommandID))
	row := orm.WorkflowCommand{CommandID: commandID, OwnerUserID: owner, SessionID: sessionID,
		ContractVersion: "workflow.tool.v1", RequestHash: controlstore.Hash(canonical), HTTPStatus: http.StatusAccepted,
		ResponseJSON: json.RawMessage(`{}`), CreatedAt: time.Now().UTC()}
	reservation := s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if reservation.Error != nil {
		return Result{}, reservation.Error
	}
	if reservation.RowsAffected == 0 {
		var previous orm.WorkflowCommand
		if err := s.DB.WithContext(ctx).Where("command_id = ?", commandID).First(&previous).Error; err != nil {
			return Result{}, err
		}
		if previous.RequestHash != row.RequestHash || previous.OwnerUserID != owner || previous.SessionID != sessionID {
			return Result{}, controlstore.Reject("COMMAND_CONFLICT", "tool command was already used with different arguments")
		}
		if previous.HTTPStatus == http.StatusAccepted {
			if time.Since(previous.CreatedAt) >= maxToolExecutionDuration+unresolvedToolGracePeriod {
				return Result{}, &ToolError{Code: "TOOL_OUTCOME_UNKNOWN", Message: "The tool call exceeded its recovery window and its outcome is unknown. Stop automatic retries, inspect any existing provider results, and obtain user confirmation before retrying as a new operation; this command will never execute again"}
			}
			return Result{}, &ToolError{Code: "TOOL_CALL_IN_PROGRESS", Message: "The tool call is running or its outcome is uncertain; retry this same command to retrieve its receipt", Retryable: true}
		}
		var replay Result
		if err := json.Unmarshal(previous.ResponseJSON, &replay); err != nil {
			return Result{}, err
		}
		return s.deliver(ctx, owner, sessionID, attemptID, input, replay)
	}
	var result Result
	// Bound discovery plus execution together, including global tools whose
	// operation may make multiple HTTP requests before its receipt is saved.
	callCtx, cancelCall := context.WithTimeout(ctx, maxToolExecutionDuration)
	defer cancelCall()
	switch source {
	case "package":
		if _, ok := academicTool(input.ToolName); ok {
			result, err = s.callAcademic(callCtx, owner, contract, input.ToolName, input.Arguments)
		} else if _, ok := knowledgeTool(input.ToolName); ok {
			result, err = s.callKnowledge(callCtx, owner, contract, input.ToolName, input.Arguments)
		} else {
			var arguments map[string]any
			arguments, err = s.resolveFileArguments(callCtx, owner, contract, input.ToolName, input.Arguments)
			if err == nil {
				err = s.packageRequest(callCtx, contract, "call", input.ToolName, arguments, &result)
			}
		}
	case "global":
		var call capability.InvocationContext
		call, err = s.invocation(callCtx, owner, sessionID, attemptID)
		if err == nil {
			call.InvocationID, call.OperationKey = commandID, input.CommandID
			var value capability.InvokeExternalToolResult
			value, err = externalcapability.New(s.DB, s.client()).InvokeExternalTool(callCtx, call, capability.InvokeExternalToolInput{ToolID: globalID(input.ToolName), Arguments: input.Arguments})
			result.Result = value.Result
		}
	}
	if err != nil {
		result = Result{Error: publicToolError(err)}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return Result{}, err
	}
	if len(encoded) > maxResultBytes {
		result = Result{Error: &ToolError{Code: "TOOL_RESULT_TOO_LARGE", Message: "Tool result exceeds the supported resource transport limit"}}
		encoded, _ = json.Marshal(result)
	}
	// Record the tool outcome independently of delivery. A disconnected client
	// or an unavailable authorization query must not overwrite a real result.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.DB.WithContext(saveCtx).Model(&orm.WorkflowCommand{}).Where("command_id = ?", commandID).Updates(map[string]any{"http_status": http.StatusOK, "response_json": json.RawMessage(encoded)}).Error; err != nil {
		return Result{}, err
	}
	return s.deliver(saveCtx, owner, sessionID, attemptID, input, result)
}

func (s *Service) deliver(ctx context.Context, owner, sessionID, attemptID string, input CallInput, result Result) (Result, error) {
	// Check again at delivery, including replay, because the handle can be
	// revoked while the provider is running or while a receipt is being read.
	_, _, err := executor.AuthorizeExternalToolCall(ctx, s.DB, owner, sessionID, attemptID, input.ExecutionHandle, input.ToolName)
	if err != nil {
		var denied *controlstore.Error
		if errors.As(err, &denied) || errors.Is(err, workflowstore.ErrPermissionDenied) || errors.Is(err, workflowstore.ErrNotFound) {
			return Result{Error: &ToolError{Code: "EXECUTION_FENCED", Message: "Execution authorization changed while the tool ran; its result was not delivered"}}, nil
		}
		return Result{}, &ToolError{Code: "TOOL_DELIVERY_UNAVAILABLE", Message: "The tool outcome is saved. Retry the same command to check execution authorization and retrieve it.", Retryable: true}
	}
	return result, nil
}

func (s *Service) packageRequest(ctx context.Context, contract executor.AttemptContext, operation, name string, arguments map[string]any, output any) error {
	var revision orm.WorkflowRevision
	if err := s.DB.WithContext(ctx).Where("id = ?", contract.WorkflowRevision).First(&revision).Error; err != nil {
		return err
	}
	var entries []orm.WorkflowRevisionEntry
	if err := s.DB.WithContext(ctx).Where("revision_id = ? AND entry_type = ?", revision.ID, "file").Order("path").Find(&entries).Error; err != nil {
		return err
	}
	files := map[string]string{}
	size := 0
	for _, entry := range entries {
		if path.IsAbs(entry.Path) || path.Clean(entry.Path) != entry.Path || strings.HasPrefix(entry.Path, "../") || strings.Contains(entry.Path, "\\") || entry.BlobHash == nil {
			return errors.New("invalid package entry")
		}
		var blob orm.WorkflowBlob
		if err := s.DB.WithContext(ctx).Where("hash = ?", *entry.BlobHash).First(&blob).Error; err != nil {
			return err
		}
		digest := sha256.Sum256(blob.Content)
		if hex.EncodeToString(digest[:]) != *entry.BlobHash {
			return errors.New("package blob hash mismatch")
		}
		size += len(blob.Content)
		if size > maxPackageBytes {
			return errors.New("package code exceeds tool execution limit")
		}
		files[entry.Path] = base64.StdEncoding.EncodeToString(blob.Content)
	}
	payload := map[string]any{"workflow_id": revision.WorkflowResourceID, "revision_id": revision.ID, "tree_hash": revision.TreeHash,
		"step_id": contract.StepID, "package": map[string]any{"files": files, "tree_hash": revision.TreeHash}}
	if operation == "call" {
		payload["tool_name"], payload["arguments"] = name, arguments
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	base := s.Endpoint
	if base == "" {
		base = common.ChatServiceEndpoint()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, common.JoinURL(base, "/api/workflow/external-tools/"+operation), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-LazyMind-Internal-Token", os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	response, err := s.client().Do(request)
	if err != nil {
		return unavailable("The workflow tool service is unavailable")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResultBytes+1))
	if err != nil || len(raw) > maxResultBytes {
		return &ToolError{Code: "TOOL_RESULT_TOO_LARGE", Message: "Tool result is unreadable or exceeds the supported resource transport limit"}
	}
	if response.StatusCode != http.StatusOK {
		type packageError struct {
			Code          string `json:"code"`
			PublicMessage string `json:"public_message"`
		}
		var envelope struct {
			Error  *packageError `json:"error"`
			Detail *packageError `json:"detail"`
		}
		if json.Unmarshal(raw, &envelope) == nil {
			failure := envelope.Error
			if failure == nil {
				failure = envelope.Detail
			}
			if failure != nil {
				switch failure.Code {
				case "DEPENDENCY_MISSING", "TOOL_ADAPTER_UNAVAILABLE":
					return &ToolError{Code: failure.Code, Message: "The workflow tool requires a configured capability or installed dependency in LazyMind; configure it, then retry the affected step"}
				case "INVALID_ARGUMENT":
					if message := strings.TrimSpace(failure.PublicMessage); message != "" && len(message) <= 1024 {
						return &ToolError{Code: failure.Code, Message: message}
					}
					fallthrough
				case "INVALID_PACKAGE", "TOOL_NOT_ALLOWED":
					return &ToolError{Code: failure.Code, Message: "The workflow tool rejected the pinned package or arguments; check its contract before retrying"}
				}
			}
		}
		return unavailable(fmt.Sprintf("The workflow tool could not execute (HTTP %d)", response.StatusCode))
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return errors.New("invalid workflow tool response")
	}
	return nil
}
