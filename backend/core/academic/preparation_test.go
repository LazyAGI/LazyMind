package academic

import (
	"encoding/json"
	"github.com/gorilla/mux"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
	"lazymind/core/workflow"
	"lazymind/core/workflow/graphengine"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPreparationCreatesRecordsWithoutChatsAndConfirmsOnce(t *testing.T) {
	db := paperFixture(t)
	for _, key := range []string{"topic-one", "topic-two", "topic-one"} {
		w := httptest.NewRecorder()
		LaunchPaperWorkflow(w, paperRequest(string(marshal(map[string]any{"kind": "expansion", "dataset_id": "papers", "direction": "RAG reliability", "request_key": key}))))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var chats, records int64
	db.Model(&orm.Conversation{}).Count(&chats)
	db.Model(&orm.AcademicExpansion{}).Count(&records)
	if chats != 0 || records != 2 {
		t.Fatalf("preparation created chats or duplicate records: %d %d", chats, records)
	}
	var record orm.AcademicExpansion
	if err := db.Where("request_key=?", "topic-one").Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	var nulls int64
	db.Model(&orm.AcademicExpansion{}).Where("conversation_id IS NULL").Count(&nulls)
	if nulls != 2 {
		t.Fatal("uncreated Chat locators must use SQL NULL")
	}
	if record.Status != "preparing" || record.BudgetMode != "balanced" || record.PreparationRevision != 1 {
		t.Fatalf("bad initial state: %+v", record)
	}
	p := expansionPreparation{UserInstruction: "RAG reliability", Summary: "Retrieved representative work", Directions: []preparationDirection{{Name: "retrieval correction", Reason: "Distinct correction methods", PaperIDs: []string{"a"}}}}
	db.Model(&record).Updates(map[string]any{"status": "awaiting_confirmation", "preparation_json": marshal(p), "discovery_json": marshal([]paperDiscovery{{Papers: []PaperRef{{PaperID: "a", Title: "a"}}}})})
	for _, revision := range []int{0, 1, 1} {
		w := httptest.NewRecorder()
		r := mux.SetURLVars(paperRequest(string(marshal(map[string]any{"revision": revision, "request_key": "confirmed", "selected_directions": []string{"retrieval correction"}, "budget_mode": "fast"}))), map[string]string{"expansion_id": record.ID})
		ConfirmExpansion(w, r)
		if revision == 0 && w.Code != 409 || revision == 1 && w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	db.Model(&orm.Conversation{}).Count(&chats)
	if chats != 1 {
		t.Fatal("confirmation must create exactly one Chat", chats)
	}
	db.Where("id=?", record.ID).Take(&record)
	if record.BudgetMode != "fast" || record.Status != "pending" {
		t.Fatal("confirmation did not preserve selected budget/state")
	}
	var input ReviewWorkflowInput
	json.Unmarshal(record.ResultJSON, &input)
	if len(input.SelectedDirections) != 1 || input.SelectedDirections[0] != "retrieval correction" {
		t.Fatal("confirmed scope not frozen")
	}
}

func TestPreparationAnswersFenceOldAttempt(t *testing.T) {
	db := paperFixture(t)
	p := expansionPreparation{UserInstruction: "agents", Summary: "need scope", Questions: []preparationQuestion{{ID: "scope", Question: "Which setting?"}}, Answers: map[string]string{}}
	record := orm.AcademicExpansion{ID: "preparing", UserID: "u", DatasetID: "papers", RequestOwner: "u", RequestKey: "prep", Status: "awaiting_info", PreparationRevision: 1, PreparationJSON: marshal(p), ResultJSON: marshal(ReviewWorkflowInput{UserInstruction: "agents", Papers: []WorkflowPaper{}})}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	body := `{"action":"answer","revision":1,"request_key":"answer-one","answers":{"scope":"local tools"}}`
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		SubmitPreparation(w, mux.SetURLVars(paperRequest(body), map[string]string{"expansion_id": record.ID}))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	db.Where("id=?", record.ID).Take(&record)
	json.Unmarshal(record.PreparationJSON, &p)
	if record.PreparationRevision != 2 || record.Status != "preparing" || p.Answers["scope"] != "local tools" {
		t.Fatal("answers were not persisted idempotently")
	}
	if _, err := runPreparation(t.Context(), asyncjob.Job{ResourceID: record.ID, CreateUserID: "u", PayloadJSON: marshal(map[string]int{"revision": 1})}, nil); err != nil {
		t.Fatal("stale attempt should stop before model call", err)
	}
}

func TestAcademicBudgetCannotResetOnAutomaticRetry(t *testing.T) {
	db := paperFixture(t)
	start := time.Now().Add(-2 * time.Minute)
	p := expansionPreparation{Selected: []string{"agents"}, ExecutionStartedAt: &start, StepStartedAt: map[string]time.Time{"prepare_plan": start}}
	record := orm.AcademicExpansion{ID: "budget", UserID: "u", DatasetID: "papers", RequestOwner: "u", RequestKey: "budget", ConversationID: "budget-chat", Status: "running", BudgetMode: "fast", PreparationJSON: marshal(p), ResultJSON: json.RawMessage(`{}`)}
	db.Create(&record)
	params := workflow.WorkflowStepParams{StepID: "prepare_plan", Runtime: graphengine.RuntimePolicy{HostExtensions: []string{"academic-paper-chat-v1"}, ExecutionLimits: map[string]graphengine.ExecutionLimit{"prepare_plan": {Rounds: 12, Timeout: 120}}}}
	for i := 0; i < 2; i++ {
		if err := configureAcademicStep(t.Context(), db.DB, "u", record.ConversationID, &params); err == nil {
			t.Fatal("automatic retry reset the exhausted budget")
		}
	}
}

func TestContentQualityAndSelectedReading(t *testing.T) {
	paper := PaperRef{Title: "Reliable retrieval correction for language models"}
	body := paper.Title + "\nAbstract\n" + "We evaluate retrieval correction with controlled experiments and discuss limitations. "
	pages := []ContentPage{{Page: 1, Text: body + body}}
	if q := checkPaperQuality(paper, pages, "external", "hash"); !q.Eligible {
		t.Fatalf("real main text rejected: %+v", q)
	}
	cases := []ContentPage{{Page: 1, Text: "Supplementary material\n" + body + body}, {Page: 1, Text: "Training slides\n" + body + body}, {Page: 1, Text: "Wrong paper on a different subject\n" + string(make([]byte, 300))}, {Page: 1, Text: ".... !!! @@@ ???"}}
	// Supplementary material remains non-main even if it repeats the paper title.
	cases[0].Text = "Supplementary material\n" + paper.Title + "\n" + "Additional experimental tables and details. Additional experimental tables and details. Additional experimental tables and details."
	cases[1].Text = "Training slides\n" + paper.Title + "\n" + "Course outline and examples. Course outline and examples. Course outline and examples. Course outline and examples."
	for _, bad := range cases {
		if q := checkPaperQuality(paper, []ContentPage{bad}, "external", "hash"); q.Eligible {
			t.Fatalf("invalid material counted as main text: %+v", q)
		}
	}
	blocks := []ContentPage{{Page: 1, Text: "intro"}, {Page: 2, Text: "methods"}, {Page: 3, Text: "latency includes sequence search"}, {Page: 4, Text: "runtime prerequisites"}, {Page: 5, Text: "references"}}
	selected, err := selectedContentPositions(blocks, []string{"latency"}, 0, 0)
	if err != nil || string(marshal(selected)) != "[1,2,3]" {
		t.Fatal("targeted reading did not include neighboring complete pages", selected, err)
	}
	selected, err = selectedContentPositions(blocks, nil, 4, 4)
	if err != nil || len(selected) != 1 || selected[0] != 3 {
		t.Fatal("explicit page selection failed", selected, err)
	}
}

func TestPaperNotificationDeduplicatesAndReadDoesNotConfirm(t *testing.T) {
	db := paperFixture(t)
	record := orm.AcademicExpansion{ID: "notice-exp", UserID: "u", DatasetID: "papers", RequestOwner: "u", RequestKey: "notice", Status: "awaiting_confirmation", ResultJSON: json.RawMessage(`{}`)}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := taskcenter.NotifyAcademicExpansion(t.Context(), db.DB, "u", "papers", record.ID, 2, "awaiting_confirmation"); err != nil {
			t.Fatal(err)
		}
	}
	var notices []orm.TaskNotification
	db.Where("user_id=?", "u").Find(&notices)
	if len(notices) != 1 {
		t.Fatal("same pending action notified twice", len(notices))
	}
	var target map[string]string
	json.Unmarshal(notices[0].TargetJSON, &target)
	if target["type"] != "academic_expansion" || target["expansion_id"] != record.ID {
		t.Fatal("notice did not identify its record")
	}
	w := httptest.NewRecorder()
	ReadPaperNotification(w, mux.SetURLVars(paperRequest(`{}`), map[string]string{"notification_id": notices[0].ID}))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	db.Where("id=?", record.ID).Take(&record)
	if record.Status != "awaiting_confirmation" || record.ConversationID != "" {
		t.Fatal("reading a notice started or changed the task")
	}
	w = httptest.NewRecorder()
	PaperNotifications(w, paperRequest(`{}`))
	if strings.Contains(w.Body.String(), notices[0].ID) {
		t.Fatal("read notice remained unread")
	}
}

func TestContentBudgetRenewsOnlyForNewAuthorizedExecution(t *testing.T) {
	db := paperFixture(t)
	old := time.Now().Add(-time.Minute)
	source := paperSource{DatasetID: "papers", Paper: PaperRef{PaperID: "budget-paper", Title: "Budget test paper"}, Deadline: &old}
	row, err := ensurePaperContent(t.Context(), db.DB, "u", source.Paper.PaperID, source)
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&row).Updates(map[string]any{"status": "unavailable", "error": "任务取文执行预算已用尽"})
	db.Model(&orm.AsyncJob{}).Where("job_type=? AND idempotency_key=?", "academic.paper_content", row.ID).Update("status", string(asyncjob.StatusFailed))
	same, err := ensurePaperContent(t.Context(), db.DB, "u", source.Paper.PaperID, source)
	if err != nil || same.Status != "unavailable" {
		t.Fatal("same exhausted attempt was renewed", err)
	}
	next := time.Now().Add(time.Minute)
	source.Deadline = &next
	for i := 0; i < 2; i++ {
		resumed, e := ensurePaperContent(t.Context(), db.DB, "u", source.Paper.PaperID, source)
		if e != nil || resumed.ID != row.ID || resumed.Status != "processing" {
			t.Fatal("new execution did not resume existing content job", e)
		}
	}
	var jobs int64
	db.Model(&orm.AsyncJob{}).Where("job_type=? AND resource_id=?", "academic.paper_content", row.ID).Count(&jobs)
	if jobs != 1 {
		t.Fatal("repeated reads queued duplicate content work", jobs)
	}
}
