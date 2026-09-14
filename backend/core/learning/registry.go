package learning

import "encoding/json"

type Field struct {
	Key          string `json:"key"`
	Type         string `json:"type"`
	LabelI18nKey string `json:"label_i18n_key"`
	HelpI18nKey  string `json:"help_i18n_key"`
	Required     bool   `json:"required"`
	Editable     bool   `json:"editable"`
}
type CachePolicy struct {
	DefaultScope     string   `json:"default_scope"`
	AllowedScopes    []string `json:"allowed_scopes"`
	ContextSensitive bool     `json:"context_sensitive"`
}
type Capability struct {
	Key                  string      `json:"key"`
	Version              int         `json:"version"`
	NameI18nKey          string      `json:"name_i18n_key"`
	DescriptionI18nKey   string      `json:"description_i18n_key"`
	LocalOnly            bool        `json:"local_only"`
	Languages            []string    `json:"languages"`
	SubjectKinds         []string    `json:"subject_kinds"`
	Fields               []Field     `json:"fields"`
	ProviderPipeline     []string    `json:"provider_pipeline"`
	AllowedQuestionTypes []string    `json:"allowed_question_types"`
	DefaultQuestionTypes []string    `json:"default_question_types"`
	CachePolicy          CachePolicy `json:"cache_policy"`
}
type QuestionType struct {
	Key         string `json:"key"`
	Version     int    `json:"version"`
	NameI18nKey string `json:"name_i18n_key"`
	Dynamic     bool   `json:"dynamic"`
}
type ProfileDefinition struct {
	Key                string   `json:"key"`
	NameI18nKey        string   `json:"name_i18n_key"`
	DescriptionI18nKey string   `json:"description_i18n_key"`
	Capabilities       []string `json:"capabilities"`
}

var capabilities = []Capability{
	{Key: "english_definition", Version: 1, NameI18nKey: "learning.capability.englishDefinition.name", DescriptionI18nKey: "learning.capability.englishDefinition.description", Languages: []string{"en"}, SubjectKinds: []string{"word", "phrase"}, Fields: []Field{{Key: "phonetic", Type: "string", LabelI18nKey: "learning.field.phonetic", Editable: true}, {Key: "meaning", Type: "text", LabelI18nKey: "learning.field.meaning", Required: true, Editable: true}, {Key: "examples", Type: "string_list", LabelI18nKey: "learning.field.examples", Editable: true}}, ProviderPipeline: []string{"preset", "cache", "english_dictionary", "llm"}, AllowedQuestionTypes: []string{"single_choice", "text_input", "cloze"}, DefaultQuestionTypes: []string{"single_choice", "text_input", "cloze"}, CachePolicy: CachePolicy{DefaultScope: "user_global", AllowedScopes: []string{"user_global", "knowledge_base", "document"}, ContextSensitive: true}},
	{Key: "chinese_definition", Version: 1, NameI18nKey: "learning.capability.chineseDefinition.name", DescriptionI18nKey: "learning.capability.chineseDefinition.description", LocalOnly: true, Languages: []string{"zh-Hans", "zh-Hant"}, SubjectKinds: []string{"character", "word", "idiom"}, Fields: []Field{{Key: "pinyin", Type: "string", LabelI18nKey: "learning.field.pinyin", Editable: true}, {Key: "meaning_in_context", Type: "text", LabelI18nKey: "learning.field.meaningInContext", Required: true, Editable: true}, {Key: "examples", Type: "string_list", LabelI18nKey: "learning.field.examples", Editable: true}}, ProviderPipeline: []string{"preset", "cache", "chinese_idiom_dictionary", "chinese_dictionary", "llm"}, AllowedQuestionTypes: []string{"single_choice", "text_input", "cloze"}, DefaultQuestionTypes: []string{"single_choice", "text_input"}, CachePolicy: CachePolicy{DefaultScope: "user_global", AllowedScopes: []string{"user_global", "knowledge_base", "document"}, ContextSensitive: true}},
	{Key: "classical_definition", Version: 1, NameI18nKey: "learning.capability.classicalDefinition.name", DescriptionI18nKey: "learning.capability.classicalDefinition.description", LocalOnly: true, Languages: []string{"lzh", "zh-Hans", "zh-Hant"}, SubjectKinds: []string{"character", "word", "phrase"}, Fields: []Field{{Key: "pinyin", Type: "string", LabelI18nKey: "learning.field.pinyin", Editable: true}, {Key: "meaning_in_context", Type: "text", LabelI18nKey: "learning.field.meaningInContext", Required: true, Editable: true}, {Key: "phenomena", Type: "string_list", LabelI18nKey: "learning.field.phenomena", Editable: true}, {Key: "citations", Type: "string_list", LabelI18nKey: "learning.field.citations", Editable: false}}, ProviderPipeline: []string{"preset", "cache", "classical_chinese_dictionary", "llm"}, AllowedQuestionTypes: []string{"single_choice", "text_input", "true_false"}, DefaultQuestionTypes: []string{"single_choice", "text_input"}, CachePolicy: CachePolicy{DefaultScope: "document", AllowedScopes: []string{"knowledge_base", "document"}, ContextSensitive: true}},
	{Key: "pinyin", Version: 1, NameI18nKey: "learning.capability.pinyin.name", DescriptionI18nKey: "learning.capability.pinyin.description", LocalOnly: true, Languages: []string{"zh-Hans", "zh-Hant", "lzh"}, SubjectKinds: []string{"character", "word", "idiom", "sentence"}, Fields: []Field{{Key: "pinyin", Type: "string", LabelI18nKey: "learning.field.pinyin", Required: true, Editable: true}, {Key: "polyphonic_note", Type: "text", LabelI18nKey: "learning.field.polyphonicNote", Editable: true}}, ProviderPipeline: []string{"preset", "cache", "chinese_idiom_dictionary", "chinese_dictionary", "classical_chinese_dictionary", "llm"}, AllowedQuestionTypes: []string{"text_input", "single_choice"}, DefaultQuestionTypes: []string{"text_input"}, CachePolicy: CachePolicy{DefaultScope: "user_global", AllowedScopes: []string{"user_global", "knowledge_base", "document"}, ContextSensitive: true}},
	{Key: "general_translation", Version: 1, NameI18nKey: "learning.capability.generalTranslation.name", DescriptionI18nKey: "learning.capability.generalTranslation.description", LocalOnly: true, Languages: []string{"*"}, SubjectKinds: []string{"word", "phrase", "sentence", "passage"}, Fields: []Field{{Key: "translation", Type: "text", LabelI18nKey: "learning.field.translation", Required: true, Editable: true}, {Key: "target_language", Type: "string", LabelI18nKey: "learning.field.targetLanguage", Required: true, Editable: true}}, ProviderPipeline: []string{"preset", "cache", "translation", "llm"}, AllowedQuestionTypes: []string{"text_input", "translation_response"}, DefaultQuestionTypes: []string{"text_input"}, CachePolicy: CachePolicy{DefaultScope: "user_global", AllowedScopes: []string{"user_global", "knowledge_base", "document"}, ContextSensitive: false}},
	{Key: "classical_translation", Version: 1, NameI18nKey: "learning.capability.classicalTranslation.name", DescriptionI18nKey: "learning.capability.classicalTranslation.description", LocalOnly: true, Languages: []string{"lzh", "zh-Hans", "zh-Hant"}, SubjectKinds: []string{"sentence", "passage"}, Fields: []Field{{Key: "translation", Type: "text", LabelI18nKey: "learning.field.translation", Required: true, Editable: true}, {Key: "key_words", Type: "string_list", LabelI18nKey: "learning.field.keyWords", Editable: true}, {Key: "special_patterns", Type: "string_list", LabelI18nKey: "learning.field.specialPatterns", Editable: true}}, ProviderPipeline: []string{"preset", "cache", "classical_chinese_dictionary", "llm"}, AllowedQuestionTypes: []string{"text_input", "translation_response", "rubric_self_assessment"}, DefaultQuestionTypes: []string{"rubric_self_assessment"}, CachePolicy: CachePolicy{DefaultScope: "document", AllowedScopes: []string{"knowledge_base", "document"}, ContextSensitive: true}},
	{Key: "literary_appreciation", Version: 1, NameI18nKey: "learning.capability.literaryAppreciation.name", DescriptionI18nKey: "learning.capability.literaryAppreciation.description", LocalOnly: true, Languages: []string{"zh-Hans", "zh-Hant"}, SubjectKinds: []string{"sentence", "passage", "document"}, Fields: []Field{{Key: "techniques", Type: "string_list", LabelI18nKey: "learning.field.techniques", Required: true, Editable: true}, {Key: "evidence", Type: "string_list", LabelI18nKey: "learning.field.evidence", Required: true, Editable: true}, {Key: "effects", Type: "string_list", LabelI18nKey: "learning.field.effects", Required: true, Editable: true}}, ProviderPipeline: []string{"preset", "cache", "llm"}, AllowedQuestionTypes: []string{"single_choice", "short_answer", "rubric_self_assessment"}, DefaultQuestionTypes: []string{"rubric_self_assessment"}, CachePolicy: CachePolicy{DefaultScope: "document", AllowedScopes: []string{"document"}, ContextSensitive: true}},
}
var questionTypes = []QuestionType{{"single_choice", 1, "learning.questionType.singleChoice.name", false}, {"text_input", 1, "learning.questionType.textInput.name", false}, {"cloze", 1, "learning.questionType.cloze.name", false}, {"true_false", 1, "learning.questionType.trueFalse.name", true}, {"translation_response", 1, "learning.questionType.translationResponse.name", true}, {"short_answer", 1, "learning.questionType.shortAnswer.name", true}, {"rubric_self_assessment", 1, "learning.questionType.rubricSelfAssessment.name", true}}
var profiles = []ProfileDefinition{{"general", "learning.profile.general.name", "learning.profile.general.description", []string{"general_translation"}}, {"academic_papers", "learning.profile.academicPapers.name", "learning.profile.academicPapers.description", []string{"english_definition", "general_translation"}}, {"chinese_modern", "learning.profile.chineseModern.name", "learning.profile.chineseModern.description", []string{"chinese_definition", "pinyin", "literary_appreciation"}}, {"chinese_classical", "learning.profile.chineseClassical.name", "learning.profile.chineseClassical.description", []string{"classical_definition", "pinyin", "classical_translation", "literary_appreciation"}}, {"english_learning", "learning.profile.englishLearning.name", "learning.profile.englishLearning.description", []string{"english_definition", "general_translation"}}, {"legal", "learning.profile.legal.name", "learning.profile.legal.description", []string{"chinese_definition", "english_definition", "general_translation"}}}

func CapabilityByKey(key string) (Capability, bool) {
	for _, v := range capabilities {
		if v.Key == key {
			return v, true
		}
	}
	return Capability{}, false
}
func marshal(v any) string { b, _ := json.Marshal(v); return string(b) }
