package modelprovider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func setupProbeTest(t *testing.T, extraModels ...any) (*gorm.DB, orm.UserModelProvider, orm.UserModelProviderGroup, orm.UserModelProviderGroupModel) {
	t.Helper()
	models := append([]any{&orm.DefaultModelProvider{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}}, extraModels...)
	db := orm.MigrateTestDB(t, models...).DB
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	base := orm.BaseModel{CreateUserID: "probe-owner", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	parent := orm.UserModelProvider{ID: "probe-supplier", Name: "Local gateway", ProtocolName: "OpenAI", IsCustom: true, Category: "model", BaseURL: "https://probe.example/v1/", ActiveKeyInitialized: true, ActiveKeyGroupID: "probe-group", ActiveKeyID: groupKeyID("probe-group", "active-test-key"), BaseModel: base}
	group := orm.UserModelProviderGroup{ID: "probe-group", UserModelProviderID: parent.ID, Name: parent.Name, BaseURL: "https://ignored.example/v1/", APIKey: "inactive-test-key\nactive-test-key", IsVerified: false, BaseModel: base}
	model := orm.UserModelProviderGroupModel{ID: "probe-model", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "stale-source", Name: "test-chat", ModelType: "llm", Vision: true, BaseModel: base}
	for _, row := range []any{&parent, &group, &model} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, parent, group, model
}

func probeRequest(parent, group, model string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/probe", nil)
	req.Header.Set("X-User-Id", "probe-owner")
	return mux.SetURLVars(req, map[string]string{"model_provider_id": parent, "group_id": group, "model_id": model})
}

func decodeProbeTest(t *testing.T, rec *httptest.ResponseRecorder) ModelProbeResponse {
	t.Helper()
	var body struct {
		Data ModelProbeResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

func TestProbeUsesOwnedSavedModelAndActiveKeyWithoutChangingRecords(t *testing.T) {
	db, parent, group, model := setupProbeTest(t)
	db.Take(&parent, "id = ?", parent.ID)
	db.Take(&group, "id = ?", group.ID)
	db.Take(&model, "id = ?", model.ID)
	before := []any{parent, group, model}
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/api/model/probe" || body["model"] != model.Name || body["model_type"] != "llm" || body["source"] != "OpenAI" || body["url"] != parent.BaseURL || body["api_key"] != "active-test-key" || body["skip_auth"] != false || body["vision"] != true {
			t.Errorf("incorrect probe request: %#v", body)
		}
		_, _ = w.Write([]byte(`{"status":"passed","code":"ok","model_type":"forged","elapsed_ms":-99,"api_key":"do-not-return"}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	rec := httptest.NewRecorder()
	ProbeGroupModel(rec, probeRequest(parent.ID, group.ID, model.ID))
	result := decodeProbeTest(t, rec)
	if rec.Code != 200 || result.Status != "passed" || result.Code != "ok" || result.ModelType != "llm" || result.ElapsedMS < 0 || strings.Contains(rec.Body.String(), "do-not-return") {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	var afterParent orm.UserModelProvider
	var afterGroup orm.UserModelProviderGroup
	var afterModel orm.UserModelProviderGroupModel
	db.Take(&afterParent, "id = ?", parent.ID)
	db.Take(&afterGroup, "id = ?", group.ID)
	db.Take(&afterModel, "id = ?", model.ID)
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal([]any{afterParent, afterGroup, afterModel})
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("probe changed saved configuration or credentials")
	}
}

func TestProbeRejectsForeignOrMismatchedPathsBeforeCallingAlgorithm(t *testing.T) {
	_, parent, group, model := setupProbeTest(t)
	var calls atomic.Int32
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	for _, ids := range [][3]string{{"foreign", group.ID, model.ID}, {parent.ID, "foreign", model.ID}, {parent.ID, group.ID, "foreign"}} {
		rec := httptest.NewRecorder()
		ProbeGroupModel(rec, probeRequest(ids[0], ids[1], ids[2]))
		if rec.Code != 404 {
			t.Fatalf("invalid path returned %d", rec.Code)
		}
	}
	req := probeRequest(parent.ID, group.ID, model.ID)
	req.Header.Set("X-User-Id", "foreign")
	rec := httptest.NewRecorder()
	ProbeGroupModel(rec, req)
	if rec.Code != 404 || calls.Load() != 0 {
		t.Fatal("foreign model reached algorithm")
	}
}

func TestProbeCredentialModesRemainReadOnly(t *testing.T) {
	for _, mode := range []string{"disabled", "keyless", "legacy-uninitialized", "v2", "official-empty"} {
		t.Run(mode, func(t *testing.T) {
			db, parent, group, model := setupProbeTest(t)
			wantKey := "active-test-key"
			switch mode {
			case "disabled":
				parent.ActiveKeyID, parent.ActiveKeyGroupID = "", ""
			case "keyless", "official-empty":
				parent.ActiveKeyID, parent.ActiveKeyGroupID, group.APIKey = "", "", ""
				if mode == "official-empty" {
					parent.IsCustom = false
				}
			case "legacy-uninitialized":
				parent.ActiveKeyInitialized, parent.ActiveKeyID, parent.ActiveKeyGroupID = false, "", ""
				wantKey = "inactive-test-key"
			case "v2":
				group.CredentialVersion, group.CredentialRevision = 2, 4
				cipher, err := encryptModelProviderAPIKeyForGroup(parent.CreateUserID, group.ID, 4, group.APIKey)
				if err != nil {
					t.Fatal(err)
				}
				group.APIKeyCiphertext, group.APIKey = cipher, ""
			}
			if err := db.Save(&parent).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Save(&group).Error; err != nil {
				t.Fatal(err)
			}
			db.Take(&parent, "id = ?", parent.ID)
			db.Take(&group, "id = ?", group.ID)
			var calls atomic.Int32
			algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if mode == "keyless" {
					if body["api_key"] != "" || body["skip_auth"] != true {
						t.Error("keyless request had auth")
					}
				} else if body["api_key"] != wantKey {
					t.Error("wrong key selected")
				}
				_, _ = w.Write([]byte(`{"status":"passed","code":"ok"}`))
			}))
			defer algorithm.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
			rec := httptest.NewRecorder()
			ProbeGroupModel(rec, probeRequest(parent.ID, group.ID, model.ID))
			got := decodeProbeTest(t, rec)
			if mode == "disabled" || mode == "official-empty" {
				if got.Status != "failed" || calls.Load() != 0 {
					t.Fatal("disabled or required key bypassed")
				}
			} else if got.Status != "passed" || calls.Load() != 1 {
				t.Fatalf("credential mode failed: %s", rec.Body.String())
			}
			var savedParent orm.UserModelProvider
			var savedGroup orm.UserModelProviderGroup
			db.Take(&savedParent, "id = ?", parent.ID)
			db.Take(&savedGroup, "id = ?", group.ID)
			if !reflect.DeepEqual(savedParent, parent) || !reflect.DeepEqual(savedGroup, group) {
				t.Fatal("credential read wrote or migrated records")
			}
		})
	}
}

func TestProbeUnsupportedTypesNeverReachAlgorithm(t *testing.T) {
	for _, typ := range []string{"", "future-task", "rerank"} {
		t.Run(typ, func(t *testing.T) {
			db, parent, group, model := setupProbeTest(t)
			db.Model(&parent).Update("protocol_name", "OpenRouter")
			db.Model(&model).Update("model_type", typ)
			var calls atomic.Int32
			algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			defer algorithm.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
			rec := httptest.NewRecorder()
			ProbeGroupModel(rec, probeRequest(parent.ID, group.ID, model.ID))
			if got := decodeProbeTest(t, rec); got.Status != "unsupported" || calls.Load() != 0 {
				t.Fatalf("unsupported model called algorithm: %s", rec.Body.String())
			}
		})
	}
}

func TestProbeLMDeployUsesOpenAIWithTheConfiguredRoot(t *testing.T) {
	db, parent, group, model := setupProbeTest(t)
	if err := db.Model(&parent).Updates(map[string]any{"name": "LMDeploy", "protocol_name": "LMDeploy", "is_custom": false, "base_url": "https://deploy.example/api/v3"}).Error; err != nil {
		t.Fatal(err)
	}
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body modelProbeSnapshot
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Source != "OpenAI" || body.URL != "https://deploy.example/api/v3/" {
			t.Errorf("unexpected local supplier request: source=%s url=%s", body.Source, body.URL)
		}
		_, _ = w.Write([]byte(`{"status":"passed","code":"ok"}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	rec := httptest.NewRecorder()
	ProbeGroupModel(rec, probeRequest(parent.ID, group.ID, model.ID))
	if got := decodeProbeTest(t, rec); got.Status != "passed" {
		t.Fatal(rec.Body.String())
	}
}

func TestProbeBoundsFailuresAndRejectsPayloadOverrides(t *testing.T) {
	_, parent, group, model := setupProbeTest(t)
	for _, fixture := range []struct {
		name, body string
		status     int
		want       string
	}{
		{"upstream error", "secret-api-key https://private.example", 502, "upstream_error"},
		{"invalid json", "secret-api-key", 200, "invalid_response"},
		{"untrusted code", `{"status":"passed","code":"secret-api-key"}`, 200, "invalid_response"},
		{"valid failure", `{"status":"failed","code":"authentication_failed","message":"secret-api-key"}`, 200, "authentication_failed"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(fixture.status)
				_, _ = w.Write([]byte(fixture.body))
			}))
			defer algorithm.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
			rec := httptest.NewRecorder()
			ProbeGroupModel(rec, probeRequest(parent.ID, group.ID, model.ID))
			got := decodeProbeTest(t, rec)
			if got.Code != fixture.want || strings.Contains(rec.Body.String(), "secret-api-key") || strings.Contains(rec.Body.String(), "private.example") {
				t.Fatalf("unsafe outcome %s", rec.Body.String())
			}
		})
	}
	req := probeRequest(parent.ID, group.ID, model.ID)
	req.Body = http.NoBody
	req = req.WithContext(context.Background())
	req.Body = io.NopCloser(strings.NewReader(`{"api_key":"override","model_type":"tts"}`))
	rec := httptest.NewRecorder()
	ProbeGroupModel(rec, req)
	if rec.Code != 400 {
		t.Fatal("caller could override saved probe configuration")
	}
}

func TestProbeTimeoutAndDuplicateRequest(t *testing.T) {
	_, parent, group, model := setupProbeTest(t)
	entered, release := make(chan struct{}), make(chan struct{})
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"status":"passed","code":"ok"}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		probeGroupModel(first, probeRequest(parent.ID, group.ID, model.ID), 20*time.Millisecond)
		close(done)
	}()
	<-entered
	second := httptest.NewRecorder()
	ProbeGroupModel(second, probeRequest(parent.ID, group.ID, model.ID))
	if got := decodeProbeTest(t, second); got.Code != "probe_in_progress" {
		t.Fatalf("duplicate request not rejected: %s", second.Body.String())
	}
	<-done
	close(release)
	if got := decodeProbeTest(t, first); got.Code != "timeout" {
		t.Fatalf("timeout misclassified: %s", first.Body.String())
	}
}

func TestVLLMDiscoveryUsesExactMaxModelLenOnlyForVLLM(t *testing.T) {
	entries := parseRemoteModelEntries([]byte(`{"data":[{"id":"local-chat","model_type":"llm","max_model_len":262144}]}`))
	got := remoteModelItem(entries[0], "VLLM", nil)
	if got.MaxInputTokens == nil || *got.MaxInputTokens != "262144" {
		t.Fatalf("missing vLLM max_model_len: %+v", got)
	}
	if got := remoteModelItem(entries[0], "OpenAI", nil); got.MaxInputTokens != nil {
		t.Fatal("vLLM-only field applied to other supplier")
	}
	entries[0].ModelType = ""
	if got := remoteModelItem(entries[0], "VLLM", nil); got.ModelType != "" || got.MaxInputTokens == nil || *got.MaxInputTokens != "262144" {
		t.Fatal("vLLM unknown alias lost explicit context or guessed a type")
	}
	entries[0].ContextLength = 128000
	if got := remoteModelItem(entries[0], "VLLM", nil); got.MaxInputTokens == nil || *got.MaxInputTokens != "128000" {
		t.Fatal("explicit context length did not take priority")
	}
}

func TestVLLMIdentitySurvivesSelectingOpenAIProtocol(t *testing.T) {
	db, parent, group, model := setupProbeTest(t)
	template := orm.DefaultModelProvider{ID: "vllm-template", Name: "VLLM", BaseURL: "http://localhost:8000/v1/", Category: "model"}
	if err := db.Create(&template).Error; err != nil {
		t.Fatal(err)
	}
	parent.DefaultModelProviderID, parent.ProtocolName, parent.IsCustom = "vllm-template", "OpenAI", true
	parent.BaseURL = template.BaseURL
	parent.ActiveKeyID, parent.ActiveKeyGroupID, group.APIKey = "", "", ""
	db.Save(&parent)
	db.Save(&group)
	if providerAPIKeyRequiredForBaseURL(t.Context(), db, parent, parent.BaseURL) {
		t.Fatal("vLLM inherited supplier requires key after OpenAI protocol selection")
	}
	var source string
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		source, _ = body["source"].(string)
		_, _ = w.Write([]byte(`{"status":"passed","code":"ok"}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	rec := httptest.NewRecorder()
	ProbeGroupModel(rec, probeRequest(parent.ID, group.ID, model.ID))
	if got := decodeProbeTest(t, rec); got.Status != "passed" || source != "VLLM" {
		t.Fatalf("vLLM profile lost after protocol save: %s source=%s", rec.Body.String(), source)
	}
}

func TestProbeDoesNotReportSuccessForChangedConfiguration(t *testing.T) {
	db, parent, group, model := setupProbeTest(t)
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := db.Model(&parent).Update("base_url", "https://changed.example/v1/").Error; err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"status":"passed","code":"ok"}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	rec := httptest.NewRecorder()
	ProbeGroupModel(rec, probeRequest(parent.ID, group.ID, model.ID))
	if got := decodeProbeTest(t, rec); got.Status != "failed" || got.Code != "configuration_changed" {
		t.Fatalf("stale success was exposed: %s", rec.Body.String())
	}
}

func TestProbeResponseReadTimeoutHasStableCode(t *testing.T) {
	_, parent, group, model := setupProbeTest(t)
	release := make(chan struct{})
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); w.(http.Flusher).Flush(); <-release }))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	rec := httptest.NewRecorder()
	probeGroupModel(rec, probeRequest(parent.ID, group.ID, model.ID), 20*time.Millisecond)
	close(release)
	if got := decodeProbeTest(t, rec); got.Code != "timeout" {
		t.Fatalf("response read timeout misclassified: %s", rec.Body.String())
	}
}

func TestVLLMKeylessDiscoveryKeepsUnknownCapacityAfterProtocolSave(t *testing.T) {
	db, parent, group, _ := setupProbeTest(t)
	remoteModelsAllowPrivateHosts = true
	t.Cleanup(func() { remoteModelsAllowPrivateHosts = false })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("keyless vLLM sent Authorization")
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen38-27b","max_model_len":262144}]}`))
	}))
	defer upstream.Close()
	parent.Name, parent.ProtocolName, parent.IsCustom, parent.ActiveKeyID, parent.ActiveKeyGroupID, parent.BaseURL = "VLLM", "OpenAI", false, "", "", upstream.URL+"/v1/"
	group.APIKey, group.BaseURL = "", parent.BaseURL
	db.Save(&parent)
	db.Save(&group)
	req := probeRequest(parent.ID, group.ID, "")
	req.Method = http.MethodGet
	rec := httptest.NewRecorder()
	ListRemoteGroupModels(rec, req)
	var body struct {
		Data remoteGroupModelsResponse `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 200 || len(body.Data.Models) != 1 || body.Data.Models[0].ModelType != "" || body.Data.Models[0].MaxInputTokens == nil || *body.Data.Models[0].MaxInputTokens != "262144" {
		t.Fatalf("vLLM discovery lost its explicit context: %s", rec.Body.String())
	}
}

func TestProbeReadsEndpointAndActiveKeyUnderParentLock(t *testing.T) {
	db, parent, group, model := setupProbeTest(t)
	if db.Dialector.Name() != "postgres" {
		t.Skip("requires PostgreSQL row locking; the remaining probe tests cover SQLite")
	}
	requests := make(chan modelProbeSnapshot, 1)
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body modelProbeSnapshot
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests <- body
		_, _ = w.Write([]byte(`{"status":"passed","code":"ok"}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	writer := db.Begin()
	defer writer.Rollback()
	if err := writer.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&parent, "id = ?", parent.ID).Error; err != nil {
		t.Fatal(err)
	}
	parent.BaseURL, parent.ActiveKeyID = "https://replacement.example/v1/", groupKeyID(group.ID, "replacement-test-key")
	group.APIKey = "replacement-test-key"
	if err := writer.Save(&parent).Error; err != nil {
		t.Fatal(err)
	}
	if err := writer.Save(&group).Error; err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		ProbeGroupModel(rec, probeRequest(parent.ID, group.ID, model.ID))
		done <- rec
	}()
	select {
	case <-requests:
		t.Fatal("probe bypassed the supplier writer lock")
	case <-time.After(50 * time.Millisecond):
	}
	if err := writer.Commit().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case rec := <-done:
		if got := decodeProbeTest(t, rec); got.Status != "passed" {
			t.Fatalf("probe did not use committed configuration: %s", rec.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not release the database lock before HTTP")
	}
	body := <-requests
	if body.URL != parent.BaseURL || body.APIKey != group.APIKey {
		t.Fatal("probe mixed endpoint and key from different saved configurations")
	}
}
