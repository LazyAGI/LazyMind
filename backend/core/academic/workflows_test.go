package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
	"lazymind/core/workflow"
)

func paperFixture(t *testing.T) *orm.DB {
	t.Helper()
	db := orm.MigrateAllModelsForTest(t)
	old, oldLazy, oldState := store.DB(), store.LazyLLMDB(), store.State()
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(old, oldLazy, oldState) })
	ds := orm.Dataset{ID: "papers", KbID: "papers", DisplayName: "Papers", ScenarioKey: "academic_papers", ProcessingLevel: "parsed", Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "u", CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	if err := db.Create(&ds).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := db.Create(&orm.AcademicWork{ID: id, CanonicalTitle: id, NormalizedTitle: id, AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.Document{ID: id, DatasetID: ds.ID, DisplayName: id + ".pdf", DocumentType: "FILE", BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.AcademicDocumentState{DocumentID: id, DatasetID: ds.ID, Kind: "paper", Status: "succeeded", PaperID: id, Revision: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func paperRequest(body string) *http.Request {
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	r.Header.Set("X-User-Id", "u")
	return r
}
func TestPaperLaunchRoutesAndIdempotency(t *testing.T) {
	db := paperFixture(t)
	for _, tc := range []struct{ kind, direction, ids string }{{"expansion", "", "[\"a\"]"}, {"expansion", "agents", "[]"}, {"review", "", "[\"a\"]"}, {"review", "agents", "[]"}, {"reflection", "", "[\"a\"]"}, {"ppt", "", "[\"a\"]"}} {
		key := tc.kind + tc.direction + tc.ids
		body := string(marshal(map[string]any{"kind": tc.kind, "dataset_id": "papers", "direction": tc.direction, "document_ids": json.RawMessage(tc.ids), "request_key": key}))
		for i := 0; i < 2; i++ {
			w := httptest.NewRecorder()
			LaunchPaperWorkflow(w, paperRequest(body))
			if w.Code != 200 {
				t.Fatalf("%s: %d %s", tc.kind, w.Code, w.Body.String())
			}
		}
	}
	var count int64
	db.Model(&orm.AcademicExpansion{}).Count(&count)
	if count != 4 {
		t.Fatalf("four expansion entry paths should create four records, got %d", count)
	}
	db.Model(&orm.AcademicHandoff{}).Count(&count)
	if count != 2 {
		t.Fatalf("reflection and ppt should have separate handoffs, got %d", count)
	}
}
func TestReviewGateFullManifestImportanceAndSeparateChat(t *testing.T) {
	db := paperFixture(t)
	input := ReviewWorkflowInput{UserInstruction: "agents", CandidateDirections: []string{"tools"}, SelectedDirections: []string{"tools"}, Papers: []WorkflowPaper{{Paper: PaperRef{PaperID: "a"}, IsSeed: true}, {Paper: PaperRef{PaperID: "b"}, IsSeed: true}, {Paper: PaperRef{PaperID: "a"}}}}
	if err := db.Create(&orm.AcademicUserPaperState{UserID: "u", PaperID: "a", IsImportant: true}).Error; err != nil {
		t.Fatal(err)
	}
	db.Create(&orm.AcademicUserPaperState{UserID: "other", PaperID: "b", IsImportant: true})
	for i, id := range []string{"a", "b"} {
		content := orm.AcademicPaperContent{ID: "content-" + id, UserID: "u", PaperID: id, Status: "ready", ContentHash: "hash-" + id, PagesJSON: marshal([]ContentPage{{Page: 1, Text: "real parsed body"}})}
		if err := db.Create(&content).Error; err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, _, err := snapshotReview(context.Background(), db.DB, "u", "papers", input, false); err == nil {
				t.Fatal("one full-text paper must not pass")
			}
		}
	}
	snapshot, contents, err := snapshotReview(context.Background(), db.DB, "u", "papers", input, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Papers) != 2 || !snapshot.Papers[0].IsImportant || snapshot.Papers[1].IsImportant {
		t.Fatalf("deduplication/personal marks: %+v", snapshot.Papers)
	}
	id, err := newAcademicConversation(db.DB, "u", "papers", "expansion", "expansion")
	if err != nil {
		t.Fatal(err)
	}
	record := orm.AcademicExpansion{ID: "record", UserID: "u", RequestOwner: "u", RequestKey: "start", DatasetID: "papers", ConversationID: id, Title: "agents", Goal: "review", Status: "completed", Version: 1, ResultJSON: marshal(input)}
	if err = db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	first, err := createMockHandoff(t.Context(), db.DB, "u", "papers", record.ID, 1, "review", "review-click", snapshot, contents, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := createMockHandoff(t.Context(), db.DB, "u", "papers", record.ID, 1, "review", "review-click", snapshot, contents, false)
	if err != nil || first.ID != second.ID {
		t.Fatal("replay duplicated review", err)
	}
	if first.ConversationID == id {
		t.Fatal("review must use a different Chat")
	}
	db.Model(&record).Updates(map[string]any{"version": 2, "result_json": marshal(ReviewWorkflowInput{UserInstruction: "changed"})})
	var stored orm.AcademicHandoff
	db.Where("id=?", first.ID).Take(&stored)
	if string(stored.InputJSON) != string(marshal(snapshot)) {
		t.Fatal("continued expansion changed review snapshot")
	}
}
func TestExpansionDeleteAndPersonalHistory(t *testing.T) {
	db := paperFixture(t)
	id, err := newAcademicConversation(db.DB, "u", "papers", "expansion", "expansion")
	if err != nil {
		t.Fatal(err)
	}
	record := orm.AcademicExpansion{ID: "record", UserID: "u", RequestOwner: "u", RequestKey: "launch", DatasetID: "papers", ConversationID: id, Title: "agents", Goal: "expansion", Status: "completed", Version: 1, ResultJSON: marshal(ReviewWorkflowInput{Papers: []WorkflowPaper{}})}
	if err = db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	// Ordinary Chat trash does not cascade into personal results.
	now := time.Now()
	db.Model(&orm.Conversation{}).Where("id=?", id).Update("deleted_at", now)
	request := mux.SetURLVars(paperRequest(""), map[string]string{"expansion_id": record.ID})
	w := httptest.NewRecorder()
	GetExpansion(w, request)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"chat_status":"trashed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	request.Header.Set("X-User-Id", "other")
	w = httptest.NewRecorder()
	GetExpansion(w, request)
	if w.Code != 404 {
		t.Fatal("personal result leaked", w.Code)
	}
	request = mux.SetURLVars(paperRequest(`{"confirm":false}`), map[string]string{"expansion_id": record.ID})
	w = httptest.NewRecorder()
	DeleteExpansion(w, request)
	if w.Code != 400 {
		t.Fatal("delete must require confirmation")
	}
	request = mux.SetURLVars(paperRequest(`{"confirm":true}`), map[string]string{"expansion_id": record.ID})
	w = httptest.NewRecorder()
	DeleteExpansion(w, request)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var stored orm.AcademicExpansion
	db.Where("id=?", record.ID).Take(&stored)
	if stored.DeletedAt == nil {
		t.Fatal("result not deleted")
	}
}

func TestExpansionProjectionCommitsOnceAndKeepsPriorSuccess(t *testing.T) {
	db := paperFixture(t)
	ctx := t.Context()
	conversation, err := newAcademicConversation(db.DB, "u", "papers", "expansion", "expansion")
	if err != nil {
		t.Fatal(err)
	}
	seed := WorkflowPaper{Paper: PaperRef{PaperID: "a"}, IsSeed: true}
	input := ReviewWorkflowInput{UserInstruction: "agents", CandidateDirections: []string{"tools"}, SelectedDirections: []string{"tools"}, Papers: []WorkflowPaper{seed, {Paper: PaperRef{PaperID: "b"}}}}
	record := orm.AcademicExpansion{ID: "projection", UserID: "u", RequestOwner: "u", RequestKey: "start-projection", DatasetID: "papers", ConversationID: conversation, Title: "agents", Goal: "review", Status: "running", ResultJSON: marshal(ReviewWorkflowInput{UserInstruction: "agents", Papers: []WorkflowPaper{seed}})}
	if err = db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	session := orm.WorkflowSession{ID: "session", ConversationID: conversation, WorkflowID: "paper_expansion", CreateUserID: "u", Status: "completed"}
	if err = db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	workflow.OnAcademicRuntimeChanged(ctx, db.DB, session.ID)
	var projectionJobs int64
	if err = db.Model(&orm.AsyncJob{}).Where("job_type=? AND resource_id=?", "academic.project", record.ID).Count(&projectionJobs).Error; err != nil || projectionJobs != 1 {
		t.Fatalf("runtime observer did not enqueue projection: %d %v", projectionJobs, err)
	}
	step := orm.WorkflowSessionStep{ID: "attempt", SessionID: session.ID, StepID: "compose_result", Status: "succeeded", Validity: "effective", Attempt: 1}
	if err = db.Create(&step).Error; err != nil {
		t.Fatal(err)
	}
	human := orm.WorkflowHumanArtifact{ID: "human", SessionID: session.ID, Slot: "expansion_result", Value: marshal(map[string]any{"data": input}), ContentType: "json"}
	if err = db.Create(&human).Error; err != nil {
		t.Fatal(err)
	}
	artifact := orm.WorkflowSlotRevision{ID: "artifact", SessionID: session.ID, SlotID: "expansion_result", Slot: "expansion_result", StepID: "compose_result", Attempt: 1, Revision: 1, Selected: true, Validity: "effective", ProducerAttemptID: step.ID, HumanArtifactID: &human.ID}
	if err = db.Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err = db.Create(&orm.AcademicPaperContent{ID: "ready-" + id, UserID: "u", PaperID: id, Status: "ready", ContentHash: id, PagesJSON: marshal([]ContentPage{{Page: 1, Text: "body"}})}).Error; err != nil {
			t.Fatal(err)
		}
	}
	job := asyncjob.Job{ResourceID: record.ID, CreateUserID: "u", PayloadJSON: marshal(map[string]string{"session_id": session.ID})}
	for i := 0; i < 2; i++ {
		if _, err = projectAcademicResult(ctx, job, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Where("id=?", record.ID).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Version != 1 || record.AutoReviewID == "" {
		t.Fatalf("result not committed/relayed: %+v", record)
	}
	var count int64
	db.Model(&orm.AcademicHandoff{}).Where("expansion_id=?", record.ID).Count(&count)
	if count != 1 {
		t.Fatal("duplicate automatic review", count)
	}
	previous := string(record.ResultJSON)
	db.Model(&session).Update("status", "waiting")
	if _, err = projectAcademicResult(ctx, job, nil); err != nil {
		t.Fatal(err)
	}
	db.Where("id=?", record.ID).Take(&record)
	if record.Status != "running" {
		t.Fatal("a ready next step must not ask the user for a nonexistent answer")
	}
	db.Model(&session).Update("last_stopped_at", time.Now().UTC())
	if _, err = projectAcademicResult(ctx, job, nil); err != nil {
		t.Fatal(err)
	}
	db.Where("id=?", record.ID).Take(&record)
	if record.Status != "stopped" || record.Version != 1 || string(record.ResultJSON) != previous {
		t.Fatal("stopping must retain the completed result")
	}
	db.Model(&artifact).Update("validity", "outdated")
	db.Model(&step).Update("status", "failed")
	db.Model(&session).Updates(map[string]any{"status": "failed", "last_stopped_at": nil})
	if _, err = projectAcademicResult(ctx, job, nil); err != nil {
		t.Fatal(err)
	}
	db.Where("id=?", record.ID).Take(&record)
	if record.Version != 1 || string(record.ResultJSON) != previous {
		t.Fatal("failure replaced completed result")
	}
}

func TestMockAdapterFreezesOriginalPapers(t *testing.T) {
	db := paperFixture(t)
	input := ReviewWorkflowInput{UserInstruction: "agents", Papers: []WorkflowPaper{{Paper: PaperRef{PaperID: "a", Title: "original a"}, IsSeed: true}, {Paper: PaperRef{PaperID: "b"}}}}
	handoff, err := createMockHandoff(t.Context(), db.DB, "u", "papers", "", 0, "ppt", "mock-adapter", input, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var first string
	for i := 0; i < 2; i++ {
		r := mux.SetURLVars(paperRequest(`{}`), map[string]string{"handoff_id": handoff.ID})
		w := httptest.NewRecorder()
		SubmitMockHandoff(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if i == 0 {
			first = w.Body.String()
		} else if first != w.Body.String() {
			t.Fatal("mock request changed on replay")
		}
	}
	var output struct {
		Papers []PaperRef `json:"papers"`
		Mock   bool       `json:"mock"`
	}
	if err := json.Unmarshal([]byte(first), &output); err != nil {
		t.Fatal(err)
	}
	if !output.Mock || len(output.Papers) != 2 || output.Papers[0].PaperID != "a" || output.Papers[0].Title != "original a" || output.Papers[1].PaperID != "b" {
		t.Fatal("mock adapter rewrote original manifest", first)
	}
}

func TestManualIdentityWinsOverOlderRecognition(t *testing.T) {
	db := paperFixture(t)
	request := mux.SetURLVars(paperRequest(`{"kind":"paper","paper":{"title":"corrected title","abstract":"","authors":[],"venue":"ICLR"}}`), map[string]string{"document_id": "a"})
	w := httptest.NewRecorder()
	UpdateDocumentIdentity(w, request)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := runIdentify(t.Context(), asyncjob.Job{ResourceID: "a", CreateUserID: "u", PayloadJSON: marshal(map[string]int{"revision": 1})}, nil); err != nil {
		t.Fatal(err)
	}
	papers, invalid, err := resolvePaperSelection(paperRequest(`{}`), "u", "papers", []string{"a"})
	if err != nil || len(invalid) != 0 || len(papers) != 1 {
		t.Fatal(papers, invalid, err)
	}
	if papers[0].Paper.Title != "corrected title" || papers[0].Paper.Abstract == nil || *papers[0].Paper.Abstract != "" || papers[0].Paper.Venue != "ICLR" {
		t.Fatal("manual metadata was lost", papers)
	}
	var work orm.AcademicWork
	db.Where("id=?", "a").Take(&work)
	if work.CanonicalTitle != "a" {
		t.Fatal("document-scoped correction changed shared identity")
	}
}

func TestFolderSelectionReadsAllPagesAndDeduplicatesVersions(t *testing.T) {
	db := paperFixture(t)
	if err := db.Create(&orm.Document{ID: "folder", DatasetID: "papers", DocumentType: "FOLDER", DisplayName: "folder", BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
		t.Fatal(err)
	}
	docs := make([]orm.Document, 0, 202)
	states := make([]orm.AcademicDocumentState, 0, 202)
	for i := 0; i < 202; i++ {
		id := fmt.Sprintf("version-%03d", i)
		paperID := "a"
		if i == 201 {
			paperID = "b"
		}
		docs = append(docs, orm.Document{ID: id, DatasetID: "papers", PID: "folder", DisplayName: id + ".pdf", DocumentType: "FILE", BaseModel: orm.BaseModel{CreateUserID: "u"}})
		states = append(states, orm.AcademicDocumentState{DocumentID: id, DatasetID: "papers", Kind: "paper", Status: "succeeded", Revision: 1, PaperID: paperID})
	}
	if err := db.Create(&docs).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&states).Error; err != nil {
		t.Fatal(err)
	}
	papers, invalid, err := resolvePaperSelection(paperRequest(`{}`), "u", "papers", []string{"folder", "a"})
	if err != nil || len(invalid) != 0 || len(papers) != 2 {
		t.Fatal("folder traversal lost papers or failed to deduplicate", papers, invalid, err)
	}
}

func TestHistoryReviewUsesRequestedVersionAndPersonalImportance(t *testing.T) {
	db := paperFixture(t)
	oldInput := ReviewWorkflowInput{UserInstruction: "original", Papers: []WorkflowPaper{{Paper: PaperRef{PaperID: "a"}, IsSeed: true}, {Paper: PaperRef{PaperID: "b"}, IsSeed: true, IsImportant: true}}}
	newInput := oldInput
	newInput.UserInstruction = "continued"
	record := orm.AcademicExpansion{ID: "versioned", UserID: "u", RequestOwner: "u", RequestKey: "source", DatasetID: "papers", ConversationID: "source-chat", Title: "agents", Goal: "expansion", Status: "completed", Version: 2, ResultJSON: marshal(newInput)}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.AcademicExpansionRound{ID: "round-v1", ExpansionID: record.ID, Version: 1, Status: "completed", InputJSON: marshal(oldInput), ResultJSON: marshal(oldInput)}).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := db.Create(&orm.AcademicPaperContent{ID: "body-" + id, UserID: "u", PaperID: id, ParserVersion: orm.AcademicPaperParserVersion, Status: "ready", ContentHash: "hash-" + id, PagesJSON: marshal([]ContentPage{{Page: 1, Text: "parsed full text"}})}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.AcademicUserPaperState{UserID: "u", PaperID: "a", IsImportant: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.AcademicUserPaperState{UserID: "other", PaperID: "b", IsImportant: true}).Error; err != nil {
		t.Fatal(err)
	}
	launch := func(key string, version int64, status int) string {
		t.Helper()
		w := httptest.NewRecorder()
		LaunchPaperWorkflow(w, paperRequest(string(marshal(map[string]any{"kind": "review", "expansion_id": record.ID, "expansion_version": version, "request_key": key}))))
		if w.Code != status {
			t.Fatalf("launch v%d: %d %s", version, w.Code, w.Body.String())
		}
		var response map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response["conversation_id"]
	}
	first := launch("v1-click", 1, 200)
	if first == "" || launch("v1-click", 1, 200) != first {
		t.Fatal("retry duplicated Chat")
	}
	second := launch("v2-click", 2, 200)
	if first == second {
		t.Fatal("new version reused old Chat")
	}
	launch("v1-click", 2, 409)
	launch("future", 3, 422)
	var handoff orm.AcademicHandoff
	if err := db.Where("conversation_id=?", first).Take(&handoff).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot ReviewWorkflowInput
	if err := json.Unmarshal(handoff.InputJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if handoff.Version != 1 || snapshot.UserInstruction != "original" || !snapshot.Papers[0].IsImportant || snapshot.Papers[1].IsImportant {
		t.Fatalf("wrong frozen input: %+v", snapshot)
	}
	w := httptest.NewRecorder()
	GetExpansion(w, mux.SetURLVars(paperRequest(""), map[string]string{"expansion_id": record.ID}))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		PaperImportance map[string]bool              `json:"paper_importance"`
		Record          orm.AcademicExpansion        `json:"record"`
		Rounds          []orm.AcademicExpansionRound `json:"rounds"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.PaperImportance["a"] || response.PaperImportance["b"] {
		t.Fatal("importance is not user-scoped", response.PaperImportance)
	}
	if string(response.Record.ResultJSON) != string(record.ResultJSON) || len(response.Rounds) != 1 || string(response.Rounds[0].ResultJSON) != string(marshal(oldInput)) {
		t.Fatal("live importance modified saved result")
	}
}

func TestPaperIdentityRequiresWritePermission(t *testing.T) {
	db := paperFixture(t)
	acl.InitStore(db)
	permissions := acl.GetStore()
	permissions.AddACL(acl.ResourceTypeDB, "papers", "user", "reader", acl.PermissionDatasetRead, "u", nil)
	permissions.AddACL(acl.ResourceTypeDB, "papers", "user", "uploader", acl.PermissionDatasetUpload, "u", nil)
	permissions.AddACL(acl.ResourceTypeDB, "papers", "user", "writer", acl.PermissionDatasetWrite, "u", nil)
	for _, user := range []string{"reader", "uploader", "writer"} {
		r := mux.SetURLVars(paperRequest(`{"kind":"other"}`), map[string]string{"document_id": "a"})
		r.Header.Set("X-User-Id", user)
		w := httptest.NewRecorder()
		UpdateDocumentIdentity(w, r)
		want := 403
		if user == "writer" {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("%s identity update: %d %s", user, w.Code, w.Body.String())
		}
		if got := doc.HasDatasetPermission(r, "papers", acl.PermissionDatasetUpload); got != (user != "reader") {
			t.Fatalf("%s upload permission: %v", user, got)
		}
	}
}

func TestPersonalImportanceIsIndependentOfWritePermissionAndOtherUsers(t *testing.T) {
	db := paperFixture(t)
	acl.InitStore(db)
	acl.GetStore().AddACL(acl.ResourceTypeDB, "papers", "user", "reader", acl.PermissionDatasetRead, "u", nil)
	for _, change := range []struct {
		user      string
		important bool
	}{{"u", true}, {"reader", true}, {"reader", false}} {
		r := mux.SetURLVars(paperRequest(string(marshal(map[string]bool{"is_important": change.important}))), map[string]string{"paper_id": "a"})
		r.Header.Set("X-User-Id", change.user)
		w := httptest.NewRecorder()
		SetImportance(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for user, important := range map[string]bool{"u": true, "reader": false} {
		var saved orm.AcademicUserPaperState
		if err := db.Where("user_id=? AND paper_id=?", user, "a").Take(&saved).Error; err != nil {
			t.Fatal(err)
		}
		if saved.IsImportant != important {
			t.Fatalf("%s personal importance changed: %+v", user, saved)
		}
	}
}

func TestHistoryReviewSelectedPapersAndIdempotency(t *testing.T) {
	db := paperFixture(t)
	if err := db.Create(&orm.AcademicWork{ID: "c", CanonicalTitle: "C", NormalizedTitle: "c", AuthorsJSON: marshal([]string{}), ExternalIDsJSON: marshal(map[string]string{}), ProvenanceJSON: marshal(map[string]string{})}).Error; err != nil {
		t.Fatal(err)
	}
	input := ReviewWorkflowInput{UserInstruction: "compare methods", SelectedDirections: []string{"tools"}, Papers: []WorkflowPaper{{Paper: PaperRef{PaperID: "a"}, IsSeed: true}, {Paper: PaperRef{PaperID: "b"}}, {Paper: PaperRef{PaperID: "c"}}}}
	old := input
	old.Papers = input.Papers[:2]
	record := orm.AcademicExpansion{ID: "selection", UserID: "u", RequestOwner: "u", RequestKey: "source", DatasetID: "papers", ConversationID: "source-chat", Title: "tools", Goal: "expansion", Status: "completed", Version: 2, ResultJSON: marshal(input)}
	for _, row := range []any{&record, &orm.AcademicExpansionRound{ID: "selection-v1", ExpansionID: record.ID, Version: 1, Status: "completed", InputJSON: marshal(old), ResultJSON: marshal(old)}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := db.Create(&orm.AcademicPaperContent{ID: "selected-" + id, UserID: "u", PaperID: id, ParserVersion: orm.AcademicPaperParserVersion, Status: "ready", ContentHash: "hash-" + id, PagesJSON: marshal([]ContentPage{{Page: 1, Text: "full text"}})}).Error; err != nil {
			t.Fatal(err)
		}
	}
	launch := func(key string, version int64, ids []string, status int) string {
		t.Helper()
		body := map[string]any{"kind": "review", "expansion_id": record.ID, "expansion_version": version, "request_key": key}
		if ids != nil {
			body["paper_ids"] = ids
		}
		w := httptest.NewRecorder()
		LaunchPaperWorkflow(w, paperRequest(string(marshal(body))))
		if w.Code != status {
			t.Fatalf("selection %v: %d %s", ids, w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		id, _ := result["conversation_id"].(string)
		return id
	}
	launch("empty", 2, []string{}, 422)
	launch("one-distinct", 2, []string{"a", "a"}, 422)
	launch("foreign", 2, []string{"a", "missing"}, 422)
	launch("wrong-version", 1, []string{"a", "c"}, 422)
	first := launch("chosen-ab", 2, []string{"a", "b", "b"}, 200)
	if first == "" || launch("chosen-ab", 2, []string{"b", "a"}, 200) != first {
		t.Fatal("retry did not reuse the selected review")
	}
	launch("chosen-ab", 2, []string{"a", "c"}, 409)
	launch("chosen-ab", 1, []string{"a", "b"}, 409)
	second := launch("chosen-ac", 2, []string{"a", "c"}, 200)
	if first == second {
		t.Fatal("changed selection reused an existing review")
	}
	var handoff orm.AcademicHandoff
	if err := db.Where("conversation_id=?", first).Take(&handoff).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot ReviewWorkflowInput
	var contents []orm.AcademicPaperContent
	if err := json.Unmarshal(handoff.InputJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(handoff.ContentsJSON, &contents); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Papers) != 2 || snapshot.Papers[0].Paper.PaperID != "a" || !snapshot.Papers[0].IsSeed || snapshot.Papers[1].Paper.PaperID != "b" || snapshot.UserInstruction != input.UserInstruction || len(contents) != 2 {
		t.Fatal("review snapshot did not preserve exactly the selected papers", snapshot, contents)
	}
	if err := db.Model(&orm.AcademicPaperContent{}).Where("paper_id=?", "c").Update("status", "failed").Error; err != nil {
		t.Fatal(err)
	}
	launch("no-fallback", 2, []string{"a", "c"}, 422)
	legacy := launch("legacy-all", 2, nil, 200)
	var legacyHandoff orm.AcademicHandoff
	if err := db.Where("conversation_id=?", legacy).Take(&legacyHandoff).Error; err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(legacyHandoff.InputJSON, &snapshot); err != nil || len(snapshot.Papers) != 3 {
		t.Fatal("legacy launch lost the full manifest", err)
	}
	if err := db.Model(&orm.AcademicPaperContent{}).Where("paper_id=?", "b").Updates(map[string]any{"source_document_id": "inaccessible", "source_dataset_id": "papers"}).Error; err != nil {
		t.Fatal(err)
	}
	launch("inaccessible", 2, []string{"a", "b"}, 422)
}
