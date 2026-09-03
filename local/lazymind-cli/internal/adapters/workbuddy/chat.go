package workbuddy

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lazymind/agentconnector/internal/agentcatalog"
	"lazymind/agentconnector/internal/chatagent"
)

const statusTimeout = 5 * time.Second

type coreClient interface {
	DoJSON(context.Context, string, string, any, any) error
}

type ChatRunner struct {
	api coreClient
}

type Status struct {
	Installed         bool   `json:"installed"`
	Ready             bool   `json:"ready"`
	UnavailableReason string `json:"unavailable_reason"`
}

type workBuddyExecution struct {
	MessageID   string                `json:"message_id"`
	Text        string                `json:"text"`
	Attachments []workBuddyAttachment `json:"attachments"`
}

type workBuddyAttachment struct {
	Filename      string `json:"filename"`
	MediaType     string `json:"media_type"`
	ContentBase64 string `json:"content_base64"`
}

func NewChatRunner(api coreClient) (*ChatRunner, error) {
	if api == nil {
		return nil, errors.New("LazyMind Core client is required")
	}
	return &ChatRunner{api: api}, nil
}

func (r *ChatRunner) Sessions(ctx context.Context) ([]chatagent.NativeSession, error) {
	return agentcatalog.WorkBuddySessions(ctx)
}

func (r *ChatRunner) Availability() (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	status, err := Probe(ctx, r.api)
	if err != nil {
		return false, err.Error()
	}
	return status.Ready, status.UnavailableReason
}

func Probe(ctx context.Context, api coreClient) (Status, error) {
	if api == nil {
		return Status{}, errors.New("LazyMind Core client is required")
	}
	var status Status
	if err := api.DoJSON(ctx, http.MethodGet, "/external-chat/providers/workbuddy/status", nil, &status); err != nil {
		return Status{Installed: true}, fmt.Errorf("check WorkBuddy status: %w", err)
	}
	return status, nil
}

func (r *ChatRunner) Run(ctx context.Context, run chatagent.Run, emit func(chatagent.Event) error) error {
	if r == nil || r.api == nil {
		return errors.New("WorkBuddy is unavailable")
	}
	var result workBuddyExecution
	if err := r.api.DoJSON(ctx, http.MethodPost, "/external-chat/providers/workbuddy:execute", map[string]string{
		"run_id": run.RunID, "conversation_id": run.ConversationID,
		"host_id": run.HostID, "lease_token": run.LeaseToken,
	}, &result); err != nil {
		return fmt.Errorf("WorkBuddy execution failed: %w", err)
	}
	threadID := strings.TrimSpace(run.ProviderThreadID)
	if threadID == "" {
		threadID = strings.TrimSpace(result.MessageID)
		if threadID == "" {
			return errors.New("WorkBuddy returned an empty message ID")
		}
		if err := emit(chatagent.Event{Type: "thread_started", ProviderThreadID: threadID}); err != nil {
			return err
		}
	}
	if text := strings.TrimSpace(result.Text); text != "" {
		if err := emit(chatagent.Event{Type: "message", Text: text}); err != nil {
			return err
		}
	}
	if err := r.emitAttachments(result.Attachments, emit); err != nil {
		return err
	}
	if strings.TrimSpace(result.Text) == "" && len(result.Attachments) == 0 {
		return errors.New("WorkBuddy ended without a response")
	}
	return nil
}

func (r *ChatRunner) emitAttachments(
	attachments []workBuddyAttachment,
	emit func(chatagent.Event) error,
) error {
	if len(attachments) == 0 {
		return nil
	}
	directory, err := os.MkdirTemp("", "lazymind-workbuddy-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	for index, attachment := range attachments {
		filename := filepath.Base(strings.TrimSpace(attachment.Filename))
		if filename == "" || filename == "." || filename == ".." {
			filename = fmt.Sprintf("workbuddy-result-%d.bin", index+1)
		}
		content, err := base64.StdEncoding.DecodeString(attachment.ContentBase64)
		if err != nil || len(content) == 0 {
			return errors.New("WorkBuddy returned an invalid attachment")
		}
		path := filepath.Join(directory, filename)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			return err
		}
		if err := emit(chatagent.Event{Type: "attachment", Attachment: &chatagent.Attachment{
			Path: path, Filename: filename, MediaType: strings.TrimSpace(attachment.MediaType),
		}}); err != nil {
			return err
		}
	}
	return nil
}
