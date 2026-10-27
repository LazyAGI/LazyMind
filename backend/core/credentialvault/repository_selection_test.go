package credentialvault

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestFinishingOlderBackupPreservesNewerSelectionRevision(t *testing.T) {
	for _, finish := range []string{"complete", "retry", "discard", "conflict"} {
		t.Run(finish, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&VaultAccount{}, &CredentialBinding{}, &backupOutboxRow{}); err != nil {
				t.Fatal(err)
			}
			scope := AccountScope{CloudIssuer: "https://cloud.example", CloudAccountID: "cloud-user"}
			now := time.Now().UTC()
			if err := db.Create(&VaultAccount{ID: "account", CloudIssuer: scope.CloudIssuer, CloudAccountID: scope.CloudAccountID, BackupEnabled: true}).Error; err != nil {
				t.Fatal(err)
			}
			repo := NewRepository(db)
			if err := EnqueueCredentialBackup(db, "group", 1, BackupUpsert, now); err != nil {
				t.Fatal(err)
			}
			item, found, err := repo.ClaimDue(t.Context(), scope, now)
			if err != nil || !found {
				t.Fatal("old backup could not start")
			}
			if err := EnqueueCredentialBackup(db, "group", 2, BackupUpsert, now); err != nil {
				t.Fatal(err)
			}
			switch finish {
			case "complete":
				err = repo.Complete(t.Context(), scope, item, CredentialBinding{ID: "binding", CloudIssuer: scope.CloudIssuer, CloudAccountID: scope.CloudAccountID, LocalProviderGroupID: "group", LastLocalCredentialRevision: 1}, now)
			case "retry":
				item.State = BackupFailed
				item.AttemptCount = 1
				err = repo.Retry(t.Context(), item)
			case "discard":
				err = repo.Discard(t.Context(), item)
			case "conflict":
				err = repo.Conflict(t.Context(), item, 3080005, now)
			}
			if err != nil {
				t.Fatal(err)
			}
			var queued backupOutboxRow
			if err := db.Take(&queued).Error; err != nil || queued.LocalCredentialRevision != 2 || queued.BackupState != string(BackupPending) {
				t.Fatal("finishing an old upload lost or changed the newer selection backup")
			}
		})
	}
}
