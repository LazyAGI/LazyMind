package doc

import (
	"context"
	"errors"
	"lazymind/core/common/orm"
	"lazymind/core/learning"
	"testing"
)

func TestKnowledgeSceneBoundariesAndAtomicity(t *testing.T) {
	s, db, op := resourceFixture(t)
	if err := db.AutoMigrate(&orm.KBModel{}, &orm.ACLModel{}, &orm.UserUIPreferences{}); err != nil {
		t.Fatal(err)
	}
	s.provision = func(context.Context, DatasetCatalogCaller, *orm.Dataset) error { return nil }
	for _, initial := range []string{"general", "academic_papers", "custom"} {
		t.Run(initial, func(t *testing.T) {
			refs := []learning.CapabilityRef{{Key: "general_translation", Version: 1, Enabled: true, DisplayOrder: 1}}
			result, err := s.CreateKnowledge(t.Context(), op, CreateKnowledgeRequest{Name: "scene-" + initial, Tags: []string{"test"}, Scene: initial, ProcessingLevel: "parsed", Capabilities: refs})
			if err != nil {
				t.Fatal(err)
			}
			target := result.Items[0].ResourceRef
			id := target.ID()
			name := "renamed-" + initial
			_, err = s.UpdateMetadata(t.Context(), op, UpdateMetadataRequest{Items: []ResourceMetadataItem{{Target: target, Changes: ResourceMetadataChanges{Name: &name, Capabilities: &refs}}}})
			if err != nil {
				t.Fatal(err)
			}
			var ds orm.Dataset
			if err = db.First(&ds, "id=?", id).Error; err != nil {
				t.Fatal(err)
			}
			if ds.Scene != initial {
				t.Fatalf("capabilities altered scene: %+v", ds)
			}
			next := "academic_papers"
			if initial == next {
				next = "general"
			}
			name = "must-roll-back"
			_, err = s.UpdateMetadata(t.Context(), op, UpdateMetadataRequest{Items: []ResourceMetadataItem{{Target: target, Changes: ResourceMetadataChanges{Scene: &next, Name: &name, Capabilities: &refs}}}})
			var resource *ResourceError
			if !errors.As(err, &resource) || resource.Code != "CONFLICT" {
				t.Fatalf("expected conflict, got %v", err)
			}
			db.First(&ds, "id=?", id)
			if ds.Scene != initial || ds.DisplayName != "renamed-"+initial {
				t.Fatalf("partial scene update: %+v", ds)
			}
			if initial == "general" {
				next = "legal"
				_, err = s.UpdateMetadata(t.Context(), op, UpdateMetadataRequest{Items: []ResourceMetadataItem{{Target: target, Changes: ResourceMetadataChanges{Scene: &next}}}})
				if err != nil {
					t.Fatal(err)
				}
				db.First(&ds, "id=?", id)
				if ds.Scene != "legal" {
					t.Fatal(ds.Scene)
				}
			}
		})
	}
	if _, err := s.CreateKnowledge(t.Context(), op, CreateKnowledgeRequest{Name: "invalid-paper", Tags: []string{"test"}, Scene: "academic_papers", ProcessingLevel: "stored"}); err == nil {
		t.Fatal("paper storage-only library accepted")
	}
	var count int64
	db.Model(&orm.Dataset{}).Where("display_name=?", "invalid-paper").Count(&count)
	if count != 0 {
		t.Fatal("failed creation left a library")
	}
}
