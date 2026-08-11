package integration_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"lazymind/core/compat/contract"
	adaptercore "lazymind/core/compat/internal/adapters/core"
	compatknowledge "lazymind/core/compat/knowledge"
	compatruntime "lazymind/core/compat/runtime"
)

func TestKnowledgeRuntimeWithRealPostgreSQLCatalog(t *testing.T) {
	if strings.TrimSpace(os.Getenv("COMPAT_INTEGRATION")) != "1" {
		t.Skip("set COMPAT_INTEGRATION=1 to run compat integration tests")
	}
	userID := strings.TrimSpace(os.Getenv("COMPAT_TEST_USER_ID"))
	if userID == "" {
		t.Fatal("COMPAT_TEST_USER_ID is required")
	}

	driver, dsn := dbConfigFromCoreEnv(t)
	if driver != orm.DriverPostgres {
		t.Fatalf("Knowledge PostgreSQL integration requires ACL_DB_DRIVER=%q, got %q", orm.DriverPostgres, driver)
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

	adapter, err := adaptercore.NewKnowledgeCatalogAdapterForDB(db.DB)
	if err != nil {
		t.Fatalf("NewKnowledgeCatalogAdapterForDB: %v", err)
	}
	rt, err := compatruntime.New(compatruntime.Dependencies{KnowledgeCatalog: adapter})
	if err != nil {
		t.Fatalf("Runtime.New: %v", err)
	}
	if rt.Knowledge == nil {
		t.Fatal("Runtime.Knowledge is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	callCtx := contract.CallContext{UserID: userID}
	list, err := rt.Knowledge.List(ctx, callCtx, compatknowledge.ListInput{
		Page: contract.PageRequest{PageSize: contract.DefaultPageSize},
	})
	if err != nil {
		t.Fatalf("Knowledge.List: %v", err)
	}
	if len(list.Items) == 0 {
		t.Fatalf("Knowledge.List returned no datasets for user %q", userID)
	}
	t.Logf("Knowledge integration DB driver=%s list_count=%d user_id=%s", driver, len(list.Items), userID)

	first := list.Items[0]
	if strings.TrimSpace(first.ID) == "" {
		t.Fatal("first knowledge ID is empty")
	}

	got, err := rt.Knowledge.Get(ctx, callCtx, compatknowledge.GetInput{KnowledgeID: first.ID})
	if err != nil {
		t.Fatalf("Knowledge.Get(%q): %v", first.ID, err)
	}
	if got.Knowledge.ID != first.ID {
		t.Fatalf("Get ID = %q, want %q", got.Knowledge.ID, first.ID)
	}
	t.Logf(
		"Compat Knowledge.List/Get: first_id=%s name=%q documents=%d size=%d",
		got.Knowledge.ID,
		got.Knowledge.Name,
		got.Knowledge.DocumentCount,
		got.Knowledge.DocumentSizeBytes,
	)
}
