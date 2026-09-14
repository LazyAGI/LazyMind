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
	DatasetID        string            `json:"dataset_id"`
	DocumentID       string            `json:"document_id"`
	DocumentRevision string            `json:"document_revision"`
	CapabilityKeys   []string          `json:"capability_keys"`
	Items            []PreanalysisItem `json:"items"`
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
	prompt := fmt.Sprintf(`You extract learning subjects from a document passage.

CAPABILITY: %s
TASK: %s
ALLOWED_LANGUAGES: %s
ALLOWED_SUBJECT_KINDS: %s

Return exactly one valid JSON object and nothing else. Do not use Markdown fences or explanatory text.
The exact schema is:
{"items":[{"text":"non-empty text copied verbatim from the passage","language":"one exact value from ALLOWED_LANGUAGES","subject_kind":"one exact value from ALLOWED_SUBJECT_KINDS"}]}

Rules:
- Extract at most %d useful, distinct subjects that genuinely occur in the passage.
- Use only the exact enum strings listed above; never invent aliases such as zh-CN, term, idiom, or concept.
- If there is no suitable subject, return {"items":[]}.

<PASSAGE>
%s
</PASSAGE>`, key, def.Analysis.Instruction, marshal(def.Languages), marshal(def.SubjectKinds), def.Analysis.MaxCandidates, item.Text)
	raw, err := algo.GenerateSkill(ctx, algo.SkillGenerateRequest{Content: item.Text, UserInstruct: prompt, LLMConfig: config})
	if err != nil {
		return fallbackPreanalysisCandidates(def, item), nil
	}
	obj, err := extractJSONObject(raw)
	if err != nil {
		repairPrompt := fmt.Sprintf(`Convert the response below into the required JSON object. Return JSON only.
Required schema: {"items":[{"text":"...","language":"one of %s","subject_kind":"one of %s"}]}
Invalid response:
%s`, marshal(def.Languages), marshal(def.SubjectKinds), truncatePreanalysisResponse(raw))
		raw, err = algo.GenerateSkill(ctx, algo.SkillGenerateRequest{Content: item.Text, UserInstruct: repairPrompt, LLMConfig: config})
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
	for _, item := range in.Items {
		for _, key := range in.CapabilityKeys {
			var state string
			_ = s.db.WithContext(ctx).Model(&PreanalysisTask{}).Select("status").Where("id = ? AND owner_id = ?", id, owner).Scan(&state).Error
			if state == "canceled" {
				return s.GetPreanalysisTask(ctx, owner, id)
			}
			candidates, expandErr := s.expandPreanalysisItem(ctx, owner, key, item)
			if expandErr != nil || len(candidates) == 0 {
				failed++
				_ = s.db.WithContext(ctx).Model(&PreanalysisTask{}).Where("id = ?", id).Updates(map[string]any{"failed": failed, "updated_at": time.Now().UTC()}).Error
				continue
			}
			if len(candidates) > 1 {
				task.Total += len(candidates) - 1
				_ = s.db.WithContext(ctx).Model(&PreanalysisTask{}).Where("id = ?", id).Update("total", task.Total).Error
			}
			for _, candidate := range candidates {
				result, err := s.ResolveContent(ctx, owner, ResolveContentRequest{CapabilityKey: key, Text: candidate.Text, Context: candidate.Context, Language: candidate.Language, SubjectKind: candidate.SubjectKind, DatasetID: in.DatasetID, DocumentID: in.DocumentID, DocumentRevision: in.DocumentRevision, SegmentID: candidate.SegmentID, Page: candidate.Page, StartOffset: candidate.StartOffset, EndOffset: candidate.EndOffset, Preanalysis: true})
				if err != nil {
					failed++
					_ = s.db.WithContext(ctx).Model(&PreanalysisTask{}).Where("id = ?", id).Updates(map[string]any{"failed": failed, "updated_at": time.Now().UTC()}).Error
					continue
				}
				completed++
				if result.Content.Status != "draft" {
					continue
				}
				cacheKey := BuildCacheKey(key, candidate.Text, candidate.Language, "", candidate.Context, in.DocumentID, candidate.StartOffset, candidate.EndOffset)
				def, _ := CapabilityByKey(key)
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
					failed++
					_ = s.db.WithContext(ctx).Model(&PreanalysisTask{}).Where("id = ?", id).Updates(map[string]any{"failed": failed, "updated_at": stamp}).Error
					continue
				}
				results = append(results, result)
				_ = s.db.WithContext(ctx).Model(&PreanalysisTask{}).Where("id = ?", id).Updates(map[string]any{"completed": completed, "result_json": marshal(results), "updated_at": time.Now().UTC()}).Error
			}
		}
	}
	status := "completed"
	message := ""
	if failed > 0 {
		status = "completed_with_errors"
		message = "one or more items could not be analyzed"
	}
	finished := time.Now().UTC()
	err := s.db.WithContext(ctx).Model(&task).Updates(map[string]any{"status": status, "completed": completed, "failed": failed, "result_json": marshal(results), "error_message": message, "completed_at": finished, "updated_at": finished}).Error
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
	if task.Status == "completed" || task.Status == "canceled" {
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

func (s *Service) GetPreanalysisTask(ctx context.Context, owner, id string) (PreanalysisTask, error) {
	var task PreanalysisTask
	err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, owner).First(&task).Error
	return task, err
}
