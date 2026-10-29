package doc

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFormulaExplanationHandlerACLCacheForceAndSourceChange(t *testing.T) {
	installOriginalSourceTransport(t)
	db := newDocumentTestDB(t)
	if e := db.AutoMigrate(&orm.AsyncJob{}); e != nil {
		t.Fatal(e)
	}
	seedDocumentListDataset(t, db, "ds", "owner")
	seedDocumentListDoc(t, db, "ds", "doc", "paper.pdf", time.Now(), "owner", nil)
	source := filepath.Join(t.TempDir(), "paper.pdf")
	os.WriteFile(source, []byte("%PDF-original"), 0600)
	db.Model(&orm.Document{}).Where("id = ?", "doc").Update("ext", mustJSON(documentExt{StoredPath: source, OriginalFilename: "paper.pdf"}))
	hash, _ := pdfir.SourceHash(source)
	ir := pdfir.Document{Version: 1, SourceSHA256: hash, CoordinateSpace: "normalized_top_left", Blocks: []pdfir.Block{{ID: "f", Type: "formula", Latex: "x^2", Text: "$$x^2$$", Page: 0}, {ID: "p", Type: "text", Text: "x is distance.", Page: 0}}}
	irpath := filepath.Join(t.TempDir(), "ir.json")
	os.WriteFile(irpath, mustJSON(ir), 0600)
	db.Create(&orm.AsyncJob{ID: "job", JobType: pdfIRJobType, ResourceType: "document_pdf_ir", ResourceID: pdfIRResource("ds", "doc"), Status: "succeeded", PayloadJSON: mustJSON(pdfIRPayload{Revision: pdfir.Revision, DatasetID: "ds", DocumentID: "doc", SourceSHA256: hash}), ResultJSON: mustJSON(pdfIRResult{IRPath: irpath}), CreatedAt: time.Now()})
	calls := 0
	changeSource := false
	modelKey := "one"
	failModel := false
	handler := formulaExplanationHandler{
		loadModel: func(context.Context, string) (map[string]any, string, string, error) {
			if failModel {
				return nil, "", "", errors.New("secret api key")
			}
			return map[string]any{"llm": map[string]any{"model": "selected"}}, modelKey, "selected", nil
		},
		generate: func(ctx context.Context, content, instruction string, cfg map[string]any) (string, error) {
			calls++
			if !strings.Contains(content, "x is distance.") {
				t.Error("missing neighboring source")
			}
			if changeSource {
				os.WriteFile(source, []byte("%PDF-changed"), 0600)
			}
			return `{"summary":"Distance squared","symbols":[{"symbol":"x","meaning":"distance","evidence_ids":["p"]}],"steps":[]}`, nil
		},
	}
	call := func(user, block, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("X-User-Id", user)
		r = mux.SetURLVars(r, map[string]string{"dataset": "ds", "document": "doc", "block": block})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call("stranger", "f", `{}`); w.Code != 404 || calls != 0 {
		t.Fatalf("unauthorized call: %d", w.Code)
	}
	if w := call("owner", "missing", `{}`); w.Code != 404 || calls != 0 {
		t.Fatalf("unknown formula: %d", w.Code)
	}
	if w := call("owner", "f", `{} {}`); w.Code != 400 {
		t.Fatalf("trailing request accepted: %d", w.Code)
	}
	w := call("owner", "f", `{}`)
	if w.Code != 200 || calls != 1 {
		t.Fatalf("generate: %d %s", w.Code, w.Body)
	}
	var envelope struct {
		Data formulaExplanation `json:"data"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &envelope); e != nil || envelope.Data.Model != "selected" || len(envelope.Data.Evidence) != 2 {
		t.Fatalf("bad envelope: %s", w.Body)
	}
	if w := call("owner", "f", `{}`); w.Code != 200 || calls != 1 {
		t.Fatal("cache miss")
	}
	if w := call("owner", "f", `{"force":true}`); w.Code != 200 || calls != 2 {
		t.Fatal("force ignored")
	}
	modelKey = "two"
	if w := call("owner", "f", `{}`); w.Code != 200 || calls != 3 {
		t.Fatal("model cache identity ignored")
	}
	if w := call("owner", "f", `{"language":"en"}`); w.Code != 200 || calls != 4 {
		t.Fatal("language cache identity ignored")
	}
	if w := call("owner", "f", `{"language":"invalid"}`); w.Code != 400 {
		t.Fatal("invalid language accepted")
	}
	failModel = true
	if w := call("owner", "f", `{}`); w.Code != 422 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("model failure: %d %s", w.Code, w.Body)
	}
	failModel = false
	changeSource = true
	if w := call("owner", "f", `{"force":true}`); w.Code != 409 {
		t.Fatalf("changed source returned explanation: %d %s", w.Code, w.Body)
	}
	if w := call("owner", "f", `{}`); w.Code != 409 {
		t.Fatalf("stale IR accepted: %d", w.Code)
	}
}

func TestFormulaExplanationLoadsOnlySelectedChatModel(t *testing.T) {
	installOriginalSourceTransport(t)
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&orm.UserModelProvider{ID: "provider", Name: "OpenAI", BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.UserModelProviderGroup{ID: "group", UserModelProviderID: "provider", BaseURL: "https://fixture.invalid/v1", APIKey: "fixture-key", IsVerified: true, CredentialRevision: 1, BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.UserModelProviderGroupModel{ID: "chat-model", UserModelProviderID: "provider", UserModelProviderGroupID: "group", ProviderName: "OpenAI", Name: "selected-chat", ModelType: "llm", BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.UserSelectedModel{UserID: "owner", ModelKey: "llm", UserModelProviderGroupModelID: "chat-model"},
		&orm.UserSelectedModel{UserID: "owner", ModelKey: "embed_main", UserModelProviderGroupModelID: "nonexistent-embedding"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	cfg, key, model, err := loadFormulaExplanationModel(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if model != "selected-chat" || key == "" || len(cfg) != 1 || cfg["llm"] == nil {
		t.Fatalf("wrong model/role: model=%s roles=%d", model, len(cfg))
	}
	if _, _, _, err := loadFormulaExplanationModel(t.Context(), "stranger"); err == nil {
		t.Fatal("private model used by another user")
	}
}
