package workbuddy

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"testing"

	"lazymind/agentconnector/internal/chatagent"
)

type fakeCoreClient struct {
	status Status
	result workBuddyExecution
	paths  []string
}

func (f *fakeCoreClient) DoJSON(_ context.Context, method, path string, _ any, output any) error {
	f.paths = append(f.paths, method+" "+path)
	switch value := output.(type) {
	case *Status:
		*value = f.status
	case *workBuddyExecution:
		*value = f.result
	}
	return nil
}

func TestWorkBuddyRunUsesOfficialGatewayAndEmitsTextAndImage(t *testing.T) {
	client := &fakeCoreClient{
		status: Status{Installed: true, Ready: true},
		result: workBuddyExecution{
			MessageID: "message-1",
			Text:      "图片已生成",
			Attachments: []workBuddyAttachment{{
				Filename: "workbuddy.png", MediaType: "image/png",
				ContentBase64: base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfixture")),
			}},
		},
	}
	runner, err := NewChatRunner(client)
	if err != nil {
		t.Fatal(err)
	}
	var events []chatagent.Event
	var attachmentBody []byte
	err = runner.Run(context.Background(), chatagent.Run{
		RunID: "run-1", ConversationID: "conversation-1", Action: "start",
		LeaseToken: "lease-1", HostID: "host-1", Prompt: "generate an image",
	}, func(event chatagent.Event) error {
		events = append(events, event)
		if event.Attachment != nil {
			attachmentBody, err = os.ReadFile(event.Attachment.Path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Type != "thread_started" || events[0].ProviderThreadID != "message-1" ||
		events[1].Type != "message" || events[1].Text != "图片已生成" || events[2].Type != "attachment" {
		t.Fatalf("events=%#v", events)
	}
	attachment := events[2].Attachment
	if attachment == nil || attachment.Filename != "workbuddy.png" || attachment.MediaType != "image/png" {
		t.Fatalf("attachment=%#v", attachment)
	}
	if string(attachmentBody) != "\x89PNG\r\n\x1a\nfixture" {
		t.Fatalf("attachment body=%q", attachmentBody)
	}
	if len(client.paths) != 1 || client.paths[0] != http.MethodPost+" /external-chat/providers/workbuddy:execute" {
		t.Fatalf("paths=%v", client.paths)
	}
}

func TestWorkBuddyAvailabilityUsesOfficialGatewayStatus(t *testing.T) {
	client := &fakeCoreClient{status: Status{
		Installed: true, Ready: false, UnavailableReason: "WorkBuddy authorization required",
	}}
	runner, err := NewChatRunner(client)
	if err != nil {
		t.Fatal(err)
	}
	if ready, reason := runner.Availability(); ready || reason != "WorkBuddy authorization required" {
		t.Fatalf("availability=(%v, %q)", ready, reason)
	}
}
