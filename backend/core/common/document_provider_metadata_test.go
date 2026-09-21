package common

import (
	"encoding/json"
	"testing"
)

func TestLocalEditCannotIntroducePublicationMetadata(t *testing.T) {
	old := json.RawMessage(`{"schema":"text/markdown","data":"original"}`)
	edited := json.RawMessage(`{"schema":"text/markdown","data":"edited","meta":{"lazymind_provider_sync":{"provider":"other","target_document":{"doc_id":"injected"}}}}`)
	var value map[string]any
	_ = json.Unmarshal(PreserveDocumentProviderMetadata(old, edited), &value)
	if meta, ok := value["meta"].(map[string]any); ok && meta["lazymind_provider_sync"] != nil {
		t.Fatal("client introduced server publication metadata")
	}
}
func TestLocalEditPreservesProviderOwnedIRIdentity(t *testing.T) {
	old := json.RawMessage(`{"schema":"application/vnd.lazymind.writer+json","data":{"document_id":"document","provider_binding":{"provider":"notion","document_id":"remote"},"blocks":[{"node_id":"p","content":"original","provider_binding":{"block_id":"bound"}}]}}`)
	edited := json.RawMessage(`{"schema":"application/vnd.lazymind.writer+json","data":{"document_id":"forged","provider_binding":{"provider":"notion","document_id":"other"},"blocks":[{"node_id":"p","content":"edited","provider_binding":{"block_id":"forged"}}]}}`)
	var value map[string]any
	_ = json.Unmarshal(PreserveDocumentProviderMetadata(old, edited), &value)
	doc := value["data"].(map[string]any)
	if doc["document_id"] != "document" || doc["provider_binding"].(map[string]any)["document_id"] != "remote" {
		t.Fatal("client changed provider document identity")
	}
	block := doc["blocks"].([]any)[0].(map[string]any)
	if block["provider_binding"].(map[string]any)["block_id"] != "bound" || block["content"] != "edited" {
		t.Fatal("provider block identity not preserved independently of content")
	}
}

func TestLocalEditCannotStageProviderIdentityInjection(t *testing.T) {
	for _, original := range []string{`{"document_id":"local","blocks":[]}`, `{"document_id":"local","blocks":[],"provider_binding":{"provider":"notion","document_id":"real"}}`} {
		first := PreserveDocumentProviderMetadata(json.RawMessage(original), json.RawMessage(`{"document_id":"forged","provider_binding":{"provider":"notion","document_id":"forged"}}`))
		second := PreserveDocumentProviderMetadata(first, json.RawMessage(`{"document_id":"forged","blocks":[],"provider_binding":{"provider":"notion","document_id":"forged"}}`))
		var got map[string]any
		_ = json.Unmarshal(second, &got)
		if binding, ok := got["provider_binding"].(map[string]any); ok && binding["document_id"] == "forged" {
			t.Fatalf("two-step injection survived: %s", second)
		}
	}
}

func TestMarkdownEditsAfterIRPublicationPreserveCloudTarget(t *testing.T) {
	previous := json.RawMessage(`{"schema":"application/vnd.lazymind.writer+json","data":{"document_id":"published","blocks":[]},"meta":{"lazymind_provider_sync":{"provider":"feishu","confirmed":true,"target_document":{"adapter":"feishu","doc_id":"test-target"}}}}`)
	edited := json.RawMessage(`{"schema":"text/markdown","text":"# New local edits"}`)
	var value map[string]any
	if err := json.Unmarshal(PreserveDocumentProviderMetadata(previous, edited), &value); err != nil {
		t.Fatal(err)
	}
	if value["schema"] != "text/markdown" || value["data"] != "# New local edits" {
		t.Fatalf("local Markdown representation changed: %v", value)
	}
	sync := value["meta"].(map[string]any)["lazymind_provider_sync"].(map[string]any)
	if sync["confirmed"] != false || sync["provider"] != "feishu" || sync["target_document"].(map[string]any)["doc_id"] != "test-target" {
		t.Fatalf("publication target or local edit state lost: %v", sync)
	}
}
