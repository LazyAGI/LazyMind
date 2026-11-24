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
	if e != nil || p.Preset != "demand" || p.Limits.DailyPDFDocuments != 100 || p.Limits.DailyStudyDocuments != 50 || p.Modes[AIStudy] != OnDemand {
		t.Fatalf("defaults: %+v %v", p, e)
	}
	p.Preset = "full"
	saved, e := Save(ctx, db.DB, "one", p)
	if e != nil || saved.Modes[AIStudy] != Automatic || saved.AutomaticEligibleAfter == nil {
		t.Fatalf("full: %+v %v", saved, e)
	}
	other, _ := Load(ctx, db.DB, "two")
	if other.Modes[AIStudy] != OnDemand {
		t.Fatal("policy leaked")
	}
	p = saved
	p.Preset = "custom"
	p.Modes[PDFParse] = Disabled
	if _, e = Save(ctx, db.DB, "one", p); e != nil {
		t.Fatal("automatic study may use cached structure", e)
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
	delete(p.Modes, AIStudy)
	p.Modes[Formula] = Disabled
	normalized, e := Normalize(p)
	if e != nil || normalized.Modes["paper_guide"] != OnDemand || normalized.Modes[AIStudy] != Disabled {
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

func TestPaperGuideModeChangePreservesAutomaticEnrollmentEpoch(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserReadingPolicy{})
	ctx := context.Background()
	p := Defaults()
	p.Preset = "custom"
	p.Modes[PDFParse], p.Modes[AIStudy] = Automatic, Automatic
	p, err := Save(ctx, db.DB, "owner", p)
	if err != nil {
		t.Fatal(err)
	}
	epoch := *p.AutomaticEligibleAfter
	importedBeforeGuideChange := epoch.Add(time.Nanosecond)
	for _, guideMode := range []string{Disabled, OnDemand} {
		p.Modes[PaperGuide] = guideMode
		p, err = Save(ctx, db.DB, "owner", p)
		if err != nil {
			t.Fatal(err)
		}
		if !p.AutomaticEligibleAfter.Equal(epoch) || !Eligible(p, importedBeforeGuideChange) {
			t.Fatalf("guide mode %s excluded a pending import: epoch %v became %v", guideMode, epoch, p.AutomaticEligibleAfter)
		}
		if p.Modes[PaperGuide] != guideMode {
			t.Fatalf("guide mode not saved: %+v", p.Modes)
		}
	}
}
