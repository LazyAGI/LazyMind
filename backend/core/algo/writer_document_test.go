package algo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWriterDocumentSyncResponseUsesProviderSynced(t *testing.T) {
	var response WriterDocumentSyncResponse
	if err := json.Unmarshal([]byte(`{
		"success":true,
		"changed":true,
		"provider_synced":true,
		"persisted_document":{"document_id":"doc-1"}
	}`), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.ProviderSynced {
		t.Fatal("provider_synced was not decoded")
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	if strings.Contains(string(encoded), "feishu_synced") {
		t.Fatalf("legacy field leaked into response: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"provider_synced":true`) {
		t.Fatalf("provider_synced missing from response: %s", encoded)
	}
}

func TestWriterDocumentSyncResponseKeepsProviderWriteResult(t *testing.T) {
	var response WriterDocumentSyncResponse
	if err := json.Unmarshal([]byte(`{
		"success":true,
		"provider_synced":true,
		"persisted_document":"# Updated\n\n![image](assets/image.png)",
		"representation":"markdown",
		"provider":"github",
		"write_result":{"commit_sha":"commit-1","pull_request_url":"https://github.com/acme/docs/pull/1"},
		"target_document":{"adapter":"github","meta":{"work_branch":"lazymind/op-1"}}
	}`), &response); err != nil {
		t.Fatalf("decode writer response: %v", err)
	}
	if response.Provider != "github" || response.Representation != "markdown" {
		t.Fatalf("unexpected provider response: %+v", response)
	}
	if !strings.Contains(string(response.WriteResult), `"commit_sha":"commit-1"`) {
		t.Fatalf("write result was not preserved: %s", response.WriteResult)
	}
	if !strings.Contains(string(response.TargetDocument), `"work_branch":"lazymind/op-1"`) {
		t.Fatalf("target document was not preserved: %s", response.TargetDocument)
	}
}
