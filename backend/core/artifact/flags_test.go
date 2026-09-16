package artifact

import (
	"context"
	"testing"

	"lazymind/core/common/orm"
)

func TestFlagsDefaultOff(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_SCHEMA_ENABLED", "")
	t.Setenv("LAZYMIND_ARTIFACT_V2_PROJECTION_ENABLED", "")
	t.Setenv("LAZYMIND_ARTIFACT_V2_CHAT_DUAL_WRITE", "")
	t.Setenv("LAZYMIND_ARTIFACT_V2_READ_PREFER_V2", "")
	t.Setenv("LAZYMIND_ARTIFACT_V2_WRITE_V2_ONLY", "")
	if SchemaEnabled() || ProjectionEnabled() || ChatDualWriteEnabled() || ReadPreferV2() || WriteV2Only() {
		t.Fatal("artifact v2 flags must default off")
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
	report, err := AuditLegacy(context.Background(), db.DB)
	if err != nil {
		t.Fatal(err)
	}
	if report.ConversationArtifactCount != 1 {
		t.Fatalf("%+v", report)
	}
}
