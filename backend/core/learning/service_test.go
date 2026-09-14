package learning

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func testService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("LAZYMIND_VOCABULARY_ENABLED", "true")
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&orm.Dataset{}, &KnowledgeBaseCapability{}, &CapabilityProfile{}, &Subject{}, &Occurrence{}, &Content{}, &Book{}, &BookEntry{}, &Preset{}, &DictionaryImport{}, &DictionaryEntry{}, &QuestionInstance{}, &Card{}, &ReviewSession{}, &ReviewSessionItem{}, &ReviewAnswer{}, &ReviewLog{}, &PreanalysisTask{}); err != nil {
		t.Fatal(err)
	}
	return New(db)
}

func TestReviewSessionSnapshotAnswerAndFSRS(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	book, err := s.CreateBook(ctx, "u", Book{Name: "English", CapabilityKey: "english_definition"}, []string{"text_input"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	subject := Subject{ID: "subject", OwnerID: "u", SubjectKind: "word", NormalizedText: "evidence", DisplayText: "evidence", Language: "en", CreatedAt: now, UpdatedAt: now}
	if err = s.db.Create(&subject).Error; err != nil {
		t.Fatal(err)
	}
	content := Content{ID: "content", OwnerID: "u", SubjectID: subject.ID, CapabilityKey: "english_definition", CapabilityVersion: 1, SchemaVersion: 1, ContentJSON: `{"meaning":"证据"}`, Status: "published", CreatedAt: now, UpdatedAt: now}
	if err = s.db.Create(&content).Error; err != nil {
		t.Fatal(err)
	}
	entry := BookEntry{ID: "entry", OwnerID: "u", BookID: book.ID, ContentID: content.ID, Status: "active", CreatedAt: now}
	if err = s.db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	view, err := s.CreateReviewSession(ctx, "u", book.ID, "zh-CN", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Items) != 1 || view.Items[0].Question.GeneratorType != "deterministic" {
		t.Fatalf("unexpected view %#v", view)
	}
	result, err := s.AnswerQuestion(ctx, "u", view.Session.ID, view.Items[0].Question.ID, "证据", "", "answer-1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Correct || result.Rating != "good" {
		t.Fatalf("unexpected answer %#v", result)
	}
	again, err := s.AnswerQuestion(ctx, "u", view.Session.ID, view.Items[0].Question.ID, "wrong", "", "answer-1")
	if err != nil || !again.Correct {
		t.Fatalf("idempotency failed %#v %v", again, err)
	}
}

func TestPreanalysisLifecycleUsesPreset(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	seedDataset(t, s, "ds", "u")
	if err := s.PutKnowledgeBaseCapabilities(ctx, "u", "ds", []CapabilityRef{{Key: "chinese_definition", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutPreset(ctx, "u", PresetInput{ScopeType: "document", ScopeID: "doc", CapabilityKey: "chinese_definition", Key: "求索", Value: map[string]any{"meaning_in_context": "探索追求"}, Origin: "user"}); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreatePreanalysisTask(ctx, "u", PreanalysisRequest{DatasetID: "ds", DocumentID: "doc", CapabilityKeys: []string{"chinese_definition"}, Items: []PreanalysisItem{{Text: "求索", Language: "zh-Hans", SubjectKind: "word"}}})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := s.RunPreanalysisTask(ctx, "u", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != "completed" || finished.Completed != 1 || finished.Failed != 0 {
		t.Fatalf("unexpected task %#v", finished)
	}
	var content Content
	if err := s.db.Order("created_at DESC").First(&content).Error; err != nil {
		t.Fatal(err)
	}
	if content.Status != "draft" || content.Origin != "llm_preanalysis" {
		t.Fatalf("preanalysis content was not draft: %#v", content)
	}
}

func TestPreanalysisTaskCanBeCanceledBeforeRun(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	seedDataset(t, s, "ds-cancel", "u")
	if err := s.PutKnowledgeBaseCapabilities(ctx, "u", "ds-cancel", []CapabilityRef{{Key: "chinese_definition", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreatePreanalysisTask(ctx, "u", PreanalysisRequest{DatasetID: "ds-cancel", DocumentID: "doc", CapabilityKeys: []string{"chinese_definition"}, Items: []PreanalysisItem{{Text: "学", Language: "zh-Hans", SubjectKind: "character"}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.CancelPreanalysisTask(ctx, "u", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "canceled" {
		t.Fatalf("status=%s", task.Status)
	}
}

func TestLatestPreanalysisTaskIsScopedToDocument(t *testing.T) {
	s := testService(t)
	now := time.Now().UTC()
	rows := []PreanalysisTask{
		{ID: "old", OwnerID: "u", DatasetID: "ds", DocumentID: "doc", Status: "completed", CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)},
		{ID: "latest", OwnerID: "u", DatasetID: "ds", DocumentID: "doc", Status: "running", CreatedAt: now, UpdatedAt: now},
		{ID: "other", OwnerID: "u", DatasetID: "ds", DocumentID: "other", Status: "running", CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)},
	}
	if err := s.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	task, err := s.LatestPreanalysisTask(context.Background(), "u", "ds", "doc")
	if err != nil || task == nil || task.ID != "latest" {
		t.Fatalf("latest task = %#v, err = %v", task, err)
	}
	missing, err := s.LatestPreanalysisTask(context.Background(), "u", "ds", "missing")
	if err != nil || missing != nil {
		t.Fatalf("missing task = %#v, err = %v", missing, err)
	}
}

func TestContextualDictionaryCapabilitiesRequireDisambiguation(t *testing.T) {
	definition, _ := CapabilityByKey("classical_definition")
	if !needsContextDisambiguation(definition, ResolveContentRequest{Context: "双兔傍地走"}) {
		t.Fatal("classical definition with context must continue to disambiguation")
	}
	if needsContextDisambiguation(definition, ResolveContentRequest{}) {
		t.Fatal("a context-free dictionary lookup should be reusable without AI")
	}
	translation, _ := CapabilityByKey("general_translation")
	if needsContextDisambiguation(translation, ResolveContentRequest{Context: "context"}) {
		t.Fatal("translation uses its own provider fallback policy")
	}
}

func TestDictionaryProvidersFollowContentAndKnowledgeBaseScenario(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	entries := []DictionaryEntry{
		{ID: "modern", ProviderKey: "chinese_dictionary", Language: "zh-Hans", NormalizedHeadword: "行", DisplayHeadword: "行", PayloadJSON: `{"pinyin":"xíng"}`, Priority: 100, SourceName: "modern", SourceVersion: "1", LicenseID: "test"},
		{ID: "idiom", ProviderKey: "chinese_idiom_dictionary", Language: "zh-Hans", NormalizedHeadword: "画龙点睛", DisplayHeadword: "画龙点睛", PayloadJSON: `{"pinyin":"huà lóng diǎn jīng","meaning_in_context":"比喻在关键处加上精辟内容"}`, Priority: 100, SourceName: "idiom", SourceVersion: "1", LicenseID: "test"},
		{ID: "classical", ProviderKey: "classical_chinese_dictionary", Language: "zh-Hans", NormalizedHeadword: "行", DisplayHeadword: "行", PayloadJSON: `{"pinyin":"háng"}`, Priority: 100, SourceName: "classical", SourceVersion: "1", LicenseID: "test"},
	}
	for i := range entries {
		if err := s.db.Create(&entries[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	definition, _ := CapabilityByKey("chinese_definition")
	value, source, err := s.runProviderPipeline(ctx, "u", definition, ResolveContentRequest{Text: "画龙点睛", Language: "zh-Hans", SubjectKind: "idiom"})
	if err != nil || source != "chinese_idiom_dictionary" || value["meaning_in_context"] == "" {
		t.Fatalf("idiom lookup did not use its provider: value=%#v source=%q err=%v", value, source, err)
	}

	seedDataset(t, s, "modern-ds", "u")
	if err := s.PutKnowledgeBaseCapabilities(ctx, "u", "modern-ds", []CapabilityRef{{Key: "chinese_definition", Enabled: true}, {Key: "pinyin", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	seedDataset(t, s, "classical-ds", "u")
	if err := s.PutKnowledgeBaseCapabilities(ctx, "u", "classical-ds", []CapabilityRef{{Key: "classical_definition", Enabled: true}, {Key: "pinyin", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	pinyin, _ := CapabilityByKey("pinyin")
	modernValue, modernSource, err := s.runProviderPipeline(ctx, "u", pinyin, ResolveContentRequest{Text: "行", Language: "zh-Hans", SubjectKind: "word", DatasetID: "modern-ds"})
	if err != nil || modernSource != "chinese_dictionary" || modernValue["pinyin"] != "xíng" {
		t.Fatalf("modern scenario routed incorrectly: value=%#v source=%q err=%v", modernValue, modernSource, err)
	}
	classicalValue, classicalSource, err := s.runProviderPipeline(ctx, "u", pinyin, ResolveContentRequest{Text: "行", Language: "zh-Hans", SubjectKind: "word", DatasetID: "classical-ds"})
	if err != nil || classicalSource != "classical_chinese_dictionary" || classicalValue["pinyin"] != "háng" {
		t.Fatalf("classical scenario routed incorrectly: value=%#v source=%q err=%v", classicalValue, classicalSource, err)
	}
}

func TestCacheKeysSeparateTranslationTargetsAndContext(t *testing.T) {
	en := BuildCacheKey("general_translation", "学", "zh-Hans", "en", "", "", 0, 0)
	ja := BuildCacheKey("general_translation", "学", "zh-Hans", "ja", "", "", 0, 0)
	if en == ja {
		t.Fatal("translation cache keys must include target language")
	}
	a := BuildCacheKey("literary_appreciation", "月", "zh-Hans", "", "context-a", "doc", 1, 2)
	b := BuildCacheKey("literary_appreciation", "月", "zh-Hans", "", "context-b", "doc", 1, 2)
	if a == b {
		t.Fatal("context-sensitive cache keys must include context signature")
	}
}

func TestPreanalysisDraftPreviewAndPublish(t *testing.T) {
	s := testService(t)
	db := s.db
	now := time.Now().UTC()
	task := PreanalysisTask{ID: "task-draft", OwnerID: "u", DatasetID: "ds", DocumentID: "doc", DocumentRevision: "r1", Status: "completed", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	preset := Preset{ID: "preset-draft", OwnerID: "u", ScopeType: "document", ScopeID: "doc", DocumentRevision: "r1", CapabilityKey: "chinese_definition", CapabilityVersion: 1, NormalizedKey: "学", ValueJSON: `{"meaning_in_context":"学习"}`, Origin: "llm_preanalysis", Status: "draft", SchemaVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&preset).Error; err != nil {
		t.Fatal(err)
	}
	drafts, err := s.ListPreanalysisDrafts(context.Background(), "u", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts.Presets) != 1 {
		t.Fatalf("expected one draft, got %d", len(drafts.Presets))
	}
	if _, err = s.PublishPreanalysisDrafts(context.Background(), "u", task.ID, "r1", []string{preset.ID}, nil); err != nil {
		t.Fatal(err)
	}
	var saved Preset
	if err = db.First(&saved, "id = ?", preset.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != "published" {
		t.Fatalf("expected published, got %s", saved.Status)
	}
}
func seedDataset(t *testing.T, s *Service, id, owner string) {
	t.Helper()
	now := time.Now()
	row := orm.Dataset{ID: id, DisplayName: id, Desc: "", CoverImage: "", KbID: id, ResourceUID: id, BucketName: "", OssPath: "", DatasetInfo: json.RawMessage(`{}`), ProcessingConfig: json.RawMessage(`{}`), EmbeddingModel: "", EmbeddingModelProvider: "", TenantID: "", BaseModel: orm.BaseModel{CreateUserID: owner, CreateUserName: owner, CreatedAt: now, UpdatedAt: now}}
	if err := s.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
}
func TestKnowledgeBaseCapabilitiesAndSelectionFiltering(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	seedDataset(t, s, "ds", "u")
	refs := []CapabilityRef{{Key: "english_definition", Enabled: true}, {Key: "classical_definition", Enabled: true}}
	if err := s.PutKnowledgeBaseCapabilities(ctx, "u", "ds", refs); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBook(ctx, "u", Book{Name: "古文", CapabilityKey: "classical_definition"}, nil); err != nil {
		t.Fatal(err)
	}
	result, err := s.AnalyzeSelection(ctx, "u", "ds", "走")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Books) != 1 || result.Books[0].CapabilityKey != "classical_definition" {
		t.Fatalf("unexpected books: %#v", result.Books)
	}
	for _, key := range result.CapabilityKeys {
		if key == "english_definition" {
			t.Fatalf("english capability should not match Han text: %#v", result)
		}
	}
}
func TestSelectionAnalyzerReturnsLongestDictionaryMatchesAndContainingTerms(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	seedDataset(t, s, "ds-match", "u")
	if err := s.PutKnowledgeBaseCapabilities(ctx, "u", "ds-match", []CapabilityRef{{Key: "chinese_definition", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	for i, word := range []string{"求", "求索", "上下求索"} {
		row := DictionaryEntry{ID: fmt.Sprint(i), ProviderKey: "chinese_dictionary", Language: "zh-Hans", NormalizedHeadword: word, DisplayHeadword: word, PayloadJSON: `{"meaning_in_context":"test"}`, Priority: 100, SourceName: "test", SourceVersion: "1", LicenseID: "test"}
		if err := s.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.AnalyzeSelection(ctx, "u", "ds-match", "吾将上下而求索")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) < 2 || result.Matches[0].Text != "求索" {
		t.Fatalf("unexpected longest matches %#v", result.Matches)
	}
	charResult, err := s.AnalyzeSelection(ctx, "u", "ds-match", "索")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, match := range charResult.Matches {
		found = found || match.Text == "求索"
	}
	if !found {
		t.Fatalf("containing term not returned %#v", charResult.Matches)
	}
}
func TestPresetPrecedenceAndIsolation(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	seedDataset(t, s, "ds", "u")
	for _, in := range []PresetInput{{ScopeType: "user_global", CapabilityKey: "general_translation", Key: "hello", Value: map[string]any{"translation": "全局"}, Origin: "user"}, {ScopeType: "knowledge_base", ScopeID: "ds", CapabilityKey: "general_translation", Key: "hello", Value: map[string]any{"translation": "知识库"}, Origin: "user"}, {ScopeType: "document", ScopeID: "doc", DocumentRevision: "r1", CapabilityKey: "general_translation", Key: "hello", Value: map[string]any{"translation": "文章"}, Origin: "user"}} {
		if _, err := s.PutPreset(ctx, "u", in); err != nil {
			t.Fatal(err)
		}
	}
	row, err := s.ResolvePreset(ctx, "u", "general_translation", "hello", "ds", "doc", "r1")
	if err != nil || row == nil || row.ScopeType != "document" {
		t.Fatalf("row=%#v err=%v", row, err)
	}
	row, err = s.ResolvePreset(ctx, "other", "general_translation", "hello", "ds", "doc", "r1")
	if err != nil || row != nil {
		t.Fatalf("owner isolation failed: %#v %v", row, err)
	}
}

func TestListPresetsExcludesDeletedRows(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	row, err := s.PutPreset(ctx, "u", PresetInput{ScopeType: "document", ScopeID: "doc", CapabilityKey: "general_translation", Key: "hello", Value: map[string]any{"translation": "你好", "target_language": "zh"}, Origin: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePreset(ctx, "u", row.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListPresets(ctx, "u", "document", "doc", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("deleted presets should be hidden, got %#v", rows)
	}
}

func TestNormalizePreanalysisCandidateEnums(t *testing.T) {
	def := Capability{Languages: []string{"zh-Hans"}, SubjectKinds: []string{"character", "word"}, Analysis: termAnalysis("", `x`, []string{"word", "character"})}
	if got := normalizePreanalysisLanguage("zh-CN", def); got != "zh-Hans" {
		t.Fatalf("language = %q", got)
	}
	if got := normalizePreanalysisSubjectKind("term", "急弯", def); got != "word" {
		t.Fatalf("subject kind = %q", got)
	}
	if got := normalizePreanalysisSubjectKind("", "学", def); got != "character" {
		t.Fatalf("single-character subject kind = %q", got)
	}
}

func TestResolvePreviewHasNoPersistenceUntilUserConfirms(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	def, _ := CapabilityByKey("chinese_definition")
	req := ResolveContentRequest{CapabilityKey: def.Key, Text: "急弯", Context: "通过急弯时应减速", Language: "zh-Hans", SubjectKind: "word", Preview: true}
	key := BuildCacheKey(def.Key, req.Text, req.Language, req.TargetLanguage, req.Context, "", 0, 0)
	if _, err := s.PutPreset(ctx, "u", PresetInput{ScopeType: "user_global", CapabilityKey: def.Key, Key: key, Value: map[string]any{"meaning_in_context": "道路方向急剧改变的弯道"}, Origin: "test"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.ResolveContent(ctx, "u", req)
	if err != nil || result.Content.ID != "" || result.Content.Status != "preview" {
		t.Fatalf("preview result = %#v, err = %v", result, err)
	}
	var contentCount, subjectCount int64
	s.db.Model(&Content{}).Count(&contentCount)
	s.db.Model(&Subject{}).Count(&subjectCount)
	if contentCount != 0 || subjectCount != 0 {
		t.Fatalf("preview persisted content=%d subjects=%d", contentCount, subjectCount)
	}
	req.Preview = false
	req.ProvidedValue = map[string]any{"meaning_in_context": "用户确认的释义"}
	result, err = s.ResolveContent(ctx, "u", req)
	if err != nil || result.Content.ID == "" || result.Content.Status != "published" {
		t.Fatalf("confirmed result = %#v, err = %v", result, err)
	}
}

func TestEveryCapabilityOwnsValidAnalysisRules(t *testing.T) {
	for _, def := range capabilities {
		if def.Analysis.Instruction == "" || def.Analysis.MaxCandidates <= 0 || def.Analysis.FallbackPattern == "" || len(def.Analysis.FallbackKinds) == 0 {
			t.Fatalf("capability %s has incomplete analysis config: %#v", def.Key, def.Analysis)
		}
		if _, err := regexp.Compile(def.Analysis.FallbackPattern); err != nil {
			t.Fatalf("capability %s has invalid fallback pattern: %v", def.Key, err)
		}
	}
}

func TestCapabilityCandidateLimitsCanOverrideDefaults(t *testing.T) {
	def, _ := CapabilityByKey("chinese_definition")
	if def.Analysis.MaxCandidates != 8 || def.Analysis.MaxDocumentCandidates != 20 {
		t.Fatalf("unexpected capability defaults: %#v", def.Analysis)
	}
	if err := validateCapabilitySettings(def, map[string]any{"max_candidates_per_block": float64(12), "max_document_candidates": float64(40)}); err != nil {
		t.Fatalf("valid candidate limits rejected: %v", err)
	}
	for _, settings := range []map[string]any{
		{"max_candidates_per_block": 0},
		{"max_candidates_per_block": 1.5},
		{"max_document_candidates": 1001},
	} {
		if err := validateCapabilitySettings(def, settings); err == nil {
			t.Fatalf("invalid candidate limits accepted: %#v", settings)
		}
	}
}

func TestFallbackPreanalysisCandidatesUsesTermsFromPassage(t *testing.T) {
	def, _ := CapabilityByKey("chinese_definition")
	rows := fallbackPreanalysisCandidates(def, PreanalysisItem{Text: "通过铁路道口、急弯、窄路时应当减速。", SegmentID: "segment-1"})
	if len(rows) == 0 || rows[0].Text != "通过铁路道口" || rows[0].Language != "zh-Hans" || rows[0].SubjectKind != "word" {
		t.Fatalf("unexpected fallback candidates: %#v", rows)
	}
	if rows[0].Context == "" || rows[0].SegmentID != "segment-1" {
		t.Fatalf("candidate lost document context: %#v", rows[0])
	}
}

func TestCustomProfileRejectsUnknownCapability(t *testing.T) {
	s := testService(t)
	if _, err := s.CreateProfile(context.Background(), "u", "自定义", "", []CapabilityRef{{Key: "missing"}}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestResolveContentUsesDocumentPresetAndAttachesCompatibleBook(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	seedDataset(t, s, "ds", "u")
	if err := s.PutKnowledgeBaseCapabilities(ctx, "u", "ds", []CapabilityRef{{Key: "general_translation", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	book, err := s.CreateBook(ctx, "u", Book{Name: "翻译", CapabilityKey: "general_translation"}, []string{"text_input"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PutPreset(ctx, "u", PresetInput{ScopeType: "document", ScopeID: "doc", DocumentRevision: "r1", CapabilityKey: "general_translation", Key: "hello", Value: map[string]any{"translation": "你好", "target_language": "zh"}, Origin: "user"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ResolveContent(ctx, "u", ResolveContentRequest{CapabilityKey: "general_translation", Text: "hello", Language: "en", SubjectKind: "word", DatasetID: "ds", DocumentID: "doc", DocumentRevision: "r1", BookIDs: []string{book.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Cached || result.Value["translation"] != "你好" {
		t.Fatalf("unexpected result %#v", result)
	}
	var count int64
	s.db.Model(&BookEntry{}).Where("book_id = ? AND content_id = ?", book.ID, result.Content.ID).Count(&count)
	if count != 1 {
		t.Fatalf("book entry count=%d", count)
	}
	again, err := s.ResolveContent(ctx, "u", ResolveContentRequest{CapabilityKey: "general_translation", Text: "hello", Language: "en", SubjectKind: "word", DatasetID: "ds", DocumentID: "doc", DocumentRevision: "r1", BookIDs: []string{book.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if again.Content.ID != result.Content.ID {
		t.Fatalf("repeat resolve created duplicate content %s != %s", again.Content.ID, result.Content.ID)
	}
	s.db.Model(&BookEntry{}).Where("book_id = ?", book.ID).Count(&count)
	if count != 1 {
		t.Fatalf("repeat resolve entry count=%d", count)
	}
}

func TestResolveContentRejectsIncompatibleBook(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	book, err := s.CreateBook(ctx, "u", Book{Name: "古文", CapabilityKey: "classical_definition"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PutPreset(ctx, "u", PresetInput{ScopeType: "user_global", CapabilityKey: "general_translation", Key: "hello", Value: map[string]any{"translation": "你好", "target_language": "zh"}, Origin: "user"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ResolveContent(ctx, "u", ResolveContentRequest{CapabilityKey: "general_translation", Text: "hello", Language: "en", SubjectKind: "word", BookIDs: []string{book.ID}})
	if err == nil {
		t.Fatal("expected capability mismatch")
	}
}

func TestConfirmContentValidatesSchemaAndBookCapability(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	subject := Subject{ID: "s", OwnerID: "u", SubjectKind: "word", NormalizedText: "求索", DisplayText: "求索", Language: "zh-Hans", CreatedAt: now, UpdatedAt: now}
	if err := s.db.Create(&subject).Error; err != nil {
		t.Fatal(err)
	}
	content := Content{ID: "c", OwnerID: "u", SubjectID: "s", CapabilityKey: "chinese_definition", CapabilityVersion: 1, SchemaVersion: 1, ContentJSON: `{}`, Status: "draft", CreatedAt: now, UpdatedAt: now}
	if err := s.db.Create(&content).Error; err != nil {
		t.Fatal(err)
	}
	compatible, err := s.CreateBook(ctx, "u", Book{Name: "汉语", CapabilityKey: "chinese_definition"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	incompatible, err := s.CreateBook(ctx, "u", Book{Name: "古文翻译", CapabilityKey: "classical_translation"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmContent(ctx, "u", "c", map[string]any{}, []string{compatible.ID}); err == nil {
		t.Fatal("expected required field validation")
	}
	if _, err = s.ConfirmContent(ctx, "u", "c", map[string]any{"meaning_in_context": "探索追求"}, []string{incompatible.ID}); err == nil {
		t.Fatal("expected capability mismatch")
	}
	row, err := s.ConfirmContent(ctx, "u", "c", map[string]any{"meaning_in_context": "探索追求"}, []string{compatible.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !row.UserEdited || row.Origin != "user" {
		t.Fatalf("unexpected content %#v", row)
	}
	var count int64
	s.db.Model(&BookEntry{}).Where("book_id=? AND content_id=?", compatible.ID, "c").Count(&count)
	if count != 1 {
		t.Fatalf("entry count=%d", count)
	}
}
