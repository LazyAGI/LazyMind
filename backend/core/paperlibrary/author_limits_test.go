package paperlibrary

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func collaborationAuthors(count int) []string {
	authors := make([]string, count)
	for i := range authors {
		authors[i] = fmt.Sprintf("Author %04d", i)
	}
	// Distinct contributors can have the same name; preserve both positions.
	if count > 21 {
		authors[21] = authors[20]
	}
	return authors
}

func TestLegacyConversionPreservesLargeCollaborationAuthors(t *testing.T) {
	db := legacyTestDB(t)
	seedLegacy(t, db)
	var graph map[string]any
	if err := json.Unmarshal([]byte(legacyGraphFixture), &graph); err != nil {
		t.Fatal(err)
	}
	papers := graph["papers"].([]any)
	want := map[string][]string{}
	for i, count := range []int{281, 419, 216, 561, 1350, 1135} {
		id := fmt.Sprintf("collaboration-%d", count)
		authors := collaborationAuthors(count)
		want[id] = authors
		separator := []string{", ", "; ", "；", "、"}[i%4]
		papers = append(papers, map[string]any{
			"id": id, "title": "Collaboration paper", "authors": strings.Join(authors, separator),
			"metadata": map[string]any{"identifiers": map[string]any{}}, "source": "zotero",
		})
	}
	graph["papers"] = papers
	raw := rawJSON(graph)
	if err := db.Model(&orm.PaperLibrary{}).Where("dataset_id=?", "library").Update("data_json", raw).Error; err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 2; run++ {
		view, err := Load(t.Context(), db, "library", "owner", nil, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Data.Papers) != len(want)+1 {
			t.Fatalf("paper count = %d, want %d", len(view.Data.Papers), len(want)+1)
		}
		for _, paper := range view.Data.Papers {
			authors, exists := want[paper.ID]
			if !exists {
				continue
			}
			if !reflect.DeepEqual(paper.Metadata.Authors, authors) {
				t.Fatalf("%s author count or order changed: got %d, want %d", paper.ID, len(paper.Metadata.Authors), len(authors))
			}
			var work orm.AcademicWork
			if err := db.Where("id=?", paper.AcademicWorkID).Take(&work).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(WorkMetadata(work).Authors, authors) {
				t.Fatalf("%s normalized work lost authors", paper.ID)
			}
		}
		var archive map[string]any
		if err := json.Unmarshal(view.Data.Legacy, &archive); err != nil {
			t.Fatal(err)
		}
		var original map[string]any
		if err := json.Unmarshal(raw, &original); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(archive, original) {
			t.Fatal("migration changed the archived graph")
		}
	}
}

func TestSnapshotImportPreservesLargeCollaborationAuthors(t *testing.T) {
	db := legacyTestDB(t)
	if err := db.Create(&orm.Dataset{ID: "library", Scene: "academic_papers", Ext: rawJSON(map[string]any{}), BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
		t.Fatal(err)
	}
	authors := collaborationAuthors(10000)
	raw := rawJSON(map[string]any{"papers": []any{map[string]any{
		"id": "collaboration", "metadata": Metadata{Title: "Collaboration paper", Authors: authors}, "source": "zotero",
	}}})
	for run := 0; run < 2; run++ {
		if err := ImportSnapshot(t.Context(), db, "library", "owner", raw); err != nil {
			t.Fatal(err)
		}
		view, err := Load(t.Context(), db, "library", "owner", nil, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Data.Papers) != 1 || !reflect.DeepEqual(view.Data.Papers[0].Metadata.Authors, authors) {
			t.Fatal("snapshot import changed author count or order")
		}
	}
}

func TestSnapshotImportRejectsExcessiveAuthorsAtomically(t *testing.T) {
	db := legacyTestDB(t)
	if err := db.Create(&orm.Dataset{ID: "library", Scene: "academic_papers", Ext: rawJSON(map[string]any{}), BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
		t.Fatal(err)
	}
	raw := rawJSON(map[string]any{"papers": []any{
		map[string]any{"id": "valid", "metadata": Metadata{Title: "Valid", Authors: []string{"Author"}}},
		map[string]any{"id": "excessive", "metadata": Metadata{Title: "Excessive", Authors: collaborationAuthors(10001)}},
	}})
	if err := ImportSnapshot(t.Context(), db, "library", "owner", raw); err == nil {
		t.Fatal("unbounded author list accepted")
	}
	for _, model := range []any{&orm.PaperLibraryEntry{}, &orm.AcademicWork{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("rejected import partially committed: count=%d, err=%v", count, err)
		}
	}
}
