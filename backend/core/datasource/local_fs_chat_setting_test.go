package datasource

import (
	"context"
	"testing"

	"lazymind/core/common/orm"
)

func TestLocalFSChatSettingDefaultsAndUpdates(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/localfs-setting.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.LocalFSChatSetting{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	enabled, err := LoadLocalFSChatEnabled(context.Background(), db.DB, "u1")
	if err != nil {
		t.Fatalf("load default setting: %v", err)
	}
	if enabled {
		t.Fatalf("default setting should be false")
	}

	enabled, err = UpsertLocalFSChatEnabled(context.Background(), db.DB, "u1", "User 1", true)
	if err != nil {
		t.Fatalf("upsert true: %v", err)
	}
	if !enabled {
		t.Fatalf("setting should be true after update")
	}

	enabled, err = UpsertLocalFSChatEnabled(context.Background(), db.DB, "u1", "User 1", false)
	if err != nil {
		t.Fatalf("upsert false: %v", err)
	}
	if enabled {
		t.Fatalf("setting should be false after update")
	}
}
