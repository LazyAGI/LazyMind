package academic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/capability"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

func TestAgentPaperImportQueuesOnlySelectedPapers(t *testing.T) {
	db := sourceTestDB(t)
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.ACLModel{}); err != nil {
		t.Fatal(err)
	}
	previous := store.DB()
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(previous, previous, nil) })
	transport := http.DefaultTransport
	http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"items":[{"dataset_id":"papers","exists":false,"allowed":true}]}`))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	for _, row := range []any{
		&orm.Dataset{ID: "papers", Scene: "academic_papers", BaseModel: orm.BaseModel{CreateUserID: "alice"}},
		&orm.Conversation{ID: "chat", BaseModel: orm.BaseModel{CreateUserID: "alice"}},
		&orm.ChatHistory{ID: "earlier", ConversationID: "chat", Seq: 1, RetrievalResult: json.RawMessage(`{"sources":[{"index":"1.1","title":"Toolformer","url":"https://doi.org/10.48550/arxiv.2302.04761","source_roles":["cited"]},{"index":"2.1","title":"Missing identifier"},{"index":"3.1","title":"Unselected","url":"https://arxiv.org/abs/2210.03629"}]}`)},
		&orm.ChatHistory{ID: "empty", ConversationID: "chat", Seq: 2, RetrievalResult: json.RawMessage(`{"sources":null,"recommended_papers":[{"title":"Old paper list","arxiv_id":"2302.04761"}]}`)},
		&orm.ChatHistory{ID: "active", ConversationID: "chat", Seq: 3, RetrievalResult: json.RawMessage(`{"sources":[]}`)},
		&orm.ChatHistory{ID: "other", ConversationID: "other-chat", Seq: 1, RetrievalResult: json.RawMessage(`{"sources":[{"index":"1.1","url":"https://arxiv.org/abs/2210.03629"}]}`)},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	call := capability.InvocationContext{ConversationID: "chat", HistoryID: "active",
		Principal: capability.Principal{UserID: "alice", Permissions: capability.NewPermissionSet("document.read", "document.write")}}
	op := doc.ResourceOperationContext{Caller: doc.DatasetCatalogCaller{UserID: "alice", Permissions: []string{"document.write", "document.read"}}, OperationKey: "same-operation",
		ValidateBeforeCommit: func(context.Context) error { return nil }, ValidateExecution: func(context.Context, *gorm.DB) error { return nil }}
	sources, err := AgentPaperSources(t.Context(), call, capability.PaperSourcesInput{})
	if err != nil || sources.AnswerID != "earlier" || len(sources.Items) != 3 || !sources.Items[0].Cited {
		t.Fatalf("earlier answer = %+v, %v", sources, err)
	}

	input := capability.PaperImportInput{KnowledgeBaseID: "papers", AnswerID: "other", SourceIDs: []string{"1.1"}}
	if _, err := AgentImportPapers(t.Context(), call, input, op); err == nil {
		t.Fatal("another conversation's source was accepted")
	}
	input.AnswerID, input.SourceIDs = "empty", nil
	var invalid *capability.Error
	if _, err := AgentImportPapers(t.Context(), call, input, op); !errors.As(err, &invalid) || invalid.Code != capability.InvalidArgument {
		t.Fatalf("an answer ID without an explicit selection must be rejected: %v", err)
	}
	input.AnswerID, input.SourceIDs = sources.AnswerID, []string{"1.1", "2.1"}
	receipt, err := AgentImportPapers(t.Context(), call, input, op)
	if err != nil || receipt.BatchID == "" || receipt.QueuedCount != 1 || len(receipt.Items) != 2 || receipt.Items[0].Status != "queued" || receipt.Items[1].Status != "unidentified" {
		t.Fatalf("import receipt = %+v, %v", receipt, err)
	}
	replay, err := AgentImportPapers(t.Context(), call, input, op)
	if err != nil || replay.BatchID != receipt.BatchID {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	var items []orm.PaperImportItem
	if err := db.Where("batch_id = ?", receipt.BatchID).Find(&items).Error; err != nil || len(items) != 1 {
		t.Fatalf("persisted items = %+v, %v", items, err)
	}
	var selected savedImportCandidate
	if err := json.Unmarshal(items[0].SelectedCandidateJSON, &selected); err != nil || selected.SourceProvider != "arxiv" || selected.URL != "https://arxiv.org/pdf/2302.04761" {
		t.Fatalf("persisted PDF candidate = %+v, %v", selected, err)
	}

	input.AnswerID, input.SourceIDs = "", []string{"4.1"}
	input.CurrentSources = []capability.PaperSourceSnapshot{{SourceID: "4.1", URL: "https://arxiv.org/abs/2210.03629v3"}}
	op.OperationKey = "current-operation"
	op.ValidateExecution = func(context.Context, *gorm.DB) error { return errors.New("execution canceled") }
	if _, err := AgentImportPapers(t.Context(), call, input, op); err == nil {
		t.Fatal("canceled run queued a batch")
	}
	op.ValidateExecution = func(context.Context, *gorm.DB) error { return nil }
	current, err := AgentImportPapers(t.Context(), call, input, op)
	if err != nil || current.BatchID == "" || current.QueuedCount != 1 {
		t.Fatalf("current sources = %+v, %v", current, err)
	}
	var batch orm.PaperImportBatch
	if err := db.Where("id = ?", current.BatchID).Take(&batch).Error; err != nil || sourcePolicy(batch).Context.HistoryID != "active" {
		t.Fatalf("current sources not attributed to their turn: %+v, %v", batch, err)
	}

	op.OperationKey = "explicit-paper"
	input = capability.PaperImportInput{KnowledgeBaseID: "papers", Papers: []capability.PaperSelection{{DOI: "10.48550/arxiv.2302.04761v2"}}}
	explicit, err := AgentImportPapers(t.Context(), call, input, op)
	if err != nil || explicit.QueuedCount != 1 || explicit.Items[0].PaperKey != "arxiv:2302.04761v2:exact" {
		t.Fatalf("explicit paper = %+v, %v", explicit, err)
	}
}
