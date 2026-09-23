package evolution

import (
	"sort"
	"strings"

	"lazymind/core/common/orm"
	skillv2 "lazymind/core/skillv2"
)

type rankedSkill struct {
	skill    orm.SkillV2Skill
	key      string
	priority bool
}

func skillKey(skill orm.SkillV2Skill) string {
	return strings.TrimSpace(skill.Category) + "/" + strings.TrimSpace(skill.SkillName)
}

func rankChatSkills(skills []orm.SkillV2Skill) []rankedSkill {
	ranked := make([]rankedSkill, 0, len(skills))
	for _, skill := range skills {
		mode := skillv2.NormalizeCallMode(skill.CallMode, skill.IsEnabled)
		if !skillv2.CallModeEnabled(mode) {
			continue
		}
		ranked = append(ranked, rankedSkill{
			skill:    skill,
			key:      skillKey(skill),
			priority: mode == skillv2.CallModePriority,
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].priority != ranked[j].priority {
			return ranked[i].priority
		}
		if ranked[i].skill.SortRank != ranked[j].skill.SortRank {
			return ranked[i].skill.SortRank > ranked[j].skill.SortRank
		}
		if !ranked[i].skill.CreatedAt.Equal(ranked[j].skill.CreatedAt) {
			return ranked[i].skill.CreatedAt.After(ranked[j].skill.CreatedAt)
		}
		return ranked[i].skill.ID > ranked[j].skill.ID
	})
	return ranked
}

func selectInjectedSkillKeys(skills []orm.SkillV2Skill, limit int) (injected []string, searchable []string) {
	if limit <= 0 {
		limit = skillv2.DefaultInjectLimit
	}
	ranked := rankChatSkills(skills)
	searchable = make([]string, 0, len(ranked))
	injected = make([]string, 0, min(limit, len(ranked)))
	seen := map[string]struct{}{}
	for _, item := range ranked {
		if item.key == "" {
			continue
		}
		if _, ok := seen[item.key]; ok {
			continue
		}
		seen[item.key] = struct{}{}
		searchable = append(searchable, item.key)
		if item.priority || len(injected) < limit {
			injected = append(injected, item.key)
		}
	}
	return injected, searchable
}

func appendUniqueSkill(dst []string, name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return dst
	}
	for _, existing := range dst {
		if existing == name {
			return dst
		}
	}
	return append(dst, name)
}
