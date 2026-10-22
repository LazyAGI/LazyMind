package workbuddyhost

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestIdentityRendezvousIsScopedAndEphemeral(t *testing.T) {
	var broker Broker
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		identity, err := broker.Resolve(ctx, "connector-a", json.RawMessage(`{"workflow_id":"wf","idempotency_key":"stable-start-key-1"}`))
		if err == nil && identity.ToolCallID != "call-1" {
			t.Error("wrong native call")
		}
		done <- err
	}()
	var pending []IdentityRequest
	for len(pending) == 0 && ctx.Err() == nil {
		pending = broker.Pending("connector-a")
		time.Sleep(time.Millisecond)
	}
	if len(pending) != 1 {
		t.Fatal("request not registered")
	}
	if len(broker.Pending("connector-b")) != 0 {
		t.Fatal("cross-connector disclosure")
	}
	identity := Identity{SessionID: "8c76b002-6467-41f5-a6aa-f9e4242d8865", RequestID: "req-1", ToolCallID: "call-1"}
	if broker.Accept("connector-b", pending[0].ID, identity) == nil {
		t.Fatal("wrong connector resolved identity")
	}
	if broker.Accept("connector-a", pending[0].ID, Identity{SessionID: identity.SessionID}) == nil {
		t.Fatal("missing evidence accepted")
	}
	if err := broker.Accept("connector-a", pending[0].ID, identity); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if broker.Accept("connector-a", pending[0].ID, identity) == nil {
		t.Fatal("expired request replayed")
	}
}
func TestIdentityTimeoutAndMissingRetryKey(t *testing.T) {
	var broker Broker
	if _, err := broker.Resolve(context.Background(), "a", json.RawMessage(`{"workflow_id":"wf"}`)); err == nil {
		t.Fatal("missing key accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := broker.Resolve(ctx, "a", json.RawMessage(`{"idempotency_key":"stable-start-key-1"}`)); err == nil {
		t.Fatal("cancelled lookup accepted")
	}
	if len(broker.Pending("a")) != 0 {
		t.Fatal("expired identity request retained")
	}
}
