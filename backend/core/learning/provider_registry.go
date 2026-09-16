package learning

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lazymind/core/translation"
)

type ProviderRequest struct {
	Capability Capability
	Input      ResolveContentRequest
	Current    map[string]any
}
type ProviderResult struct {
	Content  map[string]any
	Complete bool
	Source   string
	Evidence []map[string]any
}
type ContentProvider interface {
	Key() string
	Resolve(context.Context, string, ProviderRequest) (ProviderResult, error)
}

type providerFunc struct {
	key string
	fn  func(context.Context, string, ProviderRequest) (ProviderResult, error)
}

func (p providerFunc) Key() string { return p.key }
func (p providerFunc) Resolve(ctx context.Context, owner string, req ProviderRequest) (ProviderResult, error) {
	return p.fn(ctx, owner, req)
}

func (s *Service) providers() map[string]ContentProvider {
	out := map[string]ContentProvider{}
	for _, key := range []string{"english_dictionary", "chinese_dictionary", "chinese_idiom_dictionary", "classical_chinese_dictionary"} {
		providerKey := key
		out[key] = providerFunc{key: key, fn: func(ctx context.Context, owner string, req ProviderRequest) (ProviderResult, error) {
			if !s.dictionaryProviderApplies(ctx, owner, providerKey, req) {
				return ProviderResult{Source: providerKey}, nil
			}
			value, found, err := s.dictionaryLookup(ctx, providerKey, req.Input.Language, req.Input.Text)
			return ProviderResult{Content: value, Complete: found && len(requiredMissing(req.Capability, value)) == 0, Source: providerKey}, err
		}}
	}
	out["translation"] = providerFunc{key: "translation", fn: func(ctx context.Context, owner string, req ProviderRequest) (ProviderResult, error) {
		translated, err := translation.TranslateText(ctx, owner, req.Input.Text, req.Input.TargetLanguage)
		if err != nil {
			return ProviderResult{}, err
		}
		return ProviderResult{Content: map[string]any{"translation": translated.TranslatedText, "target_language": translated.Target}, Complete: true, Source: "translation"}, nil
	}}
	out["llm"] = providerFunc{key: "llm", fn: func(ctx context.Context, owner string, req ProviderRequest) (ProviderResult, error) {
		value, err := s.resolveWithLLM(ctx, owner, req.Capability, req.Input, req.Current)
		return ProviderResult{Content: value, Complete: err == nil && len(requiredMissing(req.Capability, value)) == 0, Source: "llm"}, err
	}}
	return out
}

var registeredProviderKeys = map[string]bool{"preset": true, "cache": true, "english_dictionary": true, "chinese_dictionary": true, "chinese_idiom_dictionary": true, "classical_chinese_dictionary": true, "translation": true, "llm": true}

type CardRecipe struct{ CapabilityKey, QuestionType string }

var cardRecipes = []CardRecipe{
	{"english_definition", "single_choice"}, {"english_definition", "text_input"}, {"english_definition", "cloze"},
	{"chinese_definition", "single_choice"}, {"chinese_definition", "text_input"}, {"chinese_definition", "cloze"},
	{"classical_definition", "single_choice"}, {"classical_definition", "text_input"},
	{"pinyin", "single_choice"}, {"pinyin", "text_input"},
	{"general_translation", "text_input"}, {"classical_translation", "text_input"},
	{"literary_appreciation", "single_choice"},
}

func (s *Service) runProviderPipeline(ctx context.Context, owner string, def Capability, in ResolveContentRequest) (map[string]any, string, error) {
	value, sources := map[string]any{}, []string{}
	providers := s.providers()
	for _, key := range def.ProviderPipeline {
		if key == "preset" || key == "cache" {
			continue
		}
		provider := providers[key]
		if provider == nil {
			return nil, "", fmt.Errorf("capability %s references unknown provider %s", def.Key, key)
		}
		current := value
		if key == "llm" && needsContextDisambiguation(def, in) {
			current = cloneLearningValue(value)
			delete(current, contextualMeaningField(def))
		}
		result, err := provider.Resolve(ctx, owner, ProviderRequest{Capability: def, Input: in, Current: current})
		// A failed non-LLM provider is a soft failure so the configured fallback can run.
		if err != nil {
			if key == "llm" && len(requiredMissing(def, value)) > 0 {
				return nil, "", err
			}
			continue
		}
		if len(result.Content) > 0 {
			if key == "llm" && needsContextDisambiguation(def, in) {
				mergeContextualMeaning(value, result.Content, def)
			}
			mergeMissing(value, result.Content)
			sources = append(sources, result.Source)
		}
		if len(requiredMissing(def, value)) == 0 && !(key != "llm" && needsContextDisambiguation(def, in)) {
			break
		}
	}
	return value, strings.Join(sources, "+"), nil
}

func needsContextDisambiguation(def Capability, in ResolveContentRequest) bool {
	if strings.TrimSpace(in.Context) == "" {
		return false
	}
	return def.Key == "english_definition" || def.Key == "chinese_definition" || def.Key == "classical_definition"
}

func contextualMeaningField(def Capability) string {
	if def.Key == "english_definition" {
		return "meaning"
	}
	return "meaning_in_context"
}

func mergeContextualMeaning(value, generated map[string]any, def Capability) {
	field := contextualMeaningField(def)
	contextual := strings.TrimSpace(fmt.Sprint(generated[field]))
	if contextual == "" || contextual == "<nil>" {
		return
	}
	dictionaryMeaning := strings.TrimSpace(fmt.Sprint(value[field]))
	if dictionaryMeaning != "" && dictionaryMeaning != "<nil>" && dictionaryMeaning != contextual {
		value["dictionary_meaning"] = dictionaryMeaning
	}
	value[field] = generated[field]
}

func cloneLearningValue(value map[string]any) map[string]any {
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func ValidateRegistry() error {
	capabilityKeys := map[string]bool{}
	questionKeys := map[string]bool{}
	dynamicQuestions := map[string]bool{}
	for _, q := range questionTypes {
		if q.Key == "" || q.Version < 1 || q.NameI18nKey == "" {
			return errors.New("invalid question type registration")
		}
		if questionKeys[q.Key] {
			return fmt.Errorf("duplicate question type registration: %s", q.Key)
		}
		questionKeys[q.Key] = true
		dynamicQuestions[q.Key] = q.Dynamic
	}
	for _, c := range capabilities {
		if c.Key == "" || c.Version < 1 || c.NameI18nKey == "" || c.DescriptionI18nKey == "" {
			return errors.New("invalid capability registration")
		}
		if capabilityKeys[c.Key] {
			return fmt.Errorf("duplicate capability registration: %s", c.Key)
		}
		capabilityKeys[c.Key] = true
		fields := map[string]bool{}
		for _, f := range c.Fields {
			if f.Key == "" || f.Type == "" || f.LabelI18nKey == "" || fields[f.Key] {
				return fmt.Errorf("invalid schema field registration for %s", c.Key)
			}
			fields[f.Key] = true
		}
		for _, provider := range c.ProviderPipeline {
			if !registeredProviderKeys[provider] {
				return fmt.Errorf("capability %s references unknown provider %s", c.Key, provider)
			}
		}
		allowed := map[string]bool{}
		for _, q := range c.AllowedQuestionTypes {
			if !questionKeys[q] {
				return fmt.Errorf("capability %s references unknown question type %s", c.Key, q)
			}
			allowed[q] = true
			if !dynamicQuestions[q] {
				found := false
				for _, recipe := range cardRecipes {
					if recipe.CapabilityKey == c.Key && recipe.QuestionType == q {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("capability %s has no recipe for question type %s", c.Key, q)
				}
			}
		}
		for _, q := range c.DefaultQuestionTypes {
			if !allowed[q] {
				return fmt.Errorf("capability %s has invalid default question type %s", c.Key, q)
			}
		}
		if c.CachePolicy.DefaultScope == "" || !contains(c.CachePolicy.AllowedScopes, c.CachePolicy.DefaultScope) {
			return fmt.Errorf("capability %s has invalid cache policy", c.Key)
		}
	}
	return nil
}

func init() {
	if err := ValidateRegistry(); err != nil {
		panic(err)
	}
}
