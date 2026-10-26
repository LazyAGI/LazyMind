package academic

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func TestReadingHandoffIncludesOnlySelectedNotesAndFreezesInput(t *testing.T) {
	db := paperFixture(t)
	var graph map[string]any
	if err := json.Unmarshal([]byte(paperLibraryFixture("Paper A")), &graph); err != nil {
		t.Fatal(err)
	}
	graph["attachments"] = []any{map[string]any{"id": "pdf", "paperId": "paper", "contentRef": "a", "name": "a.pdf", "kind": "pdf", "source": "local", "addedAt": "2026-10-26"}}
	graph["notes"] = append(graph["notes"].([]any), map[string]any{"id": "unselected-note", "paperId": "unselected", "title": "Other", "content": "Do not include", "kind": "note", "createdAt": "2026-10-26", "updatedAt": "2026-10-26"})
	if err := db.Create(&orm.PaperLibrary{DatasetID: "papers", Revision: 1, DataJSON: marshal(graph)}).Error; err != nil {
		t.Fatal(err)
	}
	body := `{"kind":"reflection","dataset_id":"papers","document_ids":["a"],"user_instruction":"关注方法贡献","request_key":"reading-notes"}`
	w := httptest.NewRecorder()
	LaunchPaperWorkflow(w, paperRequest(body))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var handoff orm.AcademicHandoff
	if err := db.Where("request_key=?", "reading-notes").Take(&handoff).Error; err != nil {
		t.Fatal(err)
	}
	var input PaperTaskInput
	if err := json.Unmarshal(handoff.InputJSON, &input); err != nil {
		t.Fatal(err)
	}
	if input.UserInstruction != "关注方法贡献" || len(input.Papers) != 1 || input.Papers[0].PaperID != "a" || len(input.Notes) != 1 || input.Notes[0].PaperID != "a" || input.Notes[0].Content != "Durable note" {
		t.Fatalf("reading-note contract mismatch: %+v", input)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(handoff.InputJSON, &fields)
	if fields["candidate_directions"] != nil || fields["selected_directions"] != nil {
		t.Fatal("reading notes received literature-review directions")
	}
	w = httptest.NewRecorder()
	r := paperRequest("")
	r.URL.RawQuery = "conversation_id=" + handoff.ConversationID
	AcademicChatContext(w, r)
	var taskContext struct {
		DatasetID string          `json:"dataset_id"`
		Input     json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &taskContext)
	if w.Code != 200 || taskContext.DatasetID != "papers" || string(taskContext.Input) != string(handoff.InputJSON) {
		t.Fatal("Workflow context lost the original input", w.Code, w.Body.String())
	}
	graph["notes"].([]any)[0].(map[string]any)["content"] = "Edited after launch"
	db.Model(&orm.PaperLibrary{}).Where("dataset_id=?", "papers").Update("data_json", marshal(graph))
	w = httptest.NewRecorder()
	LaunchPaperWorkflow(w, paperRequest(body))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	SubmitMockHandoff(w, mux.SetURLVars(paperRequest(`{}`), map[string]string{"handoff_id": handoff.ID}))
	var receipt struct {
		Input PaperTaskInput `json:"input"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &receipt)
	if w.Code != 200 || len(receipt.Input.Notes) != 1 || receipt.Input.Notes[0].Content != "Durable note" {
		t.Fatal("replay rewrote the frozen notes", w.Code, w.Body.String())
	}
}

func TestHandoffFullTextIsScopedAndPinnedWithoutAnExpansion(t *testing.T) {
	db := paperFixture(t)
	papers := []WorkflowPaper{{Paper: PaperRef{PaperID: "a", SourceDocumentID: "a"}, IsSeed: true}}
	seeds, err := seedSources(db.DB, "papers", papers)
	if err != nil {
		t.Fatal(err)
	}
	pages := []ContentPage{}
	for page := 1; page <= 7; page++ {
		pages = append(pages, ContentPage{Page: page, Text: "Selected paper text"})
	}
	content := orm.AcademicPaperContent{ID: "handoff-body", UserID: "u", PaperID: "a", ParserVersion: orm.AcademicPaperParserVersion, SourceKey: seeds[0].key(), SourceKind: "kb", SourceDatasetID: "papers", SourceDocumentID: "a", Status: "ready", ContentHash: "original-body", PagesJSON: marshal(pages)}
	if err := db.Create(&content).Error; err != nil {
		t.Fatal(err)
	}
	handoff, err := createPaperHandoff(t.Context(), db.DB, "u", "papers", "", 0, "reflection", "reading-body", ReviewWorkflowInput{UserInstruction: "Read A", Papers: papers}, []orm.AcademicPaperContent{content}, false)
	if err != nil {
		t.Fatal(err)
	}
	body := string(marshal(map[string]string{"handoff_id": handoff.ID, "paper_id": "a"}))
	w := httptest.NewRecorder()
	ReadPaperContent(w, paperRequest(body))
	var first struct {
		Pages      []ContentPage `json:"pages"`
		NextCursor string        `json:"next_cursor"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &first)
	if w.Code != 200 || len(first.Pages) != 5 || first.NextCursor == "" {
		t.Fatal("independent reading task could not read full text", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	ReadPaperContent(w, paperRequest(string(marshal(map[string]string{"cursor": first.NextCursor}))))
	var last struct {
		Pages []ContentPage `json:"pages"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &last)
	if w.Code != 200 || len(last.Pages) != 2 || last.Pages[0].Page != 6 {
		t.Fatal("cursor lost the frozen handoff", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		body string
		user string
		code int
	}{
		{string(marshal(map[string]string{"handoff_id": handoff.ID, "paper_id": "b"})), "u", 422},
		{body, "another-user", 404},
		{string(marshal(map[string]string{"cursor": first.NextCursor, "handoff_id": "another-task"})), "u", 400},
		{string(marshal(map[string]string{"cursor": first.NextCursor, "expansion_id": "another-task"})), "u", 400},
	} {
		w = httptest.NewRecorder()
		r := paperRequest(tc.body)
		r.Header.Set("X-User-Id", tc.user)
		ReadPaperContent(w, r)
		if w.Code != tc.code {
			t.Fatalf("task scope bypass: want %d, got %d: %s", tc.code, w.Code, w.Body.String())
		}
	}
	db.Model(&content).Update("content_hash", "replacement-body")
	w = httptest.NewRecorder()
	ReadPaperContent(w, paperRequest(body))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "VERSION_CHANGED") {
		t.Fatal("reader silently switched the frozen content version", w.Code, w.Body.String())
	}
}
