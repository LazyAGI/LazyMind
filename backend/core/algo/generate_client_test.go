package algo

import "testing"

func TestGenerateURLUsesChatServiceEndpoint(t *testing.T) {
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo-service.invalid")
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", "http://chat-service:8046")

	got := generateURL(rewritePath)
	want := "http://chat-service:8046/api/chat/rewrite"
	if got != want {
		t.Fatalf("expected generate URL %q, got %q", want, got)
	}
}
