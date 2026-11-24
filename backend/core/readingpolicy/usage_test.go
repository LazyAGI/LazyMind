package readingpolicy

import (
	"context"
	"errors"
	"fmt"
	"lazymind/core/common/orm"
	"sync"
	"testing"
	"time"
)

func TestIndependentPaperClaimsAndRetryAcrossDays(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserReadingPolicy{}, &orm.ReadingUsage{})
	p := Defaults()
	p.Preset = "full"
	p.Limits.DailyPDFDocuments = 2
	p.Limits.DailyStudyDocuments = 1
	if _, err := Save(context.Background(), db.DB, "u", p); err != nil {
		t.Fatal(err)
	}
	ctx := AutomaticContext(context.Background())
	day := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 100; i++ {
		if err := claimAt(ctx, db.DB, "u", AIStudy, "paper-a", day); err != nil {
			t.Fatal(err)
		}
	}
	if err := claimAt(ctx, db.DB, "u", AIStudy, "paper-b", day); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("study limit: %v", err)
	}
	for _, hash := range []string{"paper-a", "paper-b"} {
		if err := claimAt(ctx, db.DB, "u", PDFParse, hash, day); err != nil {
			t.Fatal(err)
		}
	}
	if err := claimAt(ctx, db.DB, "u", PDFParse, "paper-c", day); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("parse limit: %v", err)
	}
	if err := claimAt(ctx, db.DB, "u", AIStudy, "paper-a", day.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := claimAt(ctx, db.DB, "u", AIStudy, "paper-b", day.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&orm.ReadingUsage{}).Where("category=?", AIStudy).Count(&count)
	if count != 2 {
		t.Fatalf("one claim per paper across retries: %d", count)
	}
	if err := Claim(context.Background(), db.DB, "u", PDFParse, "manual"); err != nil {
		t.Fatal(err)
	}
	db.Model(&orm.ReadingUsage{}).Where("source_sha256=?", "manual").Count(&count)
	if count != 0 {
		t.Fatal("manual action consumed automatic quota")
	}
}

func TestConcurrentQuotaClaimsDoNotOverspend(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserReadingPolicy{}, &orm.ReadingUsage{})
	p := Defaults()
	p.Preset = "full"
	p.Limits.DailyStudyDocuments = 1
	if _, err := Save(context.Background(), db.DB, "u", p); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- Claim(AutomaticContext(context.Background()), db.DB, "u", AIStudy, fmt.Sprint(i))
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrQuotaExceeded) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("accepted %d claims", success)
	}
}
