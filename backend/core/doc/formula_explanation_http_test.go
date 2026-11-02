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
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFormulaExplanationConcurrentGenerationSharesOneCall(t *testing.T) {
	flights := &formulaExplanationFlightGroup{active: make(map[string]*formulaExplanationFlight)}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	type answer struct {
		value  formulaExplanation
		shared bool
		err    error
	}
	first := make(chan answer, 1)
	firstContext, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	go func() {
		value, shared, err := flights.do(firstContext, "concurrent-formula-test", func(ctx context.Context) (formulaExplanation, error) {
			calls.Add(1)
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return formulaExplanation{}, ctx.Err()
			}
			return formulaExplanation{Summary: "shared"}, nil
		})
		first <- answer{value, shared, err}
	}()
	<-started
	second := make(chan answer, 1)
	go func() {
		value, shared, err := flights.do(context.Background(), "concurrent-formula-test", func(context.Context) (formulaExplanation, error) {
			calls.Add(1)
			return formulaExplanation{Summary: "duplicate"}, nil
		})
		second <- answer{value, shared, err}
	}()
	deadline := time.After(time.Second)
	for {
		flights.mu.Lock()
		waiters := flights.active["concurrent-formula-test"].waiters
		flights.mu.Unlock()
		if waiters == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("second caller never joined the flight")
		default:
			runtime.Gosched()
		}
	}
	cancelFirst()
	if result := <-first; !errors.Is(result.err, context.Canceled) {
		t.Fatalf("first caller should cancel independently: %+v", result)
	}
	close(release)
	if result := <-second; result.err != nil || !result.shared || result.value.Summary != "shared" {
		t.Fatalf("second caller should receive the shared result: %+v", result)
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate model calls: %d", calls.Load())
	}
}

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
	focusedMode := ""
	handler := formulaExplanationHandler{
		loadModel: func(context.Context, string) (map[string]any, string, string, error) {
			if failModel {
				return nil, "", "", errors.New("secret api key")
			}
			return map[string]any{"llm": map[string]any{"model": "selected"}}, modelKey, "selected", nil
		},
		generate: func(ctx context.Context, content, instruction string, cfg map[string]any) (string, error) {
			calls++
			if !strings.Contains(instruction, "SAME response") {
				t.Error("repair and symbol definitions must be requested in one generation")
			}
			if !strings.Contains(instruction, "standard mathematical meaning") || !strings.Contains(instruction, "named operators such as Attention") {
				t.Error("generic operator explanations must be allowed without pretending the paper defined them")
			}
			if !strings.Contains(instruction, "at most 2 short steps") || !strings.Contains(instruction, "one concise sentence per symbol") {
				t.Error("formula output must stay concise to reduce generation latency")
			}
			if !strings.Contains(content, "x is distance.") {
				t.Error("missing neighboring source")
			}
			if changeSource {
				os.WriteFile(source, []byte("%PDF-changed"), 0600)
			}
			var input map[string]any
			json.Unmarshal([]byte(content), &input)
			for _, rawBlock := range input["source_blocks"].([]any) {
				block := rawBlock.(map[string]any)
				if len(block) != 2 || block["id"] == nil || block["text"] == nil {
					t.Error("model input contains location metadata or misses source text")
				}
			}
			if focus, ok := input["target_symbol"].(string); ok {
				if !strings.Contains(instruction, "target_symbol") {
					t.Error("focused instruction missing")
				}
				if focusedMode == "missing" {
					return `{"summary":"Missing","symbols":[],"steps":[]}`, nil
				}
				if focusedMode == "multiple" {
					return `{"summary":"Many","symbols":[{"symbol":"x","meaning":"one"},{"symbol":"y","meaning":"two"}],"steps":[]}`, nil
				}
				if focusedMode == "normalized" {
					focus = "\\mathrm{x}^2"
				}
				return string(mustJSON(map[string]any{"summary": "Focused definition", "symbols": []map[string]any{{"symbol": focus, "meaning": "distance", "evidence_ids": []string{"p"}, "inferred": true}}, "steps": []any{}})), nil
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
	if w.Header().Get("X-LazyMind-Formula-Cache") != "miss" || w.Header().Get("Server-Timing") == "" {
		t.Fatalf("missing cold-path timing or cache status: %v", w.Header())
	}
	var envelope struct {
		Data formulaExplanation `json:"data"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &envelope); e != nil || envelope.Data.Model != "selected" || len(envelope.Data.Evidence) != 2 {
		t.Fatalf("bad envelope: %s", w.Body)
	}
	if w := call("owner", "f", `{}`); w.Code != 200 || calls != 1 || w.Header().Get("X-LazyMind-Formula-Cache") != "hit" {
		t.Fatal("cache miss")
	}
	failModel = true
	cachedResponse := call("owner", "f", `{}`)
	if cachedResponse.Code != 200 || calls != 1 {
		t.Fatal("saved result requires a model")
	}
	if err := json.Unmarshal(cachedResponse.Body.Bytes(), &envelope); err != nil || envelope.Data.Model != "selected" {
		t.Fatal("cached generating model lost")
	}
	if response := call("owner", "f", `{"force":true}`); response.Code != 422 {
		t.Fatal("generation should require model")
	}
	failModel = false
	if w := call("owner", "f", `{"force":true}`); w.Code != 200 || calls != 2 {
		t.Fatal("force ignored")
	}
	modelKey = "two"
	if w := call("owner", "f", `{}`); w.Code != 200 || calls != 2 {
		t.Fatal("model selection regenerated a saved explanation")
	}
	if w := call("owner", "f", `{"force":true}`); w.Code != 200 || calls != 3 {
		t.Fatal("model cache identity ignored")
	}
	if w := call("owner", "f", `{"language":"en"}`); w.Code != 200 || calls != 4 {
		t.Fatal("language cache identity ignored")
	}
	for _, focus := range []string{"", "   ", "not-in-formula", strings.Repeat("x", 501)} {
		body := string(mustJSON(map[string]any{"focus_symbol": focus}))
		if w := call("owner", "f", body); w.Code != 400 || calls != 4 {
			t.Fatalf("invalid focus accepted/generated: %q status=%d calls=%d", focus, w.Code, calls)
		}
	}
	if w := call("stranger", "f", `{"focus_symbol":"x"}`); w.Code != 404 || calls != 4 {
		t.Fatal("focused lookup bypassed ACL")
	}
	if w := call("owner", "f", `{"focus_symbol":"x"}`); w.Code != 200 || calls != 5 {
		t.Fatalf("focused lookup reused general cache: %d %s calls=%d", w.Code, w.Body, calls)
	}
	if w := call("owner", "f", `{"focus_symbol":"x"}`); w.Code != 200 || calls != 5 {
		t.Fatal("focused cache miss")
	}
	if w := call("owner", "f", `{"focus_symbol":"x^2"}`); w.Code != 200 || calls != 6 {
		t.Fatal("different focus cache reused")
	}
	if w := call("owner", "f", `{}`); w.Code != 200 || calls != 6 {
		t.Fatal("focused request replaced general cache")
	}
	if w := call("owner", "f", `{"language":"invalid"}`); w.Code != 400 {
		t.Fatal("invalid language accepted")
	}
	for _, mode := range []string{"missing", "multiple"} {
		focusedMode = mode
		if w := call("owner", "f", `{"focus_symbol":"x^2","force":true}`); w.Code != 502 {
			t.Fatalf("ambiguous focused result accepted: %s %d", mode, w.Code)
		}
	}
	focusedMode = "normalized"
	if w := call("owner", "f", `{"focus_symbol":"x^2","force":true}`); w.Code != 200 {
		t.Fatalf("normalized symbol failed: %s", w.Body)
	} else {
		var response struct {
			Data formulaExplanation `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &response)
		if len(response.Data.Symbols) != 1 || response.Data.Symbols[0].Symbol != "x^2" || !response.Data.Symbols[0].Inferred {
			t.Fatal("requested source identity not preserved")
		}
	}
	focusedMode = ""
	failModel = true
	if w := call("owner", "f", `{"force":true}`); w.Code != 422 || strings.Contains(w.Body.String(), "secret") {
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
