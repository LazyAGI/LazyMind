package domain

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openCapabilityDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDetectSchemaCapabilitiesRequiresCompleteExpand(t *testing.T) {
	db := openCapabilityDB(t)
	if err := db.Exec(`CREATE TABLE plugin_sessions (id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	if DetectSchemaCapabilities(db).HostNeutralSessionRefs {
		t.Fatal("legacy schema must not advertise host-neutral refs")
	}
	for _, statement := range []string{
		`ALTER TABLE plugin_sessions ADD COLUMN origin_host TEXT NOT NULL DEFAULT 'lazymind'`,
		`ALTER TABLE plugin_sessions ADD COLUMN origin_ref TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE plugin_sessions ADD COLUMN controller_host TEXT NOT NULL DEFAULT 'lazymind'`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if !DetectSchemaCapabilities(db).HostNeutralSessionRefs {
		t.Fatal("expanded schema capability was not detected")
	}
}

func TestDetectSchemaCapabilitiesRejectsPartialExpand(t *testing.T) {
	db := openCapabilityDB(t)
	if err := db.Exec(`CREATE TABLE plugin_sessions (
		id TEXT PRIMARY KEY,
		origin_host TEXT NOT NULL DEFAULT 'lazymind',
		origin_ref TEXT NOT NULL DEFAULT ''
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if DetectSchemaCapabilities(db).HostNeutralSessionRefs {
		t.Fatal("partial expand must not enable new writes")
	}
}
