package workflowtool

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/core/capability"
	"lazymind/core/common/orm"
)

func TestWorkflowToolAbandonedReceiptStopsAutomaticRetries(t *testing.T) {
	var calls atomic.Int32
	service, input := workflowToolFixture(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		deadline, bounded := request.Context().Deadline()
		if !bounded || time.Until(deadline) > maxToolExecutionDuration {
			t.Fatal("tool operation has no bounded execution deadline")
		}
		return toolResponse(`{"result":"known result"}`), nil
	})
	if result, err := service.Call(t.Context(), "owner", "run", "attempt", input); err != nil || result.Error != nil {
		t.Fatalf("first call failed: %+v %v", result, err)
	}
	var receipt orm.WorkflowCommand
	if err := service.DB.First(&receipt).Error; err != nil {
		t.Fatal(err)
	}
	knownResult := append(json.RawMessage(nil), receipt.ResponseJSON...)
	// Simulate the durable state left by a process interruption before saving
	// the result; no sleep or second external operation is needed.
	if err := service.DB.Model(&receipt).Updates(map[string]any{"http_status": http.StatusAccepted,
		"response_json": json.RawMessage(`{}`), "created_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	_, err := service.Call(t.Context(), "owner", "run", "attempt", input)
	var pending *ToolError
	if !errors.As(err, &pending) || pending.Code != "TOOL_CALL_IN_PROGRESS" || !pending.Retryable {
		t.Fatalf("recent operation was not recoverable: %v", err)
	}
	if err := service.DB.Model(&receipt).Update("created_at", time.Now().UTC().Add(-7*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		_, err := service.Call(t.Context(), "owner", "run", "attempt", input)
		var unknown *ToolError
		if !errors.As(err, &unknown) || unknown.Code != "TOOL_OUTCOME_UNKNOWN" || unknown.Retryable || !strings.Contains(unknown.Message, "user confirmation") {
			t.Fatalf("abandoned operation invites endless retries: %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("unknown outcome was automatically reexecuted")
	}
	// A late known receipt remains readable under the original command.
	if err := service.DB.Model(&receipt).Updates(map[string]any{"http_status": http.StatusOK, "response_json": knownResult}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := service.Call(t.Context(), "owner", "run", "attempt", input)
	if err != nil || result.Error != nil || result.Result != "known result" || calls.Load() != 1 {
		t.Fatalf("late receipt was lost or repeated: %+v %v", result, err)
	}
}

func TestWorkflowToolPublicCapabilityErrorPreservesRemediationOnly(t *testing.T) {
	public := capability.NewError(capability.InvalidArgument, "tool.call",
		"Import the image with workflow.input.import and use resource:<id>", false, errors.New("private-provider-secret"))
	public.Details = map[string]any{"credential": "private-details"}
	public.Action = &capability.ErrorAction{Type: "internal", URL: "https://private-host.invalid"}
	projected := publicToolError(fmt.Errorf("private wrapper: %w", public))
	encoded, err := json.Marshal(projected)
	if err != nil || projected.Code != "INVALID_ARGUMENT" || projected.Message != public.Message || projected.Retryable {
		t.Fatalf("input remediation was replaced: %+v %v", projected, err)
	}
	if strings.Contains(string(encoded), "private") {
		t.Fatalf("public error leaked an internal cause/detail: %s", encoded)
	}
	public.Code, public.Message, public.Retryable = capability.DeadlineExceeded, "Retry after the connection recovers", true
	if retry := publicToolError(public); retry.Code != "DEADLINE_EXCEEDED" || !retry.Retryable {
		t.Fatalf("public retryability was lost: %+v", retry)
	}
	public.Code, public.Message = capability.ErrorCode("UNREVIEWED_INTERNAL_CODE"), "private message"
	if hidden := publicToolError(public); hidden.Code != "CAPABILITY_UNAVAILABLE" || strings.Contains(hidden.Message, "private") {
		t.Fatalf("unreviewed error was exposed: %+v", hidden)
	}
}
