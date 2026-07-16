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
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
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
	if _, ok := gotBody["skill_base_dir"]; ok {
		t.Fatalf("ReviewSkill() sent non-contract field skill_base_dir: %#v", gotBody)
	}
	if _, ok := gotBody["fs_base_url"]; ok {
		t.Fatalf("ReviewSkill() sent non-contract field fs_base_url: %#v", gotBody)
	}
	if modelConfigs, ok := gotBody["model_configs"].(map[string]any); !ok || len(modelConfigs) != 0 {
		t.Fatalf("ReviewSkill() model_configs = %#v, want empty object", gotBody["model_configs"])
	}
	if response == nil || response.Data.RequestID != "request-1" {
		t.Fatalf("ReviewSkill() response = %#v", response)
	}
}

func TestSkillOrganizeRequestMatchesAlgorithmContract(t *testing.T) {
	body, err := json.Marshal(SkillOrganizeRequest{
		RequestID: "org-request-1",
		UserID:    "user-1",
		Skills:    []string{"vcs/git-usage"},
	})
	if err != nil {
		t.Fatalf("marshal SkillOrganizeRequest: %v", err)
	}
	var gotBody map[string]any
	if err := json.Unmarshal(body, &gotBody); err != nil {
		t.Fatalf("unmarshal SkillOrganizeRequest: %v", err)
	}
	if _, ok := gotBody["fs_base_url"]; ok {
		t.Fatalf("SkillOrganizeRequest sent non-contract field fs_base_url: %#v", gotBody)
	}
}
