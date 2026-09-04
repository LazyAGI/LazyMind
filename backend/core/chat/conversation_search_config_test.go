package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
)

// TestUniqueNonEmptyStrings_NoDuplicates passes through unique strings unchanged.
func TestUniqueNonEmptyStrings_NoDuplicates(t *testing.T) {
	got := uniqueNonEmptyStrings([]string{"a", "b", "c"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestUniqueNonEmptyStrings_Dedup removes duplicate entries, preserving first occurrence order.
func TestUniqueNonEmptyStrings_Dedup(t *testing.T) {
	got := uniqueNonEmptyStrings([]string{"a", "b", "a", "c", "b"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestUniqueNonEmptyStrings_EmptyAndWhitespace filters out empty and whitespace-only values.
func TestUniqueNonEmptyStrings_EmptyAndWhitespace(t *testing.T) {
	got := uniqueNonEmptyStrings([]string{"", "  ", "x", "\t", "y"})
	want := []string{"x", "y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestUniqueNonEmptyStrings_AllEmpty returns empty slice for all-empty input.
func TestUniqueNonEmptyStrings_AllEmpty(t *testing.T) {
	got := uniqueNonEmptyStrings([]string{"", "  "})
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

// TestUniqueNonEmptyStrings_NilInput returns empty slice.
func TestUniqueNonEmptyStrings_NilInput(t *testing.T) {
	got := uniqueNonEmptyStrings(nil)
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

// TestUniqueNonEmptyStrings_SingleElement handles a single non-empty value.
func TestUniqueNonEmptyStrings_SingleElement(t *testing.T) {
	got := uniqueNonEmptyStrings([]string{"only"})
	want := []string{"only"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func newConversationSearchConfigTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newPromptTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	return db.DB
}

func seedConversationSearchConfigDataset(t *testing.T, db *gorm.DB, id, ownerID string) {
	t.Helper()
	now := time.Now().UTC()
	dataset := orm.Dataset{
		ID: id, KbID: id, DisplayName: id,
		BaseModel: orm.BaseModel{
			CreateUserID: ownerID, CreateUserName: ownerID, CreatedAt: now, UpdatedAt: now,
		},
	}
	if err := db.Create(&dataset).Error; err != nil {
		t.Fatalf("create dataset %s: %v", id, err)
	}
}

func seedConversationWithSearchConfig(t *testing.T, db *gorm.DB, id, userID string, searchConfig json.RawMessage) {
	t.Helper()
	now := time.Now().UTC()
	conversation := orm.Conversation{
		ID: id, ChannelID: "default", SearchConfig: searchConfig,
		BaseModel: orm.BaseModel{
			CreateUserID: userID, CreateUserName: userID, CreatedAt: now, UpdatedAt: now,
		},
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation %s: %v", id, err)
	}
}

func TestConversationSearchConfigDatasetIDsCollectsEveryRepresentation(t *testing.T) {
	got := conversationSearchConfigDatasetIDs(map[string]any{
		"dataset_ids": []any{"legacy-1", "duplicate", " "},
		"dataset_list": []any{
			map[string]any{"id": "selector-1"},
			map[string]string{"id": "duplicate"},
		},
	})
	want := []string{"legacy-1", "duplicate", "selector-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dataset IDs = %#v, want %#v", got, want)
	}
}

func TestAuthorizeConversationSearchConfigCombinesRequestAndPersistedIDs(t *testing.T) {
	db := newConversationSearchConfigTestDB(t)
	seedConversationSearchConfigDataset(t, db, "request-owned", "user-owner")
	seedConversationSearchConfigDataset(t, db, "persisted-owned", "user-owner")
	seedConversationWithSearchConfig(
		t, db, "conversation-owned", "user-owner",
		json.RawMessage(`{"dataset_list":[{"id":"persisted-owned"}]}`),
	)

	err := authorizeConversationSearchConfig(
		context.Background(), db, "user-owner", "conversation-owned",
		map[string]any{"dataset_ids": []any{"request-owned"}},
	)
	if err != nil {
		t.Fatalf("authorize owner search configs: %v", err)
	}
}

func TestChatConversationsRejectsRevokedNestedAndPersistedKnowledgeBases(t *testing.T) {
	tests := []struct {
		name            string
		conversationID  string
		persistedConfig json.RawMessage
		requestConfig   string
		wantRows        int64
	}{
		{
			name:           "nested request config",
			conversationID: "new-sidechat",
			requestConfig:  `{"dataset_list":[{"id":"revoked-nested"}]}`,
			wantRows:       0,
		},
		{
			name:            "persisted config",
			conversationID:  "saved-sidechat",
			persistedConfig: json.RawMessage(`{"dataset_ids":["revoked-persisted"]}`),
			requestConfig:   `{}`,
			wantRows:        1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newConversationSearchConfigTestDB(t)
			datasetID := "revoked-nested"
			if len(tt.persistedConfig) > 0 {
				datasetID = "revoked-persisted"
				seedConversationWithSearchConfig(t, db, tt.conversationID, "sidechat-user", tt.persistedConfig)
			}
			seedConversationSearchConfigDataset(t, db, datasetID, "former-owner")

			body := `{"conversation_id":"` + tt.conversationID + `","input":[{"input_type":"text","text":"question"}],"conversation":{"search_config":` + tt.requestConfig + `}}`
			request := httptest.NewRequest(http.MethodPost, "/api/v1/conversations:chat", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-User-Id", "sidechat-user")
			recorder := httptest.NewRecorder()

			ChatConversations(recorder, request)

			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusForbidden, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"message":"forbidden"`) {
				t.Fatalf("body = %q, want forbidden error", recorder.Body.String())
			}
			var conversationRows int64
			if err := db.Model(&orm.Conversation{}).Where("id = ?", tt.conversationID).Count(&conversationRows).Error; err != nil {
				t.Fatalf("count conversations: %v", err)
			}
			if conversationRows != tt.wantRows {
				t.Fatalf("conversation rows = %d, want %d", conversationRows, tt.wantRows)
			}
			var historyRows int64
			if err := db.Model(&orm.ChatHistory{}).Where("conversation_id = ?", tt.conversationID).Count(&historyRows).Error; err != nil {
				t.Fatalf("count histories: %v", err)
			}
			if historyRows != 0 {
				t.Fatalf("history rows = %d, want 0", historyRows)
			}
		})
	}
}

func TestAuthorizeKnowledgeBaseIDRejectsMissingDataset(t *testing.T) {
	db := newConversationSearchConfigTestDB(t)
	err := authorizeKnowledgeBaseID(context.Background(), db, "user-1", "missing")
	if !errors.Is(err, errKnowledgeBaseNotReadable) {
		t.Fatalf("error = %v, want %v", err, errKnowledgeBaseNotReadable)
	}
}

func TestNormalizeConversationSearchConfigValues(t *testing.T) {
	values := []string{" first ", "", "first", "second"}
	got, err := normalizeConversationSearchConfigValues(&values, "tags")
	if err != nil {
		t.Fatalf("normalize values: %v", err)
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %#v, want %#v", got, want)
	}

	tooMany := make([]string, maxConversationSearchConfigListItems+1)
	for index := range tooMany {
		tooMany[index] = strings.Repeat("x", index+1)
	}
	if _, err := normalizeConversationSearchConfigValues(&tooMany, "creators"); err == nil {
		t.Fatal("expected too many creators to fail")
	}
	tooLong := []string{strings.Repeat("界", maxConversationSearchConfigValueRunes+1)}
	if _, err := normalizeConversationSearchConfigValues(&tooLong, "tags"); err == nil {
		t.Fatal("expected overlong tag to fail")
	}
}

func patchConversationSearchConfigForTest(t *testing.T, conversationID, userID, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/core/conversations/"+conversationID+":search-config",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-User-Id", userID)
	request = mux.SetURLVars(request, map[string]string{"name": conversationID + ":search-config"})
	recorder := httptest.NewRecorder()
	PatchConversationSearchConfig(recorder, request)
	return recorder
}

func loadConversationSearchConfigForTest(t *testing.T, db *gorm.DB, conversationID string) map[string]any {
	t.Helper()
	var conversation orm.Conversation
	if err := db.Select("search_config").Where("id = ?", conversationID).Take(&conversation).Error; err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	var config map[string]any
	if err := json.Unmarshal(conversation.SearchConfig, &config); err != nil {
		t.Fatalf("decode search config: %v", err)
	}
	return config
}

func TestPatchConversationSearchConfigPersistsAllFilters(t *testing.T) {
	db := newConversationSearchConfigTestDB(t)
	seedConversationSearchConfigDataset(t, db, "kb-new", "user-1")
	seedConversationWithSearchConfig(
		t, db, "conversation-filters", "user-1",
		json.RawMessage(`{"dataset_ids":["kb-old"],"creators":["creator-old"],"tags":["tag-old"],"database_ids":["db-keep"]}`),
	)

	recorder := patchConversationSearchConfigForTest(
		t, "conversation-filters", "user-1",
		`{"dataset_ids":[" kb-new ","kb-new"],"creators":[" creator-1 ","creator-1",""] ,"tags":[" tag-1 ","tag-1","tag-2"]}`,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	config := loadConversationSearchConfigForTest(t, db, "conversation-filters")
	if _, exists := config["dataset_ids"]; exists {
		t.Fatalf("legacy dataset_ids should be removed: %#v", config)
	}
	if got := conversationSearchConfigDatasetIDs(config); !reflect.DeepEqual(got, []string{"kb-new"}) {
		t.Fatalf("dataset IDs = %#v, want [kb-new]", got)
	}
	if got := stringSliceFromAny(config["creators"]); !reflect.DeepEqual(got, []string{"creator-1"}) {
		t.Fatalf("creators = %#v, want [creator-1]", got)
	}
	if got := stringSliceFromAny(config["tags"]); !reflect.DeepEqual(got, []string{"tag-1", "tag-2"}) {
		t.Fatalf("tags = %#v, want [tag-1 tag-2]", got)
	}
	if got := stringSliceFromAny(config["database_ids"]); !reflect.DeepEqual(got, []string{"db-keep"}) {
		t.Fatalf("database_ids = %#v, want [db-keep]", got)
	}
}

func TestPatchConversationSearchConfigPreservesOmittedFields(t *testing.T) {
	db := newConversationSearchConfigTestDB(t)
	seedConversationWithSearchConfig(
		t, db, "conversation-partial", "user-1",
		json.RawMessage(`{"dataset_list":[{"id":"kb-keep"}],"creators":["creator-keep"],"tags":["tag-old"]}`),
	)

	recorder := patchConversationSearchConfigForTest(
		t, "conversation-partial", "user-1", `{"tags":[" tag-new ","tag-new"]}`,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	config := loadConversationSearchConfigForTest(t, db, "conversation-partial")
	if got := conversationSearchConfigDatasetIDs(config); !reflect.DeepEqual(got, []string{"kb-keep"}) {
		t.Fatalf("dataset IDs = %#v, want [kb-keep]", got)
	}
	if got := stringSliceFromAny(config["creators"]); !reflect.DeepEqual(got, []string{"creator-keep"}) {
		t.Fatalf("creators = %#v, want [creator-keep]", got)
	}
	if got := stringSliceFromAny(config["tags"]); !reflect.DeepEqual(got, []string{"tag-new"}) {
		t.Fatalf("tags = %#v, want [tag-new]", got)
	}
}

func TestPatchConversationSearchConfigRejectsSidechatChild(t *testing.T) {
	db := newConversationSearchConfigTestDB(t)
	seedConversationSearchConfigDataset(t, db, "kb-new", "user-1")
	seedConversationWithSearchConfig(
		t, db, "sidechat-child", "user-1",
		json.RawMessage(`{"dataset_list":[{"id":"kb-parent"}]}`),
	)
	parentID := "parent-1"
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "sidechat-child").Updates(map[string]any{
		"parent_conversation_id": parentID,
		"relation_type":          conversationRelationSidechat,
	}).Error; err != nil {
		t.Fatalf("configure sidechat child: %v", err)
	}

	recorder := patchConversationSearchConfigForTest(
		t, "sidechat-child", "user-1", `{"dataset_ids":["kb-new"]}`,
	)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	config := loadConversationSearchConfigForTest(t, db, "sidechat-child")
	if got := conversationSearchConfigDatasetIDs(config); !reflect.DeepEqual(got, []string{"kb-parent"}) {
		t.Fatalf("sidechat knowledge bases changed: %#v", got)
	}
}

func TestEnsureConversationDoesNotInitializeSidechatKnowledgeFromRequest(t *testing.T) {
	db := newConversationSearchConfigTestDB(t)
	seedConversationWithSearchConfig(
		t, db, "sidechat-empty", "user-1", json.RawMessage(`{}`),
	)
	parentID := "parent-1"
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "sidechat-empty").Updates(map[string]any{
		"parent_conversation_id": parentID,
		"relation_type":          conversationRelationSidechat,
	}).Error; err != nil {
		t.Fatalf("configure sidechat child: %v", err)
	}

	conversation, _, err := ensureConversation(
		context.Background(), db, "sidechat-empty", "",
		json.RawMessage(`{"dataset_list":[{"id":"request-kb"}]}`), nil,
		"user-1", "user-1", false, "", nil, nil,
	)
	if err != nil {
		t.Fatalf("ensure sidechat conversation: %v", err)
	}
	if string(conversation.SearchConfig) != `{}` {
		t.Fatalf("returned sidechat search config = %s, want inherited empty config", conversation.SearchConfig)
	}
	config := loadConversationSearchConfigForTest(t, db, "sidechat-empty")
	if got := conversationSearchConfigDatasetIDs(config); len(got) != 0 {
		t.Fatalf("sidechat knowledge bases initialized from request: %#v", got)
	}
}
