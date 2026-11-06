package doc

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestNormalizeParsingLLMConfigAddsCanonicalSTTType(t *testing.T) {
	input := map[string]any{
		"stt": map[string]any{
			"source": "siliconflow",
			"model":  "TeleAI/TeleSpeechASR",
		},
	}

	got := normalizeParsingLLMConfig(input)
	roleConfig, ok := got["speech_to_text"].(map[string]any)
	if !ok {
		t.Fatalf("speech_to_text config has type %T", got["speech_to_text"])
	}
	if gotType := roleConfig["type"]; gotType != "stt" {
		t.Fatalf("speech_to_text type = %v, want stt", gotType)
	}
}

func TestIndexedParsingRequiresEmbeddingConfiguration(t *testing.T) {
	db := newDocumentTestDB(t)
	loadErr := errors.New("credential store unavailable")
	if err := validateParsingLLMConfig(t.Context(), db.DB, "user-1", ProcessingLevelIndexed, nil, loadErr); !errors.Is(err, loadErr) {
		t.Fatalf("indexed load error = %v, want original failure", err)
	}
	if err := validateParsingLLMConfig(t.Context(), db.DB, "user-1", ProcessingLevelIndexed, map[string]any{"embed_main": map[string]any{"source": "openai", "model": "embed"}}, nil); err != nil {
		t.Fatalf("indexed parsing rejected an embedding model: %v", err)
	}
	if err := validateParsingLLMConfig(t.Context(), db.DB, "user-1", ProcessingLevelChunked, nil, loadErr); err != nil {
		t.Fatalf("chunked parsing rejected an optional model configuration: %v", err)
	}
}

func TestIndexedParseAndReparseRejectMissingModelConfig(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	db := newDocumentTestDB(t)
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "indexed", "kb", "general_algo", "user-1", time.Now().UTC())
	if err := db.Model(&orm.Dataset{}).Where("id = ?", "indexed").Update("processing_level", ProcessingLevelIndexed).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := startParseTasksAtLevel(t.Context(), "user-1", "indexed", nil, ""); err == nil {
		t.Fatal("indexed parse submitted without an embedding configuration")
	}
	r := httptest.NewRequest(http.MethodPost, "/datasets/indexed/tasks:start", nil)
	r.Header.Set("X-User-Id", "user-1")
	if _, err := startReparseTasksInternalAtLevel(r, "indexed", nil, ""); err == nil {
		t.Fatal("indexed reparse submitted without an embedding configuration")
	}
}

func TestIndexedParsingAcceptsStaticEmbeddingOnlyWithoutGlobalChoice(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}, &orm.EmbeddingIndexState{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", "http://static-embedding-test")
	dynamic := false
	installDocumentServiceTransport(t, func(r *http.Request) (int, string) {
		if r.URL.Path == "/api/model/role_type" {
			if dynamic {
				return http.StatusOK, `{"role":"embed_main","type":"embed","is_dynamic":true}`
			}
			return http.StatusOK, `{"role":"embed_main","type":"embed","is_dynamic":false}`
		}
		return http.StatusNotFound, `{}`
	})
	missing := map[string]any{"llm": map[string]any{"model": "chat"}}
	if err := validateParsingLLMConfig(t.Context(), db.DB, "user-1", ProcessingLevelIndexed, missing, nil); err != nil {
		t.Fatalf("static embedding role was rejected: %v", err)
	}
	dynamic = true
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", "http://dynamic-embedding-test")
	if err := validateParsingLLMConfig(t.Context(), db.DB, "user-1", ProcessingLevelIndexed, missing, nil); err == nil {
		t.Fatal("dynamic embedding role accepted a request without embed_main")
	}
	if err := db.Create(&orm.EmbeddingIndexState{Scope: "global", Phase: "ready"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := validateParsingLLMConfig(t.Context(), db.DB, "user-1", ProcessingLevelIndexed, missing, nil); err == nil {
		t.Fatal("empty global embedding state fell back to static role")
	}
}

func seedSelectedEmbeddingForDocTest(t *testing.T, db *orm.DB, userID string) {
	t.Helper()
	if err := db.AutoMigrate(&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&orm.UserModelProvider{ID: "embedding-provider", Name: "OpenAI", BaseModel: orm.BaseModel{CreateUserID: userID}},
		&orm.UserModelProviderGroup{ID: "embedding-group", UserModelProviderID: "embedding-provider", BaseURL: "https://fixture.invalid/v1", APIKey: "fixture-key", IsVerified: true, BaseModel: orm.BaseModel{CreateUserID: userID}},
		&orm.UserModelProviderGroupModel{ID: "embedding-model", UserModelProviderID: "embedding-provider", UserModelProviderGroupID: "embedding-group", ProviderName: "OpenAI", Name: "text-embedding-3-small", ModelType: "embed", Enabled: true, BaseModel: orm.BaseModel{CreateUserID: userID}},
		&orm.UserSelectedModel{UserID: userID, ModelKey: "embed_main", UserModelProviderGroupModelID: "embedding-model", Share: true},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}
