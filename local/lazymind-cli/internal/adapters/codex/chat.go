package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lazymind/agentconnector/internal/agentcatalog"
	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/chatagent"
)

const maxCodexEventBytes = 4 << 20

type ChatRunner struct {
	binary string
	self   string
	home   string
}

func NewChatRunner(binary string) (*ChatRunner, error) {
	if err := CleanupLegacyControl(); err != nil {
		return nil, fmt.Errorf("clean up legacy Codex control configuration: %w", err)
	}
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

func (r *ChatRunner) Availability() (bool, string) {
	return availability(r.binary)
}

func Probe(binary string) (bool, bool, string) {
	resolved, err := FindBinary(binary)
	if err != nil {
		return false, false, err.Error()
	}
	ready, reason := availability(resolved)
	return true, ready, reason
}

func availability(binary string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if _, err := agentexec.Run(ctx, binary, "login", "status"); err != nil {
		return false, "Codex CLI is not signed in; run `codex login`"
	}
	return true, ""
}

func (r *ChatRunner) Sessions(ctx context.Context) ([]chatagent.NativeSession, error) {
	return agentcatalog.CodexSessions(ctx)
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
	policy := []string{
		"--config", `sandbox_mode="workspace-write"`,
		"--config", `approval_policy="on-request"`,
		"--config", `approvals_reviewer="auto_review"`,
	}
	mcpConfig := []string{
		"--config", fmt.Sprintf("mcp_servers.%s.command=%q", serverName, r.self),
		"--config", fmt.Sprintf(`mcp_servers.%s.args=["mcp","proxy"]`, serverName),
		"--config", fmt.Sprintf("mcp_servers.%s.env.LAZYMIND_HOME=%q", serverName, r.home),
		"--config", fmt.Sprintf("mcp_servers.%s.env.LAZYMIND_EXTERNAL_REF=%q", serverName, run.RunID),
		"--config", fmt.Sprintf("mcp_servers.%s.env.LAZYMIND_CONVERSATION_ID=%q", serverName, run.ConversationID),
		"--config", fmt.Sprintf("mcp_servers.%s.env.LAZYMIND_EXTERNAL_LEASE=%q", serverName, run.LeaseToken),
		"--config", fmt.Sprintf("mcp_servers.%s.env.LAZYMIND_EXTERNAL_HOST=%q", serverName, run.HostID),
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
	startedAt := time.Now()
	providerThreadID := strings.TrimSpace(run.ProviderThreadID)
	sawTurnCompleted, sawMessage := false, false
	err = (agentexec.StreamCommand{
		Binary: r.binary, Arguments: arguments, Environment: agentexec.SafeEnvironment(
			"LAZYMIND_EXTERNAL_REF="+run.RunID, "LAZYMIND_EXTERNAL_LEASE="+run.LeaseToken,
			"LAZYMIND_EXTERNAL_HOST="+run.HostID,
			"LAZYMIND_CONVERSATION_ID="+run.ConversationID),
		Stdin: strings.NewReader(run.Prompt), MaxLineBytes: maxCodexEventBytes,
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
				providerThreadID = strings.TrimSpace(envelope.ThreadID)
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
	attachments := []chatagent.Attachment(nil)
	if providerThreadID != "" {
		stateHome, homeErr := codexHome()
		if homeErr != nil {
			return fmt.Errorf("resolve Codex generated images: %w", homeErr)
		}
		attachments, err = chatagent.ImageAttachmentsSince(
			filepath.Join(stateHome, "generated_images", providerThreadID), startedAt,
		)
		if err != nil {
			return fmt.Errorf("discover Codex generated images: %w", err)
		}
		for index := range attachments {
			attachment := attachments[index]
			if err := emit(chatagent.Event{Type: "attachment", Attachment: &attachment}); err != nil {
				return err
			}
		}
	}
	if !sawTurnCompleted || (!sawMessage && len(attachments) == 0) {
		return errors.New("Codex ended without a completed response")
	}
	if err := emit(chatagent.Event{Type: "turn_completed"}); err != nil {
		return err
	}
	return nil
}
