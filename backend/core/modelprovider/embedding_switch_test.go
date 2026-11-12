package modelprovider

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestAssessEmbeddingSwitchRequiresMatchingRuntimeFingerprint(t *testing.T) {
	active := orm.EmbeddingIndexState{
		ActiveModelID: "old", ActiveSource: "own",
		Signature: "v1:2:passage:query", Generation: 7,
	}
	cases := []struct {
		name, candidateID, candidateSource, signature, want string
		indexed                                             int64
	}{
		{"same runtime across suppliers", "new", "own", active.Signature, "same", 2},
		{"different runtime", "new", "own", "v1:2:other:query", "clear_required", 2},
		{"unknown candidate", "new", "own", "", "clear_required", 2},
		{"old vectors have no verified identity", "new", "own", "v1:2:passage:query", "clear_required", 2},
		{"no live documents permits orphan cleanup", "new", "own", "v1:2:other:query", "empty", 0},
		{"changed config of same model", "old", "own", "v1:2:other:query", "clear_required", 2},
		{"same model and runtime", "old", "own", active.Signature, "unchanged", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := active
			if tc.name == "old vectors have no verified identity" {
				state.Signature = ""
			}
			got := assessEmbeddingSwitch(state, tc.indexed, tc.candidateID, tc.candidateSource, tc.signature)
			if got != tc.want {
				t.Fatalf("outcome = %q, want %q", got, tc.want)
			}
		})
	}
	if got := assessEmbeddingSwitch(orm.EmbeddingIndexState{}, 0, "first", "own", "v1:2:passage:query"); got != "empty" {
		t.Fatalf("fresh selection = %q, want empty", got)
	}
	legacy := orm.EmbeddingIndexState{ActiveModelID: "old", ActiveSource: "own"}
	if got := assessEmbeddingSwitch(legacy, 0, "old", "own", "v1:2:passage:query"); got != "empty" {
		t.Fatalf("unsigned legacy selection with no Core rows = %q, want empty", got)
	}
}

func TestAssessEmbeddingSwitchAfterVerifiedOfflineClear(t *testing.T) {
	cleared := orm.EmbeddingIndexState{Scope: globalEmbeddingScope, Phase: "cleared", Generation: 9}
	cases := []struct {
		name        string
		state       orm.EmbeddingIndexState
		modelID     string
		signature   string
		wantOutcome string
	}{
		{"first new signed model despite indexed knowledge bases", cleared, "new", "v1:2:new:query", "empty"},
		{"unknown model runtime cannot claim the cleared index", cleared, "new", "", "clear_required"},
		{"no candidate cannot establish a new index identity", cleared, "", "", "clear_required"},
		{"stale active identity means clear is incomplete", orm.EmbeddingIndexState{Phase: "cleared", ActiveModelID: "old", Signature: "v1:2:old:query"}, "new", "v1:2:new:query", "clear_required"},
		{"stale target identity means clear is incomplete", orm.EmbeddingIndexState{Phase: "cleared", TargetModelID: "old"}, "new", "v1:2:new:query", "clear_required"},
		{"clearing is not a verified completion", orm.EmbeddingIndexState{Phase: "clearing"}, "new", "v1:2:new:query", "clear_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := assessEmbeddingSwitch(tc.state, 3, tc.modelID, "own", tc.signature); got != tc.wantOutcome {
				t.Fatalf("outcome = %q, want %q", got, tc.wantOutcome)
			}
		})
	}
}

func TestLegacyEmbeddingChoiceBlocksFirstSignedSelectionWithoutVectorCleanup(t *testing.T) {
	db, _, _, first := setupProbeTest(t,
		&orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{},
		&orm.EmbeddingIndexState{}, &orm.Dataset{}, &orm.Document{}, &orm.DocumentProcessingState{})
	if err := db.Model(&first).Updates(map[string]any{"model_type": "embed", "provider_name": "OpenAI"}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.UserSelectedModel{UserID: first.CreateUserID, ModelKey: "embed_main", UserModelProviderGroupModelID: first.ID, Share: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{&orm.Dataset{ID: "legacy-kb", KbID: "legacy-kb", ProcessingLevel: "indexed"}, &orm.Document{ID: "legacy-doc", DatasetID: "legacy-kb"}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ready","code":"ok","dimensions":2,"passage_signature":"` + strings.Repeat("a", 64) + `","query_signature":"` + strings.Repeat("b", 64) + `"}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	previewReq := httptest.NewRequest(http.MethodPost, "/embedding/switch-preview", strings.NewReader(`{"model_id":"`+first.ID+`","source":"own"}`))
	previewReq.Header.Set("X-User-Id", first.CreateUserID)
	previewReq.Header.Set("X-User-Role", "admin")
	previewRec := httptest.NewRecorder()
	PreviewEmbeddingModelSwitch(previewRec, previewReq)
	var preview struct {
		Data EmbeddingSwitchPreviewResponse `json:"data"`
	}
	if err := json.Unmarshal(previewRec.Body.Bytes(), &preview); err != nil || previewRec.Code != http.StatusOK || preview.Data.Outcome != "clear_required" || preview.Data.CanSwitch {
		t.Fatalf("legacy preview = %d %s (%v)", previewRec.Code, previewRec.Body.String(), err)
	}
	selectReq := httptest.NewRequest(http.MethodPut, "/selected_models", strings.NewReader(`{"selections":[{"model_key":"embed_main","model_id":"`+first.ID+`"}]}`))
	selectReq.Header.Set("X-User-Id", first.CreateUserID)
	selectReq.Header.Set("X-User-Role", "admin")
	selectRec := httptest.NewRecorder()
	SetSelectedModels(selectRec, selectReq)
	if selectRec.Code != http.StatusConflict || !strings.Contains(selectRec.Body.String(), `"code":2005089`) {
		t.Fatalf("legacy selection = %d %s", selectRec.Code, selectRec.Body.String())
	}
	var count int64
	if err := db.Model(&orm.EmbeddingIndexState{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rejected legacy switch fabricated signed state: count=%d err=%v", count, err)
	}
}

func TestLegacyEmbeddingChoiceIsUnavailableWhenOldVectorsHaveUnknownIdentity(t *testing.T) {
	db, _, _, first := setupProbeTest(t, &orm.UserSelectedModel{}, &orm.EmbeddingIndexState{}, &orm.Dataset{}, &orm.Document{}, &orm.DocumentProcessingState{})
	if err := db.Model(&first).Updates(map[string]any{"model_type": "embed", "provider_name": "OpenAI"}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.UserSelectedModel{UserID: first.CreateUserID, ModelKey: "embed_main", UserModelProviderGroupModelID: first.ID, Share: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Dataset{ID: "old-kb", KbID: "old-kb", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: first.CreateUserID, CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	choice, present, err := ResolveGlobalEmbeddingChoice(t.Context(), db)
	if err != nil || !present || choice.Phase != "unverified" {
		t.Fatalf("legacy choice must expose unsigned vector status: %+v present=%t err=%v", choice, present, err)
	}
	ready, err := IsModelReady(t.Context(), db, first.CreateUserID, "embed_main")
	if err != nil || ready {
		t.Fatalf("unsigned legacy vectors must block readiness: ready=%t err=%v", ready, err)
	}
	selected, err := loadSelectedModels(t.Context(), db, first.CreateUserID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range selected {
		if item.ModelKey == "embed_main" && item.Availability != "unavailable" {
			t.Fatalf("default model UI must not mark the unsafe legacy embedding available: %+v", item)
		}
	}
	if err := db.Create(&orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveModelID: first.ID, ActiveSource: "own", Signature: "malformed", Phase: "ready", Generation: 1, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	choice, present, err = ResolveGlobalEmbeddingChoice(t.Context(), db)
	if err != nil || !present || choice.Phase != "unverified" {
		t.Fatalf("malformed persisted signature must also block runtime: %+v present=%t err=%v", choice, present, err)
	}
	ready, err = IsModelReady(t.Context(), db, first.CreateUserID, "embed_main")
	if err != nil || ready {
		t.Fatalf("malformed persisted signature must block readiness: ready=%t err=%v", ready, err)
	}
}

func TestSelectedEmbeddingIsUnavailableWhileVectorCleanupIsIncomplete(t *testing.T) {
	db, _, _, active := setupProbeTest(t, &orm.UserSelectedModel{}, &orm.EmbeddingIndexState{})
	if err := db.Model(&active).Update("model_type", "embed").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, row := range []any{
		&orm.UserSelectedModel{UserID: active.CreateUserID, ModelKey: "embed_main", UserModelProviderGroupModelID: active.ID, CreatedAt: now, UpdatedAt: now},
		&orm.EmbeddingIndexState{
			Scope: globalEmbeddingScope, ActiveModelID: active.ID, ActiveSource: "own", ActiveModelName: active.Name,
			Signature: "v1:1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64),
			Phase:     "ready", Generation: 1, UpdatedAt: now,
		},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, phase := range []string{"clearing", "cleanup_failed"} {
		t.Run(phase, func(t *testing.T) {
			if err := db.Model(&orm.EmbeddingIndexState{}).Where("scope = ?", globalEmbeddingScope).Update("phase", phase).Error; err != nil {
				t.Fatal(err)
			}
			selected, err := loadSelectedModels(t.Context(), db, active.CreateUserID)
			if err != nil {
				t.Fatal(err)
			}
			if len(selected) != 1 || selected[0].ModelID != active.ID || selected[0].Availability != "unavailable" {
				t.Fatalf("incomplete cleanup advertised the old embedding as available: %+v", selected)
			}
			ready, err := IsModelReady(t.Context(), db, active.CreateUserID, "embed_main")
			if err != nil || ready {
				t.Fatalf("embedding readiness diverged from cleanup phase: ready=%v err=%v", ready, err)
			}
		})
	}
}

func TestGlobalEmbeddingSelectionOnlySwitchesWithVerifiedCompatibleVectors(t *testing.T) {
	db, parent, group, first := setupProbeTest(t,
		&orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{},
		&orm.EmbeddingIndexState{}, &orm.Dataset{}, &orm.Document{}, &orm.DocumentProcessingState{})
	if err := db.Model(&first).Updates(map[string]any{"model_type": "embed", "provider_name": "OpenAI"}).Error; err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID, second.Name = "same-runtime", "same-runtime"
	third := first
	third.ID, third.Name = "different-runtime", "different-runtime"
	for _, model := range []orm.UserModelProviderGroupModel{second, third} {
		if err := db.Create(&model).Error; err != nil {
			t.Fatal(err)
		}
	}
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		passage := strings.Repeat("a", 64)
		if body.Model == third.Name {
			passage = strings.Repeat("c", 64)
		}
		_, _ = w.Write([]byte(`{"status":"ready","code":"ok","dimensions":2,"passage_signature":"` + passage + `","query_signature":"` + strings.Repeat("b", 64) + `"}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	selectModel := func(modelID string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(setSelectedModelsRequest{Selections: []selectedModelUpsertItem{{ModelKey: "embed_main", ModelID: modelID}}})
		req := httptest.NewRequest(http.MethodPut, "/selected_models", bytes.NewReader(body))
		req.Header.Set("X-User-Id", first.CreateUserID)
		req.Header.Set("X-User-Role", "admin")
		rec := httptest.NewRecorder()
		SetSelectedModels(rec, req)
		return rec
	}
	unauthorizedBody := []byte(`{"selections":[{"model_key":"embed_main","model_id":"` + first.ID + `"}]}`)
	unauthorizedReq := httptest.NewRequest(http.MethodPut, "/selected_models", bytes.NewReader(unauthorizedBody))
	unauthorizedReq.Header.Set("X-User-Id", first.CreateUserID)
	unauthorizedReq.Header.Set("X-User-Role", "user")
	unauthorizedRec := httptest.NewRecorder()
	SetSelectedModels(unauthorizedRec, unauthorizedReq)
	if unauthorizedRec.Code != http.StatusForbidden {
		t.Fatalf("non-admin changed global embedding: %d %s", unauthorizedRec.Code, unauthorizedRec.Body.String())
	}
	if rec := selectModel(first.ID); rec.Code != http.StatusOK {
		t.Fatalf("first selection = %d: %s", rec.Code, rec.Body.String())
	}
	var state orm.EmbeddingIndexState
	if err := db.Take(&state, "scope = ?", globalEmbeddingScope).Error; err != nil || state.ActiveModelID != first.ID || state.Signature == "" {
		t.Fatalf("first selection did not persist a fingerprint: %+v %v", state, err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Dataset{ID: "kb", KbID: "kb", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: first.CreateUserID, CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Document{ID: "kb-doc", DatasetID: "kb"}).Error; err != nil {
		t.Fatal(err)
	}
	if rec := selectModel(second.ID); rec.Code != http.StatusOK {
		t.Fatalf("compatible selection = %d: %s", rec.Code, rec.Body.String())
	}
	if err := db.Take(&state, "scope = ?", globalEmbeddingScope).Error; err != nil || state.ActiveModelID != second.ID || state.Generation != 1 {
		t.Fatalf("compatible switch not active: %+v %v", state, err)
	}
	if rec := selectModel(third.ID); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":2005089`) {
		t.Fatalf("incompatible selection = %d: %s", rec.Code, rec.Body.String())
	}
	previewReq := httptest.NewRequest(http.MethodPost, "/embedding/switch-preview", strings.NewReader(`{"model_id":"`+third.ID+`","source":"own"}`))
	previewReq.Header.Set("X-User-Id", first.CreateUserID)
	previewReq.Header.Set("X-User-Role", "admin")
	previewRec := httptest.NewRecorder()
	PreviewEmbeddingModelSwitch(previewRec, previewReq)
	var preview struct {
		Data EmbeddingSwitchPreviewResponse `json:"data"`
	}
	if err := json.Unmarshal(previewRec.Body.Bytes(), &preview); err != nil || previewRec.Code != http.StatusOK || preview.Data.Outcome != "clear_required" || preview.Data.CanSwitch {
		t.Fatalf("incompatible preview = %d %s (%v)", previewRec.Code, previewRec.Body.String(), err)
	}
	if rec := selectModel(""); rec.Code != http.StatusConflict {
		t.Fatalf("active embedding removal = %d: %s", rec.Code, rec.Body.String())
	}
	if err := db.Take(&state, "scope = ?", globalEmbeddingScope).Error; err != nil || state.ActiveModelID != second.ID {
		t.Fatalf("rejected switch changed active model: %+v %v", state, err)
	}
	// The offline maintenance command purges the vector collections before
	// writing this state. KB rows remain indexed so they can be reembedded.
	if err := db.Model(&orm.EmbeddingIndexState{}).Where("scope = ?", globalEmbeddingScope).
		Updates(map[string]any{
			"active_model_id": "", "active_source": "", "active_model_name": "",
			"active_provider_name": "", "signature": "", "phase": "cleared",
			"target_model_id": "", "target_source": "", "target_signature": "",
			"generation": state.Generation + 1,
		}).Error; err != nil {
		t.Fatal(err)
	}
	previewReq = httptest.NewRequest(http.MethodPost, "/embedding/switch-preview", strings.NewReader(`{"model_id":"`+third.ID+`","source":"own"}`))
	previewReq.Header.Set("X-User-Id", first.CreateUserID)
	previewReq.Header.Set("X-User-Role", "admin")
	previewRec = httptest.NewRecorder()
	PreviewEmbeddingModelSwitch(previewRec, previewReq)
	if err := json.Unmarshal(previewRec.Body.Bytes(), &preview); err != nil || previewRec.Code != http.StatusOK || preview.Data.Outcome != "empty" || !preview.Data.CanSwitch {
		t.Fatalf("post-clear preview = %d %s (%v)", previewRec.Code, previewRec.Body.String(), err)
	}
	if rec := selectModel(third.ID); rec.Code != http.StatusOK {
		t.Fatalf("first post-clear selection = %d: %s", rec.Code, rec.Body.String())
	}
	if err := db.Take(&state, "scope = ?", globalEmbeddingScope).Error; err != nil ||
		state.ActiveModelID != third.ID || state.Signature == "" || state.Phase != "ready" {
		t.Fatalf("post-clear selection did not establish the new identity: %+v %v", state, err)
	}
	_ = parent
	_ = group
}

func TestSelectedModelsShowsGlobalEmbeddingInsteadOfStalePersonalChoice(t *testing.T) {
	db, parent, group, active := setupProbeTest(t, &orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{}, &orm.EmbeddingIndexState{})
	if err := db.Model(&active).Updates(map[string]any{"model_type": "embed", "provider_name": "OpenAI"}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, row := range []any{
		&orm.UserSelectedModel{UserID: active.CreateUserID, ModelKey: "embed_main", UserModelProviderGroupModelID: active.ID, Share: true, CreatedAt: now, UpdatedAt: now},
		&orm.EmbeddingIndexState{Scope: "global", ActiveModelID: active.ID, ActiveSource: "own", ActiveModelName: active.Name, ActiveProviderName: active.ProviderName, Signature: "v1:1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), Phase: "ready", Generation: 1, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	bobBase := orm.BaseModel{CreateUserID: "bob", CreatedAt: now, UpdatedAt: now}
	bobProvider := orm.UserModelProvider{ID: "bob-provider", Name: "OpenAI", ProtocolName: "OpenAI", Category: "model", Capabilities: "has_models", BaseModel: bobBase}
	bobGroup := orm.UserModelProviderGroup{ID: "bob-group", UserModelProviderID: bobProvider.ID, BaseURL: "https://example.com/v1", APIKey: "bob-key", IsVerified: true, BaseModel: bobBase}
	bobModel := orm.UserModelProviderGroupModel{ID: "bob-model", UserModelProviderID: bobProvider.ID, UserModelProviderGroupID: bobGroup.ID, ProviderName: "OpenAI", Name: "stale-embed", ModelType: "embed", Enabled: true, BaseModel: bobBase}
	for _, row := range []any{&bobProvider, &bobGroup, &bobModel, &orm.UserSelectedModel{UserID: "bob", ModelKey: "embed_main", UserModelProviderGroupModelID: bobModel.ID, CreatedAt: now, UpdatedAt: now}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	selected, err := loadSelectedModels(t.Context(), db, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].ModelID != active.ID || selected[0].Name != active.Name || !selected[0].ReadOnly {
		t.Fatalf("global selection not projected: %+v", selected)
	}
	readyReq := httptest.NewRequest(http.MethodGet, "/model_ready?model_type=embed_main", nil)
	readyReq.Header.Set("X-User-Id", "bob")
	readyRec := httptest.NewRecorder()
	GetModelReady(readyRec, readyReq)
	if readyRec.Code != http.StatusOK || !strings.Contains(readyRec.Body.String(), `"model_name":"`+active.Name+`"`) || !strings.Contains(readyRec.Body.String(), `"ready":true`) {
		t.Fatalf("readiness used stale personal embedding: %d %s", readyRec.Code, readyRec.Body.String())
	}
	ready, err := IsModelReady(t.Context(), db, "bob", "embed_main")
	if err != nil || !ready {
		t.Fatalf("global embedding not ready for bob: %v %v", ready, err)
	}
	if err := db.Delete(&orm.EmbeddingIndexState{}, "scope = ?", globalEmbeddingScope).Error; err != nil {
		t.Fatal(err)
	}
	selected, err = loadSelectedModels(t.Context(), db, "bob")
	if err != nil || len(selected) != 1 || selected[0].ModelID != active.ID {
		t.Fatalf("legacy selection diverged: %+v %v", selected, err)
	}
	shareReq := httptest.NewRequest(http.MethodPut, "/selected_models/share", strings.NewReader(`{"model_id":"`+active.ID+`","model_key":"embed_main","share":false}`))
	shareReq.Header.Set("X-User-Id", active.CreateUserID)
	shareReq.Header.Set("X-User-Role", "admin")
	shareRec := httptest.NewRecorder()
	SetSharedModel(shareRec, shareReq)
	if shareRec.Code != http.StatusBadRequest {
		t.Fatalf("embed_main share edit changed legacy global choice: %d %s", shareRec.Code, shareRec.Body.String())
	}
	_ = parent
	_ = group
}
