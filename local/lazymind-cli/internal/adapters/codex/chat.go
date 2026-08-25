package codex

import (
	"context"
	"errors"
	"strings"

	"lazymind/agentconnector/internal/agentcatalog"
	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/chatagent"
	"lazymind/agentconnector/internal/codexcontrol"
)

const codexRecoveryPrompt = `The previous process for this same LazyMind turn was interrupted. Continue the existing user request from this Codex thread; do not start the task again. Before any LazyMind write, inspect the current Workflow/session/artifact state through the lazymind MCP server and reuse completed work. Do not create a duplicate Workflow session or repeat a completed step. Finish with one final user-facing answer.`

// ChatRunner is the Codex anti-corruption adapter. It translates only the
// documented `codex exec --json` event stream into the host-neutral protocol.
type ChatRunner struct {
	binary  string
	control *codexcontrol.Controller
}

func (r *ChatRunner) Sessions(ctx context.Context) ([]chatagent.NativeSession, error) {
	return agentcatalog.CodexSessions(ctx)
}

func NewChatRunner(binary string) (*ChatRunner, error) {
	return NewChatRunnerWithControl(binary, nil)
}

func NewChatRunnerWithControl(binary string, control *codexcontrol.Controller) (*ChatRunner, error) {
	resolved, err := FindBinary(binary)
	if err != nil {
		return nil, err
	}
	return &ChatRunner{binary: resolved, control: control}, nil
}

func (r *ChatRunner) Availability() (bool, string) {
	if r == nil || r.control == nil {
		return false, codexcontrol.ErrUnavailable.Error()
	}
	status := r.control.Status()
	if !status.Ready {
		if status.LastError != "" {
			return false, status.LastError
		}
		return false, codexcontrol.ErrUnavailable.Error()
	}
	return true, ""
}

func (r *ChatRunner) Run(ctx context.Context, run chatagent.Run, emit func(chatagent.Event) error) error {
	if r == nil || strings.TrimSpace(r.binary) == "" {
		return errors.New("Codex CLI is unavailable")
	}
	if r.control == nil {
		return codexcontrol.ErrUnavailable
	}
	prompt, applicationContext := strings.TrimSpace(run.Query), codexApplicationContext(run.Prompt)
	if prompt == "" {
		prompt = run.Prompt
		applicationContext = ""
	}
	if run.Action == "recover" {
		applicationContext = strings.TrimSpace(applicationContext + "\n\n" + codexRecoveryPrompt)
	}
	workspace := ""
	if strings.TrimSpace(run.ProviderThreadID) == "" {
		var err error
		workspace, err = agentexec.EnsureConversationWorkspace(run.ConversationID)
		if err != nil {
			return err
		}
	}
	return r.control.Execute(ctx, run.ProviderThreadID, workspace, run.HistoryID, prompt, applicationContext, func(event codexcontrol.SessionEvent) error {
		return emit(chatagent.Event{
			Type: event.Type, Text: event.Text, ProviderThreadID: event.ThreadID,
		})
	})
}

func codexApplicationContext(prompt string) string {
	const marker = "\nCurrent user request:\n"
	if index := strings.LastIndex(prompt, marker); index >= 0 {
		return strings.TrimSpace(prompt[:index])
	}
	return strings.TrimSpace(prompt)
}
