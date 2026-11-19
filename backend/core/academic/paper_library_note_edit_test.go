package academic

import (
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestEditLibraryNotePreservesIdentityAndSource(t *testing.T) {
	db := paperLibraryTestDB(t)
	importDraft(t, "papers", "edit-note", libraryDraft("paper", "Paper", "10.1234/edit"))
	created := time.Now().UTC().Add(-time.Hour)
	note := orm.PaperLibraryNote{DatasetID: "papers", ID: "note", PaperID: "paper", Content: "Original", Title: "Title", Kind: "note", SourceJSON: `{"attachmentId":"attachment","page":4,"quote":"Original passage","rects":[[0.1,0.2,0.4,0.3]],"color":"yellow"}`, CreatedAt: created, UpdatedAt: created}
	if err := db.Create(&note).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		w := libraryRequest(t, "PATCH", "/library/papers/paper/notes/note", "papers", "owner", map[string]any{"content": "Edited insight"})
		if w.Code != 200 {
			t.Fatalf("edit/retry=%d %s", w.Code, w.Body.String())
		}
		source := noteSourceResponse(t, w.Body.Bytes())
		if source.Page != 4 || source.Quote != "Original passage" || source.AttachmentID != "attachment" {
			t.Fatalf("edit response lost anchor: %+v", source)
		}
	}
	var got orm.PaperLibraryNote
	if err := db.Where("dataset_id=? AND id=?", "papers", "note").Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Content != "Edited insight" || got.SourceJSON != note.SourceJSON || got.Title != note.Title || got.Kind != note.Kind || !got.CreatedAt.Equal(created) {
		t.Fatalf("edit lost note data: %+v", got)
	}
	for _, tc := range []struct {
		dataset, user, paper, content string
		status                        int
	}{{"papers", "stranger", "paper", "forbidden", 403}, {"other", "owner", "paper", "wrong dataset", 404}, {"papers", "owner", "missing", "wrong paper", 404}, {"papers", "owner", "paper", "   ", 400}} {
		w := libraryRequest(t, "PATCH", "/library/papers/"+tc.paper+"/notes/note", tc.dataset, tc.user, map[string]any{"content": tc.content})
		if w.Code != tc.status {
			t.Errorf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
	if err := db.Where("dataset_id=? AND id=?", "papers", "note").Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Content != "Edited insight" {
		t.Fatal("rejected edit mutated note")
	}
}
