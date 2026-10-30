package mcpbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/agentconnector/internal/codexhost"
	"lazymind/agentconnector/internal/workflowmcp"
)

// Recovery is confined to Codex's external transport and presentation.
func (b *Bridge) codexRecoveryMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
		call, ok := request.(*mcp.CallToolRequest)
		if method != "tools/call" || !ok || call.Params == nil {
			return next(ctx, method, request)
		}
		switch call.Params.Name {
		case "workflow.state", "workflow.session.list", "workflow.artifact.list", "workflow.artifact.get":
			return codexReadPresentation(ctx, method, call, next, func(ctx context.Context) (bool, error) {
				var args map[string]any
				if json.Unmarshal(call.Params.Arguments, &args) != nil {
					return false, errors.New("invalid Workflow arguments")
				}
				thread, err := b.codexRecoveryThread(ctx, call, args)
				if err != nil {
					return false, err
				}
				bounded, cancel := context.WithTimeout(ctx, 12*time.Second)
				defer cancel()
				return codexhost.EnsurePresentation(bounded, b.home, thread)
			})
		case "workflow.start", "workflow.step.begin", "workflow.step.claim", "workflow.step.resume":
			ctx = codexhost.WithRecoveryIntent(ctx, func() (*codexhost.RecoveryIntent, error) {
				var args map[string]any
				if json.Unmarshal(call.Params.Arguments, &args) != nil || args == nil {
					return nil, errors.New("invalid Workflow arguments")
				}
				thread, err := b.codexRecoveryThread(ctx, call, args)
				if err != nil {
					return nil, err
				}
				return newCodexRecoveryIntent(call, thread, args)
			})
		}
		return next(ctx, method, request)
	}
}

func codexReadPresentation(ctx context.Context, method string, call *mcp.CallToolRequest, next mcp.MethodHandler, restore func(context.Context) (bool, error)) (mcp.Result, error) {
	result, err := next(ctx, method, call)
	value, ok := result.(*mcp.CallToolResult)
	if err != nil || !ok || value.IsError {
		return result, err
	}
	if call.Params.Name == "workflow.state" {
		var state struct {
			Control struct {
				Continuation string `json:"continuation"`
				Admission    struct {
					CanBegin bool `json:"can_begin"`
				} `json:"admission"`
			} `json:"control"`
			Projection struct {
				Ready     []string `json:"ready"`
				Completed bool     `json:"completed"`
			} `json:"projection"`
		}
		body, _ := json.Marshal(value.StructuredContent)
		if json.Unmarshal(body, &state) == nil && (state.Control.Continuation == "continue" && state.Control.Admission.CanBegin || state.Control.Continuation == "" && !state.Projection.Completed && len(state.Projection.Ready) > 0) {
			// Execution admission must save the user's operation before offering restart.
			return result, nil
		}
	}
	if requested, restoreErr := restore(ctx); restoreErr == nil && requested {
		// Preserve the successful business read and its structured result.
		copy := *value
		copy.Content = append(append([]mcp.Content(nil), value.Content...), &mcp.TextContent{Text: "The existing Workflow results were read successfully. Its Panel connection is unavailable, so LazyMind opened a recovery window. After the user's restart choice, LazyMind will reopen this conversation and restore the existing Panel automatically. Do not start a new Workflow, advance steps, or ask the user to type Continue just to view these results."})
		return &copy, nil
	}
	return result, err
}

func (b *Bridge) codexRecoveryThread(ctx context.Context, call *mcp.CallToolRequest, args map[string]any) (string, error) {
	fallback := ""
	if call.Params.Name == "workflow.start" {
		fallback, _ = args["driver_session_id"].(string)
	}
	thread, err := workflowmcp.CodexDriver(call, fallback)
	if err == nil || call.Params.Name == "workflow.start" || !strings.HasPrefix(err.Error(), "current Codex thread UUID is unavailable") {
		return thread, err
	}
	session, _ := args["session_id"].(string)
	if session == "" {
		return "", err
	}
	var result struct {
		Control struct {
			Binding struct {
				Provider string `json:"provider"`
				Driver   string `json:"driver_session_id"`
				Bound    bool   `json:"bound"`
			} `json:"binding"`
		} `json:"control"`
	}
	if err := b.api.DoJSON(ctx, http.MethodGet, "/workflow-sessions/"+url.PathEscape(session)+"/control?view=control", nil, &result); err != nil {
		return "", err
	}
	binding := result.Control.Binding
	if !binding.Bound || binding.Provider != "codex" {
		return "", errors.New("Workflow is not bound to a Codex conversation")
	}
	return workflowmcp.CodexDriver(nil, binding.Driver)
}

func newCodexRecoveryIntent(call *mcp.CallToolRequest, thread string, args map[string]any) (*codexhost.RecoveryIntent, error) {
	canonical, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	fingerprint := sha256.Sum256(append([]byte(thread+"\n"+call.Params.Name+"\n"), canonical...))
	id, err := newInvocationID("")
	if err != nil {
		return nil, err
	}
	// Generate before persisting so retries keep the exact same Core identity.
	key := "codex-recovery-" + id
	switch call.Params.Name {
	case "workflow.start":
		if value, _ := args["idempotency_key"].(string); strings.TrimSpace(value) == "" {
			args["idempotency_key"] = key
		}
		args["driver_session_id"] = thread
	case "workflow.step.begin":
		if value, _ := args["command_id"].(string); strings.TrimSpace(value) == "" {
			args["command_id"] = key
		}
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return &codexhost.RecoveryIntent{ID: id, ThreadID: thread, Tool: call.Params.Name, Arguments: encoded, Fingerprint: hex.EncodeToString(fingerprint[:])}, nil
}
