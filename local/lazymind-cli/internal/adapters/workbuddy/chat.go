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

const maxEventBytes = 4 << 20

type ChatRunner struct {
	binary             string
	prefixArguments    []string
	runtimeEnvironment []string
	self               string
	home               string
	workBuddyHome      string
	auth               string
}

func (r *ChatRunner) Sessions(ctx context.Context) ([]chatagent.NativeSession, error) {
	return agentcatalog.WorkBuddySessions(ctx)
}

func NewChatRunner(binary string) (*ChatRunner, error) {
	workBuddyHome := workBuddyConfigDir()
	runtimeCommand, err := findRuntime(binary, workBuddyHome)
	if err != nil {
		return nil, err
	}
	self, home, err := agentexec.ConnectorRuntime()
	if err != nil {
		return nil, err
	}
	return &ChatRunner{
		binary: runtimeCommand.Binary, prefixArguments: runtimeCommand.Arguments,
		runtimeEnvironment: runtimeCommand.Environment, self: self, home: home,
		workBuddyHome: workBuddyHome, auth: authFile(),
	}, nil
}

func (r *ChatRunner) Availability() (bool, string) {
	return availability(r.auth)
}

func Probe(binary string) (bool, bool, string) {
	if _, err := findRuntime(binary, workBuddyConfigDir()); err != nil {
		return false, false, err.Error()
	}
	ready, reason := availability(authFile())
	return true, ready, reason
}

func availability(auth string) (bool, string) {
	info, err := os.Stat(auth)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return false, "WorkBuddy is not signed in; open WorkBuddy and complete sign-in"
	}
	return true, ""
}

func authFile() string {
	root, _ := os.UserConfigDir()
	return filepath.Join(root, "CodeBuddyExtension", "Data", "Public", "auth", "workbuddy-desktop.info")
}

func workBuddyConfigDir() string {
	if configured := strings.TrimSpace(os.Getenv("WORKBUDDY_CONFIG_DIR")); configured != "" {
		return filepath.Clean(configured)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".workbuddy")
}

type runtimeCommand struct {
	Binary      string
	Arguments   []string
	Environment []string
}

func findRuntime(configured, configDir string) (runtimeCommand, error) {
	if value := strings.TrimSpace(configured); value != "" {
		return configuredBinary(value)
	}
	if value := strings.TrimSpace(os.Getenv("LAZYMIND_WORKBUDDY_AGENT_BIN")); value != "" {
		return configuredBinary(value)
	}
	application, err := agentexec.FindDesktopApplication(agentexec.DesktopApplication{
		BindingTarget: agentexec.WorkBuddyDesktop,
		ExecutableNames: []string{
			"WorkBuddy", "WorkBuddy.exe",
		},
		Protocols:    []string{"workbuddy"},
		DisplayNames: []string{"WorkBuddy"},
	})
	if err != nil {
		return runtimeCommand{}, errors.New("WorkBuddy is not installed; install and open WorkBuddy once")
	}
	runtimeCommand, err := bundledRuntime(application, configDir)
	if err != nil {
		return runtimeCommand, fmt.Errorf("locate WorkBuddy runtime: %w", err)
	}
	return runtimeCommand, nil
}

func configuredBinary(value string) (runtimeCommand, error) {
	resolved, err := agentexec.Find(value, nil)
	if err != nil {
		return runtimeCommand{}, fmt.Errorf("resolve configured WorkBuddy runtime: %w", err)
	}
	return runtimeCommand{Binary: resolved}, nil
}

func (r *ChatRunner) Run(ctx context.Context, run chatagent.Run, emit func(chatagent.Event) error) error {
	if r == nil || strings.TrimSpace(r.binary) == "" {
		return errors.New("WorkBuddy is unavailable")
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
			return errors.New("WorkBuddy session workspace is unavailable")
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
	arguments := append([]string(nil), r.prefixArguments...)
	arguments = append(arguments,
		"-p", "--output-format", "stream-json", "--permission-mode", "bypassPermissions",
		"--tools", "Read,Write,Edit,Glob,Grep,ToolSearch,DeferExecuteTool,ImageGen",
		"--strict-mcp-config", "--mcp-config", mcpConfig,
	)
	if resume {
		arguments = append(arguments, "--resume", run.ProviderThreadID)
	}
	arguments = append(arguments, run.Prompt)
	startedAt := time.Now()
	sawMessage, completed, terminalError := false, false, ""
	pendingMessages := []string{}
	err = (agentexec.StreamCommand{
		Binary: r.binary, Arguments: arguments, Directory: workspace,
		Environment: agentexec.SafeEnvironment(append(append([]string(nil), r.runtimeEnvironment...),
			"WORKBUDDY_CONFIG_DIR="+r.workBuddyHome,
			"CODEBUDDY_CONFIG_DIR="+r.workBuddyHome,
			"LAZYMIND_EXTERNAL_REF="+run.RunID,
			"LAZYMIND_EXTERNAL_LEASE="+run.LeaseToken,
			"LAZYMIND_EXTERNAL_HOST="+run.HostID,
			"LAZYMIND_CONVERSATION_ID="+run.ConversationID,
		)...),
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
				terminalError = "WorkBuddy returned " + strings.TrimSpace(event.Subtype)
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
		return fmt.Errorf("WorkBuddy failed: %w", err)
	}
	attachments, err := chatagent.ImageAttachmentsSince(filepath.Join(workspace, "generated-images"), startedAt)
	if err != nil {
		return fmt.Errorf("discover WorkBuddy generated images: %w", err)
	}
	for index := range attachments {
		attachment := attachments[index]
		if err := emit(chatagent.Event{Type: "attachment", Attachment: &attachment}); err != nil {
			return err
		}
	}
	if !completed || (!sawMessage && len(attachments) == 0) {
		return errors.New("WorkBuddy ended without a completed response")
	}
	return nil
}

func (r *ChatRunner) invocationMCPConfig(run chatagent.Run) (string, error) {
	body, err := agentexec.LazyMindMCPConfig(r.self, r.home, run.RunID, run.ConversationID, run.LeaseToken, run.HostID)
	if err != nil {
		return "", fmt.Errorf("build WorkBuddy invocation MCP configuration: %w", err)
	}
	return string(body), nil
}
