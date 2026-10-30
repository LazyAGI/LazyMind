package paperlibrary

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"reflect"
	"testing"
)

func TestEffectiveMetadataFollowsPrimaryAndPreservesUserOverrides(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	work := orm.AcademicWork{ID: "work", CanonicalTitle: "Shared title", PublicationYear: 1902, AuthorsJSON: json.RawMessage(`["Shared author"]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)}
	entry := orm.PaperLibraryEntry{DatasetID: "papers", ID: "paper", AcademicWorkID: work.ID, MetadataJSON: json.RawMessage(`{}`), TagsJSON: json.RawMessage(`[]`), Revision: 1}
	for _, row := range []any{&work, &entry} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, version := range []string{"v1", "v2"} {
		metadata := `{"title":"Version one","authors":["Source author"],"year":2019}`
		if version == "v2" {
			metadata = `{"title":"Version two","year":2020}`
		}
		for _, row := range []any{
			&orm.Document{ID: version, DatasetID: "papers", DisplayName: version + ".pdf"},
			&orm.PaperLibraryAttachment{ID: version, DatasetID: "papers", PaperID: "paper", DocumentID: version, Kind: "pdf", IsPrimary: version == "v1"},
			&orm.AcademicWorkDocument{AcademicWorkID: work.ID, DatasetID: "papers", DocumentID: version, SourceProvider: "arxiv", SourceVersion: version},
			&orm.AcademicDocumentState{DocumentID: version, DatasetID: "papers", PaperID: work.ID, Kind: "paper", Status: "succeeded", MetadataJSON: json.RawMessage(metadata), Revision: 1},
		} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	check := func(title string, year int, authors []string) {
		t.Helper()
		view, err := Load(t.Context(), db, "papers", "", nil, false)
		if err != nil || len(view.Data.Papers) != 1 {
			t.Fatalf("view=%+v err=%v", view, err)
		}
		got := view.Data.Papers[0].Metadata
		if got.Title != title || got.Year != year || !reflect.DeepEqual(got.Authors, authors) {
			t.Fatalf("metadata=%+v", got)
		}
		same, err := EffectiveMetadata(db, entry, work)
		if err != nil || !reflect.DeepEqual(same, got) {
			t.Fatalf("consumer mismatch: %+v %+v %v", got, same, err)
		}
	}
	check("Version one", 2019, []string{"Source author"})
	if err := db.Model(&orm.PaperLibraryAttachment{}).Where("dataset_id=?", "papers").Update("is_primary", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.PaperLibraryAttachment{}).Where("id=?", "v2").Update("is_primary", true).Error; err != nil {
		t.Fatal(err)
	}
	check("Version two", 2020, []string{"Shared author"})
	if err := db.Model(&orm.AcademicDocumentState{}).Where("document_id=?", "v2").Update("metadata_json", json.RawMessage(`{"title":"Wrong revision title","year":2020,"provenance":{"pdf_version":"v1"}}`)).Error; err != nil {
		t.Fatal(err)
	}
	check("Shared title", 1902, []string{"Shared author"})
	if err := db.Model(&orm.AcademicDocumentState{}).Where("document_id=?", "v2").Update("paper_id", "other-work").Error; err != nil {
		t.Fatal(err)
	}
	check("Shared title", 1902, []string{"Shared author"})
	entry.MetadataJSON = json.RawMessage(`{"title":"My correction","authors":[],"year":0}`)
	if err := db.Model(&entry).Update("metadata_json", entry.MetadataJSON).Error; err != nil {
		t.Fatal(err)
	}
	check("My correction", 0, []string{})
}

func TestMetadataOnlyServerSnapshotIsUsedWithoutChangingLocalOverrides(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	work := orm.AcademicWork{ID: "work", CanonicalTitle: "Old title", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)}
	entry := orm.PaperLibraryEntry{DatasetID: "papers", ID: "paper", AcademicWorkID: work.ID, MetadataJSON: json.RawMessage(`{}`), TagsJSON: json.RawMessage(`[]`), Revision: 1}
	batch := orm.PaperImportBatch{ID: "batch", TargetDatasetID: "papers", EntryType: "paper_link", SourceDocumentIDsJSON: json.RawMessage(`[]`), PolicySnapshotJSON: json.RawMessage(`{}`)}
	item := orm.PaperImportItem{ID: "item", BatchID: "batch", AcademicWorkID: work.ID, LibraryPaperID: entry.ID, Status: "succeeded", ResultKind: "metadata_only", SelectedCandidateJSON: json.RawMessage(`{"source_provider":"openalex","verified_paper":{"paper_id":"work","title":"Verified title","authors":["Author A"],"year":2024,"source_provider":"openalex"}}`), ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), ErrorDetailsJSON: json.RawMessage(`{}`)}
	for _, row := range []any{&work, &entry, &batch, &item} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	view, err := Load(t.Context(), db, "papers", "", nil, false)
	if err != nil || len(view.Data.Papers) != 1 {
		t.Fatal(err)
	}
	got := view.Data.Papers[0]
	if got.Metadata.Title != "Verified title" || got.Metadata.Year != 2024 || got.Incomplete {
		t.Fatalf("snapshot lost: %+v", got)
	}
	var saved orm.PaperLibraryEntry
	if err := db.Where("id=?", entry.ID).Take(&saved).Error; err != nil || string(saved.MetadataJSON) != "{}" {
		t.Fatalf("automatic data became local overrides: %s %v", saved.MetadataJSON, err)
	}
}
