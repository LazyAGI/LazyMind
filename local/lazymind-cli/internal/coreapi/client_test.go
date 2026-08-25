package coreapi

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"
)

func TestCloneRequestCarriesExternalLeaseContext(t *testing.T) {
	t.Setenv("LAZYMIND_EXTERNAL_REF", "run-1")
	t.Setenv("LAZYMIND_EXTERNAL_LEASE", "lease-1")
	t.Setenv("LAZYMIND_EXTERNAL_HOST", "host-1")
	t.Setenv("LAZYMIND_CONVERSATION_ID", "conversation-1")
	request := httptest.NewRequest("POST", "http://lazymind.test/api/core/test", bytes.NewBufferString(`{}`)).
		WithContext(WithInvocation(context.Background(), InvocationMetadata{
			ID: "inv-1", ClientName: "codex", ConnectorInstanceID: "connector-1",
		}))
	clone := cloneRequest(request, []byte(`{}`), "access-token")
	if clone.Header.Get("X-LazyMind-External-Ref") != "run-1" ||
		clone.Header.Get("X-LazyMind-External-Lease") != "lease-1" ||
		clone.Header.Get("X-LazyMind-External-Host") != "host-1" ||
		clone.Header.Get("X-LazyMind-Conversation-Id") != "conversation-1" ||
		clone.Header.Get("X-LazyMind-Invocation-Id") != "inv-1" {
		t.Fatalf("missing execution context headers: %#v", clone.Header)
	}
}

func TestCloneRequestKeepsStandaloneSourceOutsideManagedLeaseHeaders(t *testing.T) {
	request := httptest.NewRequest("POST", "http://lazymind.test/api/core/test", bytes.NewBufferString(`{}`)).
		WithContext(WithInvocation(context.Background(), InvocationMetadata{
			ID: "inv-standalone", ClientName: "codex", ConnectorInstanceID: "connector-1",
			ConversationID: "conversation-standalone", ExternalRef: "run-standalone",
		}))
	clone := cloneRequest(request, []byte(`{}`), "access-token")
	if clone.Header.Get("X-LazyMind-External-Ref") != "" ||
		clone.Header.Get("X-LazyMind-Conversation-Id") != "" {
		t.Fatalf("standalone source leaked into managed lease headers: %#v", clone.Header)
	}
	if clone.Header.Get("X-LazyMind-Invocation-Conversation-Id") != "conversation-standalone" {
		t.Fatalf("standalone invocation scope is missing: %#v", clone.Header)
	}
}
