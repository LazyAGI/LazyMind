package evolution

import (
	"strconv"
	"testing"
	"time"

	"lazymind/core/common/orm"
	skillv2 "lazymind/core/skillv2"
)

func TestSelectInjectedSkillKeysPrefersPriorityThenNewest(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	skills := make([]orm.SkillV2Skill, 0, 24)
	for i := 0; i < 22; i++ {
		skills = append(skills, orm.SkillV2Skill{
			ID:        "ondemand-" + strconv.Itoa(i),
			Category:  "lab",
			SkillName: "skill-" + strconv.Itoa(i),
			IsEnabled: true,
			CallMode:  skillv2.CallModeOnDemand,
			SortRank:  int64(i),
			CreatedAt: now.Add(time.Duration(i) * time.Minute),
		})
	}
	skills = append(skills, orm.SkillV2Skill{
		ID:        "priority",
		Category:  "lab",
		SkillName: "pinned",
		IsEnabled: true,
		CallMode:  skillv2.CallModePriority,
		SortRank:  1,
		CreatedAt: now.Add(-time.Hour),
	})
	skills = append(skills, orm.SkillV2Skill{
		ID:        "disabled",
		Category:  "lab",
		SkillName: "paused",
		IsEnabled: false,
		CallMode:  skillv2.CallModeDisabled,
		CreatedAt: now.Add(time.Hour),
	})

	injected, searchable := selectInjectedSkillKeys(skills, 20)
	if len(injected) != 20 {
		t.Fatalf("injected=%d, want 20: %#v", len(injected), injected)
	}
	if injected[0] != "lab/pinned" {
		t.Fatalf("priority skill should lead injection, got %q", injected[0])
	}
	if injected[1] != "lab/skill-21" {
		t.Fatalf("newest on-demand skill should follow priority, got %q", injected[1])
	}
	if len(searchable) != 23 {
		t.Fatalf("searchable=%d, want 23 enabled skills", len(searchable))
	}
	for _, name := range injected {
		if name == "lab/paused" {
			t.Fatal("disabled skill must not be injected")
		}
	}
}

func TestSelectInjectedSkillKeysKeepsAllPrioritySkills(t *testing.T) {
	now := time.Now()
	skills := make([]orm.SkillV2Skill, 0, 22)
	for i := 0; i < 22; i++ {
		skills = append(skills, orm.SkillV2Skill{
			ID:        "p-" + strconv.Itoa(i),
			Category:  "lab",
			SkillName: "pin-" + strconv.Itoa(i),
			IsEnabled: true,
			CallMode:  skillv2.CallModePriority,
			SortRank:  int64(i),
			CreatedAt: now,
		})
	}
	injected, searchable := selectInjectedSkillKeys(skills, 20)
	if len(injected) != 22 {
		t.Fatalf("priority skills must all inject, got %d", len(injected))
	}
	if len(searchable) != 22 {
		t.Fatalf("searchable=%d, want 22", len(searchable))
	}
}

func TestManualExcludedFromDefaultCatalog(t *testing.T) {
	skills := []orm.SkillV2Skill{{ID: "manual", Category: "external", SkillName: "manual", CallMode: "manual", IsEnabled: true}}
	injected, searchable := selectInjectedSkillKeys(skills, 20)
	if len(injected) != 0 || len(searchable) != 0 {
		t.Fatalf("manual injected=%v searchable=%v", injected, searchable)
	}
}
