package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf16"

	"lazymind/core/common/orm"
	"lazymind/core/evolution"
)

func TestSkillUsageManualSelectionPersistsAndCanBeCancelled(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.SkillV2Skill{}, &orm.SkillV2RevisionEntry{}, &orm.SkillV2Blob{}, &orm.ResourceSessionSnapshot{})
	conversation := orm.Conversation{ID: "conv", Ext: json.RawMessage(`{"keep":true}`), BaseModel: orm.BaseModel{CreateUserID: "user"}}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	revision, hash := "rev", "hash"
	content := "---\nname: paper\ndescription: Write papers\n---\nUse references/style.md.\n"
	for _, row := range []any{
		&orm.SkillV2Skill{ID: "skill", OwnerUserID: "user", Category: "external", SkillName: "paper", RelativeRoot: "external/paper", CallMode: "manual", IsEnabled: false, HeadRevisionID: &revision},
		&orm.SkillV2Blob{Hash: hash, Content: []byte(content)},
		&orm.SkillV2RevisionEntry{RevisionID: revision, Path: "SKILL.md", EntryType: "file", BlobHash: &hash},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{"请使用 paper Skill", "继续"} {
		resources := &evolution.ChatResourceContext{}
		_, _, err := applyChatMentions(context.Background(), db.DB, map[string]any{}, "user", "conv", "session", query, resources)
		if err != nil {
			t.Fatal(err)
		}
		if len(resources.SearchableSkills) != 1 || resources.SearchableSkills[0] != "external/paper" {
			t.Fatalf("%s: skills=%v", query, resources.SearchableSkills)
		}
		if query == "请使用 paper Skill" {
			if len(resources.LoadedSkills) != 1 || resources.LoadedSkills[0].Content != content {
				t.Fatalf("%s: missing first-load L2: %+v", query, resources.LoadedSkills)
			}
			if len(resources.InvokedSkills) != 0 {
				t.Fatalf("%s: first load should not replay history: %+v", query, resources.InvokedSkills)
			}
		} else if len(resources.LoadedSkills) != 0 || len(resources.InvokedSkills) != 1 || resources.InvokedSkills[0].Content != content {
			t.Fatalf("%s: want persisted invocation without re-read: loaded=%+v invoked=%+v", query, resources.LoadedSkills, resources.InvokedSkills)
		}
	}
	for _, query := range []string{"不要使用 paper Skill", "继续"} {
		resources := &evolution.ChatResourceContext{AvailableSkills: []string{"external/paper"}, SearchableSkills: []string{"external/paper"}}
		_, _, err := applyChatMentions(context.Background(), db.DB, map[string]any{}, "user", "conv", "session", query, resources)
		if err != nil {
			t.Fatal(err)
		}
		if len(resources.SearchableSkills) != 0 || len(resources.LoadedSkills) != 0 {
			t.Fatalf("%s: cancelled skill still available: %+v", query, resources)
		}
	}
	if err := db.First(&conversation, "id = ?", "conv").Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conversation.Ext), `"keep":true`) {
		t.Fatalf("unrelated ext lost: %s", conversation.Ext)
	}
}

func TestSkillUsageRecognizesExplicitNameNotIncidentalText(t *testing.T) {
	skills := []orm.SkillV2Skill{{ID: "a", Category: "external", SkillName: "paper"}, {ID: "b", Category: "internal", SkillName: "paper"}}
	if _, err := explicitSkillMentions("请使用 paper Skill", skills); err == nil {
		t.Fatal("ambiguous name must require disambiguation")
	}
	for _, query := range []string{"paper Skill 是什么？", "请使用 paperclip", "不要使用 paperclip"} {
		got, err := explicitSkillMentions(query, skills)
		if err != nil || len(got) != 0 {
			t.Fatalf("%q unexpectedly selected skill: %v %v", query, got, err)
		}
	}
	got, err := explicitSkillMentions("请使用 external/paper Skill", skills)
	if err != nil || len(got) != 1 || got[0].ResourceID != "a" {
		t.Fatalf("full key: %v %v", got, err)
	}
}

func TestSkillUsageStructuredMentionDisambiguatesExactName(t *testing.T) {
	skills := []orm.SkillV2Skill{{ID: "a", Category: "external", SkillName: "paper"}, {ID: "b", Category: "internal", SkillName: "paper"}}
	position := len([]rune("请使用 @"))
	got, err := explicitSkillMentions("请使用 @paper Skill", skills, chatMention{Type: "skill", ResourceID: "b", DisplayName: "paper", Start: &position})
	if err != nil || len(got) != 1 || got[0].ResourceID != "b" {
		t.Fatalf("structured choice ignored: %v %v", got, err)
	}
}

func TestSkillUsageLaterCommandsWin(t *testing.T) {
	for _, tc := range []struct {
		query    string
		selected bool
	}{
		{"请使用 @paper，算了，不要使用 paper Skill", false},
		{"不要使用 @paper，后来请使用 paper Skill", true},
		{"请使用 @paper，不要使用 @paper", false},
		{"不要使用 @paper，请使用 @paper", true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			db := seedSkillUsagePolicyDB(t)
			first := len([]rune(strings.Split(tc.query, "paper")[0]))
			mentions := []chatMention{{Type: "skill", ResourceID: "skill", DisplayName: "paper", Start: &first}}
			if strings.Count(tc.query, "@paper") > 1 {
				last := len([]rune(tc.query[:strings.LastIndex(tc.query, "paper")]))
				mentions = append(mentions, chatMention{Type: "skill", ResourceID: "skill", DisplayName: "paper", Start: &last})
			}
			resources := &evolution.ChatResourceContext{}
			_, _, err := applyChatMentions(context.Background(), db.DB, map[string]any{"mentions": mentions}, "user", "conv", "session", tc.query, resources)
			if err != nil {
				t.Fatal(err)
			}
			if (len(resources.SearchableSkills) > 0) != tc.selected {
				t.Fatalf("selection=%v, want %v", resources.SearchableSkills, tc.selected)
			}
			if (len(resources.LoadedSkills) > 0) != tc.selected {
				t.Fatalf("first-load L2=%v, want %v", resources.LoadedSkills, tc.selected)
			}
		})
	}
}

func TestSkillUsagePreviewDoesNotPersistSnapshotsOrSelection(t *testing.T) {
	db := seedSkillUsagePolicyDB(t)
	// Include a default catalog entry as well as the manual explicit selection.
	revision, hash := "auto-rev", "auto-hash"
	for _, row := range []any{
		&orm.SkillV2Skill{ID: "auto", OwnerUserID: "user", Category: "internal", SkillName: "automatic", RelativeRoot: "internal/automatic", CallMode: "on_demand", IsEnabled: true, HeadRevisionID: &revision},
		&orm.SkillV2Blob{Hash: hash, Content: []byte("---\nname: automatic\ndescription: Auto\n---\nBody.\n")},
		&orm.SkillV2RevisionEntry{RevisionID: revision, Path: "SKILL.md", EntryType: "file", BlobHash: &hash},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	resources, err := evolution.BuildChatResourceContext(context.Background(), db.DB, "user", "", "preview", false)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = applyChatMentions(context.Background(), db.DB, map[string]any{}, "user", "conv", "preview", "请使用 paper Skill", resources, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources.LoadedSkills) != 1 {
		t.Fatal("preview did not load selected L2")
	}
	var count int64
	if err := db.Model(&orm.ResourceSessionSnapshot{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("preview created %d snapshots", count)
	}
	var conv orm.Conversation
	if err := db.First(&conv, "id = ?", "conv").Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(conv.Ext), "skill_usage") {
		t.Fatalf("preview mutated selection: %s", conv.Ext)
	}
}

func seedSkillUsagePolicyDB(t *testing.T) *orm.DB {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.SkillV2Skill{}, &orm.SkillV2RevisionEntry{}, &orm.SkillV2Blob{}, &orm.ResourceSessionSnapshot{}, &orm.UserPersonalizationSetting{}, &orm.UserUIPreferences{})
	revision, hash := "rev", "hash"
	for _, row := range []any{
		&orm.Conversation{ID: "conv", Ext: json.RawMessage(`{"keep":true}`), BaseModel: orm.BaseModel{CreateUserID: "user"}},
		&orm.SkillV2Skill{ID: "skill", OwnerUserID: "user", Category: "external", SkillName: "paper", RelativeRoot: "external/paper", CallMode: "manual", HeadRevisionID: &revision},
		&orm.SkillV2Blob{Hash: hash, Content: []byte("---\nname: paper\ndescription: Write papers\n---\nBody.\n")},
		&orm.SkillV2RevisionEntry{RevisionID: revision, Path: "SKILL.md", EntryType: "file", BlobHash: &hash},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestSkillUsageCancelsByNameAndRejectsSuffixCue(t *testing.T) {
	skills := []orm.SkillV2Skill{{ID: "skill", Category: "external", SkillName: "paper"}}
	for _, query := range []string{"refuse paper", "misuse paper", "reuse paper"} {
		got, err := explicitSkillMentions(query, skills)
		if err != nil || len(got) != 0 {
			t.Fatalf("incidental suffix authorized skill: %q %v %v", query, got, err)
		}
	}
	db := seedSkillUsagePolicyDB(t)
	for _, query := range []string{"请使用 paper Skill", "取消 paper Skill", "继续"} {
		resources := &evolution.ChatResourceContext{}
		_, _, err := applyChatMentions(context.Background(), db.DB, map[string]any{}, "user", "conv", "session", query, resources)
		if err != nil {
			t.Fatal(err)
		}
		if (len(resources.SearchableSkills) > 0) != (query == "请使用 paper Skill") {
			t.Fatalf("query=%s searchable=%v", query, resources.SearchableSkills)
		}
		if (len(resources.LoadedSkills) > 0) != (query == "请使用 paper Skill") {
			t.Fatalf("query=%s loaded=%v", query, resources.LoadedSkills)
		}
	}
}

func TestSkillUsageNormalizesBrowserUTF16OffsetsBeforeInferring(t *testing.T) {
	db := seedSkillUsagePolicyDB(t)
	query := "😀请使用 paper，不要使用 paper"
	first := len(utf16.Encode([]rune("😀请使用 ")))
	last := len(utf16.Encode([]rune(query[:strings.LastIndex(query, "paper")])))
	raw := map[string]any{"mentions": []chatMention{
		{Type: "skill", ResourceID: "skill", DisplayName: "paper", Start: &first},
		{Type: "skill", ResourceID: "skill", DisplayName: "paper", Start: &last},
	}}
	resources := &evolution.ChatResourceContext{}
	_, _, err := applyChatMentions(context.Background(), db.DB, raw, "user", "conv", "session", query, resources)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources.LoadedSkills) != 0 {
		t.Fatalf("emoji offsets lost cancellation: %v", resources.LoadedSkills)
	}
	mentions, err := parseChatMentions(raw, query)
	if err != nil {
		t.Fatal(err)
	}
	if *mentions[0].Start != len([]rune("😀请使用 ")) {
		t.Fatalf("browser offset not normalized: %d", *mentions[0].Start)
	}
}

func TestSkillUsageGlobalDenyPersistsAndHonorsLaterNameSelection(t *testing.T) {
	for _, phrase := range []string{"不要使用任何 Skill，直接回答", "请不要调用任何技能", "Do not use any skills, answer directly", "No skills, answer directly"} {
		t.Run(phrase, func(t *testing.T) {
			db := seedSkillUsagePolicyDB(t)
			otherRevision := "other-revision"
			if err := db.Create(&orm.SkillV2Skill{ID: "other", OwnerUserID: "user", Category: "internal", SkillName: "other", RelativeRoot: "internal/other", CallMode: "on_demand", HeadRevisionID: &otherRevision}).Error; err != nil {
				t.Fatal(err)
			}
			var conv orm.Conversation
			if err := db.Model(&conv).Where("id = ?", "conv").Update("ext", json.RawMessage(`{"skill_usage":{"selected_ids":["skill"],"excluded_ids":[]}}`)).Error; err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{phrase, "继续"} {
				resources := &evolution.ChatResourceContext{AvailableSkills: []string{"external/paper", "internal/other"}, SearchableSkills: []string{"external/paper", "internal/other"}}
				_, _, err := applyChatMentions(context.Background(), db.DB, map[string]any{}, "user", "conv", "session", query, resources)
				if err != nil {
					t.Fatal(err)
				}
				if len(resources.LoadedSkills) > 0 || len(resources.AvailableSkills) > 0 || len(resources.SearchableSkills) > 0 || len(resources.ExcludedSkills) != 2 {
					t.Fatalf("global deny not enforced for %q: %+v", query, resources)
				}
			}
		})
	}
	for _, tc := range []struct {
		query    string
		selected bool
	}{
		{"不要使用任何 Skill，之后请使用 paper Skill", true},
		{"请使用 paper Skill，之后不要使用任何 Skill", false},
		{"Do not use any skills; please use paper", true},
		{"Please use paper; do not use any skills", false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			db := seedSkillUsagePolicyDB(t)
			resources := &evolution.ChatResourceContext{}
			_, _, err := applyChatMentions(context.Background(), db.DB, map[string]any{}, "user", "conv", "session", tc.query, resources)
			if err != nil {
				t.Fatal(err)
			}
			if (len(resources.LoadedSkills) > 0) != tc.selected {
				t.Fatalf("wrong order for %q: %+v", tc.query, resources)
			}
		})
	}
}

func TestSkillUsageGlobalDenyDoesNotInterpretQuestions(t *testing.T) {
	for _, query := range []string{"你为什么不用任何 Skill？", "为什么不要使用任何技能？", "Why do not use any skills?", "Explain 'no skills' to me"} {
		db := seedSkillUsagePolicyDB(t)
		resources := &evolution.ChatResourceContext{AvailableSkills: []string{"external/paper"}, SearchableSkills: []string{"external/paper"}}
		_, _, err := applyChatMentions(context.Background(), db.DB, map[string]any{}, "user", "conv", "session", query, resources)
		if err != nil {
			t.Fatal(err)
		}
		if len(resources.ExcludedSkills) > 0 {
			t.Fatalf("question became command: %q", query)
		}
	}
}
