package paperguide

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
)

func guidePostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN not configured")
	}
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	admin, e := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	name := "paper_guide_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if e = admin.Exec("CREATE DATABASE " + name).Error; e != nil {
		t.Fatal(e)
	}
	u.Path = "/" + name
	db, e := gorm.Open(postgres.Open(u.String()), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		_ = admin.Exec("DROP DATABASE " + name + " WITH (FORCE)").Error
		adminSQL, _ := admin.DB()
		_ = adminSQL.Close()
	})
	if e = db.AutoMigrate(&orm.AsyncJob{}, &orm.PaperGuide{}, &orm.PaperGuideRevision{}, &orm.PaperGuideStep{}, &orm.PaperGuideProgress{}); e != nil {
		t.Fatal(e)
	}
	return db
}
func TestPostgresConcurrentGenerateHasOneCandidate(t *testing.T) {
	repo := Repository{DB: guidePostgres(t)}
	scope := Scope{OwnerID: "u", DatasetID: "d", DocumentID: "p", Language: "zh"}
	ids := make(chan string, 12)
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			row, e := repo.Create(context.Background(), CreateRevision{Scope: scope, SourceSHA256: "hash", Model: ModelIdentity{ID: "m"}, IdempotencyKey: fmt.Sprintf("request-%d", i)})
			if e != nil {
				errs <- e
			} else {
				ids <- row.ID
			}
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	id := ""
	for next := range ids {
		if id != "" && next != id {
			t.Fatal("multiple active candidates")
		}
		id = next
	}
	var count int64
	repo.DB.Model(&orm.PaperGuideRevision{}).Count(&count)
	if count != 1 {
		t.Fatalf("revisions=%d", count)
	}
}
