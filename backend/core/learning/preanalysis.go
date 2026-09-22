package learning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/algo"
	"lazymind/core/modelconfig"
)

type PreanalysisItem struct {
	Text        string `json:"text"`
	Context     string `json:"context"`
	Language    string `json:"language"`
	SubjectKind string `json:"subject_kind"`
	SegmentID   string `json:"segment_id"`
	Page        *int   `json:"page"`
	StartOffset int    `json:"start_offset"`
	EndOffset   int    `json:"end_offset"`
}

type PreanalysisRequest struct {
	DatasetID         string            `json:"dataset_id"`
	DocumentID        string            `json:"document_id"`
	DocumentRevision  string            `json:"document_revision"`
	CapabilityKeys    []string          `json:"capability_keys"`
	AnalysisDirection string            `json:"analysis_direction"`
	Items             []PreanalysisItem `json:"items"`
}

type PreanalysisDrafts struct {
	Presets  []Preset  `json:"presets"`
	Contents []Content `json:"contents"`
}

func (s *Service) ListPreanalysisDrafts(ctx context.Context, owner, taskID string) (PreanalysisDrafts, error) {
	task, err := s.GetPreanalysisTask(ctx, owner, taskID)
	if err != nil {
		return PreanalysisDrafts{}, err
	}
	out := PreanalysisDrafts{Presets: []Preset{}, Contents: []Content{}}
	if err := s.db.WithContext(ctx).Where("owner_id = ? AND scope_type = ? AND scope_id = ? AND document_revision = ? AND origin = ? AND status = ?", owner, "document", task.DocumentID, task.DocumentRevision, "llm_preanalysis", "draft").Order("created_at").Find(&out.Presets).Error; err != nil {
		return out, err
	}
	var occurrenceIDs []string
	if err := s.db.WithContext(ctx).Model(&Occurrence{}).Where("owner_id = ? AND document_id = ? AND document_revision = ?", owner, task.DocumentID, task.DocumentRevision).Pluck("id", &occurrenceIDs).Error; err != nil {
		return out, err
	}
	if len(occurrenceIDs) > 0 {
		if err := s.db.WithContext(ctx).Where("owner_id = ? AND occurrence_id IN ? AND origin = ? AND status = ?", owner, occurrenceIDs, "llm_preanalysis", "draft").Order("created_at").Find(&out.Contents).Error; err != nil {
			return out, err
		}
	}
	return out, nil
}

func (s *Service) PublishPreanalysisDrafts(ctx context.Context, owner, taskID, expectedRevision string, presetIDs, contentIDs []string) (PreanalysisDrafts, error) {
	task, err := s.GetPreanalysisTask(ctx, owner, taskID)
	if err != nil {
		return PreanalysisDrafts{}, err
	}
	if task.Status != "completed" && task.Status != "completed_with_errors" {
		return PreanalysisDrafts{}, errors.New("preanalysis task is not ready to publish")
	}
	if expectedRevision != "" && expectedRevision != task.DocumentRevision {
		return PreanalysisDrafts{}, errors.New("document revision changed before preanalysis publish")
	}
	drafts, err := s.ListPreanalysisDrafts(ctx, owner, taskID)
	if err != nil {
		return drafts, err
	}
	allowedPresets, allowedContents := map[string]Preset{}, map[string]Content{}
	for _, row := range drafts.Presets {
		allowedPresets[row.ID] = row
	}
	for _, row := range drafts.Contents {
		allowedContents[row.ID] = row
	}
	if len(presetIDs) == 0 && len(contentIDs) == 0 {
		for id := range allowedPresets {
			presetIDs = append(presetIDs, id)
		}
		for id := range allowedContents {
			contentIDs = append(contentIDs, id)
		}
	}
	for _, id := range presetIDs {
		row, found := allowedPresets[id]
		if !found {
			return drafts, errors.New("preset is not a draft of this task")
		}
		def, ok := CapabilityByKey(row.CapabilityKey)
		if !ok || row.CapabilityVersion != def.Version || row.SchemaVersion != 1 || len(requiredMissing(def, decodeObject(row.ValueJSON))) > 0 {
			return drafts, errors.New("preanalysis preset failed schema validation")
		}
	}
	for _, id := range contentIDs {
		row, found := allowedContents[id]
		if !found {
			return drafts, errors.New("content is not a draft of this task")
		}
		def, ok := CapabilityByKey(row.CapabilityKey)
		if !ok || row.CapabilityVersion != def.Version || row.SchemaVersion != 1 || len(requiredMissing(def, decodeObject(row.ContentJSON))) > 0 {
			return drafts, errors.New("preanalysis content failed schema validation")
		}
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(presetIDs) > 0 {
			if err := tx.Model(&Preset{}).Where("owner_id = ? AND id IN ? AND status = ?", owner, presetIDs, "draft").Update("status", "published").Error; err != nil {
				return err
			}
		}
		if len(contentIDs) > 0 {
			if err := tx.Model(&Content{}).Where("owner_id = ? AND id IN ? AND status = ?", owner, contentIDs, "draft").Update("status", "published").Error; err != nil {
				return err
			}
		}
		return nil
	})
	return drafts, err
}

func (s *Service) expandPreanalysisItem(ctx context.Context, owner, key string, item PreanalysisItem) ([]PreanalysisItem, error) {
	def, _ := CapabilityByKey(key)
	return s.expandPreanalysisItemWithDefinition(ctx, owner, def, item)
}

func (s *Service) expandPreanalysisItemWithDefinition(ctx context.Context, owner string, def Capability, item PreanalysisItem, analysisDirection ...string) ([]PreanalysisItem, error) {
	key := def.Key
	lang, kinds := analyzeText(item.Text)
	kind := ""
	if len(kinds) > 0 {
		kind = kinds[0]
	}
	if item.Language != "" {
		lang = item.Language
	}
	if item.SubjectKind != "" {
		kind = item.SubjectKind
	}
	if contains(def.Languages, lang) && contains(def.SubjectKinds, kind) {
		item.Language, item.SubjectKind = lang, kind
		return []PreanalysisItem{item}, nil
	}
	config, err := modelconfig.LoadLLMConfig(ctx, s.db, owner)
	if err != nil {
		return nil, err
	}
	prompt := renderPrompt(def.Analysis.ExtractionPromptTemplate, map[string]string{
		"capability": key, "instruction": def.Analysis.Instruction,
		"languages": marshal(def.Languages), "subject_kinds": marshal(def.SubjectKinds),
		"max_candidates": fmt.Sprint(def.Analysis.MaxCandidates), "text": item.Text,
		"analysis_direction": strings.TrimSpace(strings.Join(analysisDirection, "\n")),
	})
	prompt = appendAnalysisDirection(prompt, strings.Join(analysisDirection, "\n"))
	raw, err := algo.GenerateLearning(ctx, algo.LearningGenerateRequest{Content: item.Text, UserInstruct: prompt, LLMConfig: config})
	if err != nil {
		return fallbackPreanalysisCandidates(def, item), nil
	}
	obj, err := extractJSONObject(raw)
	if err != nil {
		repairPrompt := renderPrompt(def.Analysis.ExtractionRepairTemplate, map[string]string{
			"languages": marshal(def.Languages), "subject_kinds": marshal(def.SubjectKinds),
			"invalid_response": truncatePreanalysisResponse(raw),
		})
		raw, err = algo.GenerateLearning(ctx, algo.LearningGenerateRequest{Content: item.Text, UserInstruct: repairPrompt, LLMConfig: config})
		if err != nil {
			return fallbackPreanalysisCandidates(def, item), nil
		}
		obj, err = extractJSONObject(raw)
		if err != nil {
			return fallbackPreanalysisCandidates(def, item), nil
		}
	}
	rows, ok := obj["items"].([]any)
	if !ok {
		return fallbackPreanalysisCandidates(def, item), nil
	}
	out := make([]PreanalysisItem, 0, len(rows))
	for _, rawItem := range rows {
		m, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		candidate := PreanalysisItem{Text: strings.TrimSpace(fmt.Sprint(m["text"])), Language: normalizePreanalysisLanguage(strings.TrimSpace(fmt.Sprint(m["language"])), def), SubjectKind: normalizePreanalysisSubjectKind(strings.TrimSpace(fmt.Sprint(m["subject_kind"])), strings.TrimSpace(fmt.Sprint(m["text"])), def), Context: item.Text, SegmentID: item.SegmentID, Page: item.Page}
		if candidate.Text != "" && contains(def.Languages, candidate.Language) && contains(def.SubjectKinds, candidate.SubjectKind) {
			out = append(out, candidate)
		}
	}
	if len(out) == 0 {
		return fallbackPreanalysisCandidates(def, item), nil
	}
	return out, nil
}

func fallbackPreanalysisCandidates(def Capability, item PreanalysisItem) []PreanalysisItem {
	language := ""
	if contains(def.Languages, "zh-Hans") {
		language = "zh-Hans"
	} else if contains(def.Languages, "en") {
		language = "en"
	} else if len(def.Languages) > 0 {
		language = def.Languages[0]
	}
	kind := ""
	for _, candidate := range def.Analysis.FallbackKinds {
		if contains(def.SubjectKinds, candidate) {
			kind = candidate
			break
		}
	}
	if language == "" || kind == "" {
		return nil
	}
	seen := map[string]bool{}
	out := make([]PreanalysisItem, 0, 5)
	pattern, err := regexp.Compile(def.Analysis.FallbackPattern)
	if err != nil || def.Analysis.MaxCandidates <= 0 {
		return nil
	}
	for _, match := range pattern.FindAllString(item.Text, -1) {
		text := strings.TrimSpace(match)
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		out = append(out, PreanalysisItem{Text: text, Language: language, SubjectKind: kind, Context: item.Text, SegmentID: item.SegmentID, Page: item.Page})
		if len(out) == def.Analysis.MaxCandidates {
			break
		}
	}
	return out
}

func truncatePreanalysisResponse(raw string) string {
	const limit = 4000
	if len(raw) <= limit {
		return raw
	}
	return raw[:limit]
}

func normalizePreanalysisLanguage(value string, def Capability) string {
	if contains(def.Languages, value) {
		return value
	}
	if normalized := def.Analysis.LanguageAliases[strings.ToLower(value)]; contains(def.Languages, normalized) {
		return normalized
	}
	if value == "" && len(def.Languages) == 1 {
		return def.Languages[0]
	}
	return value
}

func normalizePreanalysisSubjectKind(value, text string, def Capability) string {
	if contains(def.SubjectKinds, value) {
		return value
	}
	if normalized := def.Analysis.SubjectKindAliases[strings.ToLower(value)]; contains(def.SubjectKinds, normalized) {
		return normalized
	}
	if len(def.SubjectKinds) == 1 {
		return def.SubjectKinds[0]
	}
	if len([]rune(text)) == 1 && contains(def.SubjectKinds, "character") {
		return "character"
	}
	for _, kind := range def.Analysis.FallbackKinds {
		if contains(def.SubjectKinds, kind) {
			return kind
		}
	}
	return value
}

func (s *Service) CreatePreanalysisTask(ctx context.Context, owner string, in PreanalysisRequest) (PreanalysisTask, error) {
	if err := requireLocal(); err != nil {
		return PreanalysisTask{}, err
	}
	if in.DatasetID == "" || in.DocumentID == "" || len(in.CapabilityKeys) == 0 || len(in.Items) == 0 {
		return PreanalysisTask{}, errors.New("dataset_id, document_id, capability_keys and items are required")
	}
	in.AnalysisDirection = strings.TrimSpace(in.AnalysisDirection)
	if len([]rune(in.AnalysisDirection)) > 1000 {
		return PreanalysisTask{}, errors.New("analysis_direction must not exceed 1000 characters")
	}
	configured, err := s.ListKnowledgeBaseCapabilities(ctx, owner, in.DatasetID)
	if err != nil {
		return PreanalysisTask{}, err
	}
	enabled := map[string]bool{}
	for _, row := range configured {
		enabled[row.CapabilityKey] = row.Enabled
	}
	for _, key := range in.CapabilityKeys {
		if _, ok := CapabilityByKey(key); !ok || !enabled[key] {
			return PreanalysisTask{}, errors.New("preanalysis capability is not enabled for knowledge base")
		}
	}
	now := time.Now().UTC()
	// Keep successful drafts for this revision. A later run reuses them instead
	// of spending another provider call on the same capability and candidate.
	if in.DocumentRevision != "" {
		_ = s.db.WithContext(ctx).Model(&Preset{}).Where("owner_id = ? AND scope_type = ? AND scope_id = ? AND document_revision <> ? AND origin = ? AND user_edited = ?", owner, "document", in.DocumentID, in.DocumentRevision, "llm_preanalysis", false).Update("status", "stale").Error
		var occurrenceIDs []string
		s.db.WithContext(ctx).Model(&Occurrence{}).Where("owner_id = ? AND document_id = ? AND document_revision <> ?", owner, in.DocumentID, in.DocumentRevision).Pluck("id", &occurrenceIDs)
		if len(occurrenceIDs) > 0 {
			_ = s.db.WithContext(ctx).Model(&Content{}).Where("owner_id = ? AND occurrence_id IN ? AND origin = ? AND user_edited = ?", owner, occurrenceIDs, "llm_preanalysis", false).Update("status", "stale").Error
		}
	}
	task := PreanalysisTask{ID: uuid.NewString(), OwnerID: owner, DatasetID: in.DatasetID, DocumentID: in.DocumentID, DocumentRevision: in.DocumentRevision, Status: "queued", CapabilityKeysJSON: marshal(in.CapabilityKeys), RequestJSON: marshal(in), ResultJSON: "[]", Total: len(in.Items) * len(in.CapabilityKeys), CreatedAt: now, UpdatedAt: now}
	return task, s.db.WithContext(ctx).Create(&task).Error
}

func (s *Service) RunPreanalysisTask(ctx context.Context, owner, id string) (PreanalysisTask, error) {
	var task PreanalysisTask
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, owner).First(&task).Error; err != nil {
		return task, err
	}
	if task.Status == "completed" || task.Status == "completed_with_errors" || task.Status == "canceled" {
		return task, nil
	}
	var in PreanalysisRequest
	if json.Unmarshal([]byte(task.RequestJSON), &in) != nil {
		return task, errors.New("invalid stored preanalysis request")
	}
	effectiveDefinitions := make(map[string]Capability, len(in.CapabilityKeys))
	effectiveSettings := make(map[string]map[string]any, len(in.CapabilityKeys))
	for _, key := range in.CapabilityKeys {
		def, settings, configuredErr := s.configuredCapability(ctx, owner, in.DatasetID, key)
		if configuredErr != nil {
			return task, configuredErr
		}
		if value, ok := numericSetting(settings["max_candidates_per_block"]); ok {
			def.Analysis.MaxCandidates = int(value)
		}
		if value, ok := numericSetting(settings["max_document_candidates"]); ok {
			def.Analysis.MaxDocumentCandidates = int(value)
		}
		effectiveDefinitions[key] = def
		effectiveSettings[key] = settings
	}
	now := time.Now().UTC()
	claim := s.db.WithContext(ctx).Model(&PreanalysisTask{}).Where("id = ? AND owner_id = ? AND status = ?", id, owner, "queued").Updates(map[string]any{"status": "running", "started_at": now, "updated_at": now, "error_message": ""})
	if claim.Error != nil {
		return task, claim.Error
	}
	if claim.RowsAffected != 1 {
		return task, errors.New("preanalysis task is already running")
	}
	task.Status = "running"
	results := make([]ResolveContentResult, 0, task.Total)
	completed, failed := 0, 0
	failureMessages := make([]string, 0, 5)
	recordFailure := func(stage, key, text string, failure error) {
		failed++
		reason := "no candidates were extracted"
		if failure != nil {
			reason = failure.Error()
		}
		message := fmt.Sprintf("%s[%s] %q: %s", stage, key, truncateFailureText(text, 40), truncateFailureText(reason, 240))
		if len(failureMessages) < cap(failureMessages) {
			failureMessages = append(failureMessages, message)
		}
		_ = s.db.WithContext(ctx).Model(&PreanalysisTask{}).Where("id = ?", id).Updates(map[string]any{"failed": failed, "error_message": strings.Join(failureMessages, "\n"), "updated_at": time.Now().UTC()}).Error
	}
	seen := map[string]bool{}
	capabilityCounts := map[string]int{}
	for _, item := range in.Items {
		for _, key := range in.CapabilityKeys {
			def := effectiveDefinitions[key]
			settings := effectiveSettings[key]
			if limit := def.Analysis.MaxDocumentCandidates; limit > 0 && capabilityCounts[key] >= limit {
				continue
			}
			var state string
			_ = s.db.WithContext(ctx).Model(&PreanalysisTask{}).Select("status").Where("id = ? AND owner_id = ?", id, owner).Scan(&state).Error
			if state == "canceled" {
				return s.GetPreanalysisTask(ctx, owner, id)
			}
			candidates, expandErr := s.expandPreanalysisItemWithDefinition(ctx, owner, def, item, in.AnalysisDirection)
			if ctx.Err() != nil {
				return s.GetPreanalysisTask(context.Background(), owner, id)
			}
			if expandErr != nil || len(candidates) == 0 {
				recordFailure("candidate extraction", key, item.Text, expandErr)
				continue
			}
			if len(candidates) > 1 {
				task.Total += len(candidates) - 1
				_ = s.db.WithContext(ctx).Model(&PreanalysisTask{}).Where("id = ?", id).Update("total", task.Total).Error
			}
			for _, candidate := range candidates {
				var state string
				_ = s.db.WithContext(ctx).Model(&PreanalysisTask{}).Select("status").Where("id = ? AND owner_id = ?", id, owner).Scan(&state).Error
				if state == "canceled" || ctx.Err() != nil {
					return s.GetPreanalysisTask(context.Background(), owner, id)
				}
				candidateKey := key + "\x1f" + normalize(candidate.Text)
				if def.CachePolicy.ContextSensitive {
					candidateKey += "\x1f" + normalize(candidate.Context)
				}
				if seen[candidateKey] {
					continue
				}
				if limit := def.Analysis.MaxDocumentCandidates; limit > 0 && capabilityCounts[key] >= limit {
					break
				}
				seen[candidateKey] = true
				capabilityCounts[key]++
				cacheContext := analysisCacheContext(candidate.Context, in.AnalysisDirection)
				if configured := strings.TrimSpace(fmt.Sprint(settings["output_language"])); configured != "" && configured != "<nil>" {
					if outputLanguage := resolveOutputLanguage(configured, candidate.Language, def.Analysis.OutputLanguage); outputLanguage != def.Analysis.OutputLanguage {
						cacheContext += "\x1eoutput-language:" + outputLanguage
					}
				}
				cacheKey := BuildCacheKey(key, candidate.Text, candidate.Language, "", cacheContext, in.DocumentID, candidate.StartOffset, candidate.EndOffset)
				var reusable Preset
				reuseErr := s.db.WithContext(ctx).Where("owner_id = ? AND scope_type = ? AND scope_id = ? AND document_revision = ? AND capability_key = ? AND normalized_key = ? AND schema_version = ? AND status IN ?", owner, "document", in.DocumentID, in.DocumentRevision, key, normalize(cacheKey), 1, []string{"draft", "published"}).Order("user_edited DESC, updated_at DESC").First(&reusable).Error
				if reuseErr == nil && reusable.CapabilityVersion == def.Version && len(requiredMissing(def, decodeObject(reusable.ValueJSON))) == 0 {
					completed++
					_ = s.db.WithContext(ctx).Model(&PreanalysisTask{}).Where("id = ?", id).Updates(map[string]any{"completed": completed, "updated_at": time.Now().UTC()}).Error
					continue
				}
				if reuseErr != nil && !errors.Is(reuseErr, gorm.ErrRecordNotFound) {
					recordFailure("cache lookup", key, candidate.Text, reuseErr)
					continue
				}
				// Preanalysis authors cache drafts. Learning content must not be
				// persisted until the user explicitly confirms an item.
				result, err := s.ResolveContent(ctx, owner, ResolveContentRequest{CapabilityKey: key, Text: candidate.Text, Context: candidate.Context, AnalysisDirection: in.AnalysisDirection, Language: candidate.Language, SubjectKind: candidate.SubjectKind, DatasetID: in.DatasetID, DocumentID: in.DocumentID, DocumentRevision: in.DocumentRevision, SegmentID: candidate.SegmentID, Page: candidate.Page, StartOffset: candidate.StartOffset, EndOffset: candidate.EndOffset, Preanalysis: true, Preview: true})
				if ctx.Err() != nil {
					return s.GetPreanalysisTask(context.Background(), owner, id)
				}
				if err != nil {
					recordFailure("content resolution", key, candidate.Text, err)
					continue
				}
				completed++
				if result.Cached {
					continue
				}
				stamp := time.Now().UTC()
				var preset Preset
				presetErr := s.db.WithContext(ctx).Where("owner_id = ? AND scope_type = ? AND scope_id = ? AND capability_key = ? AND normalized_key = ? AND schema_version = ?", owner, "document", in.DocumentID, key, normalize(cacheKey), 1).First(&preset).Error
				if errors.Is(presetErr, gorm.ErrRecordNotFound) {
					preset = Preset{ID: uuid.NewString(), OwnerID: owner, ScopeType: "document", ScopeID: in.DocumentID, DocumentRevision: in.DocumentRevision, CapabilityKey: key, CapabilityVersion: def.Version, NormalizedKey: normalize(cacheKey), ValueJSON: marshal(result.Value), SchemaVersion: 1, Origin: "llm_preanalysis", Status: "draft", CreatedAt: stamp, UpdatedAt: stamp}
					presetErr = s.db.WithContext(ctx).Create(&preset).Error
				} else if presetErr == nil && !preset.UserEdited && preset.Status != "published" {
					presetErr = s.db.WithContext(ctx).Model(&preset).Updates(map[string]any{"document_revision": in.DocumentRevision, "value_json": marshal(result.Value), "origin": "llm_preanalysis", "status": "draft", "updated_at": stamp}).Error
				}
				if presetErr != nil {
					recordFailure("draft persistence", key, candidate.Text, presetErr)
					continue
				}
				results = append(results, result)
				_ = s.db.WithContext(ctx).Model(&PreanalysisTask{}).Where("id = ?", id).Updates(map[string]any{"completed": completed, "result_json": marshal(results), "updated_at": time.Now().UTC()}).Error
			}
		}
	}
	status := "completed"
	message := strings.Join(failureMessages, "\n")
	if failed > 0 {
		status = "completed_with_errors"
		if message == "" {
			message = "one or more items could not be analyzed"
		}
	}
	finished := time.Now().UTC()
	err := s.db.WithContext(ctx).Model(&task).Updates(map[string]any{"status": status, "total": completed + failed, "completed": completed, "failed": failed, "result_json": marshal(results), "error_message": message, "completed_at": finished, "updated_at": finished}).Error
	if err == nil {
		err = s.db.WithContext(ctx).Where("id = ?", id).First(&task).Error
	}
	return task, err
}

func (s *Service) CancelPreanalysisTask(ctx context.Context, owner, id string) (PreanalysisTask, error) {
	var task PreanalysisTask
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, owner).First(&task).Error; err != nil {
		return task, err
	}
	if task.Status == "completed" || task.Status == "completed_with_errors" || task.Status == "canceled" {
		return task, nil
	}
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Model(&task).Updates(map[string]any{"status": "canceled", "completed_at": now, "updated_at": now}).Error; err != nil {
		return task, err
	}
	task.Status = "canceled"
	task.CompletedAt = now
	task.UpdatedAt = now
	return task, nil
}

func truncateFailureText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func (s *Service) GetPreanalysisTask(ctx context.Context, owner, id string) (PreanalysisTask, error) {
	var task PreanalysisTask
	err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, owner).First(&task).Error
	return task, err
}

func (s *Service) LatestPreanalysisTask(ctx context.Context, owner, datasetID, documentID string) (*PreanalysisTask, error) {
	if strings.TrimSpace(datasetID) == "" || strings.TrimSpace(documentID) == "" {
		return nil, errors.New("dataset_id and document_id are required")
	}
	var task PreanalysisTask
	err := s.db.WithContext(ctx).Where("owner_id = ? AND dataset_id = ? AND document_id = ?", owner, datasetID, documentID).Order("created_at DESC").First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &task, err
}
