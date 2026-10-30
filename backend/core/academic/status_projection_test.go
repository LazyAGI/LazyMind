package academic

import (
	"testing"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func TestExpansionProjectionUsesCurrentWorkflowState(t *testing.T) {
	for _, sample := range []struct {
		name, session, step, mode, want string
		question, stopped               bool
	}{
		{name: "resumed execution", session: "active", step: "running", stopped: true, want: "running"},
		{name: "current question", session: "active", question: true, want: "awaiting_input"},
		{name: "pause retains old question", session: "waiting", question: true, stopped: true, want: "stopped"},
		{name: "completion supersedes stop and question", session: "completed", step: "succeeded", question: true, stopped: true, want: "completed"},
		{name: "failure supersedes stop and question", session: "failed", step: "failed", question: true, stopped: true, want: "failed"},
		{name: "approval after resume", session: "waiting", step: "succeeded", mode: "human", stopped: true, want: "awaiting_input"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			db := paperFixture(t)
			if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}); err != nil {
				t.Fatal(err)
			}
			conversation, err := newAcademicConversation(db.DB, "u", "papers", "research", "expansion")
			if err != nil {
				t.Fatal(err)
			}
			record := orm.AcademicExpansion{ID: "record", UserID: "u", RequestOwner: "u", RequestKey: "launch", DatasetID: "papers",
				ConversationID: conversation, Goal: "expansion", Status: "running", Version: 1,
				ResultJSON: marshal(ReviewWorkflowInput{UserInstruction: "research", Papers: []WorkflowPaper{}}),
				ReportJSON: marshal(map[string]string{"narrative": "saved report"})}
			if err = db.Create(&record).Error; err != nil {
				t.Fatal(err)
			}
			result, report := string(record.ResultJSON), string(record.ReportJSON)
			stoppedAt := time.Now().UTC().Add(-time.Minute)
			session := orm.WorkflowSession{ID: "session", ConversationID: conversation, WorkflowID: "paper_expansion", CreateUserID: "u",
				Status: sample.session, ControllerHost: "lazymind", WorkflowRevisionID: "revision"}
			if sample.stopped {
				session.LastStoppedAt = &stoppedAt
			}
			graph := graphengine.CompiledStateGraph{Nodes: map[string]graphengine.CompiledNode{"analyze_relations": {ID: "analyze_relations", Mode: sample.mode}}}
			if err = db.Create(&orm.WorkflowRevision{ID: "revision", WorkflowResourceID: "resource", RevisionNo: 1, CompiledGraph: graph.JSON()}).Error; err != nil {
				t.Fatal(err)
			}
			if err = db.Create(&session).Error; err != nil {
				t.Fatal(err)
			}
			if sample.step != "" {
				if err = db.Create(&orm.WorkflowSessionStep{ID: "attempt", SessionID: session.ID, StepID: "analyze_relations", TaskID: "task",
					Status: sample.step, Attempt: 1, Validity: "effective", CreatedAt: stoppedAt.Add(time.Second)}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if sample.question {
				if err = db.Create(&orm.ChatHistory{ID: "question", ConversationID: conversation, Seq: 1,
					Ext: marshal(map[string]any{"ask_pending": []string{"scope?"}})}).Error; err != nil {
					t.Fatal(err)
				}
			}
			job := asyncjob.Job{ResourceID: record.ID, CreateUserID: "u", PayloadJSON: marshal(expansionProjectionPayload{SessionID: session.ID})}
			if _, err = projectAcademicResult(t.Context(), job, nil); err != nil {
				t.Fatal(err)
			}
			if err = db.Where("id=?", record.ID).Take(&record).Error; err != nil {
				t.Fatal(err)
			}
			if record.Status != sample.want {
				t.Fatalf("status=%q want %q", record.Status, sample.want)
			}
			if record.Version != 1 || string(record.ResultJSON) != result || string(record.ReportJSON) != report {
				t.Fatal("status projection changed saved results")
			}
		})
	}
}
