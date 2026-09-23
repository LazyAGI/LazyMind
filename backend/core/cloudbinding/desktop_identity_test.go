package cloudbinding

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestDesktopBindingIdentitySurvivesHashesAndSeparatesCloudScopes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "bindings.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := db.AutoMigrate(&Binding{}); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(db)
	for _, issuer := range []string{"https://cloud-a.example", "https://cloud-b.example"} {
		for _, account := range []string{"account-a", "account-b"} {
			for _, kind := range []string{"skill", "workflow"} {
				local := issuer + ":" + account + ":" + kind
				binding := Binding{CloudIssuer: issuer, CloudAccountID: account, ResourceType: kind, CloudResourceID: "same-remote-id", ClientResourceKey: "fixture-key", CloudContentHash: "old-cloud-hash", LocalResourceID: local, InstalledLocalRevisionID: "revision-1", InstalledLocalContentHash: "old-local-hash", CloudResourceName: "same-name"}
				initial, err := repository.Upsert(context.Background(), binding)
				if err != nil {
					t.Fatal(err)
				}
				binding.CloudContentHash = "uploaded-new-cloud-hash"
				binding.InstalledLocalContentHash = "new-local-hash"
				binding.InstalledLocalRevisionID = "revision-2"
				updated, err := repository.Upsert(context.Background(), binding)
				if err != nil {
					t.Fatal(err)
				}
				if initial.ID != updated.ID {
					t.Fatal("content changes created a second resource identity")
				}
			}
		}
	}
	for _, issuer := range []string{"https://cloud-a.example", "https://cloud-b.example"} {
		for _, account := range []string{"account-a", "account-b"} {
			for _, kind := range []string{"skill", "workflow"} {
				rows, err := repository.FindByCloudIDs(context.Background(), issuer, account, kind, []string{"same-remote-id"})
				if err != nil || len(rows) != 1 || rows["same-remote-id"].LocalResourceID != issuer+":"+account+":"+kind {
					t.Fatalf("binding crossed issuer/account/type: %+v err=%v", rows, err)
				}
			}
		}
	}
}
