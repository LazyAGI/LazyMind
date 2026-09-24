package algo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTrajToSkillUsesRegisteredChatRoute(t *testing.T) {
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

	response, status, err := TrajToSkill(context.Background(), TrajToSkillRequest{
		RequestID:  "request-1",
		UserID:     "user-1",
		SessionIDs: []string{"conversation-1"},
	})
	if err != nil {
		t.Fatalf("TrajToSkill() error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("TrajToSkill() status = %d, want %d", status, http.StatusOK)
	}
	if gotPath != "/api/chat/traj_to_skill" {
		t.Fatalf("TrajToSkill() path = %q, want %q", gotPath, "/api/chat/traj_to_skill")
	}
	if _, ok := gotBody["skill_base_dir"]; ok {
		t.Fatalf("TrajToSkill() sent non-contract field skill_base_dir: %#v", gotBody)
	}
	if _, ok := gotBody["fs_base_url"]; ok {
		t.Fatalf("TrajToSkill() sent non-contract field fs_base_url: %#v", gotBody)
	}
	if _, ok := gotBody["start_time"]; ok {
		t.Fatalf("TrajToSkill() sent removed field start_time: %#v", gotBody)
	}
	if _, ok := gotBody["end_time"]; ok {
		t.Fatalf("TrajToSkill() sent removed field end_time: %#v", gotBody)
	}
	if sessionIDs, ok := gotBody["session_ids"].([]any); !ok || len(sessionIDs) != 1 || sessionIDs[0] != "conversation-1" {
		t.Fatalf("TrajToSkill() session_ids = %#v", gotBody["session_ids"])
	}
	if _, ok := gotBody["pending_skill_ids"]; ok {
		t.Fatalf("TrajToSkill() sent removed field pending_skill_ids: %#v", gotBody)
	}
	if _, ok := gotBody["min_user_turns"]; ok {
		t.Fatalf("TrajToSkill() sent optional field min_user_turns: %#v", gotBody)
	}
	if _, ok := gotBody["min_tool_turns"]; ok {
		t.Fatalf("TrajToSkill() sent optional field min_tool_turns: %#v", gotBody)
	}
	if _, ok := gotBody["artifact_dir"]; ok {
		t.Fatalf("TrajToSkill() sent optional field artifact_dir: %#v", gotBody)
	}
	if modelConfigs, ok := gotBody["model_configs"].(map[string]any); !ok || len(modelConfigs) != 0 {
		t.Fatalf("TrajToSkill() model_configs = %#v, want empty object", gotBody["model_configs"])
	}
	if response == nil || response.Data.RequestID != "request-1" {
		t.Fatalf("TrajToSkill() response = %#v", response)
	}
}

func TestReviewMemoryMatchesAlgorithmContract(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "success",
			"task_id": "memory_review_core-task-1",
			"outcome": "saved",
		})
	}))
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)

	response, status, err := ReviewMemory(context.Background(), MemoryReviewRequest{
		TaskID:                     "memory_review_core-task-1",
		UserID:                     "user-1",
		ConversationID:             "conversation-1",
		ConversationLastActiveAtMS: 1784791231000,
		History:                    []map[string]any{{"role": "user", "content": "hello"}},
		LLMConfig:                  map[string]any{"chat": map[string]any{"model": "demo"}},
	})
	if err != nil {
		t.Fatalf("ReviewMemory() error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("ReviewMemory() status = %d, want %d", status, http.StatusOK)
	}
	if gotPath != "/api/chat/memory_review" {
		t.Fatalf("ReviewMemory() path = %q, want %q", gotPath, "/api/chat/memory_review")
	}
	if len(gotBody) != 7 || gotBody["task_id"] != "memory_review_core-task-1" || gotBody["user_id"] != "user-1" {
		t.Fatalf("ReviewMemory() body = %#v", gotBody)
	}
	if gotBody["conversation_id"] != "conversation-1" ||
		gotBody["conversation_last_active_at_ms"] != float64(1784791231000) {
		t.Fatalf("ReviewMemory() conversation context = %#v", gotBody)
	}
	if _, ok := gotBody["history"]; !ok {
		t.Fatalf("ReviewMemory() omitted history: %#v", gotBody)
	}
	if _, ok := gotBody["llm_config"]; !ok {
		t.Fatalf("ReviewMemory() omitted llm_config: %#v", gotBody)
	}
	if response == nil || response.Status != "success" || response.TaskID != "memory_review_core-task-1" || response.Outcome != "saved" {
		t.Fatalf("ReviewMemory() response = %#v", response)
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

func TestOrganizeSkillModeDefaultsAndForwards(t *testing.T) {
	var gotMode string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotMode, _ = body["mode"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"status":"pending","requestid":"org-test","taskid":"org-task"}}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	for _, mode := range []string{"", "light", "deep"} {
		_, _, err := OrganizeSkill(context.Background(), SkillOrganizeRequest{Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		want := mode
		if want == "" {
			want = "light"
		}
		if gotMode != want {
			t.Fatalf("forwarded mode=%q, want %q", gotMode, want)
		}
	}
}
