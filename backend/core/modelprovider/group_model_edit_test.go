package modelprovider

import (
	"encoding/json"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func modelEditFixture(t *testing.T, typ string, builtIn bool) (*gorm.DB, orm.UserModelProviderGroupModel) {
	t.Helper()
	seedGroupModelFixture(t)
	db := store.DB()
	tokens := "128K"
	m := orm.UserModelProviderGroupModel{ID: "editable", UserModelProviderID: "provider-openai", UserModelProviderGroupID: "group-openai", ProviderName: "OpenAI", Name: "model-original", ModelType: typ, IsDefault: builtIn, MaxInputTokens: &tokens, BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	if typ != "llm" && typ != "vlm" {
		m.MaxInputTokens = nil
	}
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	return db, m
}
func modelEditCall(t *testing.T, id, user, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/model", strings.NewReader(body))
	req.Header.Set("X-User-Id", user)
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": "provider-openai", "group_id": "group-openai", "model_id": id})
	rec := httptest.NewRecorder()
	UpdateGroupModel(rec, req)
	return rec
}
func loadEditedModel(t *testing.T, db *gorm.DB, id string) orm.UserModelProviderGroupModel {
	t.Helper()
	var m orm.UserModelProviderGroupModel
	if err := db.Take(&m, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return m
}
func addEditorSelection(t *testing.T, db *gorm.DB, m orm.UserModelProviderGroupModel, slot string) {
	t.Helper()
	if err := db.AutoMigrate(&orm.UserSelectedModel{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserSelectedModel{UserID: "user-1", ModelKey: slot, UserModelProviderGroupModelID: m.ID, Share: slot == "embed_main"}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestModelEditorKeepsUUIDAndCompatibleDefaultSelection(t *testing.T) {
	db, m := modelEditFixture(t, "llm", false)
	addEditorSelection(t, db, m, "llm")
	rec := modelEditCall(t, m.ID, "user-1", `{"name":"renamed-api-id","model_type":"llm","vision":true,"max_input_tokens":"1m"}`)
	if rec.Code != 200 {
		t.Fatalf("edit=%d %s", rec.Code, rec.Body.String())
	}
	saved := loadEditedModel(t, db, m.ID)
	if saved.ID != m.ID || saved.Name != "renamed-api-id" || saved.ModelType != "llm" || !saved.Vision || saved.MaxInputTokens == nil || *saved.MaxInputTokens != "1M" {
		t.Fatalf("incomplete metadata: %+v", saved)
	}
	var selection orm.UserSelectedModel
	if err := db.Take(&selection, "user_id = ? AND model_type = ?", "user-1", "llm").Error; err != nil || selection.UserModelProviderGroupModelID != m.ID {
		t.Fatal("edit replaced default reference")
	}
	var response struct {
		Data groupModelListItem `json:"data"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &response) != nil || response.Data.ID != m.ID || response.Data.ModelType != "llm" || !response.Data.Vision {
		t.Fatal("response metadata is stale")
	}
}
func TestModelEditorSupportsVLMAndNonChatMetadata(t *testing.T) {
	for _, tc := range []struct {
		typ, body, wantType string
		tokens              bool
	}{{"vlm", `{"max_input_tokens":"200K"}`, "vlm", true}, {"rerank", `{"name":"new-rerank-id","model_type":"tts","vision":true}`, "tts", false}} {
		t.Run(tc.typ, func(t *testing.T) {
			db, m := modelEditFixture(t, tc.typ, false)
			rec := modelEditCall(t, m.ID, "user-1", tc.body)
			if rec.Code != 200 {
				t.Fatalf("edit=%d %s", rec.Code, rec.Body.String())
			}
			saved := loadEditedModel(t, db, m.ID)
			if saved.ModelType != tc.wantType || (saved.MaxInputTokens != nil) != tc.tokens || (tc.wantType == "tts" && saved.Vision) {
				t.Fatalf("wrong metadata: %+v", saved)
			}
		})
	}
}
func TestModelEditorRequiresExplicitCatalogCustomizationAndSkipsNoOp(t *testing.T) {
	db, m := modelEditFixture(t, "llm", true)
	rec := modelEditCall(t, m.ID, "user-1", `{"max_input_tokens":"256K"}`)
	if rec.Code != 400 {
		t.Fatalf("implicit catalog edit=%d", rec.Code)
	}
	rec = modelEditCall(t, m.ID, "user-1", `{"name":"model-original","model_type":"llm","vision":false,"max_input_tokens":"128K","customize":true}`)
	if rec.Code != 200 {
		t.Fatalf("noop=%d %s", rec.Code, rec.Body.String())
	}
	if !loadEditedModel(t, db, m.ID).IsDefault {
		t.Fatal("unchanged save converted catalog")
	}
	rec = modelEditCall(t, m.ID, "user-1", `{"max_input_tokens":"256K","customize":true}`)
	if rec.Code != 200 {
		t.Fatalf("customize=%d %s", rec.Code, rec.Body.String())
	}
	saved := loadEditedModel(t, db, m.ID)
	if saved.IsDefault || saved.ID != m.ID || saved.MaxInputTokens == nil || *saved.MaxInputTokens != "256K" {
		t.Fatal("explicit customization failed")
	}
}
func TestModelEditorCatalogRenameDoesNotResurrectOriginalOnReseed(t *testing.T) {
	db, m := modelEditFixture(t, "llm", true)
	catalog := orm.DefaultModelProvider{ID: "default-openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1/", Category: "model", Capabilities: "has_models"}
	if err := db.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	original := orm.DefaultModel{ID: "catalog-model", DefaultModelProviderID: catalog.ID, ProviderName: "OpenAI", Name: m.Name, ModelType: "llm"}
	if err := db.Create(&original).Error; err != nil {
		t.Fatal(err)
	}
	another := m
	another.ID = "still-catalog"
	another.Name = "other-catalog-model"
	if err := db.Create(&another).Error; err != nil {
		t.Fatal(err)
	}
	rec := modelEditCall(t, m.ID, "user-1", `{"name":"custom-api-id","customize":true}`)
	if rec.Code != 200 {
		t.Fatalf("rename=%d %s", rec.Code, rec.Body.String())
	}
	tokens := "2M"
	if err := syncDefaultModelToUserGroups(db, time.Now(), catalog.ID, "OpenAI", m.Name, "llm", &tokens, 0, "", false); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&orm.UserModelProviderGroupModel{}).Where("user_model_provider_group_id = ? AND name = ? AND deleted_at IS NULL", m.UserModelProviderGroupID, m.Name).Count(&count)
	if count != 0 {
		t.Fatal("catalog resurrected renamed source")
	}
	saved := loadEditedModel(t, db, m.ID)
	if saved.Name != "custom-api-id" || saved.IsDefault {
		t.Fatal("reseed replaced override")
	}
}
func TestModelEditorProtectsDefaultAndConversationReferences(t *testing.T) {
	for _, tc := range []struct {
		name, slot, body string
		conversation     bool
	}{{"chat default", "llm", `{"model_type":"embed"}`, false}, {"chat default cannot become raw VLM", "llm", `{"model_type":"vlm"}`, false}, {"vision default", "vlm", `{"vision":false}`, false}, {"fixed conversation", "", `{"model_type":"embed"}`, true}} {
		t.Run(tc.name, func(t *testing.T) {
			db, m := modelEditFixture(t, "llm", false)
			if tc.slot == "vlm" {
				db.Model(&m).Update("vision", true)
			}
			if tc.conversation {
				if err := db.Exec(`CREATE TABLE conversations (id TEXT PRIMARY KEY, chat_model_id TEXT, chat_model_source TEXT, deleted_at DATETIME)`).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Exec(`INSERT INTO conversations(id,chat_model_id,chat_model_source) VALUES(?,?,'own')`, "conversation", m.ID).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				addEditorSelection(t, db, m, tc.slot)
			}
			rec := modelEditCall(t, m.ID, "user-1", tc.body)
			if rec.Code != 409 {
				t.Fatalf("invalid reference edit=%d %s", rec.Code, rec.Body.String())
			}
			saved := loadEditedModel(t, db, m.ID)
			if saved.ModelType != m.ModelType || (tc.slot == "vlm" && !saved.Vision) {
				t.Fatal("rejected mutation changed metadata")
			}
		})
	}
}
func TestModelEditorBlocksEmbeddingIdentityInUseWithoutDowngrading(t *testing.T) {
	for _, ref := range []string{"default", "indexed"} {
		t.Run(ref, func(t *testing.T) {
			db, m := modelEditFixture(t, "embed", false)
			if ref == "default" {
				addEditorSelection(t, db, m, "embed_main")
			} else {
				if err := db.AutoMigrate(&orm.Dataset{}); err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&orm.Dataset{ID: "indexed", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: "user-1"}}).Error; err != nil {
					t.Fatal(err)
				}
			}
			rec := modelEditCall(t, m.ID, "user-1", `{"name":"new-embedding-id"}`)
			if rec.Code != 409 {
				t.Fatalf("embedding edit=%d %s", rec.Code, rec.Body.String())
			}
			if loadEditedModel(t, db, m.ID).Name != m.Name {
				t.Fatal("blocked embedding renamed")
			}
			if ref == "indexed" {
				var ds orm.Dataset
				db.Take(&ds, "id = ?", "indexed")
				if ds.ProcessingLevel != "indexed" {
					t.Fatal("editor downgraded indexed KB")
				}
			}
		})
	}
}
func TestModelMutationRejectsUnknownTypesAndAnthropicNonChat(t *testing.T) {
	for _, tc := range []struct{ name, typ, protocol string }{{"unknown", "made-up-type", "OpenAI"}, {"anthropic embedding", "embed", "Claude"}, {"anthropic rerank", "rerank", "Claude"}} {
		t.Run(tc.name, func(t *testing.T) {
			db, m := modelEditFixture(t, "llm", false)
			db.Model(&orm.UserModelProvider{}).Where("id = ?", m.UserModelProviderID).Update("protocol_name", tc.protocol)
			rec := modelEditCall(t, m.ID, "user-1", `{"model_type":"`+tc.typ+`"}`)
			if rec.Code != 400 {
				t.Fatalf("invalid edit=%d %s", rec.Code, rec.Body.String())
			}
			req := httptest.NewRequest(http.MethodPost, "/models", strings.NewReader(`{"name":"new-invalid","model_type":"`+tc.typ+`"}`))
			req.Header.Set("X-User-Id", "user-1")
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": m.UserModelProviderID, "group_id": m.UserModelProviderGroupID})
			rec = httptest.NewRecorder()
			AddGroupModel(rec, req)
			if rec.Code != 400 {
				t.Fatalf("invalid create=%d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
func TestModelEditorRejectsDuplicateForeignAndInvalidName(t *testing.T) {
	db, m := modelEditFixture(t, "llm", false)
	other := m
	other.ID = "other"
	other.Name = "duplicate"
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user, body string
		status     int
	}{{"user-1", `{"name":"duplicate"}`, 409}, {"other-user", `{"name":"changed"}`, 404}, {"user-1", `{"name":"  "}`, 400}, {"user-1", `{"name":"` + strings.Repeat("x", 513) + `"}`, 400}} {
		rec := modelEditCall(t, m.ID, tc.user, tc.body)
		if rec.Code != tc.status {
			t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.status, rec.Body.String())
		}
		if loadEditedModel(t, db, m.ID).Name != m.Name {
			t.Fatal("invalid mutation renamed model")
		}
	}
}

func TestModelEditorKeepsReadOnlyEmbeddingContextOnNoOp(t *testing.T) {
	db, m := modelEditFixture(t, "embed", true)
	tokens := "8192"
	if err := db.Model(&m).Update("max_input_tokens", tokens).Error; err != nil {
		t.Fatal(err)
	}
	rec := modelEditCall(t, m.ID, "user-1", `{"name":"model-original","model_type":"embed","vision":false,"customize":true}`)
	if rec.Code != 200 {
		t.Fatalf("embedding no-op=%d %s", rec.Code, rec.Body.String())
	}
	saved := loadEditedModel(t, db, m.ID)
	if !saved.IsDefault || saved.MaxInputTokens == nil || *saved.MaxInputTokens != tokens {
		t.Fatal("no-op converted catalog or discarded read-only embedding context")
	}
}

func TestModelEditorCanUndoCatalogRenameWithoutReplacingUUID(t *testing.T) {
	db, m := modelEditFixture(t, "llm", true)
	rec := modelEditCall(t, m.ID, "user-1", `{"name":"renamed","customize":true}`)
	if rec.Code != 200 {
		t.Fatalf("rename=%d %s", rec.Code, rec.Body.String())
	}
	rec = modelEditCall(t, m.ID, "user-1", `{"name":"model-original"}`)
	if rec.Code != 200 {
		t.Fatalf("undo rename=%d %s", rec.Code, rec.Body.String())
	}
	saved := loadEditedModel(t, db, m.ID)
	if saved.Name != m.Name || saved.IsDefault {
		t.Fatal("undo replaced UUID or returned to catalog management")
	}
}

func TestModelEditorProtectsOtherUsersSharedConversationReference(t *testing.T) {
	db, m := modelEditFixture(t, "llm", false)
	if err := db.Exec(`CREATE TABLE conversations (id TEXT PRIMARY KEY, chat_model_id TEXT, chat_model_source TEXT, create_user_id TEXT, deleted_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO conversations(id,chat_model_id,chat_model_source,create_user_id) VALUES(?,?,'shared','another-user')`, "shared-conversation", m.ID).Error; err != nil {
		t.Fatal(err)
	}
	rec := modelEditCall(t, m.ID, "user-1", `{"model_type":"vlm"}`)
	if rec.Code != 409 {
		t.Fatalf("shared fixed conversation lost its chat type: %d %s", rec.Code, rec.Body.String())
	}
}

func TestModelEditorNoOpDoesNotInventCatalogContext(t *testing.T) {
	db, m := modelEditFixture(t, "vlm", true)
	if err := db.Model(&m).Update("max_input_tokens", nil).Error; err != nil {
		t.Fatal(err)
	}
	rec := modelEditCall(t, m.ID, "user-1", `{"name":"model-original","model_type":"vlm","customize":true}`)
	if rec.Code != 200 {
		t.Fatalf("context-free no-op=%d %s", rec.Code, rec.Body.String())
	}
	saved := loadEditedModel(t, db, m.ID)
	if !saved.IsDefault || saved.MaxInputTokens != nil {
		t.Fatal("no-op synthesized context and converted catalog")
	}
}
