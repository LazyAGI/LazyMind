package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lazymind/core/cloudbinding"
	"lazymind/core/cloudclient"
	"lazymind/core/cloudpackage"
	"lazymind/core/cloudresource"
	"lazymind/core/skillv2/testutil"
)

type desktopPresenceCloud struct {
	cloudresource.CloudAPI
	page cloudclient.ResourcePage
}

func (*desktopPresenceCloud) Origin() string { return "https://cloud.example" }
func (*desktopPresenceCloud) GetCurrentAccount(context.Context, string) (cloudclient.Account, error) {
	return cloudclient.Account{ID: "fixture-cloud-account"}, nil
}
func (c *desktopPresenceCloud) ListResources(context.Context, string, cloudclient.ResourceQuery) (cloudclient.ResourcePage, error) {
	return c.page, nil
}

type desktopPresenceSession struct{}

func (desktopPresenceSession) AccessToken(context.Context, time.Duration) (string, error) {
	return "fixture-access", nil
}

func TestDesktopSkillPresenceUsesOwnedLiveIdentityEvenWithoutHead(t *testing.T) {
	for _, scenario := range []string{"committed", "head_missing", "other_owner", "trashed"} {
		t.Run(scenario, func(t *testing.T) {
			db := testutil.NewTestDB(t)
			if err := db.AutoMigrate(&cloudbinding.Binding{}); err != nil {
				t.Fatal(err)
			}
			svc := NewSkillService(SkillServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
			files := map[string]cloudpackage.File{"SKILL.md": {Data: []byte("---\nname: presence-fixture\ndescription: fixture\n---\n# Local content\n")}}
			prepared, err := cloudpackage.Prepare(cloudpackage.PrepareInput{ResourceType: "skill", ResourceName: "presence-fixture", ClientResourceKey: "skill:fixture", DesktopVersion: "1.0.0", Files: files})
			if err != nil {
				t.Fatal(err)
			}
			cloudItem := cloudclient.PrivateResource{ResourceID: "cloud-fixture", ResourceType: "skill", ResourceName: "presence-fixture", ClientResourceKey: "skill:fixture", ContentHash: prepared.Manifest.ContentHash, ContentSize: prepared.Manifest.ContentSize, FormatSchema: cloudpackage.FormatSchemaV2}
			adapter := CloudAdapter{Service: svc, DesktopVersion: "1.0.0"}
			imported, err := adapter.ImportAndBind(context.Background(), cloudresource.ImportRequest{OwnerUserID: "local-owner", CloudIssuer: "https://cloud.example", CloudAccountID: "fixture-cloud-account", Resource: cloudItem, Files: files})
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "head_missing":
				err = db.Table("skills").Where("id = ?", imported.ResourceID).Update("head_revision_id", nil).Error
			case "other_owner":
				err = db.Table("skills").Where("id = ?", imported.ResourceID).Update("owner_user_id", "different-owner").Error
			case "trashed":
				err = db.Table("skills").Where("id = ?", imported.ResourceID).Update("deleted_at", time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			page, err := (cloudresource.Service{Session: desktopPresenceSession{}, Cloud: &desktopPresenceCloud{page: cloudclient.ResourcePage{Items: []cloudclient.PrivateResource{cloudItem}}}, Bindings: cloudbinding.NewRepository(db.DB)}).List(context.Background(), cloudresource.ListRequest{OwnerUserID: "local-owner", ResourceType: "skill", PageSize: 20, Adapter: adapter})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Items []struct {
					Exists *bool `json:"local_exists"`
				} `json:"items"`
			}
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			want := scenario == "committed" || scenario == "head_missing"
			if len(wire.Items) != 1 || wire.Items[0].Exists == nil || *wire.Items[0].Exists != want {
				t.Fatalf("owned SQLite identity does not determine existence: %s; want=%v", raw, want)
			}
			if strings.Contains(string(raw), "owner_user_id") {
				t.Fatal("list exposed internal ownership fields")
			}
		})
	}
}
