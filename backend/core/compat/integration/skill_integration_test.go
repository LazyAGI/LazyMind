package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/compat/contract"
	adaptercore "lazymind/core/compat/internal/adapters/core"
	compatruntime "lazymind/core/compat/runtime"
	compatskill "lazymind/core/compat/skill"
	skillservice "lazymind/core/skillv2/service"
)

func TestSkillRuntimeWithRealSkillService(t *testing.T) {
	if strings.TrimSpace(os.Getenv("COMPAT_INTEGRATION")) != "1" {
		t.Skip("set COMPAT_INTEGRATION=1 to run compat integration tests")
	}
	userID := strings.TrimSpace(os.Getenv("COMPAT_TEST_USER_ID"))
	if userID == "" {
		t.Fatal("COMPAT_TEST_USER_ID is required")
	}

	driver, dsn := dbConfigFromCoreEnv(t)
	db, err := orm.Connect(driver, dsn)
	if err != nil {
		t.Fatalf("connect core db: %v", err)
	}
	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	svc := skillservice.NewSkillService(skillservice.SkillServiceDeps{
		DB:        db.DB,
		BlobStore: skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(skillObjectRootFromCoreEnv())),
	})
	adapter, err := adaptercore.NewSkillAdapterForDB(svc, db.DB)
	if err != nil {
		t.Fatalf("NewSkillAdapterForDB: %v", err)
	}
	rt, err := compatruntime.New(compatruntime.Dependencies{SkillPort: adapter})
	if err != nil {
		t.Fatalf("Runtime.New: %v", err)
	}
	if rt.Skill == nil {
		t.Fatal("Runtime.Skill is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	callCtx := contract.CallContext{UserID: userID}
	list, err := rt.Skill.List(ctx, callCtx, compatskill.ListInput{
		Page: contract.PageRequest{
			PageSize: contract.DefaultPageSize,
		},
	})
	if err != nil {
		t.Fatalf("Skill.List: %v", err)
	}

	if len(list.Items) == 0 {
		t.Fatalf("Skill.List returned no skills for user %q", userID)
	}

	if list.Page.Total == nil {
		t.Fatal("list total is nil")
	}

	if *list.Page.Total < int64(len(list.Items)) {
		t.Fatalf(
			"total = %d, returned = %d",
			*list.Page.Total,
			len(list.Items),
		)
	}

	// 打印 Compat List 的实际返回结果。
	t.Logf(
		"Compat Skill.List: total=%d, returned=%d",
		*list.Page.Total,
		len(list.Items),
	)

	for i, item := range list.Items {
		t.Logf(
			"Compat List[%d]: id=%s name=%q category=%q enabled=%v head_revision_id=%s",
			i,
			item.ID,
			item.Name,
			item.Category,
			item.Enabled,
			item.HeadRevisionID,
		)
	}

	first := list.Items[0]
	if strings.TrimSpace(first.ID) == "" {
		t.Fatal("first skill ID is empty")
	}

	got, err := rt.Skill.Get(
		ctx,
		callCtx,
		compatskill.GetInput{
			SkillID: first.ID,
		},
	)
	if err != nil {
		t.Fatalf("Skill.Get(%q): %v", first.ID, err)
	}

	if got.Skill.ID != first.ID {
		t.Fatalf(
			"Get ID = %q, want %q",
			got.Skill.ID,
			first.ID,
		)
	}

	// 打印 Compat Get 的实际返回结果。
	t.Logf(
		"Compat Skill.Get: id=%s name=%q category=%q enabled=%v head_revision_id=%s description=%q",
		got.Skill.ID,
		got.Skill.Name,
		got.Skill.Category,
		got.Skill.Enabled,
		got.Skill.HeadRevisionID,
		got.Skill.Description,
	)
}

func dbConfigFromCoreEnv(t *testing.T) (string, string) {
	t.Helper()
	driver := strings.TrimSpace(os.Getenv("ACL_DB_DRIVER"))
	dsn := strings.TrimSpace(os.Getenv("ACL_DB_DSN"))
	if driver == "" {
		t.Fatal("ACL_DB_DRIVER is required")
	}
	if dsn == "" {
		t.Fatal("ACL_DB_DSN is required")
	}
	return driver, dsn
}

func skillObjectRootFromCoreEnv() string {
	if v := strings.TrimSpace(os.Getenv("LAZYMIND_SKILL_OBJECT_ROOT")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return filepath.Join(uploadRootFromCoreEnv(), "skill-objects")
}

func uploadRootFromCoreEnv() string {
	if v := strings.TrimSpace(os.Getenv("LAZYMIND_UPLOAD_ROOT")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "/var/lib/lazymind/uploads"
}
