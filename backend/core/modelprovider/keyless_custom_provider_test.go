package modelprovider

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestKeylessCustomProviderCanDiscoverBeforeVerificationAndConnect(t *testing.T) {
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "keyless-custom-test-secret")
	remoteModelsAllowPrivateHosts = true
	t.Cleanup(func() { remoteModelsAllowPrivateHosts = false; store.Init(nil, nil, nil) })
	var discoveryCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		discoveryCalls++
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
			t.Errorf("keyless discovery request=%s headers=%v", r.URL, r.Header)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"local-qwen","model_type":"llm"}]}`))
	}))
	defer upstream.Close()
	db := orm.MigrateTestDB(t, &orm.DefaultModelProvider{}, &orm.DefaultModel{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}).DB
	store.Init(db, db, nil)
	base := orm.BaseModel{CreateUserID: "owner", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	parent := orm.UserModelProvider{ID: "custom", DefaultModelProviderID: "missing-catalog", Name: "qwen自用", IsCustom: true, ProtocolName: "OpenAI", Category: "model", Capabilities: "has_models", BaseURL: upstream.URL + "/", BaseModel: base}
	group := orm.UserModelProviderGroup{ID: "custom-group", UserModelProviderID: parent.ID, Name: parent.Name, BaseURL: parent.BaseURL, BaseModel: base}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	request := func(method, body string) *http.Request {
		req := httptest.NewRequest(method, "/provider", strings.NewReader(body))
		req.Header.Set("X-User-Id", "owner")
		return mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	}
	discovery := httptest.NewRecorder()
	ListRemoteGroupModels(discovery, request(http.MethodGet, ""))
	if discovery.Code != http.StatusOK || discoveryCalls != 1 {
		t.Fatalf("keyless discovery=%d calls=%d: %s", discovery.Code, discoveryCalls, discovery.Body.String())
	}
	if err := db.Take(&group, "id = ?", group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if group.IsVerified {
		t.Fatal("discovery alone must not verify a connection")
	}
	if _, _, err := ResolveProviderRuntimeCredential(t.Context(), db, group.ID); !errors.Is(err, ErrProviderKeyUnverified) {
		t.Fatalf("unverified endpoint reached runtime: %v", err)
	}
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body algoModelCheckBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Source != "openai" || body.APIKey != "" || !body.SkipAuth || body.URL != upstream.URL+"/v1/" {
			t.Errorf("keyless connection check=%+v", body)
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	checked := httptest.NewRecorder()
	CheckGroup(checked, request(http.MethodPost, `{}`))
	if checked.Code != http.StatusOK {
		t.Fatalf("keyless check=%d: %s", checked.Code, checked.Body.String())
	}
	url, key, err := ResolveProviderRuntimeCredential(t.Context(), db, group.ID)
	if err != nil || key != "" || url != upstream.URL+"/v1/" {
		t.Fatalf("verified keyless runtime url=%q key=%q err=%v", url, key, err)
	}
	items := buildListItems(t.Context(), db, []orm.UserModelProvider{parent}, "zh-CN")
	if len(items) != 1 || !items[0].IsConfigured {
		t.Fatalf("verified keyless custom supplier marked unconfigured: %+v", items)
	}
}

func TestKeylessDiscoveryDoesNotBypassOfficialOrDisabledCredentials(t *testing.T) {
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "keyless-credential-test-secret")
	remoteModelsAllowPrivateHosts = true
	t.Cleanup(func() { remoteModelsAllowPrivateHosts = false; store.Init(nil, nil, nil) })
	for _, tc := range []struct {
		name, storedKey  string
		custom, official bool
	}{
		{name: "standard supplier"},
		{name: "custom at official URL", custom: true, official: true},
		{name: "disabled saved key", custom: true, storedKey: "disabled-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusOK) }))
			defer upstream.Close()
			db := orm.MigrateTestDB(t, &orm.DefaultModelProvider{}, &orm.DefaultModel{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}).DB
			store.Init(db, db, nil)
			base := orm.BaseModel{CreateUserID: "owner", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			parent := orm.UserModelProvider{ID: "supplier", DefaultModelProviderID: "catalog", Name: "OpenAI", IsCustom: tc.custom, ProtocolName: "OpenAI", Category: "model", Capabilities: "has_models", BaseURL: upstream.URL + "/v1/", ActiveKeyInitialized: true, BaseModel: base}
			group := orm.UserModelProviderGroup{ID: "group", UserModelProviderID: parent.ID, Name: parent.Name, BaseURL: parent.BaseURL, APIKey: tc.storedKey, BaseModel: base}
			if tc.official {
				if err := db.Create(&orm.DefaultModelProvider{ID: "catalog", Name: "OpenAI", BaseURL: parent.BaseURL}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&parent).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&group).Error; err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/remote_models", nil)
			req.Header.Set("X-User-Id", "owner")
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
			rec := httptest.NewRecorder()
			ListRemoteGroupModels(rec, req)
			if rec.Code != http.StatusBadRequest || calls != 0 {
				t.Fatalf("unauthorized discovery=%d calls=%d: %s", rec.Code, calls, rec.Body.String())
			}
		})
	}
}
