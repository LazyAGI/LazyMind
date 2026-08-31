package workbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lazymind/agentconnector/internal/agentcatalog"
	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/chatagent"
)

const (
	maxEventBytes     = 4 << 20
	loginPollInterval = 500 * time.Millisecond
)

var openInteractiveCommand = agentexec.OpenInteractiveCommand

type ChatRunner struct {
	binary string
	self   string
	home   string
	auth   string
}

func (r *ChatRunner) Sessions(ctx context.Context) ([]chatagent.NativeSession, error) {
	return agentcatalog.WorkBuddySessions(ctx)
}

func NewChatRunner(binary string) (*ChatRunner, error) {
	resolved, err := findBinary(binary)
	if err != nil {
		return nil, err
	}
	self, home, err := agentexec.ConnectorRuntime()
	if err != nil {
		return nil, err
	}
	return &ChatRunner{binary: resolved, self: self, home: home, auth: authFile()}, nil
}

func (r *ChatRunner) Availability() (bool, string) {
	return availability(r.auth)
}

func Probe(binary string) (bool, bool, string) {
	if _, err := findBinary(binary); err != nil {
		return false, false, err.Error()
	}
	ready, reason := availability(authFile())
	return true, ready, reason
}

func Login(ctx context.Context, binary string) error {
	return login(ctx, binary, authFile())
}

func login(ctx context.Context, binary, authenticationFile string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	resolved, err := findBinary(binary)
	if err != nil {
		return err
	}
	if err := openInteractiveCommand(resolved); err != nil {
		return err
	}
	ticker := time.NewTicker(loginPollInterval)
	defer ticker.Stop()
	for {
		if ready, _ := availability(authenticationFile); ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func availability(auth string) (bool, string) {
	info, err := os.Stat(auth)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return false, "CodeBuddy Code is not signed in; start `codebuddy` and run `/login`"
	}
	return true, ""
}

func authFile() string {
	root, _ := os.UserConfigDir()
	return filepath.Join(root, "CodeBuddyExtension", "Data", "Public", "auth", "Tencent-Cloud.coding-copilot.info")
}

func findBinary(configured string) (string, error) {
	names := []string{"codebuddy", "cbc"}
	resolved, err := agentexec.FindBound(
		configured, "LAZYMIND_WORKBUDDY_AGENT_BIN", agentexec.CodeBuddyCLI, names,
	)
	if err != nil {
		if strings.TrimSpace(configured) != "" || strings.TrimSpace(os.Getenv("LAZYMIND_WORKBUDDY_AGENT_BIN")) != "" {
			return "", fmt.Errorf("resolve configured CodeBuddy Code CLI: %w", err)
		}
		return "", errors.New("CodeBuddy Code CLI is not installed; install it from https://www.codebuddy.ai/docs/cli/quickstart")
	}
	return resolved, nil
}

func (r *ChatRunner) Run(ctx context.Context, run chatagent.Run, emit func(chatagent.Event) error) error {
	if r == nil || strings.TrimSpace(r.binary) == "" {
		return errors.New("CodeBuddy Code CLI is unavailable")
	}
	resume := (run.Action == "resume" || run.Action == "regenerate") && strings.TrimSpace(run.ProviderThreadID) != ""
	workspace := ""
	var err error
	if resume {
		var found bool
		workspace, found, err = agentcatalog.Workspace(ctx, "workbuddy", run.ProviderThreadID)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("CodeBuddy CLI session workspace is unavailable")
		}
	} else {
		workspace, err = agentexec.EnsureConversationWorkspace(run.ConversationID)
		if err != nil {
			return err
		}
	}
	mcpConfig, err := r.invocationMCPConfig(run)
	if err != nil {
		return err
	}
	arguments := []string{
		"-p", "--output-format", "stream-json", "--permission-mode", "bypassPermissions",
		"--tools", "Read,Write,Edit,Glob,Grep,ToolSearch,DeferExecuteTool",
		"--strict-mcp-config", "--mcp-config", mcpConfig,
	}
	if resume {
		arguments = append(arguments, "--resume", run.ProviderThreadID)
	}
	arguments = append(arguments, run.Prompt)
	sawMessage, completed, terminalError := false, false, ""
	pendingMessages := []string{}
	err = (agentexec.StreamCommand{
		Binary: r.binary, Arguments: arguments, Directory: workspace,
		Environment: agentexec.SafeEnvironment("LAZYMIND_EXTERNAL_REF="+run.RunID,
			"LAZYMIND_EXTERNAL_LEASE="+run.LeaseToken,
			"LAZYMIND_EXTERNAL_HOST="+run.HostID,
			"LAZYMIND_CONVERSATION_ID="+run.ConversationID),
		MaxLineBytes: maxEventBytes,
	}).Run(ctx, func(line []byte) error {
		var event struct {
			Type      string   `json:"type"`
			Subtype   string   `json:"subtype"`
			SessionID string   `json:"session_id"`
			Result    string   `json:"result"`
			IsError   bool     `json:"is_error"`
			Errors    []string `json:"errors"`
			Message   struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &event) != nil {
			return nil
		}
		if event.Type == "system" && event.Subtype == "init" && strings.TrimSpace(event.SessionID) != "" {
			if err := emit(chatagent.Event{Type: "thread_started", ProviderThreadID: event.SessionID}); err != nil {
				return err
			}
		}
		if event.Type == "assistant" {
			for _, content := range event.Message.Content {
				if content.Type == "text" && content.Text != "" {
					pendingMessages = append(pendingMessages, content.Text)
				}
			}
		}
		if event.Type == "result" {
			if event.Subtype == "success" && !event.IsError {
				completed = true
				if len(pendingMessages) == 0 && event.Result != "" {
					pendingMessages = append(pendingMessages, event.Result)
				}
				for _, message := range pendingMessages {
					if err := emit(chatagent.Event{Type: "message", Text: message}); err != nil {
						return err
					}
				}
				sawMessage = len(pendingMessages) > 0
			} else if len(event.Errors) > 0 {
				terminalError = strings.Join(event.Errors, "; ")
			} else {
				terminalError = "CodeBuddy Code returned " + strings.TrimSpace(event.Subtype)
			}
		}
		return nil
	})
	if terminalError != "" {
		return errors.New(terminalError)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("CodeBuddy Code failed: %w", err)
	}
	if !completed || !sawMessage {
		return errors.New("CodeBuddy Code ended without a completed response")
	}
	return nil
}

func (r *ChatRunner) invocationMCPConfig(run chatagent.Run) (string, error) {
	body, err := agentexec.LazyMindMCPConfig(r.self, r.home, run.RunID, run.ConversationID, run.LeaseToken, run.HostID)
	if err != nil {
		return "", fmt.Errorf("build CodeBuddy invocation MCP configuration: %w", err)
	}
	return string(body), nil
}
