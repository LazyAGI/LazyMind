package mcpbridge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/agentconnector/internal/coreapi"
)

const (
	connectorName    = "lazymind-mcp"
	connectorVersion = "v2"
	finishTimeout    = 5 * time.Second
)

type invocationRecorder interface {
	StartInvocation(context.Context, string, coreapi.InvocationStart) (coreapi.InvocationStartResult, error)
	FinishInvocation(context.Context, string, coreapi.InvocationFinish) error
}

func invocationMiddleware(recorder invocationRecorder, connectorInstanceID string, readOnlyTools map[string]bool) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, request)
			}
			call, ok := request.(*mcp.CallToolRequest)
			if !ok || call.Params == nil {
				return next(ctx, method, request)
			}
			invocationID, err := newInvocationID("inv-")
			if err != nil {
				return nil, fmt.Errorf("create LazyMind invocation ID: %w", err)
			}
			clientName, clientVersion := "unknown-mcp-client", ""
			if client := call.ClientInfo(); client != nil {
				clientName, clientVersion = strings.TrimSpace(client.Name), strings.TrimSpace(client.Version)
				if clientName == "" {
					clientName = "unknown-mcp-client"
				}
			}
			requestHash, requestSummary := summarizeArguments(call.Params.Arguments)
			start := coreapi.InvocationStart{
				ClientName: clientName, ClientVersion: clientVersion,
				ConnectorName: connectorName, ConnectorVersion: connectorVersion,
				ConnectorInstanceID: connectorInstanceID, ProtocolVersion: call.ProtocolVersion(),
				Transport: "stdio", ToolName: call.Params.Name, ReadOnly: readOnlyTools[call.Params.Name],
				RequestHash: requestHash, RequestSummary: requestSummary,
			}
			if strings.TrimSpace(os.Getenv("LAZYMIND_CONVERSATION_ID")) == "" &&
				strings.TrimSpace(os.Getenv("LAZYMIND_EXTERNAL_REF")) == "" {
				start.Source = invocationSource(clientName, call.Params.Meta, call.Params.Name, call.Params.Arguments)
			}
			started, err := recorder.StartInvocation(ctx, invocationID, start)
			if err != nil {
				return nil, fmt.Errorf("record LazyMind invocation before %s: %w", call.Params.Name, err)
			}
			conversationID, externalRef := "", ""
			if started.Source != nil {
				conversationID, externalRef = started.Source.ConversationID, started.Source.ExternalRef
			}
			callCtx := coreapi.WithInvocation(ctx, coreapi.InvocationMetadata{
				ID: invocationID, ClientName: clientName, ConnectorInstanceID: connectorInstanceID,
				ConversationID: conversationID, ExternalRef: externalRef,
			})
			result, callErr := next(callCtx, method, request)
			finish := finishEvidence(callErr, result, requestSummary)
			if finish.ExternalRef == "" {
				finish.ExternalRef = strings.TrimSpace(os.Getenv("LAZYMIND_EXTERNAL_REF"))
			}
			if finish.ExternalRef == "" {
				finish.ExternalRef = externalRef
			}
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
			finishErr := recorder.FinishInvocation(finishCtx, invocationID, finish)
			cancel()
			scheduleCodexTurnSync(recorder, start.Source)
			if finishErr != nil && result == nil && callErr == nil {
				return nil, fmt.Errorf("record LazyMind invocation result: %w", finishErr)
			}
			return result, callErr
		}
	}
}

func invocationSource(clientName string, metadata mcp.Meta, toolName string, arguments json.RawMessage) *coreapi.InvocationSource {
	provider := providerForClient(clientName)
	if provider == "" || len(metadata) == 0 {
		return nil
	}
	nested, _ := metadata["x-codex-turn-metadata"].(map[string]any)
	threadID := firstMetadataString(metadata, nested,
		"threadId", "thread_id", "conversationId", "conversation_id", "sessionId", "session_id")
	if threadID == "" {
		return nil
	}
	turnID := firstMetadataString(metadata, nested, "turnId", "turn_id", "callId", "call_id")
	threadSource := firstMetadataString(metadata, nested, "threadSource", "thread_source")
	message := ""
	if provider == "codex" {
		message = codexTurnUserMessage(threadID, turnID)
	}
	if message == "" {
		message = invocationSourceMessage(toolName, arguments)
	}
	return &coreapi.InvocationSource{
		Provider: provider, ThreadID: threadID, TurnID: turnID, ThreadSource: threadSource,
		Message: message,
	}
}

func invocationSourceMessage(toolName string, arguments json.RawMessage) string {
	var values map[string]any
	if json.Unmarshal(arguments, &values) != nil {
		return ""
	}
	keys := []string{}
	switch toolName {
	case "workflow.start":
		keys = []string{"request_context"}
	case "knowledge.search", "cloud_document.search":
		keys = []string{"query"}
	}
	for _, key := range keys {
		if value, ok := values[key].(string); ok {
			value = strings.TrimSpace(value)
			if len([]rune(value)) > 8192 {
				value = string([]rune(value)[:8192])
			}
			return value
		}
	}
	return ""
}

func providerForClient(clientName string) string {
	value := strings.ToLower(strings.TrimSpace(clientName))
	switch {
	case strings.Contains(value, "codex"):
		return "codex"
	case strings.Contains(value, "cursor"):
		return "cursor"
	case strings.Contains(value, "workbuddy"):
		return "workbuddy"
	case strings.Contains(value, "trae"):
		return "trae-work"
	case strings.Contains(value, "deepseek"), strings.Contains(value, "dsh"):
		return "deepseek-harness"
	default:
		return ""
	}
}

func firstMetadataString(primary, secondary map[string]any, keys ...string) string {
	for _, key := range keys {
		for _, values := range []map[string]any{primary, secondary} {
			if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func newInvocationID(prefix string) (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(bytes), nil
}

func summarizeArguments(arguments json.RawMessage) (string, json.RawMessage) {
	canonical := json.RawMessage(`{}`)
	var value any = map[string]any{}
	if len(arguments) > 0 && json.Unmarshal(arguments, &value) == nil {
		if encoded, err := json.Marshal(value); err == nil {
			canonical = encoded
		}
	} else if len(arguments) > 0 {
		canonical = append(json.RawMessage(nil), arguments...)
	}
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:]), summarizeValue(value)
}

func finishEvidence(callErr error, result mcp.Result, requestSummary json.RawMessage) coreapi.InvocationFinish {
	finish := coreapi.InvocationFinish{Status: "succeeded", ResultSummary: json.RawMessage(`{}`)}
	resultSummary := json.RawMessage(`{}`)
	if toolResult, ok := result.(*mcp.CallToolResult); ok && toolResult != nil {
		resultSummary = summarizeToolResult(toolResult)
		if toolResult.IsError {
			finish.Status, finish.ErrorCode = "failed", "MCP_TOOL_ERROR"
			if callErr == nil {
				callErr = toolResult.GetError()
			}
		}
	}
	if callErr != nil {
		finish.Status = "failed"
		finish.ErrorCode = "MCP_CALL_ERROR"
		if errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded) {
			finish.Status, finish.ErrorCode = "interrupted", "MCP_CALL_INTERRUPTED"
		}
		var coreErr *coreapi.Error
		if errors.As(callErr, &coreErr) {
			finish.ErrorCode, finish.Retryable = coreErr.Code, coreErr.Retryable
			if finish.ErrorCode == "" {
				finish.ErrorCode = fmt.Sprintf("CORE_HTTP_%d", coreErr.StatusCode)
			}
		}
	}
	finish.ResultSummary = resultSummary
	references := mergeEvidence(requestSummary, resultSummary)
	finish.WorkflowID = evidenceString(references, "workflow_id")
	finish.SessionID = evidenceString(references, "session_id")
	finish.StepID = evidenceString(references, "step_id")
	finish.AttemptID = firstEvidenceString(references, "attempt_id", "execution_id", "producer_attempt_id")
	finish.ResourceID = firstEvidenceString(references, "resource_id", "document_id", "knowledge_id", "skill_id")
	finish.ArtifactID = evidenceString(references, "artifact_id")
	finish.CommandID = evidenceString(references, "command_id")
	finish.ExternalRef = firstEvidenceString(references, "external_ref", "executor_ref")
	return finish
}

func summarizeToolResult(result *mcp.CallToolResult) json.RawMessage {
	if result.StructuredContent != nil {
		return summarizeValue(result.StructuredContent)
	}
	for _, content := range result.Content {
		text, ok := content.(*mcp.TextContent)
		if !ok || len(text.Text) > 64<<10 {
			continue
		}
		var value any
		if json.Unmarshal([]byte(text.Text), &value) == nil {
			return summarizeValue(value)
		}
	}
	return json.RawMessage(`{}`)
}

func summarizeValue(value any) json.RawMessage {
	switch encoded := value.(type) {
	case json.RawMessage:
		var decoded any
		if json.Unmarshal(encoded, &decoded) == nil {
			value = decoded
		}
	case []byte:
		var decoded any
		if json.Unmarshal(encoded, &decoded) == nil {
			value = decoded
		}
	}
	evidence := map[string]any{}
	collectEvidence(value, evidence, "", 0)
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func collectEvidence(value any, evidence map[string]any, parent string, depth int) {
	if depth > 6 {
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			key = strings.ToLower(strings.TrimSpace(key))
			switch {
			case safeIdentifierKey(key):
				if identifier, ok := child.(string); ok && validIdentifier(identifier) {
					if _, exists := evidence[key]; !exists {
						evidence[key] = strings.TrimSpace(identifier)
					}
				}
			case safeEvidenceKey(key):
				if scalar, ok := safeScalar(child); ok {
					if _, exists := evidence[key]; !exists {
						evidence[key] = scalar
					}
				}
			case key == "query" || key == "request_context" || key == "objective" || key == "summary":
				if text, ok := child.(string); ok {
					evidence[key+"_length"] = len([]rune(text))
				}
			case key == "local_path":
				if path, ok := child.(string); ok && path != "" {
					evidence["local_file_name"] = filepath.Base(path)
				}
			}
			collectEvidence(child, evidence, key, depth+1)
		}
	case []any:
		if safeCountKey(parent) {
			evidence[parent+"_count"] = len(typed)
		}
		if safeIDListKey(parent) {
			identifiers := make([]string, 0, min(len(typed), 50))
			for _, child := range typed[:min(len(typed), 50)] {
				if value, ok := child.(string); ok && validIdentifier(value) {
					identifiers = append(identifiers, strings.TrimSpace(value))
				}
			}
			if len(identifiers) > 0 {
				evidence[parent] = identifiers
			}
		}
		for _, child := range typed {
			collectEvidence(child, evidence, parent, depth+1)
		}
	}
}

func safeIDListKey(key string) bool { return key == "knowledge_ids" }

func safeIdentifierKey(key string) bool {
	switch key {
	case "workflow_id", "revision_id", "preparation_id", "session_id", "step_id", "attempt_id",
		"execution_id", "producer_attempt_id", "resource_id", "artifact_id", "command_id",
		"knowledge_id", "document_id", "skill_id":
		return true
	default:
		return false
	}
}

func validIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 255 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("_.:-", character) {
			continue
		}
		return false
	}
	return true
}

func safeCountKey(key string) bool {
	switch key {
	case "acceptance_criteria", "artifacts", "control_edges", "current", "declared_outputs", "edges",
		"hits", "items", "knowledge_ids", "legacy_tools", "optional_inputs", "outputs", "past", "reachable",
		"ready", "required_outputs", "rewindable", "sessions", "static_order", "tags", "typed_artifacts",
		"witnesses", "workflows":
		return true
	default:
		return false
	}
}

func safeEvidenceKey(key string) bool {
	switch key {
	case "workflow_id", "revision_id", "preparation_id", "session_id", "step_id", "attempt_id",
		"execution_id", "producer_attempt_id", "resource_id", "artifact_id", "command_id", "external_ref",
		"executor_ref", "tool_name", "status", "outcome", "attempt_status", "state_version", "revision",
		"completed", "already_terminal", "read_only", "slot", "slot_id", "content_type",
		"knowledge_id", "document_id", "skill_id":
		return true
	default:
		return false
	}
}

func safeScalar(value any) (any, bool) {
	switch typed := value.(type) {
	case string:
		runes := []rune(strings.TrimSpace(typed))
		if len(runes) > 255 {
			runes = runes[:255]
		}
		return string(runes), len(runes) > 0
	case bool, float64, int, int64:
		return typed, true
	default:
		return nil, false
	}
}

func mergeEvidence(values ...json.RawMessage) map[string]any {
	merged := map[string]any{}
	for _, value := range values {
		var object map[string]any
		if json.Unmarshal(value, &object) != nil {
			continue
		}
		for key, item := range object {
			merged[key] = item
		}
	}
	return merged
}

func evidenceString(evidence map[string]any, key string) string {
	value, _ := evidence[key].(string)
	return value
}

func firstEvidenceString(evidence map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := evidenceString(evidence, key); value != "" {
			return value
		}
	}
	return ""
}
