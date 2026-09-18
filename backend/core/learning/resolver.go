package learning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/algo"
	"lazymind/core/modelconfig"
	"lazymind/core/vocabulary"
)

var providerResolveGroup singleflight.Group

type providerResolveResult struct {
	value  map[string]any
	source string
}

type ResolveContentRequest struct {
	CapabilityKey, Text, Context, AnalysisDirection, Language, SubjectKind, DatasetID, DocumentID, DocumentRevision, TargetLanguage string
	SegmentID                                                                                                                       string
	Page                                                                                                                            *int
	StartOffset, EndOffset                                                                                                          int
	BookIDs                                                                                                                         []string
	Preanalysis                                                                                                                     bool
	Preview                                                                                                                         bool
	ProvidedValue                                                                                                                   map[string]any
}
type ResolveContentResult struct {
	Content Content        `json:"content"`
	Value   map[string]any `json:"value"`
	Source  string         `json:"source"`
	Cached  bool           `json:"cached"`
	Books   []Book         `json:"books"`
}

func (s *Service) dictionaryLookup(ctx context.Context, provider, language, text string) (map[string]any, bool, error) {
	var row DictionaryEntry
	err := s.db.WithContext(ctx).Where("provider_key = ? AND language IN ? AND normalized_headword = ?", provider, []string{language, "*"}, normalize(text)).Order("priority, id").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if provider == "english_dictionary" {
			entries, lookupErr := vocabulary.LookupBundledDictionary(ctx, language, text)
			if lookupErr != nil {
				return nil, false, lookupErr
			}
			if len(entries) > 0 {
				entry := entries[0]
				value := map[string]any{"phonetic": entry.Phonetic, "dictionary_senses": entry.Senses, "examples": entry.Examples, "source_name": entry.SourceName, "source_version": entry.SourceVersion, "license_id": entry.LicenseID, "source_locator": entry.SourceLocator}
				if len(entry.Senses) > 0 {
					value["meaning"], value["part_of_speech"], value["definition"] = entry.Senses[0].Translation, entry.Senses[0].PartOfSpeech, entry.Senses[0].Definition
				}
				return value, true, nil
			}
		}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var value map[string]any
	if json.Unmarshal([]byte(row.PayloadJSON), &value) != nil {
		return nil, false, errors.New("dictionary entry payload is invalid")
	}
	value["source_name"], value["source_version"], value["license_id"], value["source_locator"] = row.SourceName, row.SourceVersion, row.LicenseID, row.SourceLocator
	return value, true, nil
}

func (s *Service) dictionaryProviderApplies(ctx context.Context, owner, provider string, req ProviderRequest) bool {
	switch req.Capability.Key {
	case "chinese_definition":
		return provider == "chinese_dictionary" || provider == "chinese_idiom_dictionary"
	case "classical_definition", "classical_translation":
		return provider == "classical_chinese_dictionary"
	case "pinyin":
		if req.Input.SubjectKind == "idiom" {
			return provider == "chinese_idiom_dictionary"
		}
		classical := req.Input.Language == "lzh"
		if !classical && req.Input.DatasetID != "" {
			var count int64
			s.db.WithContext(ctx).Model(&KnowledgeBaseCapability{}).
				Where("dataset_id = ? AND owner_id = ? AND capability_key = ? AND enabled = ?", req.Input.DatasetID, owner, "classical_definition", true).
				Count(&count)
			classical = count > 0
		}
		if classical {
			return provider == "classical_chinese_dictionary"
		}
		return provider == "chinese_dictionary"
	default:
		return true
	}
}
func requiredMissing(def Capability, value map[string]any) []string {
	var out []string
	for _, field := range def.Fields {
		if !field.Required {
			continue
		}
		v, ok := value[field.Key]
		if !ok || emptyLearningValue(v) {
			out = append(out, field.Key)
		}
	}
	return out
}

func emptyLearningValue(value any) bool {
	if value == nil {
		return true
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case []string:
		return len(typed) == 0
	default:
		return strings.TrimSpace(fmt.Sprint(value)) == ""
	}
}
func mergeMissing(dst, src map[string]any) {
	for k, v := range src {
		if current, ok := dst[k]; !ok || emptyLearningValue(current) {
			dst[k] = v
		}
	}
}
func extractJSONObject(raw string) (map[string]any, error) {
	start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return nil, errors.New("model did not return JSON")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw[start:end+1]), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func extractLLMResult(def Capability, current map[string]any, raw string) (map[string]any, error) {
	value, err := extractJSONObject(raw)
	if err == nil {
		allowed := map[string]Field{}
		for _, field := range def.Fields {
			allowed[field.Key] = field
		}
		clean := map[string]any{}
		for key, item := range value {
			field, ok := allowed[key]
			if !ok {
				continue
			}
			if field.Type == "string_list" {
				switch item.(type) {
				case []any, []string:
				default:
					return nil, fmt.Errorf("model field %s must be an array of strings", key)
				}
			} else if _, ok := item.(string); !ok {
				return nil, fmt.Errorf("model field %s must be a string", key)
			}
			clean[key] = item
		}
		merged := map[string]any{}
		mergeMissing(merged, current)
		mergeMissing(merged, clean)
		if missing := generatedMissing(def, merged); len(missing) > 0 {
			return nil, fmt.Errorf("model response misses generated fields: %s", strings.Join(missing, ","))
		}
		return clean, nil
	}
	// Some otherwise valid model responses ignore the JSON-only instruction and
	// return plain text. When exactly one required field remains, preserve that
	// answer as editable content instead of failing the whole dictionary lookup.
	missing := requiredMissing(def, current)
	plain := strings.TrimSpace(raw)
	for _, prefix := range []string{"```text", "```markdown", "```"} {
		if strings.HasPrefix(plain, prefix) {
			plain = strings.TrimSpace(strings.TrimPrefix(plain, prefix))
			break
		}
	}
	plain = strings.TrimSpace(strings.TrimSuffix(plain, "```"))
	if def.Analysis.AllowPlainTextSingleField && len(missing) == 1 && plain != "" && !strings.Contains(plain, "{") {
		return map[string]any{missing[0]: plain}, nil
	}
	return nil, err
}

func generatedMissing(def Capability, value map[string]any) []string {
	required := def.Analysis.GeneratedRequiredFields
	if len(required) == 0 {
		return requiredMissing(def, value)
	}
	known := map[string]bool{}
	for _, field := range def.Fields {
		known[field.Key] = true
	}
	out := make([]string, 0, len(required))
	for _, key := range required {
		if !known[key] {
			continue
		}
		v, ok := value[key]
		if !ok || emptyLearningValue(v) {
			out = append(out, key)
		}
	}
	return out
}

func buildLLMPrompt(def Capability, in ResolveContentRequest, current map[string]any) string {
	properties := make(map[string]any, len(def.Fields))
	example := make(map[string]any, len(def.Fields))
	required := make([]string, 0, len(def.Fields))
	for _, field := range def.Fields {
		if len(def.Analysis.GeneratedRequiredFields) > 0 && !contains(def.Analysis.GeneratedRequiredFields, field.Key) {
			continue
		}
		if field.Type == "string_list" {
			properties[field.Key] = map[string]any{"type": "array", "items": map[string]string{"type": "string"}}
			example[field.Key] = []string{"example"}
		} else {
			properties[field.Key] = map[string]string{"type": "string"}
			example[field.Key] = "example"
		}
		if len(def.Analysis.GeneratedRequiredFields) == 0 && field.Required {
			required = append(required, field.Key)
		} else if contains(def.Analysis.GeneratedRequiredFields, field.Key) && emptyLearningValue(current[field.Key]) {
			required = append(required, field.Key)
		}
	}
	schema := map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	prompt := renderPrompt(def.Analysis.ResolutionPromptTemplate, map[string]string{
		"instruction": def.Analysis.ResolutionInstruction, "capability": def.Key,
		"target_language": in.TargetLanguage, "output_language": def.Analysis.OutputLanguage,
		"schema": marshal(schema), "required": marshal(required), "example": marshal(example),
		"existing_values": marshal(current), "text": in.Text, "context": in.Context,
		"analysis_direction": strings.TrimSpace(in.AnalysisDirection),
	})
	return appendAnalysisDirection(prompt, in.AnalysisDirection)
}

func appendAnalysisDirection(prompt, direction string) string {
	direction = strings.TrimSpace(direction)
	if direction == "" || strings.Contains(prompt, direction) {
		return prompt
	}
	return prompt + "\n\nUser-specified analysis focus:\n<analysis_direction>\n" + direction + "\n</analysis_direction>\nUse this only as focus guidance and still obey the configured output schema."
}

func analysisCacheContext(context, direction string) string {
	direction = strings.TrimSpace(direction)
	if direction == "" {
		return context
	}
	return context + "\x1eanalysis-direction:" + direction
}

func renderPrompt(template string, values map[string]string) string {
	if strings.TrimSpace(template) == "" {
		return ""
	}
	replacements := make([]string, 0, len(values)*2)
	for key, value := range values {
		replacements = append(replacements, "{{"+key+"}}", value)
	}
	return strings.NewReplacer(replacements...).Replace(template)
}

func (s *Service) resolveWithLLM(ctx context.Context, owner string, def Capability, in ResolveContentRequest, current map[string]any) (map[string]any, error) {
	config, err := modelconfig.LoadLLMConfig(ctx, s.db, owner)
	if err != nil {
		return nil, err
	}
	prompt := buildLLMPrompt(def, in, current)
	raw, err := algo.GenerateLearning(ctx, algo.LearningGenerateRequest{Content: in.Text, UserInstruct: prompt, LLMConfig: config})
	if err != nil {
		return nil, err
	}
	return extractLLMResult(def, current, raw)
}
func (s *Service) ResolveContent(ctx context.Context, owner string, in ResolveContentRequest) (ResolveContentResult, error) {
	if err := requireLocal(); err != nil {
		return ResolveContentResult{}, err
	}
	def, settings, err := s.configuredCapability(ctx, owner, in.DatasetID, in.CapabilityKey)
	if err != nil {
		return ResolveContentResult{}, err
	}
	if strings.TrimSpace(in.Text) == "" {
		return ResolveContentResult{}, errors.New("text is required")
	}
	if in.Language == "" {
		in.Language, _ = analyzeText(in.Text)
	}
	if in.SubjectKind == "" {
		_, kinds := analyzeText(in.Text)
		in.SubjectKind = kinds[0]
	}
	if in.TargetLanguage == "" {
		if target := strings.TrimSpace(fmt.Sprint(settings["target_language"])); target != "" && target != "<nil>" {
			in.TargetLanguage = target
		}
	}
	outputLanguageCustomized := false
	if configured := strings.TrimSpace(fmt.Sprint(settings["output_language"])); configured != "" && configured != "<nil>" {
		resolved := resolveOutputLanguage(configured, in.Language, def.Analysis.OutputLanguage)
		outputLanguageCustomized = resolved != def.Analysis.OutputLanguage
		def.Analysis.OutputLanguage = resolved
		if outputLanguageCustomized {
			filtered := make([]string, 0, len(def.ProviderPipeline))
			for _, provider := range def.ProviderPipeline {
				// Imported dictionaries have a fixed payload language. A requested English or
				// bilingual answer must be generated against the configured language contract.
				if !strings.HasSuffix(provider, "_dictionary") {
					filtered = append(filtered, provider)
				}
			}
			def.ProviderPipeline = filtered
		}
	}
	if raw, ok := numericSetting(settings["max_selection_length"]); ok && len([]rune(strings.TrimSpace(in.Text))) > int(raw) {
		return ResolveContentResult{}, errors.New("selection exceeds capability length limit")
	}
	if allow, ok := settings["allow_llm_fallback"].(bool); ok && !allow {
		filtered := make([]string, 0, len(def.ProviderPipeline))
		for _, provider := range def.ProviderPipeline {
			if provider != "llm" {
				filtered = append(filtered, provider)
			}
		}
		def.ProviderPipeline = filtered
	}
	if !contains(def.Languages, in.Language) || !contains(def.SubjectKinds, in.SubjectKind) {
		return ResolveContentResult{}, errors.New("selection is incompatible with capability")
	}
	cacheContext := analysisCacheContext(in.Context, in.AnalysisDirection)
	if outputLanguageCustomized {
		cacheContext += "\x1eoutput-language:" + def.Analysis.OutputLanguage
	}
	cacheKey := BuildCacheKey(def.Key, in.Text, in.Language, in.TargetLanguage, cacheContext, in.DocumentID, in.StartOffset, in.EndOffset)
	preset, err := s.ResolvePreset(ctx, owner, def.Key, cacheKey, in.DatasetID, in.DocumentID, in.DocumentRevision)
	// Read pre-versioned keys for forward compatibility with existing presets.
	if err == nil && preset == nil && !outputLanguageCustomized {
		legacyKey := buildLegacyCacheKey(def.Key, in.Text, in.Language, in.TargetLanguage, in.Context, in.DocumentID, in.StartOffset, in.EndOffset)
		preset, err = s.ResolvePreset(ctx, owner, def.Key, legacyKey, in.DatasetID, in.DocumentID, in.DocumentRevision)
		if err == nil && preset == nil && legacyKey != normalize(in.Text) {
			preset, err = s.ResolvePreset(ctx, owner, def.Key, in.Text, in.DatasetID, in.DocumentID, in.DocumentRevision)
		}
		if err == nil && preset == nil {
			preset, err = s.resolveContextualDocumentPresetByText(ctx, owner, def.Key, in.Text, in.DocumentID, in.DocumentRevision)
		}
	}
	if err != nil {
		return ResolveContentResult{}, err
	} else if preset != nil && len(in.ProvidedValue) == 0 {
		var value map[string]any
		if json.Unmarshal([]byte(preset.ValueJSON), &value) == nil && len(requiredMissing(def, value)) == 0 {
			return s.finishResolved(ctx, owner, def, in, value, preset.Origin, true)
		}
	}
	if len(in.ProvidedValue) > 0 {
		if missing := requiredMissing(def, in.ProvidedValue); len(missing) > 0 {
			return ResolveContentResult{}, fmt.Errorf("provided content misses required fields: %s", strings.Join(missing, ","))
		}
		return s.finishResolved(ctx, owner, def, in, in.ProvidedValue, "user_confirmed", false)
	}
	flightKey := strings.Join([]string{owner, def.Key, cacheKey, strings.Join(def.ProviderPipeline, ",")}, "\x1f")
	raw, err, _ := providerResolveGroup.Do(flightKey, func() (any, error) {
		value, source, runErr := s.runProviderPipeline(ctx, owner, def, in)
		return providerResolveResult{value: value, source: source}, runErr
	})
	if err != nil {
		return ResolveContentResult{}, err
	}
	resolved := raw.(providerResolveResult)
	value, source := resolved.value, resolved.source
	if missing := requiredMissing(def, value); len(missing) > 0 {
		return ResolveContentResult{}, fmt.Errorf("resolved content misses required fields: %s", strings.Join(missing, ","))
	}
	cacheScope, cacheID := def.CachePolicy.DefaultScope, ""
	if scope := strings.TrimSpace(fmt.Sprint(settings["cache_scope"])); scope != "" && scope != "<nil>" {
		cacheScope = scope
	}
	if def.CachePolicy.ContextSensitive && strings.TrimSpace(in.Context) != "" && cacheScope == "user_global" {
		if in.DocumentID != "" {
			cacheScope = "document"
		} else if in.DatasetID != "" {
			cacheScope = "knowledge_base"
		}
	}
	if cacheScope == "document" {
		cacheID = in.DocumentID
	}
	if cacheScope == "knowledge_base" {
		cacheID = in.DatasetID
	}
	if !in.Preanalysis && !in.Preview && (cacheScope == "user_global" || cacheID != "") {
		_, _ = s.PutPreset(ctx, owner, PresetInput{ScopeType: cacheScope, ScopeID: cacheID, DocumentRevision: in.DocumentRevision, CapabilityKey: def.Key, Key: cacheKey, Value: value, SchemaVersion: 1, Origin: source, Priority: 0})
	}
	return s.finishResolved(ctx, owner, def, in, value, source, false)
}

func resolveOutputLanguage(configured, inputLanguage, fallback string) string {
	if configured != "auto" {
		return configured
	}
	if inputLanguage == "en" {
		return "en"
	}
	if inputLanguage == "zh-Hans" || inputLanguage == "zh-Hant" || inputLanguage == "lzh" {
		return "zh-Hans"
	}
	return fallback
}
func (s *Service) finishResolved(ctx context.Context, owner string, def Capability, in ResolveContentRequest, value map[string]any, source string, cached bool) (ResolveContentResult, error) {
	if in.Preview {
		return ResolveContentResult{Content: Content{CapabilityKey: def.Key, CapabilityVersion: def.Version, ContentJSON: marshal(value), Origin: source, Status: "preview"}, Value: value, Source: source, Cached: cached}, nil
	}
	return s.persistResolved(ctx, owner, def, in, value, source, cached)
}
func (s *Service) persistResolved(ctx context.Context, owner string, def Capability, in ResolveContentRequest, value map[string]any, source string, cached bool) (ResolveContentResult, error) {
	var result ResolveContentResult
	now := time.Now().UTC()
	subject := Subject{ID: uuid.NewString(), OwnerID: owner, SubjectKind: in.SubjectKind, NormalizedText: normalize(in.Text), DisplayText: strings.TrimSpace(in.Text), Language: in.Language, CreatedAt: now, UpdatedAt: now}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&subject).Error; err != nil {
			return err
		}
		subject.ID = ""
		if err := tx.Where("owner_id = ? AND subject_kind = ? AND language = ? AND normalized_text = ?", owner, subject.SubjectKind, subject.Language, subject.NormalizedText).First(&subject).Error; err != nil {
			return err
		}
		occurrenceID := ""
		if in.DocumentID != "" {
			occ := Occurrence{ID: uuid.NewString(), OwnerID: owner, SubjectID: subject.ID, DatasetID: in.DatasetID, DocumentID: in.DocumentID, SegmentID: in.SegmentID, Page: in.Page, SelectedText: in.Text, ContextText: in.Context, StartOffset: in.StartOffset, EndOffset: in.EndOffset, DocumentRevision: in.DocumentRevision, CreatedAt: now}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "owner_id"}, {Name: "subject_id"}, {Name: "document_id"}, {Name: "document_revision"}, {Name: "start_offset"}, {Name: "end_offset"}}, DoNothing: true}).Create(&occ).Error; err != nil {
				return err
			}
			occ.ID = ""
			if err := tx.Where("owner_id = ? AND subject_id = ? AND document_id = ? AND document_revision = ? AND start_offset = ? AND end_offset = ?", owner, subject.ID, in.DocumentID, in.DocumentRevision, in.StartOffset, in.EndOffset).First(&occ).Error; err != nil {
				return err
			}
			occurrenceID = occ.ID
		}
		status, origin := "published", source
		if in.Preanalysis {
			status, origin = "draft", "llm_preanalysis"
		}
		content := Content{ID: uuid.NewString(), OwnerID: owner, SubjectID: subject.ID, OccurrenceID: occurrenceID, CapabilityKey: def.Key, CapabilityVersion: def.Version, SchemaVersion: 1, ContentJSON: marshal(value), Origin: origin, Status: status, GeneratorVersion: "learning-v1", ProviderTraceID: source, CreatedAt: now, UpdatedAt: now}
		created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "owner_id"}, {Name: "subject_id"}, {Name: "occurrence_id"}, {Name: "capability_key"}, {Name: "schema_version"}}, DoNothing: true}).Create(&content)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			content.ID = ""
			if err := tx.Where("owner_id = ? AND subject_id = ? AND occurrence_id = ? AND capability_key = ? AND schema_version = ?", owner, subject.ID, occurrenceID, def.Key, 1).First(&content).Error; err != nil {
				return err
			}
			if content.UserEdited || (in.Preanalysis && content.Status == "published") {
				value = decodeObject(content.ContentJSON)
				source = content.Origin
			} else {
				content.ContentJSON = marshal(value)
				content.Origin, content.Status, content.ProviderTraceID = origin, status, source
				content.UpdatedAt = now
				if err := tx.Model(&content).Updates(map[string]any{"content_json": content.ContentJSON, "origin": origin, "status": status, "provider_trace_id": source, "updated_at": now}).Error; err != nil {
					return err
				}
			}
		}
		for _, bookID := range in.BookIDs {
			var book Book
			if err := tx.Where("id = ? AND owner_id = ?", bookID, owner).First(&book).Error; err != nil {
				return err
			}
			if book.CapabilityKey != def.Key {
				return errors.New("learning collection capability mismatch")
			}
			entry := BookEntry{ID: uuid.NewString(), OwnerID: owner, BookID: book.ID, ContentID: content.ID, Status: "active", CreatedAt: now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry).Error; err != nil {
				return err
			}
		}
		result = ResolveContentResult{Content: content, Value: value, Source: source, Cached: cached}
		return nil
	})
	return result, err
}
