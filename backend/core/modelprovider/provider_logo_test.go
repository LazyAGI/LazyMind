package modelprovider

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func providerLogoImageFixture(t *testing.T, size int, metadata bool) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	img.Set(0, 0, color.NRGBA{R: 40, G: 80, B: 160, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	raw := encoded.Bytes()
	if metadata {
		payload := []byte("Comment\x00private-upload-metadata")
		chunk := make([]byte, 12+len(payload))
		binary.BigEndian.PutUint32(chunk[:4], uint32(len(payload)))
		copy(chunk[4:8], "tEXt")
		copy(chunk[8:], payload)
		binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
		raw = append(append(append([]byte(nil), raw[:len(raw)-12]...), chunk...), raw[len(raw)-12:]...)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
}

func providerLogoJSONBody(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func logoResponseData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var response struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Data
}

func TestCustomProviderLogoCreateUpdateAndClearPreserveConfiguration(t *testing.T) {
	db, source, _, _ := batchModelFixture(t)
	input := providerLogoImageFixture(t, 128, true)
	call := func(id string, fields map[string]any, update bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/custom", bytes.NewReader(providerLogoJSONBody(t, fields)))
		req.Header.Set("X-User-Id", source.CreateUserID)
		req = mux.SetURLVars(req, map[string]string{"model_provider_id": id})
		rec := httptest.NewRecorder()
		if update {
			UpdateCustomProvider(rec, req)
		} else {
			CreateCustomProvider(rec, req)
		}
		return rec
	}
	rec := call("", map[string]any{"name": "My supplier", "source_provider_id": source.ID, "base_url": "https://my.example/v1/", "logo_data_url": input}, false)
	if rec.Code != 200 {
		t.Fatalf("create=%d %s", rec.Code, rec.Body.String())
	}
	data := logoResponseData(t, rec)
	logo, _ := data["logo_data_url"].(string)
	if logo == "" || logo == input {
		t.Fatal("supplier logo absent or metadata was not stripped")
	}
	decoded, err := base64.StdEncoding.DecodeString(logo[len("data:image/png;base64,"):])
	if err != nil || bytes.Contains(decoded, []byte("private-upload-metadata")) {
		t.Fatal("supplier logo retained nonpixel metadata")
	}
	id := data["id"].(string)
	var parent orm.UserModelProvider
	if err := db.Take(&parent, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	var group orm.UserModelProviderGroup
	if err := db.Take(&group, "user_model_provider_id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	parentBefore, groupBefore := parent, group
	if out := call(id, map[string]any{"name": "Renamed"}, true); out.Code != 200 || logoResponseData(t, out)["logo_data_url"] != logo {
		t.Fatalf("rename did not preserve logo: %s", out.Body.String())
	}
	for _, fields := range []map[string]any{{"logo_data_url": providerLogoImageFixture(t, 2, false)}, {"logo_data_url": ""}} {
		out := call(id, fields, true)
		if out.Code != 200 {
			t.Fatalf("logo-only update=%d %s", out.Code, out.Body.String())
		}
		if fields["logo_data_url"] == "" && logoResponseData(t, out)["logo_data_url"] != nil {
			t.Fatal("cleared logo still returned an uploaded image")
		}
	}
	if err := db.Take(&parent, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Take(&group, "id = ?", group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if parent.ID != parentBefore.ID || parent.DefaultModelProviderID != parentBefore.DefaultModelProviderID || parent.ProtocolName != parentBefore.ProtocolName || parent.BaseURL != parentBefore.BaseURL || parent.RequestConfig != parentBefore.RequestConfig || parent.ActiveKeyID != parentBefore.ActiveKeyID || parent.ActiveKeyRevision != parentBefore.ActiveKeyRevision || group.APIKey != groupBefore.APIKey || group.APIKeyCiphertext != groupBefore.APIKeyCiphertext || group.CredentialRevision != groupBefore.CredentialRevision || group.IsVerified != groupBefore.IsVerified {
		t.Fatal("supplier logo changed credentials or protocol")
	}
	if parent.Name != "Renamed" || parent.LogoDataURL != "" {
		t.Fatal("logo-only update renamed supplier or failed to clear its logo")
	}
}

func TestCustomProviderLogoRejectsInvalidImages(t *testing.T) {
	valid := providerLogoImageFixture(t, 1, false)
	for name, logo := range map[string]string{"remote": "https://images.example/logo.png", "svg": "data:image/svg+xml;base64,PHN2Zz4=", "fake": "data:image/png;base64,aGVsbG8=", "badbase64": "data:image/png;base64,%%%", "dimensions": providerLogoImageFixture(t, 129, false), "bytes": "data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, 96*1024+1)), "truncated": valid[:len(valid)-8], "linebreak": valid + "\n"} {
		t.Run(name, func(t *testing.T) {
			db, source, _, _ := batchModelFixture(t)
			body := providerLogoJSONBody(t, map[string]any{"name": "Bad", "source_provider_id": source.ID, "base_url": "https://new.example/v1/", "logo_data_url": logo})
			req := httptest.NewRequest(http.MethodPost, "/custom", bytes.NewReader(body))
			req.Header.Set("X-User-Id", source.CreateUserID)
			rec := httptest.NewRecorder()
			CreateCustomProvider(rec, req)
			if rec.Code != 400 {
				t.Fatalf("invalid logo accepted: %d", rec.Code)
			}
			var count int64
			db.Model(&orm.UserModelProvider{}).Count(&count)
			if count != 1 {
				t.Fatal("invalid image persisted a supplier")
			}
		})
	}
}

func TestCustomProviderLogoInheritedByModelAndSelectedMetadata(t *testing.T) {
	db, parent, group, _ := batchModelFixture(t)
	if err := db.AutoMigrate(&orm.UserSelectedModel{}); err != nil {
		t.Fatal(err)
	}
	logo := providerLogoImageFixture(t, 2, false)
	req := batchModelRequest(parent.ID, group.ID, parent.CreateUserID, providerLogoJSONBody(t, map[string]any{"logo_data_url": logo}))
	rec := httptest.NewRecorder()
	UpdateCustomProvider(rec, req)
	if rec.Code != 200 {
		t.Fatalf("logo-only=%d %s", rec.Code, rec.Body.String())
	}
	if db.Migrator().HasColumn(&orm.UserModelProviderGroupModel{}, "logo_data_url") {
		t.Fatal("logo stored on model instead of supplier")
	}
	model := orm.UserModelProviderGroupModel{ID: "inherited-model", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, Name: "qwen-custom", ProviderName: "OpenAI", ModelType: "llm", BaseModel: parent.BaseModel}
	if err := db.Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	selection := orm.UserSelectedModel{UserID: parent.CreateUserID, ModelKey: "llm", UserModelProviderGroupModelID: model.ID, Share: true}
	if err := db.Create(&selection).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, key string
		handler   http.HandlerFunc
	}{{"models", "models", ListGroupModels}, {"options", "models", ListUserModelsByModelType}, {"selected", "selections", GetSelectedModels}} {
		t.Run(tc.name, func(t *testing.T) {
			request := batchModelRequest(parent.ID, group.ID, parent.CreateUserID, nil)
			request.URL.RawQuery = "model_type=llm"
			out := httptest.NewRecorder()
			tc.handler(out, request)
			if out.Code != 200 {
				t.Fatal(out.Body.String())
			}
			items, ok := logoResponseData(t, out)[tc.key].([]any)
			if !ok || len(items) != 1 || items[0].(map[string]any)["logo_data_url"] != logo {
				t.Fatalf("supplier logo not inherited: %s", out.Body.String())
			}
		})
	}
	detail, err := getSharedModelDetail(t.Context(), db, "llm")
	if err != nil || detail == nil || detail.LogoDataURL != logo {
		t.Fatalf("shared supplier logo missing: %+v %v", detail, err)
	}
	if err := db.Take(&parent, "id = ?", parent.ID).Error; err != nil {
		t.Fatal(err)
	}
	items := buildListItems(t.Context(), nil, []orm.UserModelProvider{parent}, "zh-CN")
	raw, _ := json.Marshal(items)
	var listed []map[string]any
	_ = json.Unmarshal(raw, &listed)
	if len(listed) != 1 || listed[0]["logo_data_url"] != logo {
		t.Fatal("supplier list does not return its logo")
	}
	// A stale model editor cannot write an independent per-model logo.
	modelReq := batchModelRequest(parent.ID, group.ID, parent.CreateUserID, providerLogoJSONBody(t, map[string]any{"name": model.Name, "logo_data_url": "data:image/png;base64,invalid"}))
	modelReq = mux.SetURLVars(modelReq, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID, "model_id": model.ID})
	out := httptest.NewRecorder()
	UpdateGroupModel(out, modelReq)
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	if err := db.Take(&parent, "id = ?", parent.ID).Error; err != nil || parent.LogoDataURL != logo {
		t.Fatal("model request edited supplier logo")
	}
}

func TestCustomProviderLogoIsOptionalPrivateAndDoesNotReadCredentials(t *testing.T) {
	db, parent, group, _ := batchModelFixture(t)
	logo := providerLogoImageFixture(t, 1, false)
	if err := db.Model(&parent).Update("logo_data_url", logo).Error; err != nil {
		t.Fatal(err)
	}
	invalidRequest := batchModelRequest(parent.ID, group.ID, parent.CreateUserID, providerLogoJSONBody(t, map[string]any{"logo_data_url": "data:image/png;base64,invalid"}))
	invalidResponse := httptest.NewRecorder()
	UpdateCustomProvider(invalidResponse, invalidRequest)
	if invalidResponse.Code != 400 {
		t.Fatal("invalid logo update accepted")
	}
	if err := db.Take(&parent, "id = ?", parent.ID).Error; err != nil || parent.LogoDataURL != logo {
		t.Fatal("rejected logo update changed the supplier")
	}
	request := batchModelRequest(parent.ID, group.ID, parent.CreateUserID, providerLogoJSONBody(t, map[string]any{"name": "Default cube", "source_provider_id": parent.ID, "base_url": "https://new.example/v1/"}))
	rec := httptest.NewRecorder()
	CreateCustomProvider(rec, request)
	if rec.Code != 200 || logoResponseData(t, rec)["logo_data_url"] != nil {
		t.Fatalf("omitted upload inherited source logo: %s", rec.Body.String())
	}
	if err := db.Model(&group).Updates(map[string]any{"api_key_ciphertext": "unreadable-fixture-key", "credential_version": 1, "credential_revision": 7, "is_verified": true}).Error; err != nil {
		t.Fatal(err)
	}
	request = batchModelRequest(parent.ID, group.ID, parent.CreateUserID, providerLogoJSONBody(t, map[string]any{"logo_data_url": ""}))
	rec = httptest.NewRecorder()
	UpdateCustomProvider(rec, request)
	if rec.Code != 200 {
		t.Fatalf("logo-only depended on decrypting keys: %d %s", rec.Code, rec.Body.String())
	}
	if err := db.Take(&group, "id = ?", group.ID).Error; err != nil || group.APIKeyCiphertext != "unreadable-fixture-key" || group.CredentialRevision != 7 || !group.IsVerified {
		t.Fatal("logo-only altered credential state")
	}
	request = batchModelRequest(parent.ID, group.ID, "another-user", providerLogoJSONBody(t, map[string]any{"logo_data_url": logo}))
	rec = httptest.NewRecorder()
	UpdateCustomProvider(rec, request)
	if rec.Code != 404 {
		t.Fatal("foreign supplier logo edit allowed")
	}
	if err := db.Model(&parent).Update("is_custom", false).Error; err != nil {
		t.Fatal(err)
	}
	request = batchModelRequest(parent.ID, group.ID, parent.CreateUserID, providerLogoJSONBody(t, map[string]any{"logo_data_url": logo}))
	rec = httptest.NewRecorder()
	UpdateCustomProvider(rec, request)
	if rec.Code != 400 {
		t.Fatal("builtin brand logo edit allowed")
	}
}
