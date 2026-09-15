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
