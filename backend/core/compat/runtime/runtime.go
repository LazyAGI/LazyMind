package runtime

import (
	"lazymind/core/compat/skill"
)

type Runtime struct {
	Skill *skill.Facade
}

type Dependencies struct {
	SkillPort skill.Port
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
	return rt, nil
}
