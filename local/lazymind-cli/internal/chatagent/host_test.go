package chatagent

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type retryingCoreClient struct {
	mu     sync.Mutex
	inputs []map[string]any
	fail   int
}

type countingRunner struct{ calls int }

func (r *countingRunner) Run(context.Context, Run, func(Event) error) error {
	r.calls++
	return nil
}

func (c *retryingCoreClient) DoJSON(_ context.Context, _, _ string, input, _ any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	copyInput := make(map[string]any, len(input.(map[string]any)))
	for key, value := range input.(map[string]any) {
		copyInput[key] = value
	}
	c.inputs = append(c.inputs, copyInput)
	if len(c.inputs) <= c.fail {
		return errors.New("response was lost")
	}
	return nil
}

func TestHostEventRetryKeepsIdempotencyAndLeaseTokens(t *testing.T) {
	client := &retryingCoreClient{fail: 1}
	host := &Host{api: client, id: "host-1"}
	run := Run{RunID: "run-1", LeaseToken: "lease-1"}
	if err := host.sendEvent(context.Background(), run, Event{Type: "message", Text: "answer"}); err != nil {
		t.Fatalf("send event: %v", err)
	}
	if len(client.inputs) != 2 {
		t.Fatalf("attempts=%d, want 2", len(client.inputs))
	}
	first, second := client.inputs[0], client.inputs[1]
	if first["event_id"] == "" || first["event_id"] != second["event_id"] {
		t.Fatalf("event retry changed id: %#v %#v", first, second)
	}
	if first["lease_token"] != run.LeaseToken || first["host_id"] != host.id {
		t.Fatalf("event lost lease ownership: %#v", first)
	}
}

func TestHostTerminalEventRetriesForTheDeliveryWindow(t *testing.T) {
	client := &retryingCoreClient{fail: 4}
	host := &Host{api: client, id: "host-1"}
	run := Run{RunID: "run-1", LeaseToken: "lease-1"}
	if err := host.sendTerminalEvent(context.Background(), run, Event{Type: "completed"}); err != nil {
		t.Fatalf("send terminal event: %v", err)
	}
	if len(client.inputs) != 5 {
		t.Fatalf("attempts=%d, want 5", len(client.inputs))
	}
	firstID := client.inputs[0]["event_id"]
	for _, input := range client.inputs[1:] {
		if input["event_id"] != firstID {
			t.Fatalf("terminal retry changed event id: %#v", client.inputs)
		}
	}
}

func TestHostFinalizesDurableProviderCheckpointWithoutRunningAgentAgain(t *testing.T) {
	client := &retryingCoreClient{}
	runner := &countingRunner{}
	host := &Host{api: client, runner: runner, id: "host-2"}
	host.execute(context.Background(), Run{
		RunID: "run-1", Action: "finalize", HostID: "host-2", LeaseToken: "lease-2",
	})
	if runner.calls != 0 {
		t.Fatalf("provider runner called %d times", runner.calls)
	}
	if len(client.inputs) != 1 || client.inputs[0]["type"] != "completed" ||
		client.inputs[0]["host_id"] != "host-2" || client.inputs[0]["lease_token"] != "lease-2" {
		t.Fatalf("unexpected finalization event: %#v", client.inputs)
	}
}
