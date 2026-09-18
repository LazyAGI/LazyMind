package cloudbinding

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRepositoryKeepsCloudAccountsIsolated(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:cloud_binding_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Binding{}); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(db)
	for _, account := range []string{"account-a", "account-b"} {
		_, err := repository.Upsert(context.Background(), Binding{
			CloudIssuer: "https://cloud.example", CloudAccountID: account, ResourceType: "skill",
			CloudResourceID: "resource-a", ClientResourceKey: "skill:a", CloudContentHash: "hash-a",
			LocalResourceID: "local-" + account, InstalledLocalRevisionID: "revision-a",
			InstalledLocalContentHash: "hash-a", CloudResourceName: "A",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := repository.FindByCloudIDs(context.Background(), "https://cloud.example", "account-a", "skill", []string{"resource-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows["resource-a"].LocalResourceID != "local-account-a" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestRepositoryUpsertRefreshesHashesWithoutDuplicatingBinding(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:cloud_binding_upsert_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Binding{}); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(db)
	binding := Binding{
		CloudIssuer: "https://cloud.example", CloudAccountID: "account", ResourceType: "workflow",
		CloudResourceID: "resource", ClientResourceKey: "workflow:a", CloudContentHash: "old",
		LocalResourceID: "local", LocalResourceRef: "user:account:a", InstalledLocalRevisionID: "revision-1",
		InstalledLocalContentHash: "old", CloudResourceName: "A",
	}
	first, err := repository.Upsert(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	binding.CloudContentHash = "new"
	binding.InstalledLocalContentHash = "new"
	binding.InstalledLocalRevisionID = "revision-2"
	second, err := repository.Upsert(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.CloudContentHash != "new" || second.InstalledLocalRevisionID != "revision-2" {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}
