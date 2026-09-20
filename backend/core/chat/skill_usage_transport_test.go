package chat

import (
	"context"
	"encoding/json"
	"testing"

	"lazymind/core/evolution"
)

func TestSkillUsageTransportPreservesFullL2AndExclusions(t *testing.T) {
	const content = "---\nname: paper\ntags: [original-tag]\n---\n# Paper\nRead references/style.md.\n"
	resourceContext := &evolution.ChatResourceContext{
		AvailableSkills:  []string{"external/paper"},
		SearchableSkills: []string{"external/paper", "external/search"},
		ExcludedSkills:   []string{"external/denied"},
		LoadedSkills:     []evolution.LoadedSkill{{SkillID: "paper-id", SkillKey: "external/paper", RevisionID: "rev1", Content: content}},
	}
	body := buildChatRequestBody(context.Background(), nil, "conv-1", "session-1", "请使用 paper Skill", nil, map[string]any{}, resourceContext, "user-1", 1)
	request := buildLazyChatRequest(body)
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Agent struct {
			LoadedSkills     []evolution.LoadedSkill `json:"loaded_skills"`
			ExcludedSkills   []string                `json:"excluded_skills"`
			SearchableSkills []string                `json:"searchable_skills"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Agent.LoadedSkills) != 1 || payload.Agent.LoadedSkills[0].Content != content || payload.Agent.LoadedSkills[0].RevisionID != "rev1" {
		t.Fatalf("full L2/revision lost in Core -> algorithm transport: %s", encoded)
	}
	if len(payload.Agent.ExcludedSkills) != 1 || payload.Agent.ExcludedSkills[0] != "external/denied" || len(payload.Agent.SearchableSkills) != 2 {
		t.Fatalf("skill access policy lost in Core -> algorithm transport: %s", encoded)
	}
}
