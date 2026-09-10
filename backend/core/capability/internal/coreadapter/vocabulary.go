package coreadapter

import (
	"context"
	"crypto/sha256"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"lazymind/core/capability"
	"lazymind/core/vocabulary"
)

type VocabularyTrainer struct{ service *vocabulary.Service }

func NewVocabularyTrainerForDB(db *gorm.DB) (*VocabularyTrainer, error) {
	if db == nil {
		return nil, capability.NewError(capability.Internal, "vocabulary.adapter.new", "gorm db is required", false, nil)
	}
	return &VocabularyTrainer{service: vocabulary.New(db)}, nil
}
func (v *VocabularyTrainer) ListVocabularyWordbooks(ctx context.Context, call capability.InvocationContext) (capability.ListVocabularyWordbooksResult, error) {
	provider, rows, err := v.service.ChatWordbooks(ctx, strings.TrimSpace(call.Principal.UserID))
	if err != nil {
		return capability.ListVocabularyWordbooksResult{}, err
	}
	items := make([]capability.VocabularyWordbook, 0, len(rows))
	for _, x := range rows {
		items = append(items, capability.VocabularyWordbook{ID: x.ID, Name: x.Name, Provider: x.Provider})
	}
	return capability.ListVocabularyWordbooksResult{Provider: provider, Items: items, Total: len(items)}, nil
}
func (v *VocabularyTrainer) ListVocabularyWords(ctx context.Context, call capability.InvocationContext, input capability.ListVocabularyWordsInput) (capability.ListVocabularyWordsResult, error) {
	if input.DueOnly {
		owner := strings.TrimSpace(call.Principal.UserID)
		setting, err := v.service.Setting(ctx, owner)
		if err != nil {
			return capability.ListVocabularyWordsResult{}, err
		}
		var questions []vocabulary.ReviewQuestion
		if setting.SelectedProvider == vocabulary.ProviderAnki {
			questions, err = v.service.NextAnkiQuestions(ctx, owner, 20)
		} else {
			questions, err = v.service.NextQuestions(ctx, owner, 20)
		}
		if err != nil {
			return capability.ListVocabularyWordsResult{}, err
		}
		items, seen := make([]capability.VocabularyWord, 0, len(questions)), map[string]bool{}
		for _, q := range questions {
			if seen[q.Word.ID] {
				continue
			}
			seen[q.Word.ID] = true
			items = append(items, capability.VocabularyWord{ID: q.Word.ID, Term: q.Word.Term, Meaning: q.Word.Meaning, PartOfSpeech: q.Word.PartOfSpeech, State: q.State, ReviewCount: q.Reps, Lapses: q.Lapses, Tags: q.Tags})
		}
		return capability.ListVocabularyWordsResult{Provider: setting.SelectedProvider, Items: items, Total: len(items)}, nil
	}
	provider, rows, err := v.service.ChatWords(ctx, strings.TrimSpace(call.Principal.UserID), input.WordbookID, input.Search)
	if err != nil {
		return capability.ListVocabularyWordsResult{}, err
	}
	items := make([]capability.VocabularyWord, 0, len(rows))
	for _, x := range rows {
		items = append(items, capability.VocabularyWord{ID: x.ID, Term: x.Term, Meaning: x.Meaning, PartOfSpeech: x.PartOfSpeech, State: x.State, ReviewCount: x.Reps, Lapses: x.Lapses, Tags: x.Tags})
	}
	return capability.ListVocabularyWordsResult{Provider: provider, Items: items, Total: len(items)}, nil
}
func (v *VocabularyTrainer) NextVocabularyReview(ctx context.Context, call capability.InvocationContext, input capability.NextVocabularyReviewInput) (capability.NextVocabularyReviewResult, error) {
	owner := strings.TrimSpace(call.Principal.UserID)
	if strings.TrimSpace(input.SessionID) != "" {
		items, remain, err := v.service.NextSessionQuestions(ctx, owner, strings.TrimSpace(input.SessionID), input.Count)
		if err != nil {
			return capability.NextVocabularyReviewResult{}, err
		}
		if len(items) == 0 {
			return capability.NextVocabularyReviewResult{Completed: true}, nil
		}
		questions := make([]capability.VocabularyReviewQuestion, 0, len(items))
		for _, item := range items {
			choices := []string{strings.TrimSpace(item.Expected)}
			for _, candidate := range items {
				answer := strings.TrimSpace(candidate.Expected)
				if answer != "" && !containsString(choices, answer) {
					choices = append(choices, answer)
				}
			}
			if len(choices) < 2 {
				choices = append(choices, "不确定")
			}
			hash := sha256.Sum256([]byte(item.ID))
			swap := int(hash[0]) % len(choices)
			choices[0], choices[swap] = choices[swap], choices[0]
			questions = append(questions, capability.VocabularyReviewQuestion{ReviewItemID: item.ID, CardID: item.CardID, WordID: item.WordID, Prompt: item.Prompt, Term: item.Term, Meaning: item.Meaning, CardType: item.CardType, Interaction: "single_choice", Choices: choices, RowVersion: item.RowVersion, PreviewedAt: item.PreviewedAt})
		}
		first := questions[0]
		result := capability.NextVocabularyReviewResult{Available: true, ReviewItemID: first.ReviewItemID, CardID: first.CardID, WordID: first.WordID, Prompt: first.Prompt, Term: first.Term, Meaning: first.Meaning, CardType: first.CardType, Interaction: first.Interaction, Choices: first.Choices, Remaining: remain, RowVersion: first.RowVersion, PreviewedAt: first.PreviewedAt}
		result.Questions = questions
		return result, nil
	}
	setting, err := v.service.Setting(ctx, owner)
	if err != nil {
		return capability.NextVocabularyReviewResult{}, err
	}
	var q *vocabulary.ReviewQuestion
	if setting.SelectedProvider == vocabulary.ProviderAnki {
		q, err = v.service.NextAnkiQuestion(ctx, owner)
	} else {
		q, err = v.service.NextQuestion(ctx, owner)
	}
	if err != nil {
		return capability.NextVocabularyReviewResult{}, err
	}
	if q == nil {
		return capability.NextVocabularyReviewResult{Provider: setting.SelectedProvider}, nil
	}
	return capability.NextVocabularyReviewResult{Available: true, Provider: setting.SelectedProvider, CardID: q.CardID, WordID: q.Word.ID, Prompt: q.Prompt, Term: q.Word.Term, Answer: q.Answer, Remaining: q.Remaining, RowVersion: q.RowVersion, PreviewedAt: q.PreviewedAt}, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func (v *VocabularyTrainer) StartVocabularyReview(ctx context.Context, call capability.InvocationContext, input capability.StartVocabularyReviewInput) (capability.StartVocabularyReviewResult, error) {
	batch, err := v.service.StartReviewSession(ctx, strings.TrimSpace(call.Principal.UserID), input.Count)
	if err != nil {
		return capability.StartVocabularyReviewResult{}, err
	}
	questions := make([]capability.NextVocabularyReviewResult, 0, len(batch.Questions))
	for _, q := range batch.Questions {
		questions = append(questions, capability.NextVocabularyReviewResult{Available: true, Provider: batch.Session.Provider, CardID: q.CardID, WordID: q.Word.ID, Prompt: q.Prompt, Term: q.Word.Term, Answer: q.Answer, Remaining: q.Remaining, RowVersion: q.RowVersion, PreviewedAt: q.PreviewedAt})
	}
	return capability.StartVocabularyReviewResult{SessionID: batch.Session.ID, Provider: batch.Session.Provider, WordbookName: batch.Session.WordbookName, Questions: []capability.NextVocabularyReviewResult{}, Total: len(questions), Remaining: len(questions)}, nil
}
func (v *VocabularyTrainer) AnswerVocabularyReview(ctx context.Context, call capability.InvocationContext, input capability.AnswerVocabularyReviewInput) (capability.AnswerVocabularyReviewResult, error) {
	owner := strings.TrimSpace(call.Principal.UserID)
	var err error
	if input.SessionID != "" {
		if strings.TrimSpace(input.Response) == "" {
			return capability.AnswerVocabularyReviewResult{}, capability.NewError(capability.InvalidArgument, "vocabulary.review.answer", "response is required for session review", false, nil)
		}
		if strings.TrimSpace(input.ReviewItemID) == "" {
			return capability.AnswerVocabularyReviewResult{}, capability.NewError(capability.InvalidArgument, "vocabulary.review.answer", "review_item_id is required for session review", false, nil)
		}
		item, itemErr := v.service.SessionItem(ctx, owner, input.SessionID, input.ReviewItemID)
		if itemErr != nil {
			return capability.AnswerVocabularyReviewResult{}, itemErr
		}
		input.CardID, input.WordID, input.Term = item.CardID, item.WordID, item.Term
		input.RowVersion, input.PreviewedAt = item.RowVersion, item.PreviewedAt
	}
	req := vocabulary.ReviewRequest{CardID: input.CardID, Rating: input.Rating, Response: input.Response, RowVersion: input.RowVersion, PreviewedAt: input.PreviewedAt, IdempotencyKey: uuid.NewString()}
	if input.SessionID != "" {
		err = v.service.RecordSessionAnswer(ctx, owner, input.SessionID, input.WordID, input.Term, req)
	} else if input.Provider == vocabulary.ProviderAnki {
		err = v.service.ReviewAnki(ctx, owner, req)
	} else {
		err = v.service.Review(ctx, owner, input.WordID, req)
	}
	if err != nil {
		return capability.AnswerVocabularyReviewResult{}, err
	}
	remain := int64(0)
	correct := false
	if input.SessionID != "" {
		remain, err = v.service.SessionRemaining(ctx, owner, input.SessionID)
		if err == nil {
			correct, err = v.service.SessionAnswerCorrect(ctx, owner, input.SessionID, input.CardID)
		}
	}
	return capability.AnswerVocabularyReviewResult{Accepted: err == nil, Correct: correct, Remaining: remain}, err
}
func (v *VocabularyTrainer) VocabularyReviewReport(ctx context.Context, call capability.InvocationContext, input capability.VocabularyReviewReportInput) (capability.VocabularyReviewReportResult, error) {
	report, err := v.service.CompleteReviewSession(ctx, strings.TrimSpace(call.Principal.UserID), input.SessionID)
	if err != nil {
		return capability.VocabularyReviewReportResult{}, err
	}
	return capability.VocabularyReviewReportResult{SessionID: report.Session.ID, Provider: report.Session.Provider, WordbookName: report.Session.WordbookName, Total: report.Total, Correct: report.Correct, Incorrect: report.Incorrect, Accuracy: report.Accuracy, RatingCounts: report.RatingCounts, AverageIntervalBefore: report.AverageIntervalBefore, AverageIntervalAfter: report.AverageIntervalAfter, DifficultWords: report.DifficultWords}, nil
}
