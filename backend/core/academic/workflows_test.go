package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/state"
	"lazymind/core/store"
	"lazymind/core/workflow"
	workflowstore "lazymind/core/workflow/store"
)

func paperFixture(t *testing.T) *orm.DB {
	t.Helper()
	db := orm.MigrateAllModelsForTest(t)
	old, oldLazy, oldState := store.DB(), store.LazyLLMDB(), store.State()
	shared, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shared.Close() })
	store.Init(db.DB, nil, shared)
	t.Cleanup(func() { store.Init(old, oldLazy, oldState) })
	ds := orm.Dataset{ID: "papers", KbID: "papers", DisplayName: "Papers", Scene: "academic_papers", ProcessingLevel: "parsed", Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "u", CreatedAt: time.Now(), UpdatedAt: time.Now()}}
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
	var topic orm.AcademicExpansion
	if err := db.Where("title=? AND goal=?", "agents", "expansion").Take(&topic).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	GetExpansion(w, mux.SetURLVars(paperRequest(`{}`), map[string]string{"expansion_id": topic.ID}))
	var detail map[string]any
	if json.Unmarshal(w.Body.Bytes(), &detail) != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, ok := detail["contents"].([]any); !ok {
		t.Fatal("topic preparation must return an empty content array", w.Body.String())
	}
	if _, ok := detail["record"].(map[string]any)["result"].(map[string]any)["papers"].([]any); !ok {
		t.Fatal("topic preparation must return an empty paper array", w.Body.String())
	}
	db.Model(&orm.AcademicHandoff{}).Count(&count)
	if count != 2 {
		t.Fatalf("reflection and ppt should have separate handoffs, got %d", count)
	}
}

func TestImportedIdentitySurvivesQueuedRecognitionAndManualMetadataCorrection(t *testing.T) {
	db := paperFixture(t)
	link := orm.AcademicWorkDocument{AcademicWorkID: "a", DatasetID: "papers", DocumentID: "a", SourceProvider: "arxiv", SourceVersion: "v2", VersionKind: "preprint", ContentSHA256: "original-hash", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	_, err := runIdentify(t.Context(), asyncjob.Job{ResourceID: "a", CreateUserID: "u", PayloadJSON: marshal(map[string]any{"revision": 1})}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := mux.SetURLVars(paperRequest(`{"kind":"paper","paper":{"title":"Corrected title"}}`), map[string]string{"document_id": "a"})
	w := httptest.NewRecorder()
	UpdateDocumentIdentity(w, r)
	if w.Code != 200 {
		t.Fatalf("metadata correction: %d %s", w.Code, w.Body.String())
	}
	var saved orm.AcademicWorkDocument
	if err := db.Where("document_id=?", "a").Take(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.SourceProvider != "arxiv" || saved.SourceVersion != "v2" || saved.ContentSHA256 != "original-hash" {
		t.Fatalf("source identity lost: %+v", saved)
	}
	papers, invalid, err := resolvePaperSelection(r, "u", "papers", []string{"a"})
	if err != nil || len(invalid) != 0 || len(papers) != 1 || papers[0].Paper.Title != "Corrected title" {
		t.Fatalf("Workflow metadata did not follow correction: %+v %+v %v", papers, invalid, err)
	}
}
func TestReviewGateFullManifestImportanceAndSeparateChat(t *testing.T) {
	db := paperFixture(t)
	input := ReviewWorkflowInput{UserInstruction: "agents", CandidateDirections: []string{"tools"}, SelectedDirections: []string{"tools"}, Papers: []WorkflowPaper{{Paper: PaperRef{PaperID: "a", SourceVersion: "v1", SourceProvider: "arxiv"}, IsSeed: true}, {Paper: PaperRef{PaperID: "b", SourceVersion: "v1", SourceProvider: "arxiv"}, IsSeed: true}, {Paper: PaperRef{PaperID: "a", SourceVersion: "v1", SourceProvider: "arxiv"}}}}
	if err := db.Create(&orm.AcademicUserPaperState{UserID: "u", DatasetID: "papers", PaperID: "a", IsImportant: true}).Error; err != nil {
		t.Fatal(err)
	}
	db.Create(&orm.AcademicUserPaperState{UserID: "other", DatasetID: "papers", PaperID: "b", IsImportant: true})
	for i, id := range []string{"a", "b"} {
		content := orm.AcademicPaperContent{QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"}), SourceKind: "external", SourceProvider: "arxiv", SourceVersion: "v1", SourceKey: "test-external", ParserVersion: orm.AcademicPaperParserVersion, ID: "content-" + id, UserID: "u", PaperID: id, Status: "ready", ContentHash: "hash-" + id, PagesJSON: marshal([]ContentPage{{Page: 1, Text: "real parsed body"}})}
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
	first, err := createPaperHandoff(t.Context(), db.DB, "u", "papers", record.ID, 1, "review", "review-click", snapshot, contents, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := createPaperHandoff(t.Context(), db.DB, "u", "papers", record.ID, 1, "review", "review-click", snapshot, contents, false)
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
	original := "仅比较 LoRA 和 QLoRA，不新增第三篇论文。"
	answer := "请选择综述的研究方向（可多选）: 低秩适配机制；量化低秩微调"
	expectedInstruction := original + "\n\n" + answer
	conversation, err := newAcademicConversation(db.DB, "u", "papers", "expansion", "expansion")
	if err != nil {
		t.Fatal(err)
	}
	seed := WorkflowPaper{Paper: PaperRef{PaperID: "a", SourceVersion: "v1", SourceProvider: "arxiv"}, IsSeed: true}
	directions := []string{"低秩适配机制", "量化低秩微调"}
	input := ReviewWorkflowInput{UserInstruction: "【原始要求】" + original + "\n【追问回答】" + answer, CandidateDirections: directions, SelectedDirections: directions, Papers: []WorkflowPaper{seed, {Paper: PaperRef{PaperID: "b", SourceVersion: "v1", SourceProvider: "arxiv"}}}}
	record := orm.AcademicExpansion{ID: "projection", UserID: "u", RequestOwner: "u", RequestKey: "start-projection", DatasetID: "papers", ConversationID: conversation, Title: original, Goal: "review", Status: "running", ResultJSON: marshal(ReviewWorkflowInput{UserInstruction: original, Papers: []WorkflowPaper{seed}})}
	seeds, err := seedSources(db.DB, "papers", []WorkflowPaper{seed})
	if err != nil {
		t.Fatal(err)
	}
	record.SeedSnapshotJSON = marshal(seeds)
	record.DiscoveryJSON = marshal([]paperDiscovery{{Papers: []PaperRef{input.Papers[1].Paper}}})
	if err = db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	histories := []orm.ChatHistory{
		{ID: "original-instruction", ConversationID: conversation, Seq: 1, RawContent: "请先拓展论文，达到全文要求后在新的 Chat 生成综述。\n" + original},
		{ID: "direction-question", ConversationID: conversation, Seq: 2, RawContent: "请根据调研结果继续，先确认研究方向。", Ext: marshal(map[string]any{
			"synthetic_source":  "driver",
			"ask_pending":       map[string]any{"ask_id": "direction-ask", "questions": []map[string]any{{"text": "请选择综述的研究方向（可多选）", "type": "multiple", "choices": directions}}},
			"ask_answered":      true,
			"ask_saved_answers": map[string]any{"0": map[string]any{"type": "multiple", "value": directions, "otherText": ""}},
		})},
		{ID: "direction-answer", ConversationID: conversation, Seq: 3, RawContent: answer},
		{ID: "automatic-retry", ConversationID: conversation, Seq: 4, RawContent: "关系分析因服务端 429 失败，请重试本步。", Ext: marshal(map[string]any{"synthetic_source": "auto_continue"})},
	}
	if err = db.Create(&histories).Error; err != nil {
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
		if err = db.Create(&orm.AcademicPaperContent{QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"}), SourceKind: "external", SourceProvider: "arxiv", SourceVersion: "v1", SourceKey: "test-external", ParserVersion: orm.AcademicPaperParserVersion, ID: "ready-" + id, UserID: "u", PaperID: id, Status: "ready", ContentHash: id, PagesJSON: marshal([]ContentPage{{Page: 1, Text: "body"}})}).Error; err != nil {
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
	var saved ReviewWorkflowInput
	if err = json.Unmarshal(record.ResultJSON, &saved); err != nil || saved.UserInstruction != expectedInstruction {
		t.Fatalf("projection did not preserve only actual user instructions: %q %v", saved.UserInstruction, err)
	}
	var handoff orm.AcademicHandoff
	if err = db.Where("id=?", record.AutoReviewID).Take(&handoff).Error; err != nil {
		t.Fatal(err)
	}
	var launchJob orm.AsyncJob
	if err = db.Where("job_type=? AND resource_id=?", ChatLaunchJobType, handoff.ConversationID).Take(&launchJob).Error; err != nil {
		t.Fatal(err)
	}
	var launchPayload map[string]string
	if err = json.Unmarshal(launchJob.PayloadJSON, &launchPayload); err != nil || launchPayload["query"] != "请生成论文综述。\n"+expectedInstruction {
		t.Fatalf("new Chat received internal or duplicate context: %q %v", launchPayload["query"], err)
	}

	update := "请重试本步，每篇关系和贡献压缩成两句话，保留现有两篇论文及其版本。"
	if err = db.Create(&orm.ChatHistory{ID: "continued-instruction", ConversationID: conversation, Seq: 5, RawContent: update}).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&artifact).Update("selected", false).Error; err != nil {
		t.Fatal(err)
	}
	step.ID, step.Attempt = "continued-attempt", 2
	if err = db.Create(&step).Error; err != nil {
		t.Fatal(err)
	}
	input.UserInstruction += "\n【后续补充】" + update
	input.Papers = input.Papers[:1]
	human.ID, human.Value = "continued-human", marshal(map[string]any{"data": input})
	if err = db.Create(&human).Error; err != nil {
		t.Fatal(err)
	}
	artifact.ID, artifact.Attempt, artifact.Revision, artifact.Selected = "continued-artifact", 2, 2, true
	artifact.ProducerAttemptID, artifact.HumanArtifactID = step.ID, &human.ID
	if err = db.Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = projectAcademicResult(ctx, job, nil); err != nil {
		t.Fatal(err)
	}
	if err = db.Where("id=?", record.ID).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(record.ResultJSON, &saved); err != nil || record.Version != 2 || saved.UserInstruction != expectedInstruction+"\n\n"+update {
		t.Fatalf("continued projection lost or duplicated user instructions: v%d %q %v", record.Version, saved.UserInstruction, err)
	}
	if len(saved.Papers) != 1 || saved.Papers[0].Paper.PaperID != "a" {
		t.Fatalf("excluded paper returned in current result: %+v", saved.Papers)
	}
	var firstRound orm.AcademicExpansionRound
	if err = db.Where("expansion_id=? AND version=1", record.ID).Take(&firstRound).Error; err != nil {
		t.Fatal(err)
	}
	var firstResult ReviewWorkflowInput
	if err = json.Unmarshal(firstRound.ResultJSON, &firstResult); err != nil || len(firstResult.Papers) != 2 || firstResult.UserInstruction != expectedInstruction {
		t.Fatal("previous round was overwritten", err)
	}
	var round orm.AcademicExpansionRound
	if err = db.Where("expansion_id=? AND version=2", record.ID).Take(&round).Error; err != nil {
		t.Fatal(err)
	}
	var frozen struct {
		UserUpdates  []string         `json:"user_updates"`
		Interactions []map[string]any `json:"interactions"`
	}
	if err = json.Unmarshal(round.InputJSON, &frozen); err != nil || len(frozen.UserUpdates) != 3 || len(frozen.Interactions) != 1 || frozen.Interactions[0]["answers"] == nil {
		t.Fatalf("round lost the original structured answers or updates: %+v %v", frozen, err)
	}
	late := workflowstore.Artifact{ID: "late-unseen-result", SessionID: session.ID, Slot: "expansion_result", StepID: "compose_result", Attempt: 1, ProducerAttemptID: "attempt", Revision: 1, Validity: "effective", Selected: true, CreatedAt: artifact.CreatedAt, Value: marshal(map[string]any{"data": ReviewWorkflowInput{UserInstruction: "old", CandidateDirections: directions, SelectedDirections: directions, Papers: []WorkflowPaper{seed}}})}
	lateJob := job
	lateJob.PayloadJSON = marshal(expansionProjectionPayload{SessionID: session.ID, CompletedArtifacts: []workflowstore.Artifact{late}})
	if _, err = projectAcademicResult(ctx, lateJob, nil); err != nil {
		t.Fatal(err)
	}
	if err = db.Where("id=?", record.ID).Take(&record).Error; err != nil || record.Version != 2 {
		t.Fatal("late earlier attempt replaced published round", err)
	}
	late.ID = "late-invalid-result"
	late.Value = marshal(map[string]any{"data": ReviewWorkflowInput{}})
	lateJob.PayloadJSON = marshal(expansionProjectionPayload{SessionID: session.ID, CompletedArtifacts: []workflowstore.Artifact{late}})
	if _, err = projectAcademicResult(ctx, lateJob, nil); err != nil {
		t.Fatal("invalid late event affected current publication", err)
	}
	if err = db.Where("id=?", record.ID).Take(&record).Error; err != nil || record.Version != 2 || record.Status != "completed" {
		t.Fatal("late invalid event changed publication status", record.Status, err)
	}
	previous := string(record.ResultJSON)
	previousReport := string(record.ReportJSON)
	db.Model(&session).Update("status", "active")
	if _, err = projectAcademicResult(ctx, job, nil); err != nil {
		t.Fatal(err)
	}
	db.Where("id=?", record.ID).Take(&record)
	if record.Status != "running" {
		t.Fatal("a ready next step must not ask the user for a nonexistent answer")
	}
	if err = workflow.StopActiveWorkflowSession(ctx, db.DB, store.State(), conversation); err != nil {
		t.Fatal(err)
	}
	if _, err = projectAcademicResult(ctx, job, nil); err != nil {
		t.Fatal(err)
	}
	db.Where("id=?", record.ID).Take(&record)
	if record.Status != "stopped" || record.Version != 2 || string(record.ResultJSON) != previous {
		t.Fatal("stopping must retain the completed result")
	}
	if err = db.Where("id=?", session.ID).Take(&session).Error; err != nil || session.LastStoppedAt == nil {
		t.Fatal("stop was not recorded", err)
	}
	stoppedAt := *session.LastStoppedAt
	if _, err = workflow.BeginNativeControlTurn(ctx, db.DB, "u", conversation, workflow.NativeControlCommand{
		SessionID: session.ID, Kind: "resume", ExpectedStateVersion: session.StateVersion, HistoryID: "resume-turn",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = projectAcademicResult(ctx, job, nil); err != nil {
		t.Fatal(err)
	}
	if err = db.Where("id=?", record.ID).Take(&record).Error; err != nil || record.Status != "running" {
		t.Fatal("historical stop masked the resumed workflow", record.Status, err)
	}
	if err = db.Where("id=?", session.ID).Take(&session).Error; err != nil || session.LastStoppedAt == nil || !session.LastStoppedAt.Equal(stoppedAt) {
		t.Fatal("resume must preserve stop history", err)
	}
	older := orm.WorkflowSession{ID: "older-session", ConversationID: conversation, WorkflowID: "paper_expansion", CreateUserID: "u",
		Status: "waiting", LastStoppedAt: &stoppedAt, CreatedAt: session.CreatedAt.Add(-time.Hour)}
	if err = db.Create(&older).Error; err != nil {
		t.Fatal(err)
	}
	oldJob := job
	oldJob.PayloadJSON = marshal(expansionProjectionPayload{SessionID: older.ID})
	if _, err = projectAcademicResult(ctx, oldJob, nil); err != nil {
		t.Fatal(err)
	}
	if err = db.Where("id=?", record.ID).Take(&record).Error; err != nil || record.Status != "running" {
		t.Fatal("old session event overwrote resumed status", record.Status, err)
	}
	var roundCount int64
	if err = db.Model(&orm.AcademicExpansionRound{}).Where("expansion_id=?", record.ID).Count(&roundCount).Error; err != nil {
		t.Fatal(err)
	}
	db.Model(&session).Update("status", "completed")
	for i := 0; i < 2; i++ {
		if _, err = projectAcademicResult(ctx, job, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Where("id=?", record.ID).Take(&record).Error; err != nil || record.Status != "completed" || record.Version != 2 || string(record.ResultJSON) != previous || string(record.ReportJSON) != previousReport {
		t.Fatal("replayed completion changed saved results or status", record.Status, record.Version, err)
	}
	var replayedCount int64
	if err = db.Model(&orm.AcademicExpansionRound{}).Where("expansion_id=?", record.ID).Count(&replayedCount).Error; err != nil || replayedCount != roundCount {
		t.Fatal("historical stop created an extra round", replayedCount, roundCount, err)
	}
	db.Model(&artifact).Update("validity", "outdated")
	db.Model(&step).Update("status", "failed")
	db.Model(&session).Update("status", "failed")
	if _, err = projectAcademicResult(ctx, job, nil); err != nil {
		t.Fatal(err)
	}
	db.Where("id=?", record.ID).Take(&record)
	if record.Status != "failed" || record.Version != 2 || string(record.ResultJSON) != previous {
		t.Fatal("failure replaced completed result")
	}
}

func TestMockAdapterFreezesOriginalPapers(t *testing.T) {
	db := paperFixture(t)
	input := ReviewWorkflowInput{UserInstruction: "agents", Papers: []WorkflowPaper{{Paper: PaperRef{PaperID: "a", Title: "original a"}, IsSeed: true}, {Paper: PaperRef{PaperID: "b", SourceVersion: "v1", SourceProvider: "arxiv"}}}}
	handoff, err := createPaperHandoff(t.Context(), db.DB, "u", "papers", "", 0, "ppt", "mock-adapter", input, nil, false)
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
		Input  PaperTaskInput `json:"input"`
		Status string         `json:"status"`
	}
	if err := json.Unmarshal([]byte(first), &output); err != nil {
		t.Fatal(err)
	}
	if output.Status != "awaiting_downstream" || len(output.Input.Papers) != 2 || output.Input.Papers[0].PaperID != "a" || output.Input.Papers[0].Title != "original a" || output.Input.Papers[1].PaperID != "b" {
		t.Fatal("mock adapter rewrote original manifest", first)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(first), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["papers"] != nil || fields["reflection_markdown"] != nil {
		t.Fatal("direct PPT receipt included duplicate papers or an empty reflection", first)
	}
	fields["papers"] = marshal(output.Input.Papers)
	fields["reflection_markdown"] = marshal("")
	cached := marshal(fields)
	if err := db.Model(&handoff).Update("mock_result_json", cached).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	SubmitMockHandoff(w, mux.SetURLVars(paperRequest(`{}`), map[string]string{"handoff_id": handoff.ID}))
	if w.Code != 200 || w.Body.String() != first {
		t.Fatal("cached receipt did not follow the same response contract", w.Code, w.Body.String())
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
	answer := "研究方向: 低秩适配机制；量化低秩微调"
	oldInput := ReviewWorkflowInput{UserInstruction: "original\n\n" + answer, Papers: []WorkflowPaper{{Paper: PaperRef{PaperID: "a", SourceVersion: "v1", SourceProvider: "arxiv"}, IsSeed: true}, {Paper: PaperRef{PaperID: "b", SourceVersion: "v1", SourceProvider: "arxiv"}, IsSeed: true, IsImportant: true}}}
	newInput := oldInput
	newInput.UserInstruction += "\n\ncontinued"
	record := orm.AcademicExpansion{ID: "versioned", UserID: "u", RequestOwner: "u", RequestKey: "source", DatasetID: "papers", ConversationID: "source-chat", Title: "original", Goal: "expansion", Status: "completed", Version: 2, ResultJSON: marshal(newInput)}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.AcademicExpansionRound{ID: "round-v1", ExpansionID: record.ID, RequestKey: "v1", Version: 1, Status: "completed", InputJSON: marshal(map[string]any{"workflow_input": oldInput, "user_updates": []string{"请围绕所选论文或方向开展论文拓展。\noriginal", answer}, "interactions": []any{map[string]any{"answered": true}}}), ResultJSON: marshal(oldInput)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.AcademicExpansionRound{ID: "round-v2", ExpansionID: record.ID, RequestKey: "v2", Version: 2, Status: "completed", InputJSON: marshal(map[string]any{"workflow_input": newInput, "user_updates": []string{"请围绕所选论文或方向开展论文拓展。\noriginal", answer, "continued"}}), ResultJSON: marshal(newInput)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ChatHistory{ID: "later-update", ConversationID: record.ConversationID, Seq: 3, RawContent: "Later instructions must not enter the old round"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := db.Create(&orm.AcademicPaperContent{QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"}), SourceKind: "external", SourceProvider: "arxiv", SourceVersion: "v1", SourceKey: "test-external", ID: "body-" + id, UserID: "u", PaperID: id, ParserVersion: orm.AcademicPaperParserVersion, Status: "ready", ContentHash: "hash-" + id, PagesJSON: marshal([]ContentPage{{Page: 1, Text: "parsed full text"}})}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.AcademicUserPaperState{UserID: "u", DatasetID: "papers", PaperID: "a", IsImportant: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.AcademicUserPaperState{UserID: "other", DatasetID: "papers", PaperID: "b", IsImportant: true}).Error; err != nil {
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
	if handoff.Version != 1 || snapshot.UserInstruction != "original\n\n"+answer || !snapshot.Papers[0].IsImportant || snapshot.Papers[1].IsImportant {
		t.Fatalf("wrong frozen input: %+v", snapshot)
	}
	handoff = orm.AcademicHandoff{}
	if err := db.Where("conversation_id=?", second).Take(&handoff).Error; err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(handoff.InputJSON, &snapshot); err != nil || snapshot.UserInstruction != "original\n\n"+answer+"\n\ncontinued" {
		t.Fatalf("selected round did not retain its frozen input: %q %v", snapshot.UserInstruction, err)
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
		Handoffs        []map[string]json.RawMessage `json:"handoffs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.PaperImportance["a"] || response.PaperImportance["b"] {
		t.Fatal("importance is not user-scoped", response.PaperImportance)
	}
	if string(response.Record.ResultJSON) != string(record.ResultJSON) || len(response.Rounds) != 2 || string(response.Rounds[0].ResultJSON) != string(marshal(oldInput)) || string(response.Rounds[1].ResultJSON) != string(marshal(newInput)) {
		t.Fatal("live importance modified saved result")
	}
	if len(response.Handoffs) != 2 {
		t.Fatal("handoff navigation links were lost", response.Handoffs)
	}
	for _, link := range response.Handoffs {
		if len(link) != 4 || link["id"] == nil || link["kind"] == nil || link["conversation_id"] == nil || link["version"] == nil {
			t.Fatal("handoff list leaked materials or lost navigation fields", link)
		}
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
		r := mux.SetURLVars(paperRequest(string(marshal(map[string]any{"dataset_id": "papers", "is_important": change.important}))), map[string]string{"paper_id": "a"})
		r.Header.Set("X-User-Id", change.user)
		w := httptest.NewRecorder()
		SetImportance(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for user, important := range map[string]bool{"u": true, "reader": false} {
		var saved orm.AcademicUserPaperState
		if err := db.Where("user_id=? AND dataset_id=? AND paper_id=?", user, "papers", "a").Take(&saved).Error; err != nil {
			t.Fatal(err)
		}
		if saved.IsImportant != important {
			t.Fatalf("%s personal importance changed: %+v", user, saved)
		}
	}
}

func TestImportanceRequiresAccessibleLibraryAndDoesNotLeakIntoSelection(t *testing.T) {
	db := paperFixture(t)
	if err := db.Create(&orm.AcademicUserPaperState{UserID: "u", DatasetID: "other", PaperID: "a", IsImportant: true}).Error; err != nil {
		t.Fatal(err)
	}
	papers, invalid, err := resolvePaperSelection(paperRequest(""), "u", "papers", []string{"a"})
	if err != nil || len(invalid) != 0 || len(papers) != 1 || papers[0].IsImportant {
		t.Fatalf("selection inherited another library's mark: %+v %+v %v", papers, invalid, err)
	}
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"is_important":true}`, 400},
		{`{"dataset_id":"inaccessible","is_important":true}`, 403},
	} {
		r := mux.SetURLVars(paperRequest(test.body), map[string]string{"paper_id": "a"})
		w := httptest.NewRecorder()
		SetImportance(w, r)
		if w.Code != test.status {
			t.Fatalf("unscoped or inaccessible mark: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestHistoryReviewSelectedPapersAndIdempotency(t *testing.T) {
	db := paperFixture(t)
	if err := db.Create(&orm.AcademicWork{ID: "c", CanonicalTitle: "C", NormalizedTitle: "c", AuthorsJSON: marshal([]string{}), ExternalIDsJSON: marshal(map[string]string{}), ProvenanceJSON: marshal(map[string]string{})}).Error; err != nil {
		t.Fatal(err)
	}
	input := ReviewWorkflowInput{UserInstruction: "compare methods", SelectedDirections: []string{"tools"}, Papers: []WorkflowPaper{{Paper: PaperRef{PaperID: "a", SourceVersion: "v1", SourceProvider: "arxiv"}, IsSeed: true}, {Paper: PaperRef{PaperID: "b", SourceVersion: "v1", SourceProvider: "arxiv"}}, {Paper: PaperRef{PaperID: "c", SourceVersion: "v1", SourceProvider: "arxiv"}}}}
	old := input
	old.Papers = input.Papers[:2]
	record := orm.AcademicExpansion{ID: "selection", UserID: "u", RequestOwner: "u", RequestKey: "source", DatasetID: "papers", ConversationID: "source-chat", Title: "tools", Goal: "expansion", Status: "completed", Version: 2, ResultJSON: marshal(input)}
	for _, row := range []any{&record, &orm.AcademicExpansionRound{ID: "selection-v1", ExpansionID: record.ID, Version: 1, Status: "completed", InputJSON: marshal(old), ResultJSON: marshal(old)}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := db.Create(&orm.AcademicPaperContent{QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"}), SourceKind: "external", SourceProvider: "arxiv", SourceVersion: "v1", SourceKey: "test-external", ID: "selected-" + id, UserID: "u", PaperID: id, ParserVersion: orm.AcademicPaperParserVersion, Status: "ready", ContentHash: "hash-" + id, PagesJSON: marshal([]ContentPage{{Page: 1, Text: "full text"}})}).Error; err != nil {
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
	var contents handoffContentSnapshot
	if err := json.Unmarshal(handoff.InputJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(handoff.ContentsJSON, &contents); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Papers) != 2 || snapshot.Papers[0].Paper.PaperID != "a" || !snapshot.Papers[0].IsSeed || snapshot.Papers[1].Paper.PaperID != "b" || snapshot.UserInstruction != input.UserInstruction || len(contents.Contents) != 2 || len(contents.Sources) != 2 {
		t.Fatal("review snapshot did not preserve exactly the selected papers", snapshot, contents)
	}
	if err := db.Model(&orm.AcademicPaperContent{QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"})}).Where("paper_id=?", "c").Update("status", "failed").Error; err != nil {
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
	if err := db.Model(&orm.AcademicPaperContent{QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"})}).Where("paper_id=?", "b").Updates(map[string]any{"source_kind": "kb", "source_document_id": "inaccessible", "source_dataset_id": "papers"}).Error; err != nil {
		t.Fatal(err)
	}
	launch("inaccessible", 2, []string{"a", "b"}, 422)
}
