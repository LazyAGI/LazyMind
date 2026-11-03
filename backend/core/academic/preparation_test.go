package academic

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
	"lazymind/core/workflow"
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPreparationCreatesRecordsWithoutChatsAndConfirmsOnce(t *testing.T) {
	db := paperFixture(t)
	for _, key := range []string{"topic-one", "topic-two", "topic-one"} {
		w := httptest.NewRecorder()
		LaunchPaperWorkflow(w, paperRequest(string(marshal(map[string]any{"kind": "expansion", "dataset_id": "papers", "direction": "RAG reliability", "request_key": key, "budget_mode": "balanced"}))))
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
	var input ReviewWorkflowInput
	if err := json.Unmarshal(record.ResultJSON, &input); err != nil || input.Papers == nil || len(input.Papers) != 0 {
		t.Fatalf("topic launch must store an empty paper array: %s (%v)", record.ResultJSON, err)
	}
	p := expansionPreparation{UserInstruction: "RAG reliability", Summary: "Retrieved representative work", Directions: []preparationDirection{{Name: "retrieval correction", Question: "How can retrieval be corrected?", Reason: "Distinct correction methods", PaperIDs: []string{"a"}}}}
	db.Model(&record).Updates(map[string]any{"status": "awaiting_confirmation", "preparation_json": marshal(p), "discovery_json": marshal([]paperDiscovery{{Papers: []PaperRef{{PaperID: "a", Title: "a"}}}})})
	for _, revision := range []int{0, 1, 1} {
		w := httptest.NewRecorder()
		r := mux.SetURLVars(paperRequest(string(marshal(map[string]any{"revision": revision, "request_key": "confirmed", "selected_directions": []string{"retrieval correction"}}))), map[string]string{"expansion_id": record.ID})
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
	if record.BudgetMode != "balanced" || record.Status != "pending" {
		t.Fatal("confirmation did not preserve the launch depth/state")
	}
	input = ReviewWorkflowInput{}
	if err := json.Unmarshal(record.ResultJSON, &input); err != nil || input.Papers == nil || len(input.Papers) != 0 {
		t.Fatalf("confirmation must preserve empty seeds without promoting references: %s (%v)", record.ResultJSON, err)
	}
	if len(input.SelectedDirections) != 1 || input.SelectedDirections[0] != "retrieval correction" {
		t.Fatal("confirmed scope not frozen")
	}
}

func TestPreparationAnswersFenceOldAttempt(t *testing.T) {
	db := paperFixture(t)
	p := expansionPreparation{DirectionSelection: "manual", UserInstruction: "agents", Summary: "need scope", Questions: []preparationQuestion{{ID: "scope", Question: "Which setting?"}}, Answers: map[string]string{}}
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
	var saved expansionPreparation
	if err := json.Unmarshal(record.PreparationJSON, &saved); err != nil || saved.DirectionSelection != "manual" {
		t.Fatalf("answering changed direction selection: %+v %v", saved, err)
	}
}

func TestAcademicDepthPreservesWorkLimitsWithoutWallTimeDeadline(t *testing.T) {
	db := paperFixture(t)
	start := time.Now().Add(-24 * time.Hour)
	p := expansionPreparation{Selected: []string{"agents"}, ExecutionStartedAt: &start}
	record := orm.AcademicExpansion{ID: "depth", UserID: "u", DatasetID: "papers", RequestOwner: "u", RequestKey: "depth", ConversationID: "depth-chat", Status: "running", BudgetMode: "fast", PreparationJSON: marshal(p), ResultJSON: json.RawMessage(`{}`)}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	params := workflow.WorkflowStepParams{StepID: "expand_and_screen", Runtime: graphengine.RuntimePolicy{HostExtensions: []string{"academic-paper-chat-v1"}, ExecutionLimits: map[string]graphengine.ExecutionLimit{"expand_and_screen": {Rounds: 32, Timeout: 120}}}}
	for i := 0; i < 2; i++ {
		if err := configureAcademicStep(t.Context(), db.DB, "u", record.ConversationID, &params); err != nil {
			t.Fatal(err)
		}
		limit := params.Runtime.ExecutionLimits[params.StepID]
		if limit.Timeout != 0 || limit.Rounds == 0 || limit.ToolCalls["academic_search_papers"] == 0 {
			t.Fatalf("unexpected depth limits: %+v", limit)
		}
	}
	deep, _ := depthFor("deep")
	if deep.Steps[params.StepID].Rounds <= params.Runtime.ExecutionLimits[params.StepID].Rounds {
		t.Fatal("depth did not change research work")
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

func TestRepeatedContentReadsReuseExistingJob(t *testing.T) {
	db := paperFixture(t)
	source := paperSource{DatasetID: "papers", Paper: PaperRef{PaperID: "read-paper", Title: "Repeated reading"}}
	first, err := ensurePaperContent(t.Context(), db.DB, "u", source.Paper.PaperID, source)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		row, e := ensurePaperContent(t.Context(), db.DB, "u", source.Paper.PaperID, source)
		if e != nil || row.ID != first.ID || row.Status != "processing" {
			t.Fatal("repeated read did not reuse content work", e)
		}
	}
	var jobs int64
	db.Model(&orm.AsyncJob{}).Where("job_type=? AND resource_id=?", "academic.paper_content", first.ID).Count(&jobs)
	if jobs != 1 {
		t.Fatal("repeated reads queued duplicate content work", jobs)
	}
}

func TestPreparationDepthCannotChangeAfterLaunch(t *testing.T) {
	db := paperFixture(t)
	p := expansionPreparation{Summary: "Evidence", UserInstruction: "agents", Directions: []preparationDirection{{Name: "tools", Question: "Which tools?", Reason: "evidence", PaperIDs: []string{"a"}}}}
	record := orm.AcademicExpansion{ID: "fixed-depth", UserID: "u", DatasetID: "papers", RequestOwner: "u", RequestKey: "fixed-depth", Status: "awaiting_confirmation", BudgetMode: "fast", PreparationRevision: 1, PreparationJSON: marshal(p), ResultJSON: marshal(ReviewWorkflowInput{UserInstruction: "agents"})}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"revise", "confirm"} {
		body := map[string]any{"revision": 1, "request_key": action, "budget_mode": "deep", "action": action, "user_instruction": "new question", "selected_directions": []string{"tools"}}
		w := httptest.NewRecorder()
		r := mux.SetURLVars(paperRequest(string(marshal(body))), map[string]string{"expansion_id": record.ID})
		if action == "confirm" {
			ConfirmExpansion(w, r)
		} else {
			SubmitPreparation(w, r)
		}
		if w.Code != http.StatusBadRequest {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if err := db.First(&record, "id=?", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.BudgetMode != "fast" || record.Status != "awaiting_confirmation" || record.ConversationID != "" {
		t.Fatal("follow-up changed launch settings")
	}
}

func TestPreparationRespectsDirectionSelection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		count     int
		question  bool
		status    string
		selection string
	}{
		{"legacy single", 1, false, "pending", ""}, {"legacy multiple", 2, false, "awaiting_confirmation", ""},
		{"all directions", 2, false, "pending", "all"}, {"manual single", 1, false, "awaiting_confirmation", "manual"}, {"manual multiple", 2, false, "awaiting_confirmation", "manual"},
		{"needs information", 1, true, "awaiting_info", "all"}, {"no directions", 0, false, "preparation_failed", "all"}, {"too many directions", 6, false, "preparation_failed", "all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := paperFixture(t)
			base := orm.BaseModel{CreateUserID: "u", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			for _, model := range []any{
				&orm.UserModelProvider{ID: "provider", Name: "openai", Category: "model", Capabilities: "has_models", BaseModel: base},
				&orm.UserModelProviderGroup{ID: "group", UserModelProviderID: "provider", Name: "test", BaseURL: "https://model.invalid/v1", APIKey: "fixture-key", IsVerified: true, BaseModel: base},
				&orm.UserModelProviderGroupModel{ID: "model", UserModelProviderID: "provider", UserModelProviderGroupID: "group", ProviderName: "openai", Name: "gpt-5", ModelType: "llm", BaseModel: base},
				&orm.UserSelectedModel{UserID: "u", ModelKey: "llm", UserModelProviderGroupModelID: "model"},
			} {
				if err := db.Create(model).Error; err != nil {
					t.Fatal(err)
				}
			}
			prepared := expansionPreparation{Summary: "Grounded research", UserInstruction: "agents", DirectionSelection: "all"}
			if tc.selection == "all" {
				prepared.DirectionSelection = "manual"
			}
			for i := 0; i < tc.count; i++ {
				prepared.Directions = append(prepared.Directions, preparationDirection{Name: fmt.Sprintf("direction-%d", i), Question: "Which evidence?", Reason: "Distinct evidence", PaperIDs: []string{"a"}})
			}
			if tc.question {
				prepared.Questions = []preparationQuestion{{ID: "scope", Question: "Which scenario?"}}
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request struct {
					Depth expansionDepth `json:"depth"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request.Depth.Preparation.Rounds == 0 || request.Depth.Guidance == "" {
					t.Error("fixed depth missing from preparation")
				}
				w.Header().Set("Content-Type", "application/json")
				response := map[string]any{}
				if err := json.Unmarshal(marshal(prepared), &response); err != nil {
					t.Error(err)
				}
				response["title"] = "Research title"
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "fixture-token")
			old := time.Now().Add(-24 * time.Hour)
			record := orm.AcademicExpansion{ID: "prepared", UserID: "u", DatasetID: "papers", RequestOwner: "u", RequestKey: "prepared", Status: "preparing", BudgetMode: "deep", PreparationRevision: 1, PreparationJSON: marshal(expansionPreparation{UserInstruction: "agents", StartedAt: &old, DirectionSelection: tc.selection}), ResultJSON: marshal(ReviewWorkflowInput{UserInstruction: "agents", Papers: []WorkflowPaper{}}), DiscoveryJSON: marshal([]paperDiscovery{{Papers: []PaperRef{{PaperID: "a", Title: "Actual paper"}}}})}
			if err := db.Create(&record).Error; err != nil {
				t.Fatal(err)
			}
			job := asyncjob.Job{ResourceID: record.ID, CreateUserID: "u", PayloadJSON: marshal(map[string]int{"revision": 1})}
			for i := 0; i < 2; i++ {
				if _, err := runPreparation(t.Context(), job, nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.First(&record, "id=?", record.ID).Error; err != nil {
				t.Fatal(err)
			}
			if record.Status != tc.status || record.BudgetMode != "deep" || calls != 1 {
				t.Fatalf("unexpected state: %s, depth: %s, calls: %d", record.Status, record.BudgetMode, calls)
			}
			var chats, jobs int64
			db.Model(&orm.Conversation{}).Count(&chats)
			db.Model(&orm.AsyncJob{}).Where("job_type=?", ChatLaunchJobType).Count(&jobs)
			var saved expansionPreparation
			if err := json.Unmarshal(record.PreparationJSON, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.DirectionSelection != tc.selection {
				t.Fatalf("model changed launch selection: %+v", saved)
			}
			if tc.status == "pending" {
				if chats != 1 || jobs != 1 || !saved.AutoConfirmed || len(saved.Selected) != tc.count {
					t.Fatalf("selected directions did not start exactly once: chats %d, jobs %d, preparation %+v", chats, jobs, saved)
				}
			} else if chats != 0 || jobs != 0 {
				t.Fatal("unconfirmed or invalid preparation started a Chat")
			}
		})
	}
}

func TestPreparationUsesNewlyRegisteredCandidatesAndFencesLateCompletion(t *testing.T) {
	db := paperFixture(t)
	base := orm.BaseModel{CreateUserID: "u", CreateUserName: "u", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	for _, row := range []any{
		&orm.UserModelProvider{ID: "preparation-provider", Name: "OpenAI", Category: "model", Capabilities: "has_models", BaseModel: base},
		&orm.UserModelProviderGroup{ID: "preparation-group", UserModelProviderID: "preparation-provider", Name: "test", BaseURL: "https://example.invalid/v1", APIKey: "fixture", IsVerified: true, BaseModel: base},
		&orm.UserModelProviderGroupModel{ID: "preparation-model", UserModelProviderID: "preparation-provider", UserModelProviderGroupID: "preparation-group", ProviderName: "OpenAI", Name: "gpt-5", ModelType: "llm", BaseModel: base},
		&orm.UserSelectedModel{UserID: "u", UserName: "u", ModelKey: "llm", UserModelProviderGroupModelID: "preparation-model"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	mode, source, modelID := "fixed", "own", "scoped-kimi-model"
	for _, row := range []any{
		&orm.UserModelProviderGroupModel{ID: modelID, UserModelProviderID: "preparation-provider", UserModelProviderGroupID: "preparation-group", ProviderName: "OpenAI", Name: "k3", ModelType: "llm", BaseModel: base},
		&orm.Conversation{ID: "scoped-chat", ChatModelMode: &mode, ChatModelID: &modelID, ChatModelSource: &source, ChatModelVersion: 1, BaseModel: base},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	work, _, err := findOrCreateWork(t.Context(), db.DB, WorkInput{Title: "Actual representative study", DOI: "10.1234/representative"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			ExpansionID string                    `json:"expansion_id"`
			LLM         map[string]map[string]any `json:"llm_config"`
		}
		if e := json.NewDecoder(r.Body).Decode(&payload); e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		expectedModel := "gpt-5"
		if payload.ExpansionID == "scoped" {
			expectedModel = "k3"
		}
		if payload.LLM["llm"]["model"] != expectedModel {
			t.Errorf("preparation model = %v, want %s", payload.LLM["llm"]["model"], expectedModel)
		}
		if payload.ExpansionID == "stale" {
			db.Model(&orm.AcademicExpansion{}).Where("id=?", "stale").Updates(map[string]any{"preparation_revision": 2, "preparation_json": marshal(expansionPreparation{Summary: "new attempt", UserInstruction: "new scope"})})
		} else {
			db.Model(&orm.AcademicExpansion{}).Where("id=?", payload.ExpansionID).Update("discovery_json", marshal([]paperDiscovery{{Papers: []PaperRef{paperRef(work)}}}))
		}
		json.NewEncoder(w).Encode(expansionPreparation{
			Summary:           "Grounded scope",
			Directions:        []preparationDirection{{Name: "retrieval correction", Question: "When does correction help?", Difference: "retrieval-side changes", Reason: "registered representative", PaperIDs: []string{work.ID}}},
			ResearchQuestions: []preparationResearchQuestion{{ID: "origin", Direction: "retrieval correction", Question: "Which design source explains correction?", Category: "predecessor"}},
			NamedClues: []preparationNamedClue{{Name: "Query Router", Role: "named mechanism", SourcePaperID: work.ID,
				SourceLocation: "Section 3, p. 4", SourceVersion: "source-v1", SourceExcerpt: "The Query Router selects a retrieval strategy.", Positions: []int{0}, Identity: map[string]string{"doi": "10.1234/representative"}, QuestionIDs: []string{"origin"}, PaperID: work.ID}},
		})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "preparation-fixture")
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	for _, id := range []string{"fresh", "scoped", "stale"} {
		record := orm.AcademicExpansion{ID: id, UserID: "u", DatasetID: "papers", RequestOwner: "u", RequestKey: id, Status: "preparing", PreparationRevision: 1, BudgetMode: "fast", DiscoveryMode: "external", DiscoveryJSON: marshal([]paperDiscovery{}), ResultJSON: marshal(ReviewWorkflowInput{Papers: []WorkflowPaper{}}), PreparationJSON: marshal(expansionPreparation{UserInstruction: "RAG reliability"})}
		if id == "scoped" {
			record.ConversationID = "scoped-chat"
		}
		if err = db.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
		if _, err = runPreparation(t.Context(), asyncjob.Job{ResourceID: id, CreateUserID: "u", PayloadJSON: marshal(map[string]int{"revision": 1})}, nil); err != nil {
			t.Fatal(err)
		}
		if err = db.Where("id=?", id).Take(&record).Error; err != nil {
			t.Fatal(err)
		}
		var preparation expansionPreparation
		json.Unmarshal(record.PreparationJSON, &preparation)
		if (id == "fresh" || id == "scoped") && (record.Status != "pending" || !preparation.AutoConfirmed || len(preparation.Directions) != 1 || len(preparation.Directions[0].Papers) != 1) {
			t.Fatal("newly registered candidates were not validated and materialized", record.Status, preparation.Error)
		}
		if id == "scoped" && preparation.Model != "k3" {
			t.Fatalf("preparation attempt recorded model %q, want k3", preparation.Model)
		}
		if id == "fresh" {
			if len(preparation.ResearchQuestions) != 1 || preparation.ResearchQuestions[0].Category != "predecessor" || len(preparation.NamedClues) != 1 {
				t.Fatal("preparation dropped structured research questions or named clues", preparation)
			}
			clue := preparation.NamedClues[0]
			if clue.Name != "Query Router" || clue.SourcePaperID != work.ID || clue.SourceLocation != "Section 3, p. 4" || clue.SourceVersion != "source-v1" || clue.SourceExcerpt != "The Query Router selects a retrieval strategy." || len(clue.Positions) != 1 || clue.Positions[0] != 0 || clue.Identity["doi"] != "10.1234/representative" || len(clue.QuestionIDs) != 1 || clue.QuestionIDs[0] != "origin" {
				t.Fatal("preparation dropped original clue provenance or question links", clue)
			}
		}
		if id == "stale" && (record.PreparationRevision != 2 || record.Status != "preparing" || preparation.Summary != "new attempt") {
			t.Fatal("old attempt overwrote newer preparation")
		}
	}
}

func TestRoundReportSeparatesReusedSearchesFromCurrentUsage(t *testing.T) {
	db := paperFixture(t)
	start := time.Now().UTC()
	record := orm.AcademicExpansion{UserID: "u", PreparationJSON: marshal(expansionPreparation{ExecutionStartedAt: &start}), DiscoveryJSON: marshal([]paperDiscovery{
		{SessionID: "session", Query: "previous round", CreatedAt: start.Add(-time.Minute)},
		{SessionID: "session", Query: "current round", CreatedAt: start.Add(time.Second)},
	})}
	raw, err := expansionReportSnapshot(db.DB, record, "session", "attempt", nil)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Calls  []paperDiscovery `json:"source_queries"`
		Reused []paperDiscovery `json:"reused_source_queries"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Calls) != 1 || result.Calls[0].Query != "current round" || len(result.Reused) != 1 || result.Reused[0].Query != "previous round" {
		t.Fatal("reused sources were counted as new round calls")
	}
}

func TestPDFParserVersionMismatchCannotBecomeUsableText(t *testing.T) {
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "parser-fixture")
	version := "pdf-reader-v1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"pages": []ContentPage{{Page: 1, Text: "non-empty text"}}, "parser_version": version})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	path := filepath.Join(t.TempDir(), "test.pdf")
	if err := os.WriteFile(path, []byte("%PDF-test"), 0600); err != nil {
		t.Fatal(err)
	}
	pages, hash, err := parsePaperPDF(t.Context(), path, "")
	if err == nil || len(pages) != 0 || hash == "" {
		t.Fatal("old service output accepted, or successful download hash lost")
	}
	version = orm.AcademicPaperParserVersion
	pages, _, err = parsePaperPDF(t.Context(), path, "")
	if err != nil || len(pages) != 1 {
		t.Fatal("matching parser contract rejected", err)
	}
}

func TestPreparationRequiresAnchoredNamedClues(t *testing.T) {
	db := paperFixture(t)
	source := PaperRef{PaperID: "a", SourceProvider: "arxiv", SourceVersion: "v1"}
	record := orm.AcademicExpansion{DatasetID: "papers", SeedSnapshotJSON: marshal([]paperSource{}), DiscoveryJSON: marshal([]paperDiscovery{{Papers: []PaperRef{source}}})}
	preparation := expansionPreparation{Summary: "Grounded research scope", Directions: []preparationDirection{{Name: "cache sharing", Question: "What is shared?", Reason: "Original method", PaperIDs: []string{"a"}}},
		ResearchQuestions: []preparationResearchQuestion{{ID: "origin", Direction: "cache sharing", Question: "Which source method is used?", Category: "predecessor"}}}
	clue := preparationNamedClue{Name: "Original method", Role: "design source", SourcePaperID: "a", SourceVersion: "v1", SourceExcerpt: "We build on Original method.", Positions: []int{0}, QuestionIDs: []string{"origin"}}
	cases := []struct {
		name   string
		change func(*preparationNamedClue)
		valid  bool
	}{
		{name: "content block", change: func(*preparationNamedClue) {}, valid: true},
		{name: "bibliography reference", change: func(c *preparationNamedClue) { c.Positions = nil; c.ReferenceKey = "7" }, valid: true},
		{name: "missing excerpt", change: func(c *preparationNamedClue) { c.SourceExcerpt = " " }},
		{name: "missing anchor", change: func(c *preparationNamedClue) { c.Positions = nil }},
		{name: "too many blocks", change: func(c *preparationNamedClue) { c.Positions = []int{0, 1, 2, 3, 4, 5} }},
		{name: "negative block", change: func(c *preparationNamedClue) { c.Positions = []int{-1} }},
		{name: "duplicate block", change: func(c *preparationNamedClue) { c.Positions = []int{0, 0} }},
		{name: "different source revision", change: func(c *preparationNamedClue) { c.SourceVersion = "v2" }},
		{name: "missing known source revision", change: func(c *preparationNamedClue) { c.SourceVersion = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := clue
			tc.change(&candidate)
			preparation.NamedClues = []preparationNamedClue{candidate}
			err := validatePreparation(db.DB, record, preparation)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, got error %v", tc.valid, err)
			}
		})
	}
	preparation.Questions = []preparationQuestion{{ID: "scope", Question: "Which workload should be studied?"}}
	clue.SourceExcerpt = ""
	preparation.NamedClues = []preparationNamedClue{clue}
	if err := validatePreparation(db.DB, record, preparation); err == nil {
		t.Fatal("awaiting user information allowed an unanchored original-source clue")
	}

}

func TestPaperUserInstructionOmitsConfirmedLaunchTemplate(t *testing.T) {
	for _, launch := range []string{directExpansionLaunchInstruction, confirmedExpansionLaunchInstruction, "请按照已确认范围执行四步论文拓展。准备成果已保存，请先读取任务上下文。"} {
		got := paperUserInstruction("缓存压缩", []string{launch, "关注共享机制"})
		if got != "缓存压缩\n\n关注共享机制" {
			t.Fatalf("launch template leaked into research scope: %q", got)
		}
	}
}

func TestExpansionSessionHasPlannedStagesBeforeExecution(t *testing.T) {
	db := paperFixture(t)
	root := filepath.Join("..", "..", "..", "workflows", "paper_expansion")
	definition, err := os.ReadFile(filepath.Join(root, "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(filepath.Join(root, "scenario", "state.yml"))
	if err != nil {
		t.Fatal(err)
	}
	compiled := graphengine.Compile(string(definition), string(state), "", graphengine.ProfileRuntimeLoad)
	if !compiled.Valid {
		t.Fatal(compiled.Diagnostics)
	}
	pkg := workflowstore.WorkflowPackage{WorkflowID: "paper_expansion", RevisionID: "revision", CompiledGraph: marshal(compiled.Graph)}
	for _, tc := range []struct {
		mode, goal string
		markers    []string
		next       []string
	}{
		{"fast", "expansion", []string{"analysis_not_required", "screening_not_required"}, nil},
		{"balanced", "expansion", []string{"analysis_not_required"}, []string{"screen_and_supplement"}},
		{"deep", "expansion", []string{}, []string{"screen_and_supplement"}},
		{"balanced", "review", []string{"screening_not_required"}, []string{"analyze_relations"}},
	} {
		t.Run(tc.goal+"_"+tc.mode, func(t *testing.T) {
			id := tc.goal + "_" + tc.mode
			if err := db.Create(&orm.Conversation{ID: id, BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.AcademicExpansion{ID: id, UserID: "u", ConversationID: id, DatasetID: "papers", RequestOwner: "u", RequestKey: id, Goal: tc.goal, BudgetMode: tc.mode, Status: "pending", ResultJSON: marshal(ReviewWorkflowInput{})}).Error; err != nil {
				t.Fatal(err)
			}
			repo := workflowstore.New(db.DB)
			for range 2 {
				if _, _, err := repo.CreateHostSession(t.Context(), "u", id, id, "lazymind", id, "lazymind", pkg); err != nil {
					t.Fatal(err)
				}
			}
			slots, err := workflow.LoadDisplaySlots(t.Context(), db.DB, id)
			if err != nil {
				t.Fatal(err)
			}
			markers := []string{}
			snapshot := graphengine.RuntimeSnapshot{Materials: []graphengine.MaterialValue{{MaterialID: "expansion_id", RevisionID: "input", Valid: true}}}
			for _, slot := range slots {
				markers = append(markers, slot.SlotID)
				snapshot.Materials = append(snapshot.Materials, graphengine.MaterialValue{MaterialID: slot.SlotID, RevisionID: slot.ID, Valid: true})
			}
			if !reflect.DeepEqual(markers, tc.markers) {
				t.Fatalf("startup materials: got %v, want %v", markers, tc.markers)
			}
			if initial := graphengine.Project(compiled.Graph, snapshot); initial.Completed || !reflect.DeepEqual(initial.Ready, []string{"expand_and_screen"}) {
				t.Fatalf("startup must still run search: %+v", initial)
			}
			snapshot.Attempts = []graphengine.AttemptFact{{StepID: "expand_and_screen", Status: "succeeded", Validity: "effective"}}
			snapshot.Materials = append(snapshot.Materials, graphengine.MaterialValue{MaterialID: "candidate_set", RevisionID: "candidate", Valid: true})
			next := graphengine.Project(compiled.Graph, snapshot)
			if len(next.Ready) != len(tc.next) || len(tc.next) > 0 && !reflect.DeepEqual(next.Ready, tc.next) {
				t.Fatalf("after search: %+v", next)
			}
			if next.Completed != (tc.mode == "fast") {
				t.Fatalf("unexpected completion: %+v", next)
			}
		})
	}
}

func TestPreparationScopeChangesStayBeforeExecution(t *testing.T) {
	for _, status := range []string{"awaiting_confirmation", "preparation_failed", "pending", "running", "completed", "failed", "stopped"} {
		t.Run(status, func(t *testing.T) {
			db := paperFixture(t)
			p := expansionPreparation{UserInstruction: "original scope"}
			record := orm.AcademicExpansion{ID: "scope", UserID: "u", DatasetID: "papers", RequestOwner: "u", RequestKey: "scope", Status: status, PreparationRevision: 1, PreparationJSON: marshal(p), ResultJSON: marshal(ReviewWorkflowInput{UserInstruction: "original scope", Papers: []WorkflowPaper{}})}
			if err := db.Create(&record).Error; err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			SubmitPreparation(response, mux.SetURLVars(paperRequest(`{"action":"revise","revision":1,"request_key":"new-scope","user_instruction":"new topic"}`), map[string]string{"expansion_id": record.ID}))
			var saved orm.AcademicExpansion
			if err := db.First(&saved, "id=?", record.ID).Error; err != nil {
				t.Fatal(err)
			}
			if status == "awaiting_confirmation" || status == "preparation_failed" {
				if response.Code != 200 || saved.Status != "preparing" || saved.PreparationRevision != 2 {
					t.Fatalf("scope update failed: %d %s %+v", response.Code, response.Body.String(), saved)
				}
			} else {
				if response.Code != 409 || !strings.Contains(response.Body.String(), "新建任务") {
					t.Fatal(response.Code, response.Body.String())
				}
				if saved.Status != status || saved.PreparationRevision != 1 || string(saved.PreparationJSON) != string(record.PreparationJSON) {
					t.Fatal("rejected revision changed the record")
				}
				var jobs int64
				db.Model(&orm.AsyncJob{}).Count(&jobs)
				if jobs != 0 {
					t.Fatal("rejected revision queued work", jobs)
				}
			}
		})
	}
}
