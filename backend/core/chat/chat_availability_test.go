package chat

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatRuntimeDropsCloudConfigWhenNoConnectionsRemain(t *testing.T) {
	database := newPromptTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"items":[]}}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", server.URL)
	t.Setenv("LAZYMIND_MODEL_CONFIG_PATH", "dynamic")
	body := map[string]any{"tool_config": map[string]any{"feishu": "old-fixture-token"}}
	if err := applyChatRuntimeConfigs(t.Context(), database.DB, "fixture-owner", body); err != nil {
		t.Fatal(err)
	}
	if config, _ := body["tool_config"].(map[string]any); len(config) != 0 {
		t.Fatalf("previous credentials survived an empty connection result: %v", config)
	}
}
