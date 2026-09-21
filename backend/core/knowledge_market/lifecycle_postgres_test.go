package knowledge_market

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestMarketConcurrentActionsPostgres(t *testing.T) {
	if os.Getenv("TEST_DB_DRIVER") != "postgres" {
		t.Skip("requires isolated PostgreSQL test schema")
	}
	db := orm.OpenTestDB(t).DB
	if err := db.AutoMigrate(&orm.KnowledgeMarketItem{}, &orm.KnowledgeMarketInstall{}, &orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	store.Init(db, db, nil)
	if err := SeedCatalog(context.Background(), db, writeCatalog(t, handlerTestCatalog)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.KnowledgeMarketInstall{MarketItemID: "law-cn", UserID: "user-test", DatasetID: "ds-test", InstallState: "done", Config: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, kind := range []string{installJobType, updateJobType} {
		go func(kind string) {
			<-start
			_, err := enqueueMarketItem(ctx, db, asyncjob.EnqueueRequest{JobType: kind, ResourceType: "knowledge_market_item", ResourceID: "law-cn", IdempotencyKey: kind + ":user-test", CreateUserID: "user-test"}, "")
			results <- err
		}(kind)
	}
	close(start)
	succeeded, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			succeeded++
		} else if errors.Is(err, errMarketBusy) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicts != 1 {
		t.Fatalf("concurrent actions: %d accepted, %d conflicts", succeeded, conflicts)
	}
	var count int64
	if err := db.Model(&orm.AsyncJob{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("created %d jobs for conflicting actions", count)
	}
}
