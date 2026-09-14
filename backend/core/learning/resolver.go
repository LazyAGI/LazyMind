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
	CapabilityKey, Text, Context, Language, SubjectKind, DatasetID, DocumentID, DocumentRevision, TargetLanguage string
	SegmentID                                                                                                    string
	Page                                                                                                         *int
	StartOffset, EndOffset                                                                                       int
	BookIDs                                                                                                      []string
	Preanalysis                                                                                                  bool
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
func requiredMissing(def Capability, value map[string]any) []string {
	var out []string
	for _, field := range def.Fields {
		if !field.Required {
			continue
		}
		v, ok := value[field.Key]
		if !ok || strings.TrimSpace(fmt.Sprint(v)) == "" {
			out = append(out, field.Key)
		}
	}
	return out
}
func mergeMissing(dst, src map[string]any) {
	for k, v := range src {
		if current, ok := dst[k]; !ok || strings.TrimSpace(fmt.Sprint(current)) == "" {
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
func (s *Service) resolveWithLLM(ctx context.Context, owner string, def Capability, in ResolveContentRequest, current map[string]any) (map[string]any, error) {
	config, err := modelconfig.LoadLLMConfig(ctx, s.db, owner)
	if err != nil {
		return nil, err
	}
	schema := marshal(def.Fields)
	prompt := fmt.Sprintf("Return one JSON object only. Capability: %s. Output fields: %s. Selected text: %q. Context: %q. Existing candidate fields: %s. Fill required fields accurately; do not invent dictionary citations.", def.Key, schema, in.Text, in.Context, marshal(current))
	raw, err := algo.GenerateSkill(ctx, algo.SkillGenerateRequest{Content: in.Text, UserInstruct: prompt, LLMConfig: config})
	if err != nil {
		return nil, err
	}
	return extractJSONObject(raw)
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
	cacheKey := BuildCacheKey(def.Key, in.Text, in.Language, in.TargetLanguage, in.Context, in.DocumentID, in.StartOffset, in.EndOffset)
	preset, err := s.ResolvePreset(ctx, owner, def.Key, cacheKey, in.DatasetID, in.DocumentID, in.DocumentRevision)
	// Read pre-versioned keys for forward compatibility with existing presets.
	if err == nil && preset == nil {
		legacyKey := buildLegacyCacheKey(def.Key, in.Text, in.Language, in.TargetLanguage, in.Context, in.DocumentID, in.StartOffset, in.EndOffset)
		preset, err = s.ResolvePreset(ctx, owner, def.Key, legacyKey, in.DatasetID, in.DocumentID, in.DocumentRevision)
		if err == nil && preset == nil && legacyKey != normalize(in.Text) {
			preset, err = s.ResolvePreset(ctx, owner, def.Key, in.Text, in.DatasetID, in.DocumentID, in.DocumentRevision)
		}
	}
	if err != nil {
		return ResolveContentResult{}, err
	} else if preset != nil {
		var value map[string]any
		if json.Unmarshal([]byte(preset.ValueJSON), &value) == nil && len(requiredMissing(def, value)) == 0 {
			return s.persistResolved(ctx, owner, def, in, value, preset.Origin, true)
		}
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
	if !in.Preanalysis && (cacheScope == "user_global" || cacheID != "") {
		_, _ = s.PutPreset(ctx, owner, PresetInput{ScopeType: cacheScope, ScopeID: cacheID, DocumentRevision: in.DocumentRevision, CapabilityKey: def.Key, Key: cacheKey, Value: value, SchemaVersion: 1, Origin: source, Priority: 0})
	}
	return s.persistResolved(ctx, owner, def, in, value, source, false)
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
