package knowledge_market

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func testJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestDecodeInstallPayload(t *testing.T) {
	raw := json.RawMessage(`{"market_item_id":" law-cn ","user_id":" u1 ","revision":" master "}`)
	p, err := decodeInstallPayload(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.MarketItemID != "law-cn" || p.UserID != "u1" || p.Revision != "master" {
		t.Fatalf("unexpected payload %+v", p)
	}

	if _, err := decodeInstallPayload(json.RawMessage(`{"user_id":"u1"}`)); err == nil {
		t.Fatal("expected error for missing market_item_id")
	}
	if _, err := decodeInstallPayload(json.RawMessage(`{`)); err == nil {
		t.Fatal("expected error for malformed payload")
	}
}

func TestTagsFromItem(t *testing.T) {
	item := orm.KnowledgeMarketItem{Tags: json.RawMessage(`["法律法规","司法解释"]`)}
	tags := tagsFromItem(item)
	if len(tags) != 2 || tags[0] != "法律法规" {
		t.Fatalf("unexpected tags %v", tags)
	}
}

func TestSetInstallStateUpsert(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.KnowledgeMarketInstall{}); err != nil {
		t.Fatalf("auto migrate installs: %v", err)
	}
	payload := installJobPayload{MarketItemID: "law-cn", UserID: "u1", Revision: "master"}

	// Upsert twice; the row must be updated in place (single PK pair).
	if err := setInstallState(context.Background(), db, payload.MarketItemID, payload.UserID, orm.InstallStateDownloading, "", nil); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	cfg := &installConfig{Revision: "master", Files: []fileSnapshot{{Path: "law.jsonl", Size: 5, SHA256: "abc"}}}
	if err := setInstallState(context.Background(), db, payload.MarketItemID, payload.UserID, orm.InstallStateDone, "ds_1", cfg); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	var rows []orm.KnowledgeMarketInstall
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%d, want 1", len(rows))
	}
	row := rows[0]
	if row.InstallState != string(orm.InstallStateDone) || row.DatasetID != "ds_1" {
		t.Fatalf("unexpected row %+v", row)
	}
	if row.InstalledAt == nil {
		t.Fatal("installed_at must be set on done")
	}
	if !json.Valid(row.Config) {
		t.Fatalf("invalid config json %s", row.Config)
	}
}

func TestFailInstallCleansResidualDataset(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.KnowledgeMarketInstall{}, &orm.Dataset{}, &orm.EvalSet{}, &orm.DefaultDataset{}); err != nil {
		t.Fatalf("auto migrate install models: %v", err)
	}
	store.Init(db, db, nil)

	now := time.Now().UTC()
	if err := db.Create(&orm.KnowledgeMarketInstall{
		MarketItemID:     "law-cn",
		UserID:           "u1",
		InstalledVersion: "v1.0.0",
		DatasetID:        "ds_residual",
		InstallState:     string(orm.InstallStateImporting),
		CreatedAt:        now,
		UpdatedAt:        now,
		Config:           json.RawMessage(`{}`),
	}).Error; err != nil {
		t.Fatalf("seed install row: %v", err)
	}
	if err := db.Create(&orm.Dataset{
		ID:           "ds_residual",
		KbID:         "ds_residual",
		DisplayName:  "Residual KB",
		DatasetState: 0,
		ShareType:    0,
		Type:         1,
		Ext:          json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "u1",
			CreateUserName: "u1",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("seed residual dataset: %v", err)
	}

	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/kbs/ds_residual" {
			t.Errorf("unexpected request %s %q", r.Method, r.URL.Path)
			return testJSONResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
		return testJSONResponse(http.StatusOK, `{}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")

	payload := installJobPayload{MarketItemID: "law-cn", UserID: "u1"}
	res, err := failInstall(context.Background(), db, payload, errors.New("boom"))
	if err == nil {
		t.Fatal("expected failInstall to return the original error")
	}
	if res.ErrorCode != asyncjob.ErrorCodeHandlerFailed {
		t.Fatalf("unexpected error code %q", res.ErrorCode)
	}

	var install orm.KnowledgeMarketInstall
	if err := db.Where("market_item_id = ? AND user_id = ?", "law-cn", "u1").Take(&install).Error; err != nil {
		t.Fatalf("load install row: %v", err)
	}
	if install.InstallState != string(orm.InstallStateFailed) {
		t.Fatalf("install_state = %q, want %q", install.InstallState, orm.InstallStateFailed)
	}
	if install.DatasetID != "" {
		t.Fatalf("dataset_id = %q, want cleared", install.DatasetID)
	}

	var ds orm.Dataset
	if err := db.Where("id = ?", "ds_residual").Take(&ds).Error; err != nil {
		t.Fatalf("load residual dataset: %v", err)
	}
	if ds.DeletedAt == nil {
		t.Fatal("expected residual dataset to be soft deleted")
	}
}
