package resourceupdate

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

func TestApplySkillTaskLLMPrefersEvolution(t *testing.T) {
	worker := &Worker{
		resolveChatLLM: func(context.Context, *gorm.DB, string) (map[string]any, error) {
			t.Fatal("chat fallback must not be used when evo_llm is configured")
			return nil, nil
		},
	}
	got := worker.applySkillTaskLLM(context.Background(), "user-1", map[string]any{
		"llm":     map[string]any{"source": "openai", "model": "chat-model"},
		"evo_llm": map[string]any{"source": "openai", "model": "evo-model"},
	})
	llm, _ := got["llm"].(map[string]any)
	if llm["model"] != "evo-model" {
		t.Fatalf("llm = %#v, want evo-model", llm)
	}
}

func TestApplySkillTaskLLMFallsBackToChat(t *testing.T) {
	worker := &Worker{
		resolveChatLLM: func(context.Context, *gorm.DB, string) (map[string]any, error) {
			return map[string]any{"source": "openai", "model": "chat-default"}, nil
		},
	}
	got := worker.applySkillTaskLLM(context.Background(), "user-1", map[string]any{
		"embed_main": map[string]any{"source": "openai", "model": "embed"},
	})
	llm, _ := got["llm"].(map[string]any)
	if llm["model"] != "chat-default" {
		t.Fatalf("llm = %#v, want chat-default", llm)
	}
}
