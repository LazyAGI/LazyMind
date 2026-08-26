package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"lazymind/agentconnector/internal/agentcatalog"
	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/chatagent"
)

const maxEventBytes = 4 << 20

const (
	statusTimeout = 5 * time.Second
	loginTimeout  = 2 * time.Minute
)

type ChatRunner struct {
	binary string
	self   string
	home   string
}

func (r *ChatRunner) Sessions(ctx context.Context) ([]chatagent.NativeSession, error) {
	return agentcatalog.CursorSessions(ctx)
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
	return &ChatRunner{binary: resolved, self: self, home: home}, nil
}

func findBinary(configured string) (string, error) {
	name := "cursor-agent"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	home, _ := os.UserHomeDir()
	var candidates []string
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", name))
		versions, _ := filepath.Glob(filepath.Join(home, ".local", "share", "cursor-agent", "versions", "*", name))
		sort.Sort(sort.Reverse(sort.StringSlice(versions)))
		candidates = append(candidates, versions...)
	}
	resolved, err := agentexec.Find(configured, "LAZYMIND_CURSOR_AGENT_BIN", []string{name}, candidates)
	if err != nil {
		if strings.TrimSpace(configured) != "" || strings.TrimSpace(os.Getenv("LAZYMIND_CURSOR_AGENT_BIN")) != "" {
			return "", fmt.Errorf("resolve configured Cursor Agent CLI: %w", err)
		}
		return "", errors.New("Cursor Agent CLI is not installed; install it from https://cursor.com/docs/cli/installation")
	}
	return resolved, nil
}

func Login(ctx context.Context, binary string) error {
	resolved, err := findBinary(binary)
	if err != nil {
		return err
	}
	loginCtx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	_, err = agentexec.Run(loginCtx, resolved, "login")
	return err
}

func (r *ChatRunner) Availability() (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	status, err := agentexec.Run(ctx, r.binary, "status")
	if err != nil || strings.Contains(strings.ToLower(status), "not logged in") ||
		strings.Contains(strings.ToLower(status), "not authenticated") {
		return false, "Cursor Agent CLI is not signed in; run `cursor-agent login`"
	}
	return true, ""
}

func (r *ChatRunner) Run(ctx context.Context, run chatagent.Run, emit func(chatagent.Event) error) error {
	if r == nil || strings.TrimSpace(r.binary) == "" {
		return errors.New("Cursor Agent CLI is unavailable")
	}
	resume := (run.Action == "resume" || run.Action == "regenerate") && strings.TrimSpace(run.ProviderThreadID) != ""
	workspace := ""
	var err error
	if resume {
		var found bool
		workspace, found, err = agentcatalog.Workspace(ctx, "cursor", run.ProviderThreadID)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("Cursor CLI session workspace is unavailable")
		}
	} else {
		workspace, err = agentexec.EnsureConversationWorkspace(run.ConversationID)
		if err != nil {
			return err
		}
		if err := r.writeInvocationMCPConfig(workspace, run); err != nil {
			return err
		}
	}
	arguments := []string{
		"-p", "--output-format", "stream-json", "--stream-partial-output",
		"--approve-mcps", "--trust", "--auto-review", "--sandbox", "enabled", "--workspace", workspace,
	}
	if resume {
		arguments = append(arguments, "--resume", run.ProviderThreadID)
	}
	arguments = append(arguments, run.Prompt)
	sawMessage, completed, terminalError := false, false, ""
	err = (agentexec.StreamCommand{
		Binary: r.binary, Arguments: arguments, Environment: agentexec.SafeEnvironment(
			"LAZYMIND_EXTERNAL_REF="+run.RunID, "LAZYMIND_EXTERNAL_LEASE="+run.LeaseToken,
			"LAZYMIND_EXTERNAL_HOST="+run.HostID,
			"LAZYMIND_CONVERSATION_ID="+run.ConversationID),
		MaxLineBytes: maxEventBytes,
	}).Run(ctx, func(line []byte) error {
		var event struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			SessionID string `json:"session_id"`
			ModelCall string `json:"model_call_id"`
			Timestamp int64  `json:"timestamp_ms"`
			Result    string `json:"result"`
			IsError   bool   `json:"is_error"`
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
		// With --stream-partial-output Cursor emits individual text deltas,
		// then repeats them as model-call and whole-turn aggregates. Only the
		// timestamped events without a model_call_id are deltas.
		if event.Type == "assistant" && event.Timestamp > 0 && event.ModelCall == "" {
			for _, content := range event.Message.Content {
				if content.Type == "text" && content.Text != "" {
					sawMessage = true
					if err := emit(chatagent.Event{Type: "message", Text: content.Text}); err != nil {
						return err
					}
				}
			}
		}
		if event.Type == "result" {
			if event.Subtype == "success" && !event.IsError {
				completed = true
				if !sawMessage && event.Result != "" {
					sawMessage = true
					if err := emit(chatagent.Event{Type: "message", Text: event.Result}); err != nil {
						return err
					}
				}
			} else {
				terminalError = "Cursor Agent returned " + strings.TrimSpace(event.Subtype)
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
		return fmt.Errorf("Cursor Agent failed: %w", err)
	}
	if !completed || !sawMessage {
		return errors.New("Cursor Agent ended without a completed response")
	}
	return nil
}

func (r *ChatRunner) writeInvocationMCPConfig(workspace string, run chatagent.Run) error {
	body, err := agentexec.LazyMindMCPConfig(r.self, r.home, run.RunID, run.ConversationID, run.LeaseToken, run.HostID)
	if err != nil {
		return fmt.Errorf("build Cursor invocation MCP configuration: %w", err)
	}
	directory := filepath.Join(workspace, ".cursor")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create Cursor invocation MCP directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "mcp.json"), append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("write Cursor invocation MCP configuration: %w", err)
	}
	return nil
}
