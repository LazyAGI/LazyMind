package skill

import "lazymind/core/compat/contract"

type ListInput struct {
	Keyword  string               // Matches metadata and head text.
	Category string               // Exact category filter.
	Tags     []string             // Required tags; all must match.
	Page     contract.PageRequest // Pagination input.
}

type Summary struct {
	ID             string       // Stable skill ID.
	Name           string       // Display name.
	Description    string       // Short description.
	Category       string       // Skill category.
	Tags           []string     // Skill tags.
	HeadRevisionID string       // Current head revision ID.
	AutoEvo        bool         // Whether auto evolution is enabled.
	Enabled        bool         // Whether the skill is enabled.
	Draft          DraftSummary // Draft state summary.
}

type DraftSummary struct {
	HasUncommittedDraft bool   // Whether draft overlays exist.
	TaskID              string // Current draft task ID.
	Version             int64  // Draft version.
}

type ListResult struct {
	Items []Summary           // Page items.
	Page  contract.PageResult // Pagination result.
}

type GetInput struct {
	SkillID        string // Skill ID to fetch.
	IncludeContent bool   // Whether to include SKILL.md content.
}

type Content struct {
	Path string // Content path.
	Text string // Text content.
}

type GetResult struct {
	Skill   Summary  // Skill summary.
	Content *Content // Optional SKILL.md content.
}
