package artifact

import (
	"context"
	"testing"

	"lazymind/core/common/orm"
)

func TestFlagsDefaultOff(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "")
	if Enabled() {
		t.Fatal("artifact v2 flags must default off")
	}
}

func TestArtifactV2UsesOneSwitch(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	if !Enabled() {
		t.Fatal("artifact v2 must enable from its one switch")
	}
}

func TestAuditLegacyOmitsPaths(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ConversationArtifact{})
	row := orm.ConversationArtifact{
		ID: "a1", ConversationID: "c1", HistoryID: "h1", Filename: "a.txt",
		Slot: "a.txt", ContentType: "text", Value: []byte(`{"text":"x"}`), CreateUserID: "u1",
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	report, err := AuditLegacy(context.Background(), db.DB, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if report.ConversationArtifactCount != 1 {
		t.Fatalf("%+v", report)
	}
	other := orm.ConversationArtifact{
		ID: "a2", ConversationID: "c2", HistoryID: "h2", Filename: "b.txt",
		Slot: "b.txt", ContentType: "text", Value: []byte(`{"text":"y"}`), CreateUserID: "u2",
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	scoped, err := AuditLegacy(context.Background(), db.DB, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if scoped.ConversationArtifactCount != 1 {
		t.Fatalf("owner-scoped audit leaked other users: %+v", scoped)
	}
}
