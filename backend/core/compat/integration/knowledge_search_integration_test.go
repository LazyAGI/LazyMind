package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/compat/contract"
	adaptercore "lazymind/core/compat/internal/adapters/core"
	compatknowledge "lazymind/core/compat/knowledge"
	compatruntime "lazymind/core/compat/runtime"
	"lazymind/core/doc"
)

func TestKnowledgeRuntimeWithRealPureSearch(t *testing.T) {
	if strings.TrimSpace(os.Getenv("COMPAT_INTEGRATION")) != "1" ||
		strings.TrimSpace(os.Getenv("COMPAT_KNOWLEDGE_SEARCH_INTEGRATION")) != "1" {
		t.Skip("set COMPAT_INTEGRATION=1 and COMPAT_KNOWLEDGE_SEARCH_INTEGRATION=1 to run knowledge search integration tests")
	}
	userID := strings.TrimSpace(os.Getenv("COMPAT_TEST_USER_ID"))
	if userID == "" {
		t.Fatal("COMPAT_TEST_USER_ID is required")
	}
	query := strings.TrimSpace(os.Getenv("COMPAT_KNOWLEDGE_SEARCH_QUERY"))
	if query == "" {
		t.Fatal("COMPAT_KNOWLEDGE_SEARCH_QUERY is required for pure search integration")
	}
	knowledgeID := strings.TrimSpace(os.Getenv("COMPAT_KNOWLEDGE_SEARCH_DATASET_ID"))
	if knowledgeID == "" {
		t.Fatal("COMPAT_KNOWLEDGE_SEARCH_DATASET_ID is required so the test can assert dataset.id != kb_id")
	}

	driver, dsn := dbConfigFromCoreEnv(t)
	if driver != orm.DriverPostgres {
		t.Fatalf("Knowledge search integration requires ACL_DB_DRIVER=%q, got %q", orm.DriverPostgres, driver)
	}
	db, err := orm.Connect(driver, dsn)
	if err != nil {
		t.Fatalf("connect core db: %v", err)
	}
	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	acl.InitStore(db)

	var ds orm.Dataset
	if err := db.WithContext(context.Background()).Where("id = ? AND deleted_at IS NULL", knowledgeID).First(&ds).Error; err != nil {
		t.Fatalf("load dataset %q: %v", knowledgeID, err)
	}
	if strings.TrimSpace(ds.KbID) == "" {
		t.Fatalf("dataset %q has empty kb_id", knowledgeID)
	}
	if ds.KbID == ds.ID {
		t.Fatalf("integration requires dataset.id != kb_id, both are %q", ds.ID)
	}

	searchAdapter, err := adaptercore.NewKnowledgeSearchAdapterForDB(db.DB, common.ChatServiceEndpoint())
	if err != nil {
		t.Fatalf("NewKnowledgeSearchAdapterForDB: %v", err)
	}
	docService, err := doc.NewDocumentService(doc.DocumentServiceDeps{DB: db.DB})
	if err != nil {
		t.Fatalf("NewDocumentService: %v", err)
	}
	documentAdapter, err := adaptercore.NewKnowledgeDocumentAdapter(docService)
	if err != nil {
		t.Fatalf("NewKnowledgeDocumentAdapter: %v", err)
	}
	rt, err := compatruntime.New(compatruntime.Dependencies{
		KnowledgeSearch:   searchAdapter,
		KnowledgeDocument: documentAdapter,
	})
	if err != nil {
		t.Fatalf("Runtime.New search: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := rt.Knowledge.Search(ctx, contract.CallContext{UserID: userID}, compatknowledge.SearchInput{
		Query:        query,
		KnowledgeIDs: []string{knowledgeID},
		TopK:         10,
	})
	if err != nil {
		t.Fatalf("Knowledge.Search: %v", err)
	}
	if result.Hits == nil {
		t.Fatalf("Knowledge.Search returned nil Hits")
	}
	if len(result.Hits) == 0 {
		t.Fatalf("Knowledge.Search returned no hits")
	}
	first := result.Hits[0]
	if first.KnowledgeID != knowledgeID {
		t.Fatalf("hit knowledge id = %q, want %q", first.KnowledgeID, knowledgeID)
	}
	if strings.TrimSpace(first.DocumentID) == "" {
		t.Fatalf("hit did not map to Core document id: %#v", first)
	}
	if _, err := rt.Knowledge.GetDocument(ctx, contract.CallContext{UserID: userID}, compatknowledge.GetDocumentInput{
		KnowledgeID: first.KnowledgeID,
		DocumentID:  first.DocumentID,
	}); err != nil {
		t.Fatalf("GetDocument with search hit document id: %v", err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	lower := strings.ToLower(string(raw))
	for _, forbidden := range []string{"answer", "conversation", "message_id", "local_path", "stored_path", "parse_stored_path", "metadata", "global_metadata", "lazyllm_doc_id", "docid"} {
		if strings.Contains(lower, `"`+forbidden+`"`) {
			t.Fatalf("search result leaked %q: %s", forbidden, raw)
		}
	}
	t.Logf("Knowledge pure search integration driver=%s dataset_id=%s kb_id=%s hit_count=%d", driver, ds.ID, ds.KbID, len(result.Hits))
}
