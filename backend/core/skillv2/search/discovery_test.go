package search

import (
	"context"
	"testing"
)

func TestNaturalQueryMatchesTokens(t *testing.T) {
	db := newSearchTestDB(t)
	head := "r1"
	if err := db.Create(&skillRow{ID: "s1", OwnerUserID: "u1", Category: "external", SkillName: "research", Description: "Summarize scientific papers", HeadRevisionID: &head, CallMode: "on_demand"}).Error; err != nil {
		t.Fatal(err)
	}
	hits, err := NewService(ServiceDeps{DB: db}).Search(context.Background(), "u1", "please summarize papers", 5, nil)
	if err != nil || len(hits) != 1 {
		t.Fatalf("natural query hits=%v err=%v", hits, err)
	}
}
func TestManualIsNotDiscoveredAutomatically(t *testing.T) {
	db := newSearchTestDB(t)
	head := "r1"
	for _, mode := range []string{"manual", "disabled"} {
		if err := db.Create(&skillRow{ID: mode, OwnerUserID: "u1", Category: "external", SkillName: mode, Description: "papers", HeadRevisionID: &head, IsEnabled: true, CallMode: mode}).Error; err != nil {
			t.Fatal(err)
		}
	}
	hits, err := NewService(ServiceDeps{DB: db}).Search(context.Background(), "u1", "papers", 5, nil)
	if err != nil || len(hits) != 0 {
		t.Fatalf("manual exposed: %v %v", hits, err)
	}
}

func TestStructuredDiscoveryRespectsOwnershipAndManualSelection(t *testing.T) {
	db := newSearchTestDB(t)
	head := "r1"
	for _, row := range []skillRow{
		{ID: "auto", OwnerUserID: "u1", Category: "external", SkillName: "automatic", Field: "writing", Aliases: []byte(`["论文写作"]`), Tags: []byte(`["academic","paper"]`), Keywords: []byte(`["literature","润色"]`), HeadRevisionID: &head, CallMode: "priority"},
		{ID: "manual", OwnerUserID: "u1", Category: "external", SkillName: "manual", Field: "writing", HeadRevisionID: &head, CallMode: "manual"},
		{ID: "other", OwnerUserID: "u2", Category: "external", SkillName: "other", Field: "writing", HeadRevisionID: &head, CallMode: "manual"},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := NewService(ServiceDeps{DB: db})
	hits, err := svc.Discover(context.Background(), "u1", Request{Field: "field", Value: []string{"writing"}, AllowedSkillKeys: []string{"external/manual", "external/other"}})
	if err != nil || len(hits) != 2 {
		t.Fatalf("ownership+manual hits=%v err=%v", hits, err)
	}
	for _, q := range []string{"论文写作", "润色", "literature"} {
		hits, err = svc.Search(context.Background(), "u1", q, 5, nil)
		if err != nil || len(hits) != 1 || hits[0].SkillID != "auto" {
			t.Fatalf("query=%s hits=%v err=%v", q, hits, err)
		}
	}
	hits, err = svc.Discover(context.Background(), "u1", Request{Field: "tags", Value: []string{"academic", "missing"}})
	if err != nil || len(hits) != 0 {
		t.Fatalf("all-of tags=%v err=%v", hits, err)
	}
	hits, err = svc.Discover(context.Background(), "u1", Request{Field: "name", Value: []string{"AUTOMATIC"}})
	if err != nil || len(hits) != 1 {
		t.Fatalf("exact field=%v err=%v", hits, err)
	}
}
func TestExactNamePrecedesMetadataAndDescription(t *testing.T) {
	db := newSearchTestDB(t)
	head := "r1"
	for _, row := range []skillRow{
		{ID: "exact", OwnerUserID: "u1", Category: "external", SkillName: "paper", HeadRevisionID: &head, CallMode: "on_demand"},
		{ID: "other", OwnerUserID: "u1", Category: "external", SkillName: "paper-helper", Description: "paper paper paper", SortRank: 999, HeadRevisionID: &head, CallMode: "priority"},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	hits, err := NewService(ServiceDeps{DB: db}).Search(context.Background(), "u1", "paper", 5, nil)
	if err != nil || len(hits) != 2 || hits[0].SkillID != "exact" {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
}

func TestChineseNaturalQueryFindsAliasPhrase(t *testing.T) {
	db := newSearchTestDB(t)
	head := "r1"
	if err := db.Create(&skillRow{ID: "s1", OwnerUserID: "u1", Category: "external", SkillName: "academic-writing", Aliases: []byte(`["论文写作"]`), HeadRevisionID: &head, CallMode: "on_demand"}).Error; err != nil {
		t.Fatal(err)
	}
	hits, err := NewService(ServiceDeps{DB: db}).Search(context.Background(), "u1", "请帮我进行论文写作", 5, nil)
	if err != nil || len(hits) != 1 {
		t.Fatalf("Chinese natural query=%v err=%v", hits, err)
	}
}
