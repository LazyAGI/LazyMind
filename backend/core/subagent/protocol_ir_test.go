package subagent

import (
	"bytes"
	"encoding/json"
	"lazymind/core/common/orm"
	"testing"
)

func TestRemoteStepPreservesAssistantContinuation(t *testing.T) {
	var event TaskEvent
	if err := json.Unmarshal([]byte(`{"type":"tool_calls","tool_calls":[{"id":"c1","name":"render","args":{"page":1}}],"durable_assistant_message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"render","arguments":"{\"page\":1}"}}],"_lazyllm_provider_items":{"protocol":"responses","items":[{"type":"reasoning","encrypted_content":"opaque"}]}}}`), &event); err != nil {
		t.Fatal(err)
	}
	role, raw := remoteStepContent(event)
	var content map[string]json.RawMessage
	if err := json.Unmarshal(raw, &content); err != nil {
		t.Fatal(err)
	}
	if role != "assistant" || len(content["assistant_message"]) == 0 {
		t.Fatalf("continuation missing: %s", raw)
	}
	var message map[string]json.RawMessage
	if err := json.Unmarshal(content["assistant_message"], &message); err != nil {
		t.Fatal(err)
	}
	if len(message["_lazyllm_provider_items"]) == 0 {
		t.Fatalf("opaque continuation missing: %s", raw)
	}
}

func TestAssistantContinuationIsPrivateToResume(t *testing.T) {
	step := &orm.SubAgentStep{Role: "assistant", Content: json.RawMessage(`{"text":"visible","tool_calls":[],"assistant_message":{"_lazyllm_provider_items":{"encrypted_content":"private"}}}`)}
	public := toStepDTO(step)
	if bytes.Contains(public.Content, []byte("private")) || bytes.Contains(public.Content, []byte("assistant_message")) {
		t.Fatalf("private continuation exposed: %s", public.Content)
	}
	if !bytes.Contains(toPrivateStepDTO(step).Content, []byte("private")) {
		t.Fatal("resume lost continuation")
	}
	if !bytes.Contains(step.Content, []byte("private")) {
		t.Fatal("public projection mutated stored step")
	}
}
