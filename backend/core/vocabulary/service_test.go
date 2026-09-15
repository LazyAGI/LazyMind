package vocabulary

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	fsrs "github.com/open-spaced-repetition/go-fsrs/v3"
	"gorm.io/gorm"
)

func testService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv("LAZYMIND_VOCABULARY_ENABLED", "true")
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []any{&ProviderSetting{}, &Word{}, &Example{}, &SourceRef{}, &providerOperation{}, &ReviewCard{}, &ReviewLogRow{}, &ReviewSession{}, &ReviewSessionItem{}, &ReviewSessionAnswer{}, &Wordbook{}, &WordbookEntry{}, &Tag{}, &WordTag{}, &ExampleTag{}, &DictionaryEntry{}, &DictionarySense{}, &DictionaryExample{}, &DictionaryImport{}, &FSRSProfile{}}
	if err = db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}
	return New(db)
}
func selectLocal(t *testing.T, s *Service) {
	t.Helper()
	if _, err := s.SaveSetting(context.Background(), "u", ProviderSetting{SelectedProvider: "local", AnkiEndpoint: defaultAnkiURL, AnkiDeckName: defaultAnkiDeck}); err != nil {
		t.Fatal(err)
	}
}

func TestDisabledFeatureRejectsLocalProvider(t *testing.T) {
	s := testService(t)
	t.Setenv("LAZYMIND_VOCABULARY_ENABLED", "false")
	if _, err := s.SaveSetting(context.Background(), "u", ProviderSetting{SelectedProvider: "local", AnkiEndpoint: defaultAnkiURL, AnkiDeckName: defaultAnkiDeck}); err == nil {
		t.Fatal("expected provider rejection")
	}
	if _, err := s.NextQuestion(context.Background(), "u"); err == nil {
		t.Fatal("expected review rejection")
	}
}

func TestLocalAddMergesAndCreatesCardTypes(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	first, err := s.AddWord(ctx, "u", AddWordRequest{Term: "Context", Language: "en", Sentence: "Context makes meaning clear.", Tags: []string{"Reading"}, DocumentID: "doc1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.AddWord(ctx, "u", AddWordRequest{Term: " context ", Language: "en", Sentence: "A second context helps.", DocumentID: "doc2"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Word.ID != second.Word.ID {
		t.Fatal("duplicate was not merged")
	}
	var words, cards, examples int64
	s.db.Model(&Word{}).Count(&words)
	s.db.Model(&ReviewCard{}).Count(&cards)
	s.db.Model(&Example{}).Count(&examples)
	if words != 1 || cards != 4 || examples != 2 {
		t.Fatalf("words=%d cards=%d examples=%d", words, cards, examples)
	}
	items, err := s.DocumentWords(ctx, "u", "doc1")
	if err != nil || len(items) != 1 {
		t.Fatalf("document items=%d err=%v", len(items), err)
	}
}

func TestDocumentWordCanOnlyBeDeletedAtLastSource(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	first, err := s.AddWord(ctx, "u", AddWordRequest{Term: "source", Language: "en", DocumentID: "doc1", OriginType: "document"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddWord(ctx, "u", AddWordRequest{Term: "source", Language: "en", DocumentID: "doc2", OriginType: "document"}); err != nil {
		t.Fatal(err)
	}
	items, err := s.DocumentWords(ctx, "u", "doc1")
	if err != nil || len(items) != 1 || items[0].SourceCount != 2 || items[0].CanDelete {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if err = s.DeleteDocumentWord(ctx, "u", "doc1", first.Word.ID); err == nil {
		t.Fatal("expected deletion with another source to be rejected")
	}
	if err = s.RemoveDocumentSource(ctx, "u", "doc1", first.Word.ID); err != nil {
		t.Fatal(err)
	}
	items, err = s.DocumentWords(ctx, "u", "doc2")
	if err != nil || len(items) != 1 || items[0].SourceCount != 1 || !items[0].CanDelete {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if err = s.DeleteDocumentWord(ctx, "u", "doc2", first.Word.ID); err != nil {
		t.Fatal(err)
	}
	var count int64
	s.db.Model(&Word{}).Where("id = ?", first.Word.ID).Count(&count)
	if count != 0 {
		t.Fatal("word was not deleted")
	}
}

func TestFSRSPreviewMatchesSubmitAndRejectsDuplicate(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	result, err := s.AddWord(ctx, "u", AddWordRequest{Term: "evidence", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.NextQuestion(ctx, "u")
	if err != nil || q == nil {
		t.Fatalf("question=%v err=%v", q, err)
	}
	expected := q.Options["good"]
	req := ReviewRequest{CardID: q.CardID, Rating: "good", RowVersion: q.RowVersion, IdempotencyKey: "review-1", PreviewedAt: q.PreviewedAt}
	if err = s.Review(ctx, "u", result.Word.ID, req); err != nil {
		t.Fatal(err)
	}
	var row ReviewCard
	s.db.Where("id = ?", q.CardID).First(&row)
	var card fsrs.Card
	if err = json.Unmarshal([]byte(row.FSRSCardJSON), &card); err != nil {
		t.Fatal(err)
	}
	if !card.Due.Equal(expected) {
		t.Fatalf("due=%v preview=%v", card.Due, expected)
	}
	if err = s.Review(ctx, "u", result.Word.ID, req); err == nil {
		t.Fatal("expected stale review rejection")
	}
	var logs int64
	s.db.Model(&ReviewLogRow{}).Count(&logs)
	if logs != 1 {
		t.Fatalf("logs=%d", logs)
	}
}

func TestReviewSessionUsesSharedReviewPathAndBuildsReport(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	result, err := s.AddWord(ctx, "u", AddWordRequest{Term: "evidence", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.StartReviewSession(ctx, "u", 5)
	if err != nil || len(batch.Questions) == 0 {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	for index, q := range batch.Questions {
		err = s.RecordSessionAnswer(ctx, "u", batch.Session.ID, result.Word.ID, result.Word.Term, ReviewRequest{
			CardID: q.CardID, Rating: "good", RowVersion: q.RowVersion,
			IdempotencyKey: fmt.Sprintf("session-review-%d", index), PreviewedAt: q.PreviewedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	report, err := s.CompleteReviewSession(ctx, "u", batch.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != len(batch.Questions) || report.Correct != len(batch.Questions) || report.Incorrect != 0 || report.Accuracy != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	var logs int64
	s.db.Model(&ReviewLogRow{}).Where("idempotency_key LIKE ?", "session-review-%").Count(&logs)
	if logs != int64(len(batch.Questions)) {
		t.Fatalf("shared review path logs=%d, want %d", logs, len(batch.Questions))
	}
}

func TestReviewSessionCannotCompleteWithRemainingQuestions(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	if _, err := s.AddWord(ctx, "u", AddWordRequest{Term: "inference", Meaning: "推断", Language: "en"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWord(ctx, "u", AddWordRequest{Term: "diverse", Meaning: "多样的", Language: "en"}); err != nil {
		t.Fatal(err)
	}
	batch, err := s.StartReviewSession(ctx, "u", 5)
	if err != nil || len(batch.Questions) < 2 {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	q := batch.Questions[0]
	if err := s.RecordSessionAnswer(ctx, "u", batch.Session.ID, q.Word.ID, q.Word.Term, ReviewRequest{CardID: q.CardID, Response: "wrong", RowVersion: q.RowVersion, IdempotencyKey: "partial", PreviewedAt: q.PreviewedAt}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteReviewSession(ctx, "u", batch.Session.ID); err == nil || !strings.Contains(err.Error(), "remaining questions") {
		t.Fatalf("CompleteReviewSession error=%v", err)
	}
}

func TestNextSessionQuestionIsStickyAndBackendGradesAnswer(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	if _, err := s.AddWord(ctx, "u", AddWordRequest{Term: "inference", Meaning: "推断", Language: "en"}); err != nil {
		t.Fatal(err)
	}
	batch, err := s.StartReviewSession(ctx, "u", 5)
	if err != nil {
		t.Fatal(err)
	}
	first, remain, err := s.NextSessionQuestion(ctx, "u", batch.Session.ID)
	if err != nil || first == nil || remain != int64(len(batch.Questions)) {
		t.Fatalf("first=%#v remain=%d err=%v", first, remain, err)
	}
	again, _, err := s.NextSessionQuestion(ctx, "u", batch.Session.ID)
	if err != nil || again == nil || again.ID != first.ID {
		t.Fatalf("issued question was not sticky: first=%#v again=%#v err=%v", first, again, err)
	}
	if err := s.RecordSessionAnswer(ctx, "u", batch.Session.ID, "untrusted-word", "untrusted-term", ReviewRequest{CardID: first.CardID, Response: first.Expected, Rating: "again", RowVersion: first.RowVersion, IdempotencyKey: "graded", PreviewedAt: first.PreviewedAt}); err != nil {
		t.Fatal(err)
	}
	correct, err := s.SessionAnswerCorrect(ctx, "u", batch.Session.ID, first.CardID)
	if err != nil || !correct {
		t.Fatalf("correct=%v err=%v", correct, err)
	}
	if err := s.RecordSessionAnswer(ctx, "u", batch.Session.ID, first.WordID, first.Term, ReviewRequest{CardID: first.CardID, Response: first.Expected, RowVersion: first.RowVersion, IdempotencyKey: "duplicate", PreviewedAt: first.PreviewedAt}); err == nil || !strings.Contains(err.Error(), "already answered") {
		t.Fatalf("duplicate answer error=%v", err)
	}
}

func TestNextSessionQuestionsReturnsStickyBatch(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	for _, word := range []AddWordRequest{
		{Term: "inference", Meaning: "推断", Language: "en"},
		{Term: "diverse", Meaning: "多样的", Language: "en"},
		{Term: "context", Meaning: "语境", Language: "en"},
	} {
		if _, err := s.AddWord(ctx, "u", word); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := s.StartReviewSession(ctx, "u", 5)
	if err != nil || len(batch.Questions) < 3 {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	first, remain, err := s.NextSessionQuestions(ctx, "u", batch.Session.ID, 3)
	if err != nil || len(first) != 3 || remain != int64(len(batch.Questions)) {
		t.Fatalf("first=%#v remain=%d err=%v", first, remain, err)
	}
	again, againRemain, err := s.NextSessionQuestions(ctx, "u", batch.Session.ID, 3)
	if err != nil || len(again) != len(first) || againRemain != remain {
		t.Fatalf("again=%#v remain=%d err=%v", again, againRemain, err)
	}
	for i := range first {
		if again[i].ID != first[i].ID {
			t.Fatalf("batch was not sticky at %d: first=%s again=%s", i, first[i].ID, again[i].ID)
		}
	}
}

func TestPrepareSessionQuestionsBuildsObjectiveModes(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	if _, err := s.AddWord(ctx, "u", AddWordRequest{Term: "context", Meaning: "语境", PartOfSpeech: "n.", Language: "en"}); err != nil {
		t.Fatal(err)
	}
	batch, err := s.StartReviewSession(ctx, "u", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.NextSessionQuestions(ctx, "u", batch.Session.ID, 1); err != nil {
		t.Fatal(err)
	}
	questions, err := s.PrepareSessionQuestions(ctx, "u", batch.Session.ID, "c2e", "choice")
	if err != nil || len(questions) != 1 || questions[0].Type != "single" || len(questions[0].Choices) != 6 {
		t.Fatalf("questions=%#v err=%v", questions, err)
	}
	foundPOS := false
	for _, choice := range questions[0].Choices {
		if strings.Contains(choice, "（n.）") {
			foundPOS = true
		}
	}
	if !foundPOS {
		t.Fatalf("part of speech missing from choices: %#v", questions[0].Choices)
	}
	questions, err = s.PrepareSessionQuestions(ctx, "u", batch.Session.ID, "c2e", "fill")
	if err != nil || questions[0].Type != "text" || len(questions[0].Choices) != 0 {
		t.Fatalf("fill questions=%#v err=%v", questions, err)
	}
	if _, err = s.PrepareSessionQuestions(ctx, "u", batch.Session.ID, "e2c", "fill"); err == nil {
		t.Fatal("e2c fill must be rejected")
	}
}

func TestIssueClozeSessionWordsSignsOnlyCorrectAnswers(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	answers := make([]string, 0, 10)
	for i := 0; i < 20; i++ {
		term := fmt.Sprintf("candidate%02d", i)
		if _, err := s.AddWord(ctx, "u", AddWordRequest{Term: term, Meaning: "候选", Language: "en"}); err != nil {
			t.Fatal(err)
		}
		if i < 10 {
			answers = append(answers, term)
		}
	}
	batch, err := s.PreviewReviewSession(ctx, "u", 20)
	if err != nil || len(batch.Questions) != 20 {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	var previewCount int64
	s.db.Model(&ReviewSessionItem{}).Where("session_id = ?", batch.Session.ID).Count(&previewCount)
	if previewCount != 0 {
		t.Fatalf("preview unexpectedly issued %d words", previewCount)
	}
	_, issued, err := s.IssueReviewSessionWords(ctx, "u", answers)
	if err != nil || len(issued) != 10 {
		t.Fatalf("issued=%#v err=%v", issued, err)
	}
	var total, issuedCount int64
	s.db.Model(&ReviewSessionItem{}).Where("session_id = ?", batch.Session.ID).Count(&total)
	s.db.Model(&ReviewSessionItem{}).Where("session_id = ? AND status = ?", batch.Session.ID, "issued").Count(&issuedCount)
	if total != 10 || issuedCount != 10 {
		t.Fatalf("total=%d issued=%d", total, issuedCount)
	}
	var stored ReviewSessionItem
	if err := s.db.Where("id = ?", issued[0].ReviewItemID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Expected != stored.Term || stored.CardType != "create" {
		t.Fatalf("stored cloze item=%#v", stored)
	}
}

func TestReviewSessionIsSharedUntilCompletedOrExpired(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	if _, err := s.AddWord(ctx, "u", AddWordRequest{Term: "shared", Meaning: "共享", Language: "en"}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewReviewSession(ctx, "u", 5)
	if err != nil {
		t.Fatal(err)
	}
	pageBatch, err := s.StartReviewSession(ctx, "u", 5)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Session.ID != pageBatch.Session.ID {
		t.Fatalf("chat session %q and page session %q must match", preview.Session.ID, pageBatch.Session.ID)
	}
	past := time.Now().UTC().Add(-time.Minute)
	if err := s.db.Model(&ReviewSession{}).Where("id = ?", preview.Session.ID).Update("expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	next, err := s.PreviewReviewSession(ctx, "u", 5)
	if err != nil {
		t.Fatal(err)
	}
	if next.Session.ID == preview.Session.ID {
		t.Fatal("expired session was reused")
	}
	var expired ReviewSession
	if err := s.db.Where("id = ?", preview.Session.ID).First(&expired).Error; err != nil {
		t.Fatal(err)
	}
	if expired.Status != "expired" || expired.CompletedAt == nil {
		t.Fatalf("expired session lifecycle not persisted: %#v", expired)
	}
}

func TestDeleteWordbookMovesOrDeletesWordsAndProtectsDefault(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	defaultBook, err := s.EnsureDefaultWordbook(ctx, "u")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteWordbook(ctx, "u", defaultBook.ID, "delete_words", ""); err == nil {
		t.Fatal("default wordbook should be protected")
	}
	source, _ := s.CreateWordbook(ctx, "u", WordbookInput{Name: "source"})
	word, err := s.AddWord(ctx, "u", AddWordRequest{Term: "move-me", Language: "en", WordbookIDs: []string{source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteWordbook(ctx, "u", source.ID, "move", defaultBook.ID); err != nil {
		t.Fatal(err)
	}
	var moved int64
	s.db.Model(&WordbookEntry{}).Where("owner_id = ? AND wordbook_id = ? AND word_id = ?", "u", defaultBook.ID, word.Word.ID).Count(&moved)
	if moved != 1 {
		t.Fatalf("moved entries=%d", moved)
	}
	deleteBook, _ := s.CreateWordbook(ctx, "u", WordbookInput{Name: "delete"})
	deletedWord, _ := s.AddWord(ctx, "u", AddWordRequest{Term: "delete-me", Language: "en", WordbookIDs: []string{deleteBook.ID}})
	if err = s.DeleteWordbook(ctx, "u", deleteBook.ID, "delete_words", ""); err != nil {
		t.Fatal(err)
	}
	var remaining int64
	s.db.Model(&Word{}).Where("owner_id = ? AND id = ?", "u", deletedWord.Word.ID).Count(&remaining)
	if remaining != 0 {
		t.Fatal("word should be deleted with its wordbook")
	}
}

func TestReviewPriorityAndSentenceExtraction(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	a, _ := s.AddWord(ctx, "u", AddWordRequest{Term: "alpha", Language: "en"})
	_, _ = s.AddWord(ctx, "u", AddWordRequest{Term: "beta", Language: "en"})
	var row ReviewCard
	s.db.Where("word_id = ? AND card_type = ?", a.Word.ID, "word_to_meaning").First(&row)
	card := fsrs.NewCard()
	card.State = fsrs.Relearning
	card.Due = time.Now().Add(-time.Minute)
	raw, _ := json.Marshal(card)
	s.db.Model(&row).Update("fsrs_card_json", string(raw))
	q, err := s.NextQuestion(ctx, "u")
	if err != nil || q.Word.ID != a.Word.ID {
		t.Fatalf("question=%v err=%v", q, err)
	}
	if got := extractSentence("evidence", "First. Strong evidence matters! Last."); got != "Strong evidence matters!" {
		t.Fatalf("got %q", got)
	}
	if got := extractSentence("语境", "前句。这个语境很清楚！后句。"); got != "这个语境很清楚！" {
		t.Fatalf("got %q", got)
	}
}

func TestBundledDictionariesPreferECDICTAndKeepFreeDict(t *testing.T) {
	s := testService(t)
	rows, err := s.LookupDictionary(context.Background(), "en", "evidence")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("expected bundled dictionary entry")
	}
	if rows[0].SourceName != "ECDICT" || rows[0].LicenseID != "MIT" {
		t.Fatalf("ECDICT must be preferred: %#v", rows[0])
	}
	hasFreeDict := false
	for _, row := range rows {
		hasFreeDict = hasFreeDict || row.SourceName == "FreeDict eng-zho"
	}
	if !hasFreeDict || len(rows[0].Senses) == 0 || rows[0].Senses[0].Translation == "" {
		t.Fatalf("missing fallback or translation: %#v", rows)
	}
}

func TestECDICTCommonWordMeanings(t *testing.T) {
	s := testService(t)
	for _, term := range []string{"here", "moment"} {
		rows, err := s.LookupDictionary(context.Background(), "en", term)
		if err != nil || len(rows) == 0 || rows[0].SourceName != "ECDICT" || len(rows[0].Senses) == 0 {
			t.Fatalf("term=%s rows=%#v err=%v", term, rows, err)
		}
	}
}

func TestBundledFreeDictDoesNotSuggestObsoleteSingleCharacterSense(t *testing.T) {
	s := testService(t)
	rows, err := s.LookupDictionary(context.Background(), "en", "moment")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if len(row.Senses) > 0 && row.Senses[0].Translation == "刻" {
			t.Fatalf("obsolete sense must not be auto-suggested: %+v", row.Senses[0])
		}
	}
}

func TestLocalAddPersistsDefaultWordbookAndExampleTags(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	result, err := s.AddWord(context.Background(), "u", AddWordRequest{Term: "atomic", Language: "en", Sentence: "An atomic write is reliable.", ExampleTags: []string{"From document"}})
	if err != nil {
		t.Fatal(err)
	}
	var books, tags int64
	s.db.Model(&WordbookEntry{}).Where("word_id = ?", result.Word.ID).Count(&books)
	s.db.Model(&ExampleTag{}).Where("example_id = ?", result.Example.ID).Count(&tags)
	if books != 1 || tags != 1 {
		t.Fatalf("wordbooks=%d example_tags=%d", books, tags)
	}
}

func TestAnkiMutationIsQueuedWhenOffline(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	setting := ProviderSetting{OwnerID: "u", SelectedProvider: ProviderAnki, AnkiEndpoint: "http://127.0.0.1:1", AnkiDeckName: defaultAnkiDeck, AnkiModelVersion: ankiModelVersion}
	if err := s.db.Create(&setting).Error; err != nil {
		t.Fatal(err)
	}
	word := Word{ID: "w", OwnerID: "u", Provider: ProviderAnki, ProviderNoteID: "42", NormalizedTerm: "before", Term: "before", Language: "en", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := s.db.Create(&word).Error; err != nil {
		t.Fatal(err)
	}
	after := "after"
	if _, err := s.UpdateWord(ctx, "u", word.ID, WordUpdate{Term: &after}); err != nil {
		t.Fatal(err)
	}
	var operation providerOperation
	if err := s.db.Where("operation_type = ? AND status = ?", "update_word", "pending").First(&operation).Error; err != nil {
		t.Fatal(err)
	}
	if operation.RetryCount != 0 {
		t.Fatalf("retry_count=%d", operation.RetryCount)
	}
}

func TestExampleCRUDAndSiblingDeferral(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	result, err := s.AddWord(ctx, "u", AddWordRequest{Term: "sibling", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	ex, err := s.AddExample(ctx, "u", result.Word.ID, ExampleInput{Sentence: "Sibling cards should be separated.", Translation: "同词卡应分开。", Tags: []string{"manual"}})
	if err != nil {
		t.Fatal(err)
	}
	ex.Translation = "同词卡片应错开。"
	if _, err = s.UpdateExample(ctx, "u", ex.ID, ExampleInput{Sentence: ex.Sentence, Translation: ex.Translation, Tags: []string{"edited"}}); err != nil {
		t.Fatal(err)
	}
	q, err := s.NextQuestion(ctx, "u")
	if err != nil || q == nil {
		t.Fatalf("question=%v err=%v", q, err)
	}
	if err = s.Review(ctx, "u", result.Word.ID, ReviewRequest{CardID: q.CardID, Rating: "good", RowVersion: q.RowVersion, IdempotencyKey: "sibling-review", PreviewedAt: q.PreviewedAt}); err != nil {
		t.Fatal(err)
	}
	next, err := s.NextQuestion(ctx, "u")
	if err != nil {
		t.Fatal(err)
	}
	if next != nil && next.Word.ID == result.Word.ID {
		t.Fatal("a sibling card was returned immediately")
	}
	if err = s.DeleteExample(ctx, "u", ex.ID); err != nil {
		t.Fatal(err)
	}
	var count int64
	s.db.Model(&Example{}).Where("id = ?", ex.ID).Count(&count)
	if count != 0 {
		t.Fatal("example was not deleted")
	}
}

func TestBatchResetWordbook(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	book, err := s.CreateWordbook(ctx, "u", WordbookInput{Name: "批量重置"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.AddWord(ctx, "u", AddWordRequest{Term: "resettable", Meaning: "可重置", WordbookIDs: []string{book.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Master(ctx, "u", result.Word.ID); err != nil {
		t.Fatal(err)
	}
	count, err := s.ResetWords(ctx, "u", "wordbook", book.ID)
	if err != nil || count != 1 {
		t.Fatalf("reset count=%d err=%v", count, err)
	}
	var word Word
	if err = s.db.Where("id = ?", result.Word.ID).First(&word).Error; err != nil || word.MasteredAt != nil {
		t.Fatalf("word was not reset: %+v err=%v", word, err)
	}
}

func TestObjectiveReviewQuestionsAndServerGrading(t *testing.T) {
	s := testService(t)
	selectLocal(t, s)
	ctx := context.Background()
	_, err := s.AddWord(ctx, "u", AddWordRequest{Term: "context", Language: "en", Meaning: "语境", PartOfSpeech: "n."})
	if err != nil {
		t.Fatal(err)
	}
	questions, err := s.NextQuestions(ctx, "u", 10)
	if err != nil {
		t.Fatal(err)
	}
	var choice *ReviewQuestion
	for i := range questions {
		if questions[i].CardType == "word_to_meaning" {
			choice = &questions[i]
			break
		}
	}
	if choice == nil || choice.Interaction != "multiple_choice" || len(choice.Choices) != 6 {
		t.Fatalf("unexpected question: %+v", choice)
	}
	if choice.Answer != "" {
		t.Fatal("objective answer must not be sent to the client")
	}
	rating, err := s.gradeResponse(ctx, "u", ReviewRequest{CardID: choice.CardID, Response: "语境"})
	if err != nil || rating != "good" {
		t.Fatalf("correct response rating=%q err=%v", rating, err)
	}
	rating, err = s.gradeResponse(ctx, "u", ReviewRequest{CardID: choice.CardID, Response: "错误释义"})
	if err != nil || rating != "again" {
		t.Fatalf("incorrect response rating=%q err=%v", rating, err)
	}
}
