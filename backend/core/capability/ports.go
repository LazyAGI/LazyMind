package capability

import "context"

type SkillListQuery struct {
	Keyword  string
	Category string
	Tags     []string
	Offset   int
	Limit    int
}

type SkillListPage struct {
	Items []SkillSummary
	Total int64
}

type SkillMetadata struct {
	Summary   SkillSummary
	Published bool
}

type SkillReader interface {
	ListSkills(context.Context, InvocationContext, SkillListQuery) (SkillListPage, error)
	GetSkillMetadata(context.Context, InvocationContext, string) (SkillMetadata, error)
	ReadSkillContent(context.Context, InvocationContext, string, string) (SkillContent, error)
}

type KnowledgeListQuery struct {
	Keyword string
	Tags    []string
	Offset  int
	Limit   int
}

type KnowledgeListPage struct {
	Items      []KnowledgeSummary
	Total      int64
	HasMore    bool
	NextOffset int
}

type KnowledgeCatalog interface {
	ListKnowledge(context.Context, InvocationContext, KnowledgeListQuery) (KnowledgeListPage, error)
}

type KnowledgeDocumentListQuery struct {
	KnowledgeID string
	Offset      int
	Limit       int
}

type KnowledgeDocumentListPage struct {
	Items []KnowledgeDocumentSummary
	Total int64
}

type KnowledgeDocumentReader interface {
	ListKnowledgeDocuments(context.Context, InvocationContext, KnowledgeDocumentListQuery) (KnowledgeDocumentListPage, error)
	GetKnowledgeDocument(context.Context, InvocationContext, GetKnowledgeDocumentInput) (GetKnowledgeDocumentResult, error)
}

type KnowledgeSearcher interface {
	SearchKnowledge(context.Context, InvocationContext, SearchKnowledgeInput) (SearchKnowledgeResult, error)
}
