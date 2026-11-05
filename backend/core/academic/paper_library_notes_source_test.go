package academic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"lazymind/core/common/orm"
)

type noteSourceContract struct {
	AttachmentID string       `json:"attachmentId"`
	Page         int          `json:"page"`
	Rects        [][4]float64 `json:"rects"`
	BBox         *[4]float64  `json:"bbox"`
	Quote        string       `json:"quote"`
	Color        string       `json:"color"`
}

func noteSourceResponse(t *testing.T, body []byte) noteSourceContract {
	t.Helper()
	var response struct {
		Data struct {
			Notes []struct {
				Source *noteSourceContract `json:"source"`
			} `json:"notes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Notes) != 1 || response.Data.Notes[0].Source == nil {
		t.Fatalf("saved note source missing from library response: %s", body)
	}
	return *response.Data.Notes[0].Source
}

func TestLibraryNoteSourceSurvivesSaveRetryAndReload(t *testing.T) {
	db := paperLibraryTestDB(t)
	importDraft(t, "papers", "note-source-paper", libraryDraft("paper", "Paper", "10.1234/note-source"))
	if err := db.Create(&orm.PaperLibraryAttachment{DatasetID: "papers", ID: "attachment", PaperID: "paper", DocumentID: "pdf", Kind: "pdf", Name: "paper.pdf", IsPrimary: true}).Error; err != nil {
		t.Fatal(err)
	}
	bbox := [4]float64{0.1, 0.2, 0.7, 0.3}
	source := noteSourceContract{AttachmentID: "attachment", Page: 4, Rects: [][4]float64{{0.1, 0.2, 0.4, 0.3}, {0.4, 0.2, 0.7, 0.3}}, BBox: &bbox, Quote: "Selected passage", Color: "yellow"}
	request := map[string]any{"id": "anchored-note", "content": "My own note", "source": source}
	for attempt := 0; attempt < 2; attempt++ {
		w := libraryRequest(t, "POST", "/library/papers/paper/notes", "papers", "owner", request)
		if w.Code != 200 {
			t.Fatalf("save/retry %d: %d %s", attempt, w.Code, w.Body.String())
		}
		if got := noteSourceResponse(t, w.Body.Bytes()); !reflect.DeepEqual(got, source) {
			t.Fatalf("save/retry %d changed note source: %#v", attempt, got)
		}
	}
	w := libraryRequest(t, "GET", "/library", "papers", "owner", nil)
	if w.Code != 200 {
		t.Fatalf("reload: %d %s", w.Code, w.Body.String())
	}
	if got := noteSourceResponse(t, w.Body.Bytes()); !reflect.DeepEqual(got, source) {
		t.Fatalf("reload changed note source: %#v", got)
	}

	request["source"] = noteSourceContract{AttachmentID: "attachment", Page: 5, Color: "blue"}
	w = libraryRequest(t, "POST", "/library/papers/paper/notes", "papers", "owner", request)
	if w.Code != 422 {
		t.Fatalf("same note ID accepted a different anchor: %d %s", w.Code, w.Body.String())
	}
}

func TestLibraryNoteRejectsInvalidSource(t *testing.T) {
	db := paperLibraryTestDB(t)
	importDraft(t, "papers", "note-source-validation", libraryDraft("paper", "Paper", "10.1234/note-validation"))
	if err := db.Create(&orm.PaperLibraryAttachment{DatasetID: "papers", ID: "attachment", PaperID: "paper", DocumentID: "pdf", Kind: "pdf", Name: "paper.pdf"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		source map[string]any
	}{
		{"zero page", map[string]any{"attachmentId": "attachment", "page": 0, "color": "yellow"}},
		{"foreign attachment", map[string]any{"attachmentId": "elsewhere", "page": 4, "color": "yellow"}},
		{"unknown color", map[string]any{"attachmentId": "attachment", "page": 4, "color": "plaid"}},
		{"invalid rectangle", map[string]any{"attachmentId": "attachment", "page": 4, "rects": [][]float64{{0.4, 0.2, 1.5, 0.3}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := libraryRequest(t, "POST", "/library/papers/paper/notes", "papers", "owner", map[string]any{"id": "note-" + tc.name, "content": "My note", "source": tc.source})
			if w.Code != 400 {
				t.Fatalf("invalid source accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestLibraryNoteSaveDoesNotRequireProcessingStatus(t *testing.T) {
	db := paperLibraryTestDB(t)
	importDraft(t, "papers", "note-status", libraryDraft("paper", "Paper", "10.1234/note-status"))
	path := filepath.Join(t.TempDir(), "paper.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.Dataset{}).Where("id=?", "papers").Update("processing_level", "indexed").Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&orm.Document{ID: "pdf", DatasetID: "papers", DocumentType: "PDF", Ext: marshal(map[string]any{"stored_path": path})},
		&orm.PaperLibraryAttachment{DatasetID: "papers", ID: "attachment", PaperID: "paper", DocumentID: "pdf", Kind: "pdf", Name: "paper.pdf", IsPrimary: true},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	// This independent processing table must not decide whether a note write succeeds.
	if err := db.Migrator().DropTable(&orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	w := libraryRequest(t, "POST", "/library/papers/paper/notes", "papers", "owner", map[string]any{"id": "note", "content": "A durable observation"})
	if w.Code != 200 {
		t.Fatalf("note save depended on processing status: %d %s", w.Code, w.Body.String())
	}
	var response libraryChanges
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Papers) != 1 || len(response.Data.Papers[0].NoteIDs) != 1 || response.Data.Papers[0].NoteIDs[0] != "note" || len(response.Data.Notes) != 1 || response.Data.Notes[0].Content != "A durable observation" {
		t.Fatalf("note response no longer contains the paper and its notes: %s", w.Body.String())
	}
}

func TestLibraryNotesReadWithoutProcessingStatus(t *testing.T) {
	db := paperLibraryTestDB(t)
	importDraft(t, "papers", "note-read-status", libraryDraft("paper", "Paper", "10.1234/note-read-status"))
	path := filepath.Join(t.TempDir(), "paper.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Document{ID: "pdf", DatasetID: "papers", DocumentType: "PDF", Ext: marshal(map[string]any{"stored_path": path})}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.PaperLibraryAttachment{DatasetID: "papers", ID: "attachment", PaperID: "paper", DocumentID: "pdf", Kind: "pdf", Name: "paper.pdf", IsPrimary: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.Dataset{}).Where("id=?", "papers").Update("processing_level", "indexed").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	if full := libraryRequest(t, "GET", "/library", "papers", "owner", nil); full.Code == 200 {
		t.Fatalf("fixture did not break the processing-status read: %s", full.Body.String())
	}
	w := libraryRequest(t, "GET", "/library?include_processing=false", "papers", "owner", nil)
	if w.Code != 200 {
		t.Fatalf("reading notes should not depend on processing status: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Data struct {
			Papers []struct {
				ID string `json:"id"`
			} `json:"papers"`
			Attachments []struct {
				ID string `json:"id"`
			} `json:"attachments"`
		} `json:"data"`
		CanWrite bool `json:"can_write"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.CanWrite || len(response.Data.Papers) != 1 || response.Data.Papers[0].ID != "paper" || len(response.Data.Attachments) != 1 || response.Data.Attachments[0].ID != "attachment" {
		t.Fatalf("lightweight read lost note context: %s", w.Body.String())
	}
}
