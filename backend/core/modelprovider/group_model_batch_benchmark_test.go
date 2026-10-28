package modelprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type batchModelSQLCounter struct {
	logger.Interface
	count atomic.Int64
}

func (counter *batchModelSQLCounter) Trace(ctx context.Context, begin time.Time, statement func() (string, int64), err error) {
	counter.count.Add(1)
	counter.Interface.Trace(ctx, begin, statement, err)
}

func batchModelFixture(t testing.TB) (*gorm.DB, orm.UserModelProvider, orm.UserModelProviderGroup, *batchModelSQLCounter) {
	t.Helper()
	db := orm.MigrateTestDB(t, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}).DB
	counter := &batchModelSQLCounter{Interface: logger.Default.LogMode(logger.Silent)}
	db = db.Session(&gorm.Session{Logger: counter})
	base := orm.BaseModel{CreateUserID: "batch-owner", CreateUserName: "Batch owner", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	parent := orm.UserModelProvider{ID: "batch-provider", Name: "Local gateway", ProtocolName: "OpenAI", IsCustom: true, BaseURL: "https://batch.example/v1/", Category: "model", Capabilities: "has_models,custom_base_url", BaseModel: base}
	group := orm.UserModelProviderGroup{ID: "batch-group", UserModelProviderID: parent.ID, Name: parent.Name, BaseURL: parent.BaseURL, BaseModel: base}
	for _, row := range []any{&parent, &group} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	counter.count.Store(0)
	return db, parent, group, counter
}

func batchModelRequest(parent, group, user string, body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/models:batch", strings.NewReader(string(body)))
	req.Header.Set("X-User-Id", user)
	return mux.SetURLVars(req, map[string]string{"model_provider_id": parent, "group_id": group})
}

func BenchmarkGroupModelImportSequential600(b *testing.B) {
	const modelCount = 600
	var statements int64
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		_, parent, group, counter := batchModelFixture(b)
		b.StartTimer()
		for index := 0; index < modelCount; index++ {
			raw, _ := json.Marshal(addGroupModelRequest{Name: fmt.Sprintf("batch-model-%04d", index), ModelType: "llm"})
			rec := httptest.NewRecorder()
			AddGroupModel(rec, batchModelRequest(parent.ID, group.ID, parent.CreateUserID, raw))
			if rec.Code != http.StatusOK {
				b.Fatalf("single add=%d %s", rec.Code, rec.Body.String())
			}
		}
		statements += counter.count.Load()
	}
	b.ReportMetric(float64(statements)/float64(b.N), "sql/import")
	b.ReportMetric(float64(modelCount), "models/import")
}

func BenchmarkGroupModelImportBatch600(b *testing.B) {
	const modelCount = 600
	var statements int64
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		_, parent, group, counter := batchModelFixture(b)
		b.StartTimer()
		models := make([]addGroupModelRequest, modelCount)
		for index := range models {
			models[index] = addGroupModelRequest{Name: fmt.Sprintf("batch-model-%04d", index), ModelType: "llm"}
		}
		raw, _ := json.Marshal(batchAddGroupModelsRequest{Models: models})
		rec := httptest.NewRecorder()
		BatchAddGroupModels(rec, batchModelRequest(parent.ID, group.ID, parent.CreateUserID, raw))
		if rec.Code != http.StatusOK {
			b.Fatalf("batch add=%d %s", rec.Code, rec.Body.String())
		}
		statements += counter.count.Load()
	}
	b.ReportMetric(float64(statements)/float64(b.N), "sql/import")
	b.ReportMetric(float64(modelCount), "models/import")
}
