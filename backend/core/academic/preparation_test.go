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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreparationCreatesRecordsWithoutChatsAndConfirmsOnce(t *testing.T) {
	db := paperFixture(t)
	for _, key := range []string{"topic-one", "topic-two", "topic-one"} {
		w := httptest.NewRecorder()
		LaunchPaperWorkflow(w, paperRequest(string(marshal(map[string]any{"kind": "expansion", "dataset_id": "papers", "direction": "RAG reliability", "request_key": key, "budget_mode": "fast"}))))
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
	if record.Status != "preparing" || record.BudgetMode != "fast" || record.PreparationRevision != 1 {
		t.Fatalf("bad initial state: %+v", record)
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
	if record.BudgetMode != "fast" || record.Status != "pending" {
		t.Fatal("confirmation did not preserve the launch depth/state")
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

func TestPreparationAutomaticallyContinuesOnlyOneGroundedDirection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		count    int
		question bool
		status   string
	}{
		{"single", 1, false, "pending"}, {"multiple", 2, false, "awaiting_confirmation"}, {"needs information", 1, true, "awaiting_info"}, {"no directions", 0, false, "preparation_failed"}, {"too many directions", 6, false, "preparation_failed"},
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
			prepared := expansionPreparation{Summary: "Grounded research", UserInstruction: "agents"}
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
			record := orm.AcademicExpansion{ID: "prepared", UserID: "u", DatasetID: "papers", RequestOwner: "u", RequestKey: "prepared", Status: "preparing", BudgetMode: "deep", PreparationRevision: 1, PreparationJSON: marshal(expansionPreparation{UserInstruction: "agents", StartedAt: &old}), ResultJSON: marshal(ReviewWorkflowInput{UserInstruction: "agents", Papers: []WorkflowPaper{}}), DiscoveryJSON: marshal([]paperDiscovery{{Papers: []PaperRef{{PaperID: "a", Title: "Actual paper"}}}})}
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
			if tc.status == "pending" {
				if chats != 1 || jobs != 1 || !saved.AutoConfirmed || len(saved.Selected) != 1 {
					t.Fatalf("single direction did not start exactly once: chats %d, jobs %d, preparation %+v", chats, jobs, saved)
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
		if payload.LLM["llm"]["model"] != "gpt-5" {
			t.Error("preparation did not use the selected default model")
		}
		if payload.ExpansionID == "stale" {
			db.Model(&orm.AcademicExpansion{}).Where("id=?", "stale").Updates(map[string]any{"preparation_revision": 2, "preparation_json": marshal(expansionPreparation{Summary: "new attempt", UserInstruction: "new scope"})})
		} else {
			db.Model(&orm.AcademicExpansion{}).Where("id=?", payload.ExpansionID).Update("discovery_json", marshal([]paperDiscovery{{Papers: []PaperRef{paperRef(work)}}}))
		}
		json.NewEncoder(w).Encode(expansionPreparation{Summary: "Grounded scope", Directions: []preparationDirection{{Name: "retrieval correction", Question: "When does correction help?", Difference: "retrieval-side changes", Reason: "registered representative", PaperIDs: []string{work.ID}}}})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "preparation-fixture")
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	for _, id := range []string{"fresh", "stale"} {
		record := orm.AcademicExpansion{ID: id, UserID: "u", DatasetID: "papers", RequestOwner: "u", RequestKey: id, Status: "preparing", PreparationRevision: 1, BudgetMode: "fast", DiscoveryMode: "external", DiscoveryJSON: marshal([]paperDiscovery{}), ResultJSON: marshal(ReviewWorkflowInput{Papers: []WorkflowPaper{}}), PreparationJSON: marshal(expansionPreparation{UserInstruction: "RAG reliability"})}
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
		if id == "fresh" && (record.Status != "pending" || !preparation.AutoConfirmed || len(preparation.Directions) != 1 || len(preparation.Directions[0].Papers) != 1) {
			t.Fatal("newly registered candidates were not validated and materialized", record.Status, preparation.Error)
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
