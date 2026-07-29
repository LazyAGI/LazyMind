package runtime

import (
	"lazymind/core/compat/knowledge"
	"lazymind/core/compat/skill"
)

type Runtime struct {
	Skill     *skill.Facade
	Knowledge *knowledge.Facade
}

type Dependencies struct {
	SkillPort         skill.Port
	KnowledgeCatalog  knowledge.CatalogPort
	KnowledgeDocument knowledge.DocumentPort
}

func New(deps Dependencies) (*Runtime, error) {
	rt := &Runtime{}
	if deps.SkillPort != nil {
		facade, err := skill.NewFacade(deps.SkillPort)
		if err != nil {
			return nil, err
		}
		rt.Skill = facade
	}
	if deps.KnowledgeCatalog != nil || deps.KnowledgeDocument != nil {
		facade, err := knowledge.NewFacadeWithDeps(knowledge.FacadeDeps{
			Catalog:  deps.KnowledgeCatalog,
			Document: deps.KnowledgeDocument,
		})
		if err != nil {
			return nil, err
		}
		rt.Knowledge = facade
	}
	return rt, nil
}
