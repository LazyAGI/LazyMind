package settings

import (
	"context"
	"testing"

	"lazymind/core/common/orm"
)

func TestLoadFeatureControlsDefaultsWhenPreferencesTableIsMissing(t *testing.T) {
	db := orm.MigrateTestDB(t)

	controls, err := LoadFeatureControls(context.Background(), db.DB, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if controls != DefaultFeatureControls() {
		t.Fatalf("controls=%#v, want defaults", controls)
	}
}
