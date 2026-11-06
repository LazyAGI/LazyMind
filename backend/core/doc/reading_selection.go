package doc

import (
	"context"
	"lazymind/core/modelconfig"
	"lazymind/core/readingpolicy"
	"lazymind/core/store"
)

func loadReadingParser(ctx context.Context, user string) (map[string]any, error) {
	p, err := readingpolicy.Load(ctx, store.DB(), user)
	if err != nil {
		return nil, err
	}
	return modelconfig.ResolveReadingParser(ctx, store.DB(), user, p.ParserRef)
}
func loadReadingStudyModel(ctx context.Context, user string) (*modelconfig.AvailableChatModel, error) {
	p, err := readingpolicy.Load(ctx, store.DB(), user)
	if err != nil {
		return nil, err
	}
	return modelconfig.ResolveReadingStudyModel(ctx, store.DB(), user, p.StudyModelRef)
}

type readingSelection struct {
	Options           []modelconfig.ReadingOption `json:"options"`
	DefaultLabel      string                      `json:"default_label,omitempty"`
	EffectiveLabel    string                      `json:"effective_label,omitempty"`
	UnavailableReason string                      `json:"unavailable_reason,omitempty"`
}

func readingSelectionMetadata(ctx context.Context, user string, p readingpolicy.Policy, parseErr, modelErr error) map[string]readingSelection {
	parsers, pe := modelconfig.ReadingParserOptions(ctx, store.DB(), user)
	models, me := modelconfig.ReadingStudyOptions(ctx, store.DB(), user)
	parser := readingSelection{Options: parsers}
	study := readingSelection{Options: models}
	if cfg, err := modelconfig.LoadOCRConfig(ctx, store.DB(), user); err == nil && cfg != nil {
		parser.DefaultLabel, _ = cfg["ocr_type"].(string)
	}
	if m, err := modelconfig.ResolveReadingStudyModel(ctx, store.DB(), user, ""); err == nil {
		study.DefaultLabel = m.ModelName
	}
	fill := func(s *readingSelection, ref string, err error) {
		if ref == "" {
			s.EffectiveLabel = s.DefaultLabel
		} else {
			for _, o := range s.Options {
				if o.ID == ref {
					s.EffectiveLabel = o.Label
				}
			}
		}
		if err != nil || s.EffectiveLabel == "" {
			s.UnavailableReason = "所选服务不可用，请重新选择或配置"
		}
	}
	if pe == nil {
		pe = parseErr
	}
	if me == nil {
		me = modelErr
	}
	fill(&parser, p.ParserRef, pe)
	fill(&study, p.StudyModelRef, me)
	return map[string]readingSelection{"parser": parser, "study": study}
}
