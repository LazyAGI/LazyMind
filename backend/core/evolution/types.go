package evolution

const (
	ResourceTypeSkill = "skill"

	SkillNodeTypeParent = "parent"
	SkillNodeTypeChild  = "child"

	UpdateStatusUpToDate = "up_to_date"
)

type ChatResourceContext struct {
	DisabledTools      []string
	AvailableSkills    []string
	SkillAliases       map[string][]string
	UsePersonalization bool
}
