package evolution

import (
	"strings"
)

const (
	ResourceTypeSkill          = "skill"
	ResourceTypeMemory         = "memory"
	ResourceTypeUserPreference = "user_preference"

	SkillNodeTypeParent = "parent"
	SkillNodeTypeChild  = "child"

	UpdateStatusUpToDate = "up_to_date"

	AutoEvoApplyStatusIdle    = "idle"
	AutoEvoApplyStatusRunning = "running"
	AutoEvoApplyStatusFailed  = "failed"
)

type ChatResourceContext struct {
	DisabledTools      []string
	AvailableSkills    []string
	Memory             string
	UserPreference     string
	UsePersonalization bool
}

func NormalizeAutoEvoApplyStatus(status string) string {
	switch strings.TrimSpace(status) {
	case AutoEvoApplyStatusRunning:
		return AutoEvoApplyStatusRunning
	case AutoEvoApplyStatusFailed:
		return AutoEvoApplyStatusFailed
	default:
		return AutoEvoApplyStatusIdle
	}
}
