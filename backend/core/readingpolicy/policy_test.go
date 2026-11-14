package readingpolicy

import (
	"context"
	"lazymind/core/common/orm"
	"testing"
	"time"
)

func TestDefaultsValidationAndIsolation(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserReadingPolicy{})
	ctx := context.Background()
	p, e := Load(ctx, db.DB, "one")
	if e != nil || p.Preset != "demand" || p.Limits.DailyDocuments != 5 || p.Modes[Formula] != OnDemand {
		t.Fatalf("defaults: %+v %v", p, e)
	}
	p.Preset = "full"
	saved, e := Save(ctx, db.DB, "one", p)
	if e != nil || saved.Modes[ChatPrepare] != OnDemand || saved.Modes[Figure] != Automatic || saved.AutomaticEligibleAfter == nil {
		t.Fatalf("full: %+v %v", saved, e)
	}
	other, _ := Load(ctx, db.DB, "two")
	if other.Modes[Figure] != OnDemand {
		t.Fatal("policy leaked")
	}
	p = saved
	p.Preset = "custom"
	p.Modes[PDFParse] = Disabled
	if _, e = Save(ctx, db.DB, "one", p); e == nil {
		t.Fatal("automatic studies need parser")
	}
	p = saved
	p.Limits.AutomaticConcurrency = 2
	if _, e = Save(ctx, db.DB, "one", p); e == nil {
		t.Fatal("unbounded concurrency")
	}
	if Eligible(saved, time.Now().Add(-time.Hour)) {
		t.Fatal("historical document eligible")
	}
	if !Eligible(saved, time.Now().Add(time.Second)) {
		t.Fatal("new document ineligible")
	}
	p = saved
	p.Preset = "demand"
	p, _ = Save(ctx, db.DB, "one", p)
	if Eligible(p, time.Now().Add(time.Second)) {
		t.Fatal("stopped automatic still eligible")
	}
}

func TestRegistryDoesNotAdvertiseRemovedPaperOverview(t *testing.T) {
	for _, capability := range Registry() {
		if capability.ID == "overview" {
			t.Fatal("removed paper overview is still advertised")
		}
	}
}

func TestPolicyRangeChangesNeverEnrollPriorDocuments(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserReadingPolicy{})
	ctx := context.Background()
	p := Defaults()
	p.Preset = "advance"
	p, e := Save(ctx, db.DB, "u", p)
	if e != nil {
		t.Fatal(e)
	}
	created := time.Now()
	p.Preset = "full"
	p, e = Save(ctx, db.DB, "u", p)
	if e != nil {
		t.Fatal(e)
	}
	if Eligible(p, created) {
		t.Fatal("expanded auto scope enrolled pre-change document")
	}
}

func TestPaperGuideLegacyPolicyAndOldClientPreserveDisabled(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserReadingPolicy{})
	p := Defaults()
	p.Preset = "custom"
	delete(p.Modes, "paper_guide")
	p.Modes[Formula] = Disabled
	normalized, e := Normalize(p)
	if e != nil || normalized.Modes["paper_guide"] != OnDemand || normalized.Modes[Formula] != Disabled {
		t.Fatalf("legacy normalization: %+v %v", normalized, e)
	}
	p = Defaults()
	p.Preset = "custom"
	p.Modes["paper_guide"] = Disabled
	if _, e = Save(context.Background(), db.DB, "owner", p); e != nil {
		t.Fatal(e)
	}
	old := Defaults()
	old.Preset = "full"
	delete(old.Modes, "paper_guide")
	saved, e := Save(context.Background(), db.DB, "owner", old)
	if e != nil || saved.Modes["paper_guide"] != Disabled {
		t.Fatalf("old client enabled new paid feature: %+v %v", saved, e)
	}
	p = Defaults()
	p.Preset = "full"
	p, e = Normalize(p)
	if e != nil || p.Modes["paper_guide"] != OnDemand {
		t.Fatal("full auto-enabled guide")
	}
	p.Preset = "custom"
	p.Modes["paper_guide"] = Automatic
	if _, e = Normalize(p); e == nil {
		t.Fatal("guide must be on demand")
	}
}
