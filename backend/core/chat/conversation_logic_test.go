package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/store"
)

func TestBuildChatRequestBodyUsesConversationIDDerivedSessionID(t *testing.T) {
	body := buildChatRequestBody(nil, nil, "conv-1", "", "hello", nil, map[string]any{}, nil, "", 1)
	sessionID, ok := body["session_id"].(string)
	if !ok {
		t.Fatalf("expected session_id string, got %T", body["session_id"])
	}
	if !strings.HasPrefix(sessionID, "conv-1_") {
		t.Fatalf("expected session_id to start with conversation id, got %q", sessionID)
	}
	suffix := strings.TrimPrefix(sessionID, "conv-1_")
	if suffix == "" {
		t.Fatalf("expected timestamp suffix in session_id, got %q", sessionID)
	}
	if _, err := strconv.ParseInt(suffix, 10, 64); err != nil {
		t.Fatalf("expected millisecond timestamp suffix, got %q: %v", suffix, err)
	}
}

func TestBuildChatRequestBodyUsesDatasetListFilters(t *testing.T) {
	body := buildChatRequestBody(nil, nil, "conv-1", "", "hello", nil, map[string]any{
		"conversation": map[string]any{
			"search_config": map[string]any{
				"dataset_list": []any{
					map[string]any{"id": "ds_1"},
					map[string]any{"id": "ds_2"},
				},
				"creators": []any{"user_a"},
				"tags":     []any{"tag_a", "tag_b"},
			},
		},
	}, nil, "", 1)

	filters, ok := body["filters"].(map[string]any)
	if !ok {
		t.Fatalf("expected filters map, got %T", body["filters"])
	}

	kbIDs, ok := filters["kb_id"].([]string)
	if !ok {
		t.Fatalf("expected kb_id []string, got %T", filters["kb_id"])
	}
	if len(kbIDs) != 2 || kbIDs[0] != "ds_1" || kbIDs[1] != "ds_2" {
		t.Fatalf("unexpected kb_id: %#v", kbIDs)
	}

	creators, ok := filters["creator"].([]string)
	if !ok {
		t.Fatalf("expected creator []string, got %T", filters["creator"])
	}
	if len(creators) != 1 || creators[0] != "user_a" {
		t.Fatalf("unexpected creator: %#v", creators)
	}

	tags, ok := filters["tags"].([]string)
	if !ok {
		t.Fatalf("expected tags []string, got %T", filters["tags"])
	}
	if len(tags) != 2 || tags[0] != "tag_a" || tags[1] != "tag_b" {
		t.Fatalf("unexpected tags: %#v", tags)
	}
}

func TestBuildChatRequestBodyLoadsFiltersFromConversationDB(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/chat-filters.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.Conversation{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	now := time.Now()
	searchConfig := json.RawMessage(`{"dataset_list":[{"id":"ds_db_1"},{"id":"ds_db_2"}],"creators":["u1"]}`)
	if err := db.Create(&orm.Conversation{
		ID:           "conv-db",
		DisplayName:  "test",
		ChannelID:    "default",
		SearchConfig: searchConfig,
		BaseModel: orm.BaseModel{
			CreateUserID: "u1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	body := buildChatRequestBody(t.Context(), db.DB, "conv-db", "", "hello", nil, map[string]any{}, nil, "", 2)
	filters, ok := body["filters"].(map[string]any)
	if !ok {
		t.Fatalf("expected filters map from DB search_config, got %T", body["filters"])
	}
	kbIDs, ok := filters["kb_id"].([]string)
	if !ok || len(kbIDs) != 2 || kbIDs[0] != "ds_db_1" || kbIDs[1] != "ds_db_2" {
		t.Fatalf("unexpected kb_id from DB: %#v", filters["kb_id"])
	}
}

func TestBuildChatRequestBodyKeepsExistingFilters(t *testing.T) {
	existing := map[string]any{"kb_id": []string{"manual"}}
	body := buildChatRequestBody(nil, nil, "conv-1", "", "hello", nil, map[string]any{
		"filters": existing,
		"conversation": map[string]any{
			"search_config": map[string]any{
				"dataset_list": []any{map[string]any{"id": "ds_1"}},
			},
		},
	}, nil, "", 1)

	filters, ok := body["filters"].(map[string]any)
	if !ok {
		t.Fatalf("expected filters map, got %T", body["filters"])
	}

	kbIDs, ok := filters["kb_id"].([]string)
	if !ok {
		t.Fatalf("expected kb_id []string, got %T", filters["kb_id"])
	}
	if len(kbIDs) != 1 || kbIDs[0] != "manual" {
		t.Fatalf("expected existing filters to be preserved, got %#v", kbIDs)
	}
}

func TestBuildChatRequestBodyAddsEvolutionContext(t *testing.T) {
	memoryContent := "---\nagent_persona: |-\n 严谨助手\npreferred_name: |-\n 老师\nresponse_style: |-\n 简洁\n---\n\nmemory-content"
	ctx := &evolution.ChatResourceContext{
		DisabledTools:      []string{"bing"},
		AvailableSkills:    []string{"coding/git-workflow"},
		Memory:             memoryContent,
		UserPreference:     "preference-content",
		UsePersonalization: true,
	}
	body := buildChatRequestBody(nil, nil, "conv-1", "session-1", "hello", nil, map[string]any{}, ctx, "user-1", 1)

	if got := body["session_id"]; got != "session-1" {
		t.Fatalf("expected session_id to be preserved, got %#v", got)
	}
	if got := body["user_id"]; got != "user-1" {
		t.Fatalf("expected user_id to be forwarded, got %#v", got)
	}
	if got, ok := body["disabled_tools"].([]string); !ok || len(got) != 1 || got[0] != "bing" {
		t.Fatalf("unexpected disabled_tools: %#v", body["disabled_tools"])
	}
	if got, ok := body["available_skills"].([]string); !ok || len(got) != 1 || got[0] != "coding/git-workflow" {
		t.Fatalf("unexpected available_skills: %#v", body["available_skills"])
	}
	if _, ok := body["skill_fs_url"]; ok {
		t.Fatalf("expected skill_fs_url to be omitted")
	}
	if got := body["memory"]; got != memoryContent {
		t.Fatalf("unexpected memory: %#v", got)
	}
	if got := body["user_preference"]; got != "preference-content" {
		t.Fatalf("unexpected user_preference: %#v", got)
	}
	if got, ok := body["use_memory"].(bool); !ok || !got {
		t.Fatalf("expected use_memory default true, got %#v", body["use_memory"])
	}
	if got, ok := body["reasoning"].(bool); !ok || !got {
		t.Fatalf("expected reasoning default true, got %#v", body["reasoning"])
	}
}

func TestBuildChatRequestBodySkipsMemoryAndPreferenceWhenPersonalizationDisabled(t *testing.T) {
	ctx := &evolution.ChatResourceContext{
		DisabledTools:      []string{},
		AvailableSkills:    []string{"coding/git-workflow"},
		Memory:             "memory-content",
		UserPreference:     "preference-content",
		UsePersonalization: false,
	}
	body := buildChatRequestBody(nil, nil, "conv-1", "session-1", "hello", nil, map[string]any{}, ctx, "", 1)

	if got, ok := body["use_memory"].(bool); !ok || got {
		t.Fatalf("expected use_memory false, got %#v", body["use_memory"])
	}
	if _, ok := body["memory"]; ok {
		t.Fatalf("expected memory to be omitted when personalization is disabled")
	}
	if _, ok := body["user_preference"]; ok {
		t.Fatalf("expected user_preference to be omitted when personalization is disabled")
	}
}

func TestBuildChatRequestBodyPreservesExplicitReasoningFalse(t *testing.T) {
	body := buildChatRequestBody(nil, nil, "conv-1", "", "hello", nil, map[string]any{
		"reasoning": false,
	}, nil, "", 1)

	if got, ok := body["reasoning"].(bool); !ok || got {
		t.Fatalf("expected reasoning false, got %#v", body["reasoning"])
	}
}

func TestBuildChatHistoryExtPreservesMultimodalInput(t *testing.T) {
	ext := buildChatHistoryExt(map[string]any{
		"input": []any{
			map[string]any{"input_type": "text", "text": "记住这个是王牌超"},
			map[string]any{
				"input_type":   "image",
				"uri":          "/var/lib/lazymind/uploads/tmp/users/u1/files/upload_a.jpg",
				"input_base64": "data:image/jpeg;base64,/9j/abc",
			},
		},
	}, "记住这个是王牌超")

	var payload struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(ext, &payload); err != nil {
		t.Fatalf("unmarshal ext: %v", err)
	}
	if len(payload.Input) != 2 {
		t.Fatalf("expected 2 input items, got %#v", payload.Input)
	}
	if got := payload.Input[1]["input_type"]; got != "image" {
		t.Fatalf("expected image item to be preserved, got %#v", got)
	}
	if got := payload.Input[1]["input_base64"]; got != "data:image/jpeg;base64,/9j/abc" {
		t.Fatalf("expected image base64 to be preserved, got %#v", got)
	}
}

func TestGetConversationDetailReturnsStoredMultimodalInput(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/chat-detail.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	ext := buildChatHistoryExt(map[string]any{
		"input": []any{
			map[string]any{"input_type": "text", "text": "记住这个是王牌超"},
			map[string]any{
				"input_type":   "image",
				"uri":          "/var/lib/lazymind/uploads/tmp/users/u1/files/upload_a.jpg",
				"input_base64": "data:image/jpeg;base64,/9j/abc",
			},
		},
	}, "记住这个是王牌超")
	if err := db.Create(&orm.Conversation{
		ID:           "conv-1",
		DisplayName:  "记住这个是王牌超",
		ChannelID:    "default",
		SearchConfig: json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "u1",
			CreateUserName: "User 1",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&orm.ChatHistory{
		ID:             "h_1",
		Seq:            1,
		ConversationID: "conv-1",
		RawContent:     "记住这个是王牌超",
		Content:        "记住这个是王牌超",
		Result:         "好的",
		Ext:            ext,
		TimeMixin:      orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error; err != nil {
		t.Fatalf("create history: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/conversations/conv-1:detail", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"name": "conv-1:detail"})
	rec := httptest.NewRecorder()

	GetConversationDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Conversation struct {
			ConversationID string `json:"conversation_id"`
			DisplayName    string `json:"display_name"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Conversation.ConversationID != "conv-1" {
		t.Fatalf("expected conversation_id conv-1, got %q", resp.Conversation.ConversationID)
	}
	if resp.Conversation.DisplayName != "记住这个是王牌超" {
		t.Fatalf("expected display_name preserved, got %q", resp.Conversation.DisplayName)
	}
}

func TestGetConversationDetailFiltersMissingDatasets(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/chat-detail-datasets.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}, &orm.Dataset{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	deletedAt := now.Add(-time.Hour)
	if err := db.Create([]orm.Dataset{
		{
			ID:          "ds_live",
			KbID:        "ds_live",
			DisplayName: "Live Dataset",
			BaseModel: orm.BaseModel{
				CreateUserID:   "u1",
				CreateUserName: "User 1",
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		},
		{
			ID:          "ds_deleted",
			KbID:        "ds_deleted",
			DisplayName: "Deleted Dataset",
			BaseModel: orm.BaseModel{
				CreateUserID:   "u1",
				CreateUserName: "User 1",
				CreatedAt:      now,
				UpdatedAt:      now,
				DeletedAt:      &deletedAt,
			},
		},
	}).Error; err != nil {
		t.Fatalf("create datasets: %v", err)
	}
	if err := db.Create(&orm.Conversation{
		ID:           "conv-1",
		DisplayName:  "test",
		ChannelID:    "default",
		SearchConfig: json.RawMessage(`{"dataset_list":[{"id":"ds_live"},{"id":"ds_deleted"},{"id":"ds_missing"}],"creators":["u1"],"top_k":3}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "u1",
			CreateUserName: "User 1",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/conversations/conv-1:detail", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"name": "conv-1:detail"})
	rec := httptest.NewRecorder()

	GetConversationDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Conversation struct {
			SearchConfig map[string]any `json:"search_config"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	rawList, ok := resp.Conversation.SearchConfig["dataset_list"].([]any)
	if !ok {
		t.Fatalf("expected dataset_list array, got %T", resp.Conversation.SearchConfig["dataset_list"])
	}
	if len(rawList) != 1 {
		t.Fatalf("expected one existing dataset, got %#v", rawList)
	}
	selector, _ := rawList[0].(map[string]any)
	if selector["id"] != "ds_live" {
		t.Fatalf("expected ds_live to remain, got %#v", rawList)
	}
	if resp.Conversation.SearchConfig["top_k"] != float64(3) {
		t.Fatalf("expected top_k preserved, got %#v", resp.Conversation.SearchConfig["top_k"])
	}
}

func TestGetConversationHistoryReturnsStoredMultimodalInput(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/chat-history.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	ext := buildChatHistoryExt(map[string]any{
		"input": []any{
			map[string]any{"input_type": "text", "text": "记住这个是王牌超"},
			map[string]any{
				"input_type":   "image",
				"uri":          "/var/lib/lazymind/uploads/tmp/users/u1/files/upload_a.jpg",
				"input_base64": "data:image/jpeg;base64,/9j/abc",
			},
		},
	}, "记住这个是王牌超")
	if err := db.Create(&orm.Conversation{
		ID:           "conv-1",
		DisplayName:  "记住这个是王牌超",
		ChannelID:    "default",
		SearchConfig: json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "u1",
			CreateUserName: "User 1",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&orm.ChatHistory{
		ID:             "h_1",
		Seq:            1,
		ConversationID: "conv-1",
		RawContent:     "记住这个是王牌超",
		Content:        "记住这个是王牌超",
		Result:         "好的",
		Ext:            ext,
		TimeMixin:      orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error; err != nil {
		t.Fatalf("create history: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/conversations/conv-1:history", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"name": "conv-1:history"})
	rec := httptest.NewRecorder()

	GetConversationHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ConversationID string `json:"conversation_id"`
		History        []struct {
			Input []map[string]any `json:"input"`
		} `json:"history"`
		TotalSize int `json:"total_size"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ConversationID != "conv-1" {
		t.Fatalf("expected conversation_id conv-1, got %q", resp.ConversationID)
	}
	if resp.TotalSize != 1 {
		t.Fatalf("expected total_size 1, got %d", resp.TotalSize)
	}
	if len(resp.History) != 1 || len(resp.History[0].Input) != 2 {
		t.Fatalf("expected response history input to include 2 items, got %#v", resp.History)
	}
	if got := resp.History[0].Input[1]["input_type"]; got != "image" {
		t.Fatalf("expected image input in history response, got %#v", got)
	}
	if got := resp.History[0].Input[1]["uri"]; got != "/var/lib/lazymind/uploads/tmp/users/u1/files/upload_a.jpg" {
		t.Fatalf("expected image uri in history response, got %#v", got)
	}
}

func TestBuildChatRequestBodyMergesInputURIsIntoFiles(t *testing.T) {
	body := buildChatRequestBody(nil, nil, "conv-1", "sid", "what animal", nil, map[string]any{
		"input": []any{
			map[string]any{"input_type": "text", "text": "hello"},
			map[string]any{"input_type": "image", "uri": "/var/lib/lazymind/uploads/tmp/u1/a.png"},
			map[string]any{"input_type": "file", "uri": "/var/lib/lazymind/uploads/tmp/u1/b.pdf"},
		},
	}, nil, "", 1)

	files, ok := body["files"].(map[string][]string)
	if !ok {
		t.Fatalf("expected files to be map[string][]string, got %#v", body["files"])
	}
	currentFiles := files["1"]
	if len(currentFiles) != 2 {
		t.Fatalf("expected 2 file paths from input, got %#v", currentFiles)
	}
	if currentFiles[0] != "/var/lib/lazymind/uploads/tmp/u1/a.png" || currentFiles[1] != "/var/lib/lazymind/uploads/tmp/u1/b.pdf" {
		t.Fatalf("unexpected files order/content: %#v", currentFiles)
	}
}

func TestBuildChatRequestBodyFilesMergeDedupesAndSkipsHTTP(t *testing.T) {
	body := buildChatRequestBody(nil, nil, "conv-1", "sid", "q", nil, map[string]any{
		"files": []any{"/data/x.jpg"},
		"input": []any{
			map[string]any{"input_type": "image", "uri": "https://cdn.example.com/p.png"},
			map[string]any{"input_type": "image", "uri": "/data/x.jpg"},
			map[string]any{"input_type": "image", "uri": "/data/y.jpeg"},
		},
	}, nil, "", 1)

	files, ok := body["files"].(map[string][]string)
	if !ok {
		t.Fatalf("expected files to be map[string][]string, got %#v", body["files"])
	}
	currentFiles := files["1"]
	if len(currentFiles) != 2 {
		t.Fatalf("expected 2 paths (dedupe + skip https), got %#v", currentFiles)
	}
	if currentFiles[0] != "/data/x.jpg" || currentFiles[1] != "/data/y.jpeg" {
		t.Fatalf("unexpected files: %#v", currentFiles)
	}
}

func TestBuildLazyChatRequestMapsAllFields(t *testing.T) {
	req := buildLazyChatRequest(map[string]any{
		"query":      "hello",
		"session_id": "conv-1",
		"history": []any{
			map[string]any{"role": "user", "content": "q1"},
			map[string]any{"role": "assistant", "content": "a1"},
		},
		"filters": map[string]any{
			"kb_id":   []any{"ds_1"},
			"creator": []any{"u1"},
			"tags":    []any{"t1"},
		},
		"files":            map[string]any{"1": []any{"f1", "f2"}},
		"current_turn_seq": 7,
		"reasoning":        false,
		"databases":        []any{map[string]any{"name": "db1"}},
		"dataset":          "default",
		"local_fs_sources": []any{
			map[string]any{"source_id": "src-1"},
		},
		"disabled_tools": []any{"bing"},
		"available_skills": []any{
			"coding/git-workflow",
		},
		"memory":          "memory-content",
		"user_preference": "preference-content",
		"use_memory":      true,
		"environment_context": map[string]any{
			"time": map[string]any{
				"now":      "2026-05-11T11:48:00.000Z",
				"timezone": "Asia/Shanghai",
			},
		},
		"user_id":         "user-1",
		"conversation_id": "conv-id-1",
		"mode":            "manual",
		"debug":           true,
		"priority":        9,
		"trace":           true,
		"llm_config": map[string]any{
			"llm": map[string]any{"source": "openai", "model": "gpt-4o"},
		},
		"tool_config": map[string]any{
			"bing": "token-1",
		},
		"mcp_config": []any{
			map[string]any{
				"id":        "msp_1",
				"name":      "context7",
				"transport": "sse",
				"url":       "https://mcp.example.com/sse",
			},
		},
		"has_subagents":   true,
		"enable_plugin":   true,
		"enable_subagent": false,
		"plugin_context": map[string]any{
			"session_id": "plugin-session-1",
		},
		"ask_response": map[string]any{
			"ask_id": "ask-1",
		},
	})

	if req.Message.Query != "hello" || req.Conversation.SessionID != "conv-1" {
		t.Fatalf("unexpected base fields: %#v", req)
	}
	if len(req.Message.History) != 2 || req.Message.History[0].Role != "user" || req.Message.History[1].Content != "a1" {
		t.Fatalf("unexpected history: %#v", req.Message.History)
	}
	if req.Retrieval.Filters == nil || len(req.Retrieval.Filters.DatasetIDs) != 1 || req.Retrieval.Filters.DatasetIDs[0] != "ds_1" {
		t.Fatalf("unexpected filters: %#v", req.Retrieval.Filters)
	}
	if len(req.Retrieval.Filters.Creators) != 1 || req.Retrieval.Filters.Creators[0] != "u1" {
		t.Fatalf("unexpected creators: %#v", req.Retrieval.Filters.Creators)
	}
	if len(req.Retrieval.Filters.Tags) != 1 || req.Retrieval.Filters.Tags[0] != "t1" {
		t.Fatalf("unexpected tags: %#v", req.Retrieval.Filters.Tags)
	}
	if len(req.Message.Files) != 1 || len(req.Message.Files["1"]) != 2 || req.Message.Files["1"][0] != "f1" || req.Message.Files["1"][1] != "f2" {
		t.Fatalf("unexpected files: %#v", req.Message.Files)
	}
	if req.Message.CurrentTurnSeq != 7 {
		t.Fatalf("unexpected current_turn_seq: %d", req.Message.CurrentTurnSeq)
	}
	if len(req.Retrieval.Databases) != 1 || req.Retrieval.Dataset != "default" || len(req.Retrieval.LocalFSSources) != 1 {
		t.Fatalf("unexpected retrieval: %#v", req.Retrieval)
	}
	if req.Runtime.Reasoning {
		t.Fatalf("expected reasoning to be false")
	}
	if !req.Runtime.Debug || req.Runtime.Priority == nil || *req.Runtime.Priority != 9 || !req.Runtime.Trace {
		t.Fatalf("unexpected runtime flags: %#v", req.Runtime)
	}
	if len(req.Agent.DisabledTools) != 1 || req.Agent.DisabledTools[0] != "bing" {
		t.Fatalf("unexpected disabled_tools: %#v", req.Agent.DisabledTools)
	}
	if len(req.Agent.AvailableSkills) != 1 || req.Agent.AvailableSkills[0] != "coding/git-workflow" {
		t.Fatalf("unexpected available_skills: %#v", req.Agent.AvailableSkills)
	}
	if !req.Agent.HasSubagents || req.Agent.EnableSubagent == nil || *req.Agent.EnableSubagent {
		t.Fatalf("unexpected agent flags: %#v", req.Agent)
	}
	if req.Personalization.Memory != "memory-content" || req.Personalization.UserPreference != "preference-content" {
		t.Fatalf("unexpected memory context: %+v", req)
	}
	if !req.Personalization.UseMemory {
		t.Fatalf("expected use_memory to be true")
	}
	timeContext, _ := req.Runtime.EnvironmentContext["time"].(map[string]any)
	if timeContext["now"] != "2026-05-11T11:48:00.000Z" || timeContext["timezone"] != "Asia/Shanghai" {
		t.Fatalf("unexpected environment_context: %#v", req.Runtime.EnvironmentContext)
	}
	if req.Conversation.UserID != "user-1" || req.Conversation.ConversationID != "conv-id-1" || req.Conversation.Mode != "manual" {
		t.Fatalf("unexpected conversation: %#v", req.Conversation)
	}
	if req.Runtime.LLMConfig == nil || req.Runtime.LLMConfig["llm"] == nil {
		t.Fatalf("expected llm_config to be forwarded, got %#v", req.Runtime.LLMConfig)
	}
	if req.Runtime.ToolConfig == nil || req.Runtime.ToolConfig["bing"] != "token-1" {
		t.Fatalf("expected tool_config to be forwarded, got %#v", req.Runtime.ToolConfig)
	}
	if len(req.Runtime.MCPConfig) != 1 {
		t.Fatalf("expected mcp_config to be forwarded, got %#v", req.Runtime.MCPConfig)
	}
	if req.Plugin.EnablePlugin == nil || !*req.Plugin.EnablePlugin || req.Plugin.PluginContext["session_id"] != "plugin-session-1" || req.Plugin.AskResponse["ask_id"] != "ask-1" {
		t.Fatalf("unexpected plugin options: %#v", req.Plugin)
	}

	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	for _, key := range []string{"message", "conversation", "retrieval", "runtime", "personalization", "agent", "plugin"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("expected grouped key %q in payload: %s", key, payload)
		}
	}
	for _, key := range []string{"query", "history", "session_id", "filters", "llm_config", "plugin_context", "enable_thinking"} {
		if _, ok := raw[key]; ok {
			t.Fatalf("unexpected top-level key %q in payload: %s", key, payload)
		}
	}
}

func TestBuildLLMConfigFromSelectedModels(t *testing.T) {
	llmConfig := buildLLMConfig([]selectedRuntimeModel{
		{ModelType: "llm", ProviderName: "OpenAI", ModelName: "gpt-4o", BaseURL: "https://api.openai.com/v1/", APIKey: "sk-from-db"},
		{ModelType: "evo_llm", ProviderName: "OpenAI", ModelName: "gpt-4o-mini", BaseURL: "https://api.openai.com/v1/", APIKey: "sk-from-db"},
		{ModelType: "embed_main", ProviderName: "OpenAI", ModelName: "text-embedding-3-small", BaseURL: "https://api.openai.com/v1/", APIKey: "sk-from-db"},
		{ModelType: "reranker", ProviderName: "OpenAI", ModelName: "rerank-multilingual-v3.0", BaseURL: "https://api.openai.com/v1/", APIKey: "sk-from-db"},
	})

	chatCfg := llmConfig["llm"].(map[string]any)
	evoCfg := llmConfig["evo_llm"].(map[string]any)
	embedCfg := llmConfig["embed_main"].(map[string]any)
	rerankCfg := llmConfig["reranker"].(map[string]any)

	if chatCfg["source"] != "openai" || chatCfg["model"] != "gpt-4o" || chatCfg["api_key"] != "sk-from-db" {
		t.Fatalf("unexpected llm config: %#v", chatCfg)
	}
	if evoCfg["model"] != "gpt-4o-mini" {
		t.Fatalf("unexpected evo_llm config: %#v", evoCfg)
	}
	if embedCfg["model"] != "text-embedding-3-small" {
		t.Fatalf("unexpected embed_main config: %#v", embedCfg)
	}
	if rerankCfg["model"] != "rerank-multilingual-v3.0" {
		t.Fatalf("unexpected reranker config: %#v", rerankCfg)
	}
}

func TestBuildLazyChatRequestDefaultsReasoningTrue(t *testing.T) {
	req := buildLazyChatRequest(map[string]any{
		"query":      "hello",
		"session_id": "conv-1",
	})

	if !req.Runtime.Reasoning {
		t.Fatalf("expected reasoning default true")
	}
}

func TestShouldEmitStreamFrame(t *testing.T) {
	tests := []struct {
		name    string
		delta   string
		sources []any
		want    bool
	}{
		{name: "text chunk", delta: "answer", sources: nil, want: true},
		{name: "source-only chunk", delta: "", sources: []any{map[string]any{"index": 1}}, want: true},
		{name: "empty chunk", delta: "", sources: nil, want: false},
	}

	for _, tt := range tests {
		if got := shouldEmitStreamFrame(tt.delta, tt.sources); got != tt.want {
			t.Fatalf("%s: got %v want %v", tt.name, got, tt.want)
		}
	}
}

func TestFeedBackChatHistoryCancelsFeedback(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/feedback.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.ChatHistory{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	if err := db.Create(&orm.ChatHistory{
		ID:             "h_1",
		Seq:            1,
		ConversationID: "conv-1",
		RawContent:     "question",
		Content:        "question",
		Result:         "answer",
		FeedBack:       2,
		Reason:         "not helpful",
		ExpectedAnswer: "better answer",
		TimeMixin:      orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error; err != nil {
		t.Fatalf("create history: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/core/conversations:feedBackChatHistory",
		strings.NewReader(`{"history_id":"h_1","type":"FEED_BACK_TYPE_UNSPECIFIED"}`),
	)
	rec := httptest.NewRecorder()

	FeedBackChatHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var history orm.ChatHistory
	if err := db.Where("id = ?", "h_1").First(&history).Error; err != nil {
		t.Fatalf("load history: %v", err)
	}
	if history.FeedBack != 0 {
		t.Fatalf("expected feedback to be cancelled, got %d", history.FeedBack)
	}
	if history.Reason != "" || history.ExpectedAnswer != "" {
		t.Fatalf("expected feedback detail to be cleared, got reason=%q expected_answer=%q", history.Reason, history.ExpectedAnswer)
	}
}

func TestPluginModeFromReqBody(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{
			name: "plugin_context auto wins",
			body: map[string]any{
				"plugin_context": map[string]any{"plugin_mode": "auto"},
				"agentic_config": map[string]any{"plugin_mode": "dynamic"},
			},
			want: "auto",
		},
		{
			name: "agentic_config fallback",
			body: map[string]any{
				"agentic_config": map[string]any{"plugin_mode": "auto"},
			},
			want: "auto",
		},
		{
			name: "missing defaults to dynamic",
			body: map[string]any{},
			want: "dynamic",
		},
		{
			name: "invalid value defaults to dynamic",
			body: map[string]any{
				"plugin_context": map[string]any{"plugin_mode": "invalid"},
			},
			want: "dynamic",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pluginModeFromReqBody(tc.body); got != tc.want {
				t.Fatalf("pluginModeFromReqBody() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolvePluginModeWithFallback(t *testing.T) {
	raw := map[string]any{"plugin_mode": "auto"}
	reqBody := map[string]any{
		"agentic_config": map[string]any{"plugin_mode": "dynamic"},
	}
	if got := resolvePluginModeWithFallback(raw, reqBody); got != "auto" {
		t.Fatalf("expected raw body to win, got %q", got)
	}
	if got := resolvePluginModeWithFallback(map[string]any{}, reqBody); got != "dynamic" {
		t.Fatalf("expected agentic_config fallback, got %q", got)
	}
}
