package academic

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func TestExpansionChatSummaryPreservesReviewGoal(t *testing.T) {
	db := paperFixture(t)
	for _, goal := range []string{"review", "expansion"} {
		conversation, err := newAcademicConversation(db.DB, "u", "papers", goal, "expansion")
		if err != nil {
			t.Fatal(err)
		}
		record := orm.AcademicExpansion{ID: goal, UserID: "u", RequestOwner: "u", RequestKey: goal, DatasetID: "papers", ConversationID: conversation, Goal: goal, Status: "running", ResultJSON: marshal(ReviewWorkflowInput{Papers: []WorkflowPaper{}})}
		if err := db.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
		r := paperRequest("")
		r.URL.RawQuery = url.Values{"conversation_id": {conversation}, "view": {"summary"}}.Encode()
		w := httptest.NewRecorder()
		AcademicChatContext(w, r)
		var summary academicChatSummary
		if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil || w.Code != 200 || summary.Kind != "expansion" || summary.Goal != goal {
			t.Fatal("summary lost the task goal", w.Code, w.Body.String(), err)
		}
	}
}

func TestHandoffContextSeparatesBusinessInputFromNavigation(t *testing.T) {
	db := paperFixture(t)
	input := ReviewWorkflowInput{UserInstruction: "Read the selected paper", Papers: []WorkflowPaper{{Paper: PaperRef{PaperID: "a", Title: "Frozen title", SourceDocumentID: "a"}, IsSeed: true}}}
	handoff, err := createPaperHandoff(t.Context(), db.DB, "u", "papers", "", 0, "ppt", "summary", input, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	read := func(view, user string) *httptest.ResponseRecorder {
		t.Helper()
		r := paperRequest("")
		r.Header.Set("X-User-Id", user)
		r.URL.RawQuery = url.Values{"conversation_id": {handoff.ConversationID}, "view": {view}}.Encode()
		w := httptest.NewRecorder()
		AcademicChatContext(w, r)
		return w
	}
	for _, view := range []string{"", "summary"} {
		w := read(view, "u")
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil || w.Code != 200 {
			t.Fatal(w.Code, w.Body.String(), err)
		}
		if string(fields["handoff_id"]) != string(marshal(handoff.ID)) || fields["contents"] != nil || fields["mock_result"] != nil {
			t.Fatal("context lost its locator or exposed internal sources", w.Body.String())
		}
		if view == "summary" && fields["input"] != nil || view == "" && string(fields["input"]) != string(handoff.InputJSON) {
			t.Fatal("context view did not preserve the frozen input boundary", w.Body.String())
		}
		if w := read(view, "other"); w.Code != 404 {
			t.Fatal("context view allowed access to another user's Chat", view, w.Code)
		}
	}
	if w := read("unknown", "u"); w.Code != 400 {
		t.Fatal("unknown view was accepted", w.Code)
	}
	prefix := academicChatContext(t.Context(), db.DB, "u", handoff.ConversationID)
	if prefix != "" {
		t.Fatal("PPT Chat must use the launch prompt and ordinary attachments", prefix)
	}
	if err := db.Model(&orm.Dataset{}).Where("id=?", "papers").Update("create_user_id", "other").Error; err != nil {
		t.Fatal(err)
	}
	for _, view := range []string{"", "summary"} {
		if w := read(view, "u"); w.Code != 403 {
			t.Fatal("context view bypassed dataset permissions", view, w.Code, w.Body.String())
		}
	}
}

func TestPPTReceiptUsesCompletedReflectionAndKeepsItFrozen(t *testing.T) {
	db := paperFixture(t)
	input := ReviewWorkflowInput{UserInstruction: "Present this paper", Papers: []WorkflowPaper{{Paper: PaperRef{PaperID: "a", SourceDocumentID: "a"}, IsSeed: true}}}
	handoff, err := createPaperHandoff(t.Context(), db.DB, "u", "papers", "", 0, "ppt", "with-reflection", input, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	submit := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		SubmitMockHandoff(w, mux.SetURLVars(paperRequest(body), map[string]string{"handoff_id": handoff.ID}))
		return w
	}
	if w := submit(`{}`); w.Code != 422 {
		t.Fatal("PPT accepted an unfinished reflection", w.Code, w.Body.String())
	}
	session := orm.WorkflowSession{ID: "reflection-session", ConversationID: handoff.ConversationID, WorkflowID: "paper_ppt_with_reflection_mock", CreateUserID: "u", Status: "running"}
	step := orm.WorkflowSessionStep{ID: "reflection-attempt", SessionID: session.ID, StepID: "reflection", Status: "succeeded", Validity: "effective", Attempt: 1}
	human := orm.WorkflowHumanArtifact{ID: "saved-reflection", SessionID: session.ID, Slot: "reflection_markdown", Value: marshal(map[string]string{"text": "Saved reflection body"}), ContentType: "text"}
	artifact := orm.WorkflowSlotRevision{ID: "reflection-revision", SessionID: session.ID, SlotID: "reflection_markdown", Slot: "reflection_markdown", StepID: "reflection", Attempt: 1, Revision: 1, Selected: true, Validity: "effective", ProducerAttemptID: step.ID, HumanArtifactID: &human.ID}
	for _, value := range []any{&session, &step, &human, &artifact} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	if w := submit(`{"reflection_markdown":"Model-authored replacement"}`); w.Code != 422 {
		t.Fatal("PPT accepted a replacement for the saved reflection", w.Code, w.Body.String())
	}
	w := submit(`{}`)
	var result mockHandoffResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.ReflectionMarkdown != "Saved reflection body" || string(result.Input) != string(handoff.InputJSON) {
		t.Fatal("PPT lost the frozen papers or the saved reflection", w.Code, w.Body.String(), err)
	}
	if err := db.Model(&human).Update("value", marshal(map[string]string{"text": "Later edit"})).Error; err != nil {
		t.Fatal(err)
	}
	if replay := submit(`{}`); replay.Code != 200 || replay.Body.String() != w.Body.String() {
		t.Fatal("receipt replay switched to a later reflection", replay.Code, replay.Body.String())
	}
}

func TestPaperTaskHandoffIncludesApplicableNotesAndFreezesInput(t *testing.T) {
	for _, kind := range []string{"reflection", "ppt"} {
		t.Run(kind, func(t *testing.T) {
			db := paperFixture(t)
			for _, id := range []string{"a", "b"} {
				var work orm.AcademicWork
				if err := db.Where("id=?", id).Take(&work).Error; err != nil {
					t.Fatal(err)
				}
				entry, err := importedLibraryEntry(t.Context(), db.DB, "papers", work, nil, "", "bibliography", true)
				if err != nil {
					t.Fatal(err)
				}
				content := "Durable note"
				if id == "b" {
					content = "Do not include"
				}
				if err = db.Create(&orm.PaperLibraryNote{DatasetID: "papers", ID: "note-" + id, PaperID: entry.ID, Title: "Note", Content: content, Kind: "note"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			body := string(marshal(map[string]any{"kind": kind, "dataset_id": "papers", "document_ids": []string{"a"}, "user_instruction": "关注方法贡献", "request_key": "reading-notes"}))
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
			if input.UserInstruction != "关注方法贡献" || len(input.Papers) != 1 || input.Papers[0].PaperID != "a" {
				t.Fatalf("paper task contract mismatch: %+v", input)
			}
			if kind == "reflection" && (len(input.Notes) != 1 || input.Notes[0].PaperID != "a" || input.Notes[0].Content != "Durable note") {
				t.Fatalf("reflection lost its selected notes: %+v", input)
			}
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(handoff.InputJSON, &fields)
			if fields["candidate_directions"] != nil || fields["selected_directions"] != nil {
				t.Fatal("paper task received literature-review directions")
			}
			if fields["notes"] == nil {
				t.Fatal("paper task lost existing notes")
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
			db.Model(&orm.PaperLibraryNote{}).Where("dataset_id=? AND id=?", "papers", "note-a").Update("content", "Edited after launch")
			w = httptest.NewRecorder()
			LaunchPaperWorkflow(w, paperRequest(body))
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			w = httptest.NewRecorder()
			SubmitMockHandoff(w, mux.SetURLVars(paperRequest(`{}`), map[string]string{"handoff_id": handoff.ID}))
			var receipt struct {
				Input json.RawMessage `json:"input"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &receipt)
			if w.Code != 200 || string(receipt.Input) != string(handoff.InputJSON) {
				t.Fatal("replay rewrote the frozen input", w.Code, w.Body.String())
			}
		})
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
	content := orm.AcademicPaperContent{QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"}), ID: "handoff-body", UserID: "u", PaperID: "a", ParserVersion: orm.AcademicPaperParserVersion, SourceKey: seeds[0].key(), SourceKind: "kb", SourceDatasetID: "papers", SourceDocumentID: "a", Status: "ready", ContentHash: "original-body", PagesJSON: marshal(pages)}
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

func TestPPTLibraryLaunchUsesRealWorkflowAndFrozenSelection(t *testing.T) {
	db := paperFixture(t)
	for _, ids := range [][]string{{"a"}, {"a", "b"}} {
		for _, reflection := range []bool{false, true} {
			key := fmt.Sprintf("ppt-%d-%t", len(ids), reflection)
			body := string(marshal(map[string]any{"kind": "ppt", "dataset_id": "papers", "document_ids": ids,
				"request_key": key, "reflection_first": reflection, "user_instruction": "学生组会，比较方法，8页"}))
			for retry := 0; retry < 2; retry++ {
				w := httptest.NewRecorder()
				LaunchPaperWorkflow(w, paperRequest(body))
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			var handoff orm.AcademicHandoff
			if err := db.Where("request_key=?", key).Take(&handoff).Error; err != nil {
				t.Fatal(err)
			}
			var input PaperTaskInput
			if err := json.Unmarshal(handoff.InputJSON, &input); err != nil {
				t.Fatal(err)
			}
			if len(input.Papers) != len(ids) || input.UserInstruction != "学生组会，比较方法，8页" {
				t.Fatalf("lost selection: %+v", input)
			}
			for i, id := range ids {
				if input.Papers[i].PaperID != id {
					t.Fatalf("lost identity: %+v", input)
				}
			}
			var jobs []orm.AsyncJob
			if err := db.Where("resource_id=? AND job_type=?", handoff.ConversationID, ChatLaunchJobType).Find(&jobs).Error; err != nil {
				t.Fatal(err)
			}
			if len(jobs) != 1 {
				t.Fatalf("duplicate launches: %d", len(jobs))
			}
			var payload map[string]string
			_ = json.Unmarshal(jobs[0].PayloadJSON, &payload)
			if payload["workflow_id"] != "ppt-workflow" {
				t.Fatalf("still mock: %+v", payload)
			}
			if strings.Contains(academicChatContext(t.Context(), db.DB, "u", handoff.ConversationID), "生成能力待对接") {
				t.Fatal("PPT still has mock instruction")
			}
			w := httptest.NewRecorder()
			r := paperRequest("")
			r.URL.RawQuery = "conversation_id=" + handoff.ConversationID
			AcademicChatContext(w, r)
			var returned struct {
				ReflectionFirst bool           `json:"reflection_first"`
				Input           PaperTaskInput `json:"input"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &returned)
			if w.Code != 200 || returned.ReflectionFirst != reflection || len(returned.Input.Papers) != len(ids) {
				t.Fatal(w.Code, w.Body.String())
			}
			w = httptest.NewRecorder()
			r.Header.Set("X-User-Id", "another-user")
			AcademicChatContext(w, r)
			if w.Code != 404 {
				t.Fatalf("handoff ownership bypass: %d", w.Code)
			}
		}
	}
}
