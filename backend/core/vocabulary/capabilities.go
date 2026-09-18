package vocabulary

import (
	"errors"
	"sort"
	"strings"
)

type CapabilityDefinition struct {
	Key                  string   `json:"key"`
	NameI18nKey          string   `json:"name_i18n_key"`
	DescriptionI18nKey   string   `json:"description_i18n_key"`
	LocalOnly            bool     `json:"local_only"`
	AllowedQuestionTypes []string `json:"allowed_question_types"`
	DefaultQuestionTypes []string `json:"default_question_types"`
}

var capabilityDefinitions = map[string]CapabilityDefinition{
	"english_definition":    {Key: "english_definition", NameI18nKey: "vocabulary.capabilities.englishDefinition.name", DescriptionI18nKey: "vocabulary.capabilities.englishDefinition.description", AllowedQuestionTypes: []string{"single_choice", "text_input", "cloze"}, DefaultQuestionTypes: []string{"single_choice", "text_input", "cloze"}},
	"chinese_definition":    {Key: "chinese_definition", NameI18nKey: "vocabulary.capabilities.chineseDefinition.name", DescriptionI18nKey: "vocabulary.capabilities.chineseDefinition.description", LocalOnly: true, AllowedQuestionTypes: []string{"single_choice", "text_input", "cloze"}, DefaultQuestionTypes: []string{"single_choice", "text_input"}},
	"classical_definition":  {Key: "classical_definition", NameI18nKey: "vocabulary.capabilities.classicalDefinition.name", DescriptionI18nKey: "vocabulary.capabilities.classicalDefinition.description", LocalOnly: true, AllowedQuestionTypes: []string{"single_choice", "text_input"}, DefaultQuestionTypes: []string{"single_choice", "text_input"}},
	"general_translation":   {Key: "general_translation", NameI18nKey: "vocabulary.capabilities.generalTranslation.name", DescriptionI18nKey: "vocabulary.capabilities.generalTranslation.description", LocalOnly: true, AllowedQuestionTypes: []string{"text_input"}, DefaultQuestionTypes: []string{"text_input"}},
	"classical_translation": {Key: "classical_translation", NameI18nKey: "vocabulary.capabilities.classicalTranslation.name", DescriptionI18nKey: "vocabulary.capabilities.classicalTranslation.description", LocalOnly: true, AllowedQuestionTypes: []string{"text_input"}, DefaultQuestionTypes: []string{"text_input"}},
	"literary_appreciation": {Key: "literary_appreciation", NameI18nKey: "vocabulary.capabilities.literaryAppreciation.name", DescriptionI18nKey: "vocabulary.capabilities.literaryAppreciation.description", LocalOnly: true, AllowedQuestionTypes: []string{"single_choice"}, DefaultQuestionTypes: []string{"single_choice"}},
}

func ListCapabilityDefinitions() []CapabilityDefinition {
	rows := make([]CapabilityDefinition, 0, len(capabilityDefinitions))
	for _, row := range capabilityDefinitions {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows
}

func validateBookCapability(key string, questions []string) (CapabilityDefinition, []string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		key = "english_definition"
	}
	def, ok := capabilityDefinitions[key]
	if !ok {
		return def, nil, errors.New("unsupported learning capability")
	}
	if len(questions) == 0 {
		questions = append([]string(nil), def.DefaultQuestionTypes...)
	}
	allowed := map[string]bool{}
	for _, q := range def.AllowedQuestionTypes {
		allowed[q] = true
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(questions))
	for _, q := range questions {
		q = strings.TrimSpace(q)
		if !allowed[q] {
			return def, nil, errors.New("question type is not supported by learning capability")
		}
		if !seen[q] {
			seen[q] = true
			out = append(out, q)
		}
	}
	return def, out, nil
}
