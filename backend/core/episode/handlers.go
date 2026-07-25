package episode

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"lazymind/core/common"
	"lazymind/core/store"
)

const internalTokenHeader = "X-LazyMind-Internal-Token"
const internalTokenEnv = "LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"

func InternalCreate(w http.ResponseWriter, r *http.Request) {
	if !requireInternalToken(w, r) {
		return
	}
	var input CreateInput
	if err := decodeJSONBody(r, &input); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := prepareCreateInput(input); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	repo, err := repository()
	if err != nil {
		common.ReplyErr(w, "episode repository unavailable", http.StatusInternalServerError)
		return
	}
	result, err := repo.Create(r.Context(), input)
	if err != nil {
		common.ReplyErr(w, "create episode failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, result)
}

func InternalDelete(w http.ResponseWriter, r *http.Request) {
	if !requireInternalToken(w, r) {
		return
	}
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	episodeID := strings.TrimSpace(common.PathVar(r, "episode_id"))
	if userID == "" {
		common.ReplyErr(w, "user_id is required", http.StatusBadRequest)
		return
	}
	if episodeID == "" {
		common.ReplyErr(w, "episode_id is required", http.StatusBadRequest)
		return
	}
	repo, err := repository()
	if err != nil {
		common.ReplyErr(w, "episode repository unavailable", http.StatusInternalServerError)
		return
	}
	status := DeleteStatusDeleted
	if err := repo.Delete(r.Context(), userID, episodeID); err != nil {
		if !errors.Is(err, ErrNotFound) {
			common.ReplyErr(w, "delete episode failed", http.StatusInternalServerError)
			return
		}
		status = DeleteStatusNotFound
	}
	common.ReplyOK(w, DeleteResult{
		Status: status,
		ID:     episodeID,
	})
}

func InternalSearchCandidates(w http.ResponseWriter, r *http.Request) {
	if !requireInternalToken(w, r) {
		return
	}
	var request struct {
		UserID string   `json:"user_id"`
		Terms  []string `json:"terms"`
		Limit  int      `json:"limit"`
	}
	if err := decodeJSONBody(r, &request); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	request.UserID = strings.TrimSpace(request.UserID)
	if request.UserID == "" {
		common.ReplyErr(w, "user_id is required", http.StatusBadRequest)
		return
	}
	if len(request.Terms) == 0 {
		common.ReplyErr(w, "terms must be a non-empty array", http.StatusBadRequest)
		return
	}
	if request.Limit == 0 {
		request.Limit = DefaultCandidateLimit
	}
	if request.Limit < 1 || request.Limit > MaxCandidateLimit {
		common.ReplyErr(w, "limit must be between 1 and 100", http.StatusBadRequest)
		return
	}
	repo, err := repository()
	if err != nil {
		common.ReplyErr(w, "episode repository unavailable", http.StatusInternalServerError)
		return
	}
	items, err := repo.SearchCandidates(r.Context(), request.UserID, request.Terms, request.Limit)
	if err != nil {
		common.ReplyErr(w, "search episode candidates failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"items": items})
}

func InternalListByConversation(w http.ResponseWriter, r *http.Request) {
	if !requireInternalToken(w, r) {
		return
	}
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversation_id"))
	if userID == "" {
		common.ReplyErr(w, "user_id is required", http.StatusBadRequest)
		return
	}
	if conversationID == "" {
		common.ReplyErr(w, "conversation_id is required", http.StatusBadRequest)
		return
	}
	repo, err := repository()
	if err != nil {
		common.ReplyErr(w, "episode repository unavailable", http.StatusInternalServerError)
		return
	}
	items, err := repo.ListByConversation(r.Context(), userID, conversationID)
	if err != nil {
		common.ReplyErr(w, "list conversation episodes failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"items": items})
}

func InternalListRecent(w http.ResponseWriter, r *http.Request) {
	if !requireInternalToken(w, r) {
		return
	}
	var request struct {
		UserID      string `json:"user_id"`
		EpisodeType string `json:"episode_type"`
		Limit       int    `json:"limit"`
	}
	if err := decodeJSONBody(r, &request); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	request.UserID = strings.TrimSpace(request.UserID)
	request.EpisodeType = strings.TrimSpace(request.EpisodeType)
	if request.UserID == "" {
		common.ReplyErr(w, "user_id is required", http.StatusBadRequest)
		return
	}
	if !validEpisodeType(request.EpisodeType) {
		common.ReplyErr(w, "episode_type is invalid", http.StatusBadRequest)
		return
	}
	if request.Limit == 0 {
		request.Limit = DefaultRecentLimit
	}
	if request.Limit < 1 || request.Limit > MaxRecentLimit {
		common.ReplyErr(
			w,
			fmt.Sprintf("limit must be between 1 and %d", MaxRecentLimit),
			http.StatusBadRequest,
		)
		return
	}
	repo, err := repository()
	if err != nil {
		common.ReplyErr(w, "episode repository unavailable", http.StatusInternalServerError)
		return
	}
	items, err := repo.ListRecent(
		r.Context(),
		request.UserID,
		request.EpisodeType,
		request.Limit,
	)
	if err != nil {
		common.ReplyErr(w, "list recent episodes failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"items": items})
}

func InternalRecordHits(w http.ResponseWriter, r *http.Request) {
	if !requireInternalToken(w, r) {
		return
	}
	var request struct {
		UserID     string   `json:"user_id"`
		EpisodeIDs []string `json:"episode_ids"`
	}
	if err := decodeJSONBody(r, &request); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	request.UserID = strings.TrimSpace(request.UserID)
	if request.UserID == "" {
		common.ReplyErr(w, "user_id is required", http.StatusBadRequest)
		return
	}
	if len(request.EpisodeIDs) == 0 {
		common.ReplyErr(w, "episode_ids must be a non-empty array", http.StatusBadRequest)
		return
	}
	repo, err := repository()
	if err != nil {
		common.ReplyErr(w, "episode repository unavailable", http.StatusInternalServerError)
		return
	}
	results, err := repo.RecordHits(r.Context(), request.UserID, request.EpisodeIDs)
	if err != nil {
		common.ReplyErr(w, "record episode hits failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"results": results})
}

func ListEpisodes(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(common.UserID(r))
	if userID == "" {
		common.ReplyAppErr(
			w,
			common.NewAppError(http.StatusUnauthorized, common.ErrCodeUnauthorized, "X-User-Id is required"),
		)
		return
	}
	pageSize := DefaultPageSize
	if rawPageSize := strings.TrimSpace(r.URL.Query().Get("page_size")); rawPageSize != "" {
		parsed, err := strconv.Atoi(rawPageSize)
		if err != nil || parsed < 1 || parsed > MaxPageSize {
			common.ReplyErr(w, "page_size must be between 1 and 100", http.StatusBadRequest)
			return
		}
		pageSize = parsed
	}
	pageToken := strings.TrimSpace(r.URL.Query().Get("page_token"))
	repo, err := repository()
	if err != nil {
		common.ReplyErr(w, "episode repository unavailable", http.StatusInternalServerError)
		return
	}
	page, err := repo.List(r.Context(), userID, pageSize, pageToken)
	if err != nil {
		if strings.Contains(err.Error(), "page_token") {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		common.ReplyErr(w, "list episodes failed", http.StatusInternalServerError)
		return
	}
	publicItems := make([]PublicEpisode, 0, len(page.Items))
	for _, item := range page.Items {
		publicItems = append(publicItems, publicEpisode(item))
	}
	common.ReplyOK(w, PublicPage{
		Items:         publicItems,
		TotalSize:     page.TotalSize,
		NextPageToken: page.NextPageToken,
	})
}

func GetEpisode(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(common.UserID(r))
	if userID == "" {
		common.ReplyAppErr(
			w,
			common.NewAppError(http.StatusUnauthorized, common.ErrCodeUnauthorized, "X-User-Id is required"),
		)
		return
	}
	episodeID := strings.TrimSpace(common.PathVar(r, "episode_id"))
	if episodeID == "" {
		common.ReplyErr(w, "episode_id is required", http.StatusBadRequest)
		return
	}
	repo, err := repository()
	if err != nil {
		common.ReplyErr(w, "episode repository unavailable", http.StatusInternalServerError)
		return
	}
	item, err := repo.Get(r.Context(), userID, episodeID)
	if errors.Is(err, ErrNotFound) {
		common.ReplyErr(w, "episode not found", http.StatusNotFound)
		return
	}
	if err != nil {
		common.ReplyErr(w, "get episode failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, publicEpisode(item))
}

func DeleteEpisode(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(common.UserID(r))
	if userID == "" {
		common.ReplyAppErr(
			w,
			common.NewAppError(http.StatusUnauthorized, common.ErrCodeUnauthorized, "X-User-Id is required"),
		)
		return
	}
	episodeID := strings.TrimSpace(common.PathVar(r, "episode_id"))
	if episodeID == "" {
		common.ReplyErr(w, "episode_id is required", http.StatusBadRequest)
		return
	}
	repo, err := repository()
	if err != nil {
		common.ReplyErr(w, "episode repository unavailable", http.StatusInternalServerError)
		return
	}
	err = repo.Delete(r.Context(), userID, episodeID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		common.ReplyErr(w, "delete episode failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func publicEpisode(item Episode) PublicEpisode {
	return PublicEpisode{
		ID:             item.ID,
		ConversationID: item.ConversationID,
		SourceKind:     item.SourceKind,
		EpisodeType:    item.EpisodeType,
		Summary:        item.Summary,
		OccurredAtMS:   item.OccurredAtMS,
		RecordedAtMS:   item.RecordedAtMS,
		HitCount:       item.HitCount,
	}
}

func requireInternalToken(w http.ResponseWriter, r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv(internalTokenEnv))
	provided := strings.TrimSpace(r.Header.Get(internalTokenHeader))
	if expected == "" ||
		provided == "" ||
		subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		common.ReplyErr(w, "internal token required", http.StatusUnauthorized)
		return false
	}
	return true
}

func ValidateInternalTokenConfig() error {
	if strings.TrimSpace(os.Getenv(internalTokenEnv)) == "" {
		return fmt.Errorf("%s must be configured for Core internal APIs", internalTokenEnv)
	}
	return nil
}

func repository() (*Repository, error) {
	return NewRepository(store.DB())
}

func decodeJSONBody(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid request body")
	}
	return nil
}
