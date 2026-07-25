package episode

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/store"
)

func TestInternalCreateRequiresConfiguredMatchingToken(t *testing.T) {
	repo := newSQLiteRepository(t)
	store.Init(repo.db, repo.db, nil)
	body := map[string]any{
		"user_id":           "user-1",
		"conversation_id":   "conversation-1",
		"source_kind":       SourceKindChatExplicit,
		"episode_type":      EpisodeTypeDecision,
		"summary":           "Use Core as the Episode authority",
		"search_text":       "core episode authority",
		"tokenizer_version": "jieba-v1",
		"occurred_at_ms":    1_721_800_000_000,
	}

	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "")
	assertInternalCreateStatus(t, body, "any-token", http.StatusUnauthorized)

	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")
	assertInternalCreateStatus(t, body, "", http.StatusUnauthorized)
	assertInternalCreateStatus(t, body, "wrong-secret", http.StatusUnauthorized)

	recorder := assertInternalCreateStatus(t, body, "internal-secret", http.StatusOK)
	var response struct {
		Code int `json:"code"`
		Data struct {
			Status string `json:"status"`
			ID     string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != 0 ||
		response.Data.Status != CreateStatusCreated ||
		response.Data.ID == "" {
		t.Fatalf("unexpected create response: %#v", response)
	}
}

func TestInternalDeleteIsTenantScopedAndIdempotent(t *testing.T) {
	repo := newSQLiteRepository(t)
	store.Init(repo.db, repo.db, nil)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")
	created := mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-1",
		ConversationID:   "conversation-1",
		SourceKind:       SourceKindMemoryReview,
		EpisodeType:      EpisodeTypeDecision,
		Summary:          "Use training content as the fitness record",
		SearchText:       "training content fitness record",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     1_721_800_000_000,
	})

	performDelete := func(userID, token string) (int, DeleteResult) {
		request := httptest.NewRequest(
			http.MethodDelete,
			"/internal/memory/episodes/"+created.ID+"?user_id="+userID,
			nil,
		)
		request.Header.Set("X-LazyMind-Internal-Token", token)
		request = mux.SetURLVars(request, map[string]string{"episode_id": created.ID})
		recorder := httptest.NewRecorder()
		InternalDelete(recorder, request)
		var response struct {
			Data DeleteResult `json:"data"`
		}
		if recorder.Code == http.StatusOK {
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode delete response: %v", err)
			}
		}
		return recorder.Code, response.Data
	}

	if status, _ := performDelete("user-1", "wrong-secret"); status != http.StatusUnauthorized {
		t.Fatalf("wrong-token delete status = %d, want %d", status, http.StatusUnauthorized)
	}
	if status, result := performDelete("user-2", "internal-secret"); status != http.StatusOK ||
		result.Status != DeleteStatusNotFound ||
		result.ID != created.ID {
		t.Fatalf("cross-tenant delete = status %d result %#v", status, result)
	}
	if _, err := repo.Get(t.Context(), "user-1", created.ID); err != nil {
		t.Fatalf("cross-tenant delete removed owner episode: %v", err)
	}
	if status, result := performDelete("user-1", "internal-secret"); status != http.StatusOK ||
		result.Status != DeleteStatusDeleted ||
		result.ID != created.ID {
		t.Fatalf("owner delete = status %d result %#v", status, result)
	}
	if status, result := performDelete("user-1", "internal-secret"); status != http.StatusOK ||
		result.Status != DeleteStatusNotFound ||
		result.ID != created.ID {
		t.Fatalf("idempotent delete = status %d result %#v", status, result)
	}
}

func TestInternalDeleteValidatesRequiredContext(t *testing.T) {
	repo := newSQLiteRepository(t)
	store.Init(repo.db, repo.db, nil)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")

	for _, testCase := range []struct {
		name      string
		userID    string
		episodeID string
	}{
		{name: "missing user", episodeID: "ep_1"},
		{name: "missing episode", userID: "user-1"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodDelete,
				"/internal/memory/episodes/"+testCase.episodeID+"?user_id="+testCase.userID,
				nil,
			)
			request.Header.Set("X-LazyMind-Internal-Token", "internal-secret")
			request = mux.SetURLVars(
				request,
				map[string]string{"episode_id": testCase.episodeID},
			)
			recorder := httptest.NewRecorder()
			InternalDelete(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					recorder.Code,
					http.StatusBadRequest,
					recorder.Body.String(),
				)
			}
		})
	}
}

func TestValidateInternalTokenConfigFailsClosed(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "")
	if err := ValidateInternalTokenConfig(); err == nil {
		t.Fatal("empty internal token config unexpectedly accepted")
	}
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "   ")
	if err := ValidateInternalTokenConfig(); err == nil {
		t.Fatal("blank internal token config unexpectedly accepted")
	}
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")
	if err := ValidateInternalTokenConfig(); err != nil {
		t.Fatalf("configured internal token rejected: %v", err)
	}
}

func TestInternalSearchCandidatesWireContract(t *testing.T) {
	repo := newSQLiteRepository(t)
	store.Init(repo.db, repo.db, nil)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")
	created := mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-1",
		ConversationID:   "conversation-1",
		SourceKind:       SourceKindMemoryReview,
		EpisodeType:      EpisodeTypeResult,
		Summary:          "Episode search moved into Core",
		SearchText:       "episode search core",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     1_721_800_000_000,
	})

	recorder := performJSONHandler(
		t,
		InternalSearchCandidates,
		http.MethodPost,
		"/internal/memory/episodes:searchCandidates",
		map[string]any{
			"user_id": "user-1",
			"terms":   []string{"episode", "core"},
			"limit":   20,
		},
		"internal-secret",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Code int `json:"code"`
		Data struct {
			Items []SearchCandidate `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	if response.Code != 0 ||
		len(response.Data.Items) != 1 ||
		response.Data.Items[0].Episode.ID != created.ID ||
		response.Data.Items[0].LexicalScore <= 0 {
		t.Fatalf("unexpected search response: %#v", response)
	}
}

func TestInternalListByConversationWireContract(t *testing.T) {
	repo := newSQLiteRepository(t)
	store.Init(repo.db, repo.db, nil)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")
	var recordedAt int64 = 1000
	repo.clockMS = func() int64 {
		recordedAt += 100
		return recordedAt
	}
	first := mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-1",
		ConversationID:   "conversation-1",
		SourceKind:       SourceKindChatExplicit,
		EpisodeType:      EpisodeTypeProgress,
		Summary:          "First episode",
		SearchText:       "first episode",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     1000,
	})
	second := mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-1",
		ConversationID:   "conversation-1",
		SourceKind:       SourceKindMemoryReview,
		EpisodeType:      EpisodeTypeResult,
		Summary:          "Second episode",
		SearchText:       "second episode",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     2000,
	})
	mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-1",
		ConversationID:   "conversation-2",
		SourceKind:       SourceKindMemoryReview,
		EpisodeType:      EpisodeTypeEvent,
		Summary:          "Other conversation",
		SearchText:       "other conversation",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     3000,
	})

	request := httptest.NewRequest(
		http.MethodGet,
		"/internal/memory/episodes?user_id=user-1&conversation_id=conversation-1",
		nil,
	)
	request.Header.Set("X-LazyMind-Internal-Token", "internal-secret")
	recorder := httptest.NewRecorder()
	InternalListByConversation(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Items []Episode `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(response.Data.Items) != 2 ||
		response.Data.Items[0].ID != first.ID ||
		response.Data.Items[1].ID != second.ID {
		t.Fatalf("conversation items = %#v", response.Data.Items)
	}
}

func TestInternalListRecentWireContract(t *testing.T) {
	repo := newSQLiteRepository(t)
	store.Init(repo.db, repo.db, nil)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")
	older := mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-1",
		ConversationID:   "conversation-1",
		SourceKind:       SourceKindMemoryReview,
		EpisodeType:      EpisodeTypeProgress,
		Summary:          "Older progress",
		SearchText:       "older progress",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     1_000,
	})
	newer := mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-1",
		ConversationID:   "conversation-2",
		SourceKind:       SourceKindChatExplicit,
		EpisodeType:      EpisodeTypeProgress,
		Summary:          "Newer progress",
		SearchText:       "newer progress",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     2_000,
	})
	mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-1",
		ConversationID:   "conversation-3",
		SourceKind:       SourceKindMemoryReview,
		EpisodeType:      EpisodeTypeResult,
		Summary:          "Newest result",
		SearchText:       "newest result",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     3_000,
	})

	recorder := performJSONHandler(
		t,
		InternalListRecent,
		http.MethodPost,
		"/internal/memory/episodes:listRecent",
		map[string]any{
			"user_id":      "user-1",
			"episode_type": EpisodeTypeProgress,
			"limit":        2,
		},
		"internal-secret",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Code int `json:"code"`
		Data struct {
			Items []Episode `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode recent list response: %v", err)
	}
	if response.Code != 0 ||
		len(response.Data.Items) != 2 ||
		response.Data.Items[0].ID != newer.ID ||
		response.Data.Items[1].ID != older.ID {
		t.Fatalf("unexpected recent list response: %#v", response)
	}
}

func TestInternalListRecentValidatesTokenAndRequest(t *testing.T) {
	repo := newSQLiteRepository(t)
	store.Init(repo.db, repo.db, nil)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")
	validBody := map[string]any{
		"user_id":      "user-1",
		"episode_type": EpisodeTypeProgress,
		"limit":        3,
	}
	if recorder := performJSONHandler(
		t,
		InternalListRecent,
		http.MethodPost,
		"/internal/memory/episodes:listRecent",
		validBody,
		"wrong-secret",
	); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("token validation status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	for _, body := range []map[string]any{
		{"episode_type": EpisodeTypeProgress, "limit": 3},
		{"user_id": "user-1", "episode_type": "unknown", "limit": 3},
		{"user_id": "user-1", "episode_type": EpisodeTypeProgress, "limit": 101},
	} {
		recorder := performJSONHandler(
			t,
			InternalListRecent,
			http.MethodPost,
			"/internal/memory/episodes:listRecent",
			body,
			"internal-secret",
		)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %#v status = %d, want %d", body, recorder.Code, http.StatusBadRequest)
		}
	}
}

func TestInternalRecordHitsWireContract(t *testing.T) {
	repo := newSQLiteRepository(t)
	store.Init(repo.db, repo.db, nil)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")
	created := mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-1",
		ConversationID:   "conversation-1",
		SourceKind:       SourceKindChatExplicit,
		EpisodeType:      EpisodeTypeDecision,
		Summary:          "Record one hit",
		SearchText:       "record hit",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     1000,
	})

	recorder := performJSONHandler(
		t,
		InternalRecordHits,
		http.MethodPost,
		"/internal/memory/episodes:recordHits",
		map[string]any{
			"user_id":     "user-1",
			"episode_ids": []string{created.ID, created.ID, "missing"},
		},
		"internal-secret",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Results map[string]bool `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode hits response: %v", err)
	}
	if !response.Data.Results[created.ID] || response.Data.Results["missing"] {
		t.Fatalf("record hit results = %#v", response.Data.Results)
	}
	record, err := repo.Get(t.Context(), "user-1", created.ID)
	if err != nil {
		t.Fatalf("get hit episode: %v", err)
	}
	if record.HitCount != 1 {
		t.Fatalf("hit_count = %d, want 1", record.HitCount)
	}
}

func TestPublicEpisodeHandlersAreGatewayUserScoped(t *testing.T) {
	repo := newSQLiteRepository(t)
	store.Init(repo.db, repo.db, nil)
	var recordedAt int64 = 1000
	repo.clockMS = func() int64 {
		recordedAt += 100
		return recordedAt
	}
	var userOneIDs []string
	for index, summary := range []string{"first", "second", "third"} {
		created := mustCreateEpisode(t, repo, CreateInput{
			UserID:           "user-1",
			ConversationID:   "conversation-1",
			SourceKind:       SourceKindChatExplicit,
			EpisodeType:      EpisodeTypeEvent,
			Summary:          summary,
			SearchText:       summary,
			TokenizerVersion: "jieba-v1",
			OccurredAtMS:     int64(index + 1),
		})
		userOneIDs = append(userOneIDs, created.ID)
	}
	mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-2",
		ConversationID:   "conversation-2",
		SourceKind:       SourceKindMemoryReview,
		EpisodeType:      EpisodeTypeResult,
		Summary:          "other tenant",
		SearchText:       "other tenant",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     10,
	})

	listRequest := httptest.NewRequest(http.MethodGet, "/memory/episodes?page_size=2", nil)
	listRequest.Header.Set("X-User-Id", "user-1")
	listRecorder := httptest.NewRecorder()
	ListEpisodes(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var listResponse struct {
		Data PublicPage `json:"data"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listResponse); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listResponse.Data.TotalSize != 3 ||
		len(listResponse.Data.Items) != 2 ||
		listResponse.Data.Items[0].Summary != "third" ||
		listResponse.Data.NextPageToken == "" {
		t.Fatalf("unexpected list response: %#v", listResponse.Data)
	}
	if bytes.Contains(listRecorder.Body.Bytes(), []byte(`"user_id"`)) {
		t.Fatalf("public list leaked user_id: %s", listRecorder.Body.String())
	}

	ownerGet := httptest.NewRequest(http.MethodGet, "/memory/episodes/"+userOneIDs[0], nil)
	ownerGet.Header.Set("X-User-Id", "user-1")
	ownerGet = mux.SetURLVars(ownerGet, map[string]string{"episode_id": userOneIDs[0]})
	ownerGetRecorder := httptest.NewRecorder()
	GetEpisode(ownerGetRecorder, ownerGet)
	if ownerGetRecorder.Code != http.StatusOK {
		t.Fatalf("owner get status = %d; body=%s", ownerGetRecorder.Code, ownerGetRecorder.Body.String())
	}
	if bytes.Contains(ownerGetRecorder.Body.Bytes(), []byte(`"user_id"`)) {
		t.Fatalf("public detail leaked user_id: %s", ownerGetRecorder.Body.String())
	}

	crossTenantGet := httptest.NewRequest(http.MethodGet, "/memory/episodes/"+userOneIDs[0], nil)
	crossTenantGet.Header.Set("X-User-Id", "user-2")
	crossTenantGet = mux.SetURLVars(crossTenantGet, map[string]string{"episode_id": userOneIDs[0]})
	crossTenantGetRecorder := httptest.NewRecorder()
	GetEpisode(crossTenantGetRecorder, crossTenantGet)
	if crossTenantGetRecorder.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant get status = %d", crossTenantGetRecorder.Code)
	}

	crossTenantDelete := httptest.NewRequest(http.MethodDelete, "/memory/episodes/"+userOneIDs[0], nil)
	crossTenantDelete.Header.Set("X-User-Id", "user-2")
	crossTenantDelete = mux.SetURLVars(crossTenantDelete, map[string]string{"episode_id": userOneIDs[0]})
	crossTenantDeleteRecorder := httptest.NewRecorder()
	DeleteEpisode(crossTenantDeleteRecorder, crossTenantDelete)
	if crossTenantDeleteRecorder.Code != http.StatusNoContent ||
		crossTenantDeleteRecorder.Body.Len() != 0 {
		t.Fatalf("cross-tenant delete status = %d", crossTenantDeleteRecorder.Code)
	}

	ownerDelete := httptest.NewRequest(http.MethodDelete, "/memory/episodes/"+userOneIDs[0], nil)
	ownerDelete.Header.Set("X-User-Id", "user-1")
	ownerDelete = mux.SetURLVars(ownerDelete, map[string]string{"episode_id": userOneIDs[0]})
	ownerDeleteRecorder := httptest.NewRecorder()
	DeleteEpisode(ownerDeleteRecorder, ownerDelete)
	if ownerDeleteRecorder.Code != http.StatusNoContent ||
		ownerDeleteRecorder.Body.Len() != 0 {
		t.Fatalf("owner delete status = %d; body=%s", ownerDeleteRecorder.Code, ownerDeleteRecorder.Body.String())
	}
}

func TestPublicEpisodeHandlersRequireGatewayIdentity(t *testing.T) {
	repo := newSQLiteRepository(t)
	store.Init(repo.db, repo.db, nil)
	for _, testCase := range []struct {
		name    string
		method  string
		path    string
		handler http.HandlerFunc
	}{
		{"list", http.MethodGet, "/memory/episodes", ListEpisodes},
		{"detail", http.MethodGet, "/memory/episodes/ep_missing", GetEpisode},
		{"delete", http.MethodDelete, "/memory/episodes/ep_missing", DeleteEpisode},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(testCase.method, testCase.path, nil)
			request = mux.SetURLVars(request, map[string]string{"episode_id": "ep_missing"})
			recorder := httptest.NewRecorder()
			testCase.handler(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func performJSONHandler(
	t *testing.T,
	handler http.HandlerFunc,
	method string,
	path string,
	body any,
	token string,
) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if token != "" {
		request.Header.Set("X-LazyMind-Internal-Token", token)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func assertInternalCreateStatus(
	t *testing.T,
	body map[string]any,
	token string,
	wantStatus int,
) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/internal/memory/episodes", bytes.NewReader(raw))
	if token != "" {
		request.Header.Set("X-LazyMind-Internal-Token", token)
	}
	recorder := httptest.NewRecorder()
	InternalCreate(recorder, request)
	if recorder.Code != wantStatus {
		t.Fatalf(
			"token %q status = %d, want %d; body=%s",
			token,
			recorder.Code,
			wantStatus,
			recorder.Body.String(),
		)
	}
	return recorder
}
