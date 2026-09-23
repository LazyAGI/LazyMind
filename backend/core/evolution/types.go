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
	SearchableSkills   []string
	ExcludedSkills     []string
	LoadedSkills       []LoadedSkill
	InvokedSkills      []LoadedSkill
	UsePersonalization bool
}

type LoadedSkill struct {
	SkillID    string `json:"skill_id"`
	SkillKey   string `json:"skill_key"`
	RevisionID string `json:"revision_id"`
	Content    string `json:"content"`
}
