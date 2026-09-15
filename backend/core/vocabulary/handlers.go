package vocabulary

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"lazymind/core/common"
	"lazymind/core/store"
)

func service() *Service { return New(store.DB()) }

func GetProvider(w http.ResponseWriter, r *http.Request) {
	setting, err := service().Setting(r.Context(), store.UserID(r))
	if err != nil {
		common.ReplyErr(w, "load vocabulary provider failed", 500)
		return
	}
	common.ReplyOK(w, setting)
}
func PutProvider(w http.ResponseWriter, r *http.Request) {
	var req ProviderSetting
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	setting, err := service().SaveSetting(r.Context(), store.UserID(r), req)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, setting)
}
func Status(w http.ResponseWriter, r *http.Request) {
	common.ReplyOK(w, service().Status(r.Context(), store.UserID(r)))
}
func Initialize(w http.ResponseWriter, r *http.Request) {
	if err := service().Initialize(r.Context(), store.UserID(r)); err != nil {
		common.ReplyErr(w, err.Error(), 502)
		return
	}
	common.ReplyOK(w, map[string]bool{"initialized": true})
}
func RequestPermission(w http.ResponseWriter, r *http.Request) {
	permission, err := service().RequestAnkiPermission(r.Context(), store.UserID(r))
	if err != nil {
		common.ReplyErr(w, err.Error(), 502)
		return
	}
	common.ReplyOK(w, map[string]string{"permission": permission})
}
func Sync(w http.ResponseWriter, r *http.Request) {
	if err := service().Sync(r.Context(), store.UserID(r)); err != nil {
		common.ReplyErr(w, err.Error(), 502)
		return
	}
	common.ReplyOK(w, map[string]bool{"synced": true})
}
func ListAnkiDecks(w http.ResponseWriter, r *http.Request) {
	items, err := service().ListAnkiDecks(r.Context(), store.UserID(r))
	if err != nil {
		common.ReplyErr(w, err.Error(), 502)
		return
	}
	common.ReplyOK(w, map[string]any{"items": items})
}
func CreateAnkiDeck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if err := service().CreateAnkiDeck(r.Context(), store.UserID(r), req.Name); err != nil {
		common.ReplyErr(w, err.Error(), 502)
		return
	}
	common.ReplyOK(w, map[string]string{"name": strings.TrimSpace(req.Name)})
}
func DeleteAnkiDeck(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if err := service().DeleteAnkiDeck(r.Context(), store.UserID(r), name, r.URL.Query().Get("mode"), r.URL.Query().Get("target")); err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, map[string]bool{"deleted": true})
}
func AddWord(w http.ResponseWriter, r *http.Request) {
	var req AddWordRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	result, err := service().AddWord(r.Context(), store.UserID(r), req)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, result)
}
func ListDocumentWords(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(mux.Vars(r)["document_id"])
	if id == "" {
		common.ReplyErr(w, "document_id is required", 400)
		return
	}
	items, err := service().DocumentWords(r.Context(), store.UserID(r), id)
	if err != nil {
		common.ReplyErr(w, "list document vocabulary failed", 500)
		return
	}
	common.ReplyOK(w, map[string]any{"items": items})
}
func ListWords(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := service().SearchWords(r.Context(), store.UserID(r), WordQuery{Provider: defaultString(q.Get("provider"), ProviderAnki), Search: q.Get("search"), State: q.Get("state"), WordbookID: q.Get("wordbook_id"), DocumentID: q.Get("document_id"), TagsAll: q["tag_all"], TagsAny: q["tag_any"], TagsNot: q["tag_not"]})
	if err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, map[string]any{"items": items})
}
func NextReview(w http.ResponseWriter, r *http.Request) {
	var item *ReviewQuestion
	var err error
	if r.URL.Query().Get("provider") == ProviderAnki {
		item, err = service().NextAnkiQuestion(r.Context(), store.UserID(r))
	} else {
		item, err = service().NextQuestion(r.Context(), store.UserID(r))
	}
	if err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, item)
}
func StartReviewSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Count int `json:"count"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.Count == 0 {
		in.Count = 5
	}
	batch, err := service().StartReviewSession(r.Context(), store.UserID(r), in.Count)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, batch)
}
func PreviewReviewSession(w http.ResponseWriter, r *http.Request) {
	count := 5
	if raw := strings.TrimSpace(r.URL.Query().Get("count")); raw != "" {
		_, _ = fmt.Sscanf(raw, "%d", &count)
	}
	batch, err := service().PreviewReviewSession(r.Context(), store.UserID(r), count)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, batch)
}
func GetActiveReviewSession(w http.ResponseWriter, r *http.Request) {
	session, err := service().ActiveReviewSession(r.Context(), store.UserID(r))
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	if session == nil {
		common.ReplyOK(w, map[string]any{"active": false})
		return
	}
	remaining, _ := service().SessionRemaining(r.Context(), store.UserID(r), session.ID)
	common.ReplyOK(w, map[string]any{"active": true, "session": session, "remaining": remaining})
}
func IssueReviewSessionWords(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Terms []string `json:"terms"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	session, items, err := service().IssueReviewSessionWords(r.Context(), store.UserID(r), in.Terms)
	if err != nil {
		common.ReplyErr(w, err.Error(), 409)
		return
	}
	common.ReplyOK(w, map[string]any{"session": session, "items": items})
}
func NextReviewSessionQuestions(w http.ResponseWriter, r *http.Request) {
	count := 5
	if raw := strings.TrimSpace(r.URL.Query().Get("count")); raw != "" {
		_, _ = fmt.Sscanf(raw, "%d", &count)
	}
	items, remaining, err := service().NextSessionQuestions(r.Context(), store.UserID(r), mux.Vars(r)["session_id"], count)
	if err != nil {
		common.ReplyErr(w, err.Error(), 409)
		return
	}
	common.ReplyOK(w, map[string]any{"items": items, "remaining": remaining, "complete": remaining == 0})
}
func PrepareReviewSessionQuestions(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode           string   `json:"mode"`
		QuestionType   string   `json:"question_type"`
		CorrectAnswers []string `json:"correct_answers"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if strings.EqualFold(strings.TrimSpace(in.Mode), "create") && strings.EqualFold(strings.TrimSpace(in.QuestionType), "cloze") {
		items, err := service().IssueClozeSessionWords(r.Context(), store.UserID(r), mux.Vars(r)["session_id"], in.CorrectAnswers)
		if err != nil {
			common.ReplyErr(w, err.Error(), 409)
			return
		}
		common.ReplyOK(w, map[string]any{"items": items})
		return
	}
	questions, err := service().PrepareSessionQuestions(r.Context(), store.UserID(r), mux.Vars(r)["session_id"], in.Mode, in.QuestionType)
	if err != nil {
		common.ReplyErr(w, err.Error(), 409)
		return
	}
	common.ReplyOK(w, map[string]any{"questions": questions})
}
func SubmitSessionReview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		WordID string `json:"word_id"`
		Term   string `json:"term"`
		ReviewRequest
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if err := service().RecordSessionAnswer(r.Context(), store.UserID(r), mux.Vars(r)["session_id"], in.WordID, in.Term, in.ReviewRequest); err != nil {
		common.ReplyErr(w, err.Error(), 409)
		return
	}
	common.ReplyOK(w, map[string]bool{"accepted": true})
}
func RegisterSessionReview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ReviewItemID string   `json:"review_item_id"`
		WordID       string   `json:"word_id"`
		Correct      *bool    `json:"correct,omitempty"`
		Score        *float64 `json:"score,omitempty"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || (strings.TrimSpace(in.ReviewItemID) == "" && strings.TrimSpace(in.WordID) == "") {
		common.ReplyErr(w, "word_id is required", 400)
		return
	}
	owner, sessionID := store.UserID(r), mux.Vars(r)["session_id"]
	var item ReviewSessionItem
	var err error
	if strings.TrimSpace(in.WordID) != "" {
		var session ReviewSession
		session, item, err = service().ActiveSessionItemByWord(r.Context(), owner, in.WordID)
		sessionID = session.ID
	} else {
		item, err = service().SessionItem(r.Context(), owner, sessionID, in.ReviewItemID)
	}
	if err != nil {
		common.ReplyErr(w, err.Error(), 404)
		return
	}
	score := 0.0
	if in.Score != nil {
		score = *in.Score
	} else if in.Correct != nil && *in.Correct {
		score = 1
	}
	if score < 0 || score > 1 {
		common.ReplyErr(w, "score must be between 0 and 1", 400)
		return
	}
	rating := "again"
	if score >= 0.9 {
		rating = "easy"
	} else if score >= 0.7 {
		rating = "good"
	} else if score >= 0.4 {
		rating = "hard"
	}
	err = service().RecordSessionAnswer(r.Context(), owner, sessionID, item.WordID, item.Term, ReviewRequest{CardID: item.CardID, Rating: rating, RowVersion: item.RowVersion, PreviewedAt: item.PreviewedAt, IdempotencyKey: uuid.NewString()})
	if err != nil {
		common.ReplyErr(w, err.Error(), 409)
		return
	}
	remaining, _ := service().SessionRemaining(r.Context(), owner, sessionID)
	common.ReplyOK(w, map[string]any{"accepted": true, "remaining": remaining, "score": score, "rating": rating})
}
func CompleteReviewSession(w http.ResponseWriter, r *http.Request) {
	report, err := service().CompleteReviewSession(r.Context(), store.UserID(r), mux.Vars(r)["session_id"])
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, report)
}
func SubmitReview(w http.ResponseWriter, r *http.Request) {
	var req ReviewRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	var err error
	if r.URL.Query().Get("provider") == ProviderAnki {
		err = service().ReviewAnki(r.Context(), store.UserID(r), req)
	} else {
		err = service().Review(r.Context(), store.UserID(r), mux.Vars(r)["word_id"], req)
	}
	if err != nil {
		common.ReplyErr(w, err.Error(), 409)
		return
	}
	common.ReplyOK(w, map[string]bool{"reviewed": true})
}

func ListWordbooks(w http.ResponseWriter, r *http.Request) {
	rows, err := service().ListWordbooks(r.Context(), store.UserID(r))
	if err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, map[string]any{"items": rows})
}
func CreateWordbook(w http.ResponseWriter, r *http.Request) {
	var in WordbookInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	row, err := service().CreateWordbook(r.Context(), store.UserID(r), in)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, row)
}
func UpdateWordbook(w http.ResponseWriter, r *http.Request) {
	var in WordbookInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	row, err := service().UpdateWordbook(r.Context(), store.UserID(r), mux.Vars(r)["id"], in)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, row)
}
func DeleteWordbook(w http.ResponseWriter, r *http.Request) {
	if err := service().DeleteWordbook(r.Context(), store.UserID(r), mux.Vars(r)["id"], r.URL.Query().Get("mode"), r.URL.Query().Get("target_id")); err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, map[string]bool{"deleted": true})
}
func GetWord(w http.ResponseWriter, r *http.Request) {
	row, err := service().GetWord(r.Context(), store.UserID(r), mux.Vars(r)["id"])
	if err != nil {
		common.ReplyErr(w, err.Error(), 404)
		return
	}
	common.ReplyOK(w, row)
}
func UpdateWord(w http.ResponseWriter, r *http.Request) {
	var in WordUpdate
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	row, err := service().UpdateWord(r.Context(), store.UserID(r), mux.Vars(r)["id"], in)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, row)
}
func ResumeWord(w http.ResponseWriter, r *http.Request) {
	if err := service().ResumeWord(r.Context(), store.UserID(r), mux.Vars(r)["id"]); err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, map[string]bool{"resumed": true})
}
func ResetWord(w http.ResponseWriter, r *http.Request) {
	if err := service().ResetWord(r.Context(), store.UserID(r), mux.Vars(r)["id"]); err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, map[string]bool{"reset": true})
}
func ResetWords(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Scope      string `json:"scope"`
		WordbookID string `json:"wordbook_id"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	count, err := service().ResetWords(r.Context(), store.UserID(r), in.Scope, in.WordbookID)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, map[string]int64{"reset": count})
}
func ResolveSelection(w http.ResponseWriter, r *http.Request) {
	var in SelectionResolveRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	result, err := service().ResolveSelection(r.Context(), store.UserID(r), in)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, result)
}
func RemoveDocumentSource(w http.ResponseWriter, r *http.Request) {
	if err := service().RemoveDocumentSource(r.Context(), store.UserID(r), mux.Vars(r)["document_id"], mux.Vars(r)["word_id"]); err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, map[string]bool{"removed": true})
}
func DeleteDocumentWord(w http.ResponseWriter, r *http.Request) {
	if err := service().DeleteDocumentWord(r.Context(), store.UserID(r), mux.Vars(r)["document_id"], mux.Vars(r)["word_id"]); err != nil {
		common.ReplyErr(w, err.Error(), 409)
		return
	}
	common.ReplyOK(w, map[string]bool{"deleted": true})
}
func DictionaryLookup(w http.ResponseWriter, r *http.Request) {
	rows, err := service().LookupDictionary(r.Context(), r.URL.Query().Get("language"), r.URL.Query().Get("term"))
	if err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, map[string]any{"items": rows})
}
func ReviewStatsHandler(w http.ResponseWriter, r *http.Request) {
	var row ReviewStats
	var err error
	if r.URL.Query().Get("provider") == ProviderAnki {
		row, err = service().AnkiStats(r.Context(), store.UserID(r))
	} else {
		row, err = service().Stats(r.Context(), store.UserID(r))
	}
	if err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, row)
}
func GetFSRSProfile(w http.ResponseWriter, r *http.Request) {
	row, err := service().ActiveProfile(r.Context(), store.UserID(r))
	if err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, row)
}
func PutFSRSProfile(w http.ResponseWriter, r *http.Request) {
	var in FSRSProfile
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	row, err := service().SaveProfile(r.Context(), store.UserID(r), in)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, row)
}
func ExportReviewLogs(w http.ResponseWriter, r *http.Request) {
	var rows []ReviewLogExport
	var err error
	if r.URL.Query().Get("provider") == ProviderAnki {
		rows, err = service().ExportAnkiReviewLogs(r.Context(), store.UserID(r))
	} else {
		rows, err = service().ExportReviewLogs(r.Context(), store.UserID(r))
	}
	if err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, map[string]any{"items": rows, "scheduler_version": "go-fsrs/v3.3.1"})
}
func ReviewHistory(w http.ResponseWriter, r *http.Request) {
	rows, err := service().ReviewHistory(r.Context(), store.UserID(r), mux.Vars(r)["id"])
	if err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, map[string]any{"items": rows})
}
func AddExample(w http.ResponseWriter, r *http.Request) {
	var in ExampleInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	row, err := service().AddExample(r.Context(), store.UserID(r), mux.Vars(r)["id"], in)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, row)
}
func UpdateExample(w http.ResponseWriter, r *http.Request) {
	var in ExampleInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	row, err := service().UpdateExample(r.Context(), store.UserID(r), mux.Vars(r)["id"], in)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, row)
}
func DeleteExample(w http.ResponseWriter, r *http.Request) {
	if err := service().DeleteExample(r.Context(), store.UserID(r), mux.Vars(r)["id"]); err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, map[string]bool{"deleted": true})
}
func AddWordTags(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tags []string `json:"tags"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if err := service().setTags(store.DB().WithContext(r.Context()), store.UserID(r), mux.Vars(r)["id"], in.Tags, false); err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, map[string]bool{"updated": true})
}
func RemoveWordTag(w http.ResponseWriter, r *http.Request) {
	if err := service().RemoveWordTag(r.Context(), store.UserID(r), mux.Vars(r)["id"], mux.Vars(r)["tag"]); err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	common.ReplyOK(w, map[string]bool{"removed": true})
}
func MasterWord(w http.ResponseWriter, r *http.Request) {
	if err := service().Master(r.Context(), store.UserID(r), mux.Vars(r)["word_id"]); err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, map[string]bool{"mastered": true})
}
func DeleteVocabularyWord(w http.ResponseWriter, r *http.Request) {
	if err := service().DeleteWord(r.Context(), store.UserID(r), mux.Vars(r)["word_id"]); err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, map[string]bool{"deleted": true})
}
