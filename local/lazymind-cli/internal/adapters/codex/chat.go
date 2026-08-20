package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/chatagent"
)

const maxCodexEventBytes = 4 << 20

const codexRecoveryPrompt = `The previous process for this same LazyMind turn was interrupted. Continue the existing user request from this Codex thread; do not start the task again. Before any LazyMind write, inspect the current Workflow/session/artifact state through the lazymind MCP server and reuse completed work. Do not create a duplicate Workflow session or repeat a completed step. Finish with one final user-facing answer.`

// ChatRunner is the Codex anti-corruption adapter. It translates only the
// documented `codex exec --json` event stream into the host-neutral protocol.
type ChatRunner struct {
	binary string
	self   string
	home   string
}

func NewChatRunner(binary string) (*ChatRunner, error) {
	resolved, err := FindBinary(binary)
	if err != nil {
		return nil, err
	}
	self, home, err := agentexec.ConnectorRuntime()
	if err != nil {
		return nil, err
	}
	return &ChatRunner{binary: resolved, self: self, home: home}, nil
}

func (r *ChatRunner) Run(ctx context.Context, run chatagent.Run, emit func(chatagent.Event) error) error {
	if r == nil || strings.TrimSpace(r.binary) == "" {
		return errors.New("Codex CLI is unavailable")
	}
	workspace, err := agentexec.EnsureConversationWorkspace(run.ConversationID)
	if err != nil {
		return err
	}
	arguments := []string{"exec"}
	mcpConfig := []string{
		"--config", fmt.Sprintf("mcp_servers.%s.command=%q", serverName, r.self),
		"--config", fmt.Sprintf(`mcp_servers.%s.args=["mcp","proxy"]`, serverName),
		"--config", fmt.Sprintf("mcp_servers.%s.env.LAZYMIND_HOME=%q", serverName, r.home),
		"--config", fmt.Sprintf("mcp_servers.%s.env.LAZYMIND_EXTERNAL_REF=%q", serverName, run.RunID),
		"--config", fmt.Sprintf("mcp_servers.%s.env.LAZYMIND_CONVERSATION_ID=%q", serverName, run.ConversationID),
		"--config", fmt.Sprintf("mcp_servers.%s.env.LAZYMIND_EXTERNAL_LEASE=%q", serverName, run.LeaseToken),
		"--config", fmt.Sprintf("mcp_servers.%s.env.LAZYMIND_EXTERNAL_HOST=%q", serverName, run.HostID),
	}
	policy := []string{
		"--config", `sandbox_mode="workspace-write"`,
		"--config", `approval_policy="on-request"`,
		"--config", `approvals_reviewer="auto_review"`,
	}
	resume := (run.Action == "resume" || run.Action == "recover" || run.Action == "regenerate") && strings.TrimSpace(run.ProviderThreadID) != ""
	if resume {
		arguments = append(arguments, "resume")
		arguments = append(arguments, policy...)
		arguments = append(arguments, mcpConfig...)
		arguments = append(arguments, "--json", "--skip-git-repo-check", "--ignore-user-config", run.ProviderThreadID, "-")
	} else {
		arguments = append(arguments, policy...)
		arguments = append(arguments, mcpConfig...)
		arguments = append(arguments, "--json", "--skip-git-repo-check", "--ignore-user-config", "-C", workspace, "-")
	}
	prompt := run.Prompt
	if run.Action == "recover" {
		prompt = codexRecoveryPrompt
	}
	sawTurnCompleted, sawMessage := false, false
	err = (agentexec.StreamCommand{
		Binary: r.binary, Arguments: arguments, Environment: agentexec.SafeEnvironment(
			"LAZYMIND_EXTERNAL_REF="+run.RunID, "LAZYMIND_EXTERNAL_LEASE="+run.LeaseToken,
			"LAZYMIND_EXTERNAL_HOST="+run.HostID,
			"LAZYMIND_CONVERSATION_ID="+run.ConversationID),
		Stdin: strings.NewReader(prompt), MaxLineBytes: maxCodexEventBytes,
	}).Run(ctx, func(line []byte) error {
		var envelope struct {
			Type     string          `json:"type"`
			ThreadID string          `json:"thread_id"`
			Item     json.RawMessage `json:"item"`
		}
		if json.Unmarshal(line, &envelope) != nil {
			return nil
		}
		switch envelope.Type {
		case "thread.started":
			if strings.TrimSpace(envelope.ThreadID) != "" {
				return emit(chatagent.Event{Type: "thread_started", ProviderThreadID: envelope.ThreadID})
			}
		case "item.completed":
			var item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(envelope.Item, &item) == nil && item.Type == "agent_message" && item.Text != "" {
				sawMessage = true
				return emit(chatagent.Event{Type: "message", Text: item.Text})
			}
		case "turn.completed":
			if !sawTurnCompleted {
				sawTurnCompleted = true
				if sawMessage {
					return emit(chatagent.Event{Type: "turn_completed"})
				}
			}
		}
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("Codex failed: %w", err)
	}
	if !sawTurnCompleted || !sawMessage {
		return errors.New("Codex ended without a completed response")
	}
	return nil
}
