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
)

func TestKnowledgeRuntimeWithRealChatSearch(t *testing.T) {
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
		query = "请根据指定知识库回答。"
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

	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("begin tx: %v", tx.Error)
	}
	t.Cleanup(func() { _ = tx.Rollback().Error })

	catalogAdapter, err := adaptercore.NewKnowledgeCatalogAdapterForDB(tx)
	if err != nil {
		t.Fatalf("NewKnowledgeCatalogAdapterForDB: %v", err)
	}
	rtForCatalog, err := compatruntime.New(compatruntime.Dependencies{KnowledgeCatalog: catalogAdapter})
	if err != nil {
		t.Fatalf("Runtime.New catalog: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	callCtx := contract.CallContext{UserID: userID}
	list, err := rtForCatalog.Knowledge.List(ctx, callCtx, compatknowledge.ListInput{
		Page: contract.PageRequest{PageSize: contract.DefaultPageSize},
	})
	if err != nil {
		t.Fatalf("Knowledge.List: %v", err)
	}
	if len(list.Items) == 0 {
		t.Fatalf("Knowledge.List returned no datasets for user %q", userID)
	}
	knowledgeID := list.Items[0].ID

	searchAdapter, err := adaptercore.NewKnowledgeSearchAdapterForDB(tx, common.ChatServiceEndpoint())
	if err != nil {
		t.Fatalf("NewKnowledgeSearchAdapterForDB: %v", err)
	}
	rt, err := compatruntime.New(compatruntime.Dependencies{KnowledgeSearch: searchAdapter})
	if err != nil {
		t.Fatalf("Runtime.New search: %v", err)
	}
	if rt.Knowledge == nil {
		t.Fatal("Runtime.Knowledge is nil")
	}

	result, err := rt.Knowledge.Search(ctx, callCtx, compatknowledge.SearchInput{
		Query:        query,
		KnowledgeIDs: []string{knowledgeID},
	})
	if err != nil {
		t.Fatalf("Knowledge.Search: %v", err)
	}
	if strings.TrimSpace(result.Answer) == "" {
		t.Fatalf("Knowledge.Search returned empty answer")
	}
	if strings.TrimSpace(result.ConversationID) == "" || strings.TrimSpace(result.MessageID) == "" {
		t.Fatalf("missing conversation/message id: %#v", result)
	}
	for _, source := range result.Sources {
		if source.KnowledgeID != "" && source.KnowledgeID != knowledgeID {
			t.Fatalf("source escaped requested knowledge id: %#v", source)
		}
		raw, err := json.Marshal(source)
		if err != nil {
			t.Fatalf("marshal source: %v", err)
		}
		lower := strings.ToLower(string(raw))
		for _, forbidden := range []string{"local_path", "stored_path", "parse_stored_path", "metadata", "global_metadata", "lazyllm_doc_id", "docid"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("source leaked %q: %s", forbidden, raw)
			}
		}
	}
	t.Logf("Knowledge Search integration driver=%s knowledge_id=%s answer_len=%d source_count=%d", driver, knowledgeID, len(result.Answer), len(result.Sources))
}
