package algo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReviewSkillUsesRegisteredChatRoute(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"msg":  "accepted",
			"data": map[string]any{
				"status":    "running",
				"requestid": "request-1",
			},
		})
	}))
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)

	response, status, err := ReviewSkill(context.Background(), SkillReviewRequest{RequestID: "request-1"})
	if err != nil {
		t.Fatalf("ReviewSkill() error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("ReviewSkill() status = %d, want %d", status, http.StatusOK)
	}
	if gotPath != "/api/chat/skill_review" {
		t.Fatalf("ReviewSkill() path = %q, want %q", gotPath, "/api/chat/skill_review")
	}
	if response == nil || response.Data.RequestID != "request-1" {
		t.Fatalf("ReviewSkill() response = %#v", response)
	}
}
