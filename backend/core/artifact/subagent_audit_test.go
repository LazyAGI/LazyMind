package artifact

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestAuditSubAgentShadowCountsMappedMissingAndWorkflowExcluded(t *testing.T) {
	db := orm.MigrateTestDB(t,
		&orm.SubAgentTask{}, &orm.SubAgentArtifact{},
		&orm.ArtifactV2{}, &orm.ArtifactBlob{}, &orm.ArtifactRevision{}, &orm.ArtifactHead{},
		&orm.ArtifactBinding{}, &orm.ArtifactDependency{}, &orm.ArtifactIdempotency{}, &orm.ArtifactEventOutbox{},
	)
	now := time.Now().UTC()
	for _, task := range []orm.SubAgentTask{
		{ID: "ordinary", AgentType: "research", CreateUserID: "u1", Status: "running", Params: json.RawMessage(`{}`), InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`), LastHeartbeat: now, CreatedAt: now, UpdatedAt: now},
		{ID: "workflow", AgentType: "workflow_step", CreateUserID: "u1", Status: "running", Params: json.RawMessage(`{}`), InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`), LastHeartbeat: now, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []orm.SubAgentArtifact{
		{ID: "mapped", TaskID: "ordinary", Slot: "a", ContentType: "text", Value: json.RawMessage(`{"text":"a"}`), CreatedAt: now},
		{ID: "missing", TaskID: "ordinary", Slot: "b", ContentType: "text", Value: json.RawMessage(`{"text":"b"}`), CreatedAt: now},
		{ID: "workflow-row", TaskID: "workflow", Slot: "c", ContentType: "text", Value: json.RawMessage(`{"text":"c"}`), CreatedAt: now},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.ArtifactBinding{ID: "binding", ArtifactID: "v2", RevisionID: "rev", ScopeType: ScopeSubAgentLegacyRow, ScopeID: "mapped", Role: RoleOutput, Validity: ValidityEffective, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	report, err := AuditSubAgentShadow(context.Background(), db.DB, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if report.LegacyVisibleRows != 2 || report.MappedRows != 1 || report.MissingMappings != 1 || report.WorkflowExcluded != 1 {
		t.Fatalf("report=%+v", report)
	}
}

func TestReplaySubAgentArtifactIsIdempotent(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	db := orm.MigrateTestDB(t,
		&orm.SubAgentTask{}, &orm.SubAgentArtifact{},
		&orm.ArtifactV2{}, &orm.ArtifactBlob{}, &orm.ArtifactRevision{}, &orm.ArtifactHead{},
		&orm.ArtifactBinding{}, &orm.ArtifactDependency{}, &orm.ArtifactIdempotency{}, &orm.ArtifactEventOutbox{},
	)
	now := time.Now().UTC()
	task := orm.SubAgentTask{ID: "task-1", AgentType: "research", CreateUserID: "u1", Status: "running", Params: json.RawMessage(`{}`), InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`), LastHeartbeat: now, CreatedAt: now, UpdatedAt: now}
	row := orm.SubAgentArtifact{ID: "saa-1", TaskID: task.ID, Slot: "result", ContentType: "text", Value: json.RawMessage(`{"text":"ok"}`), CreatedAt: now}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	first, err := ReplaySubAgentArtifact(context.Background(), db.DB, "u1", row.ID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ReplaySubAgentArtifact(context.Background(), db.DB, "u1", row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.RevisionID != again.RevisionID {
		t.Fatalf("first=%s again=%s", first.RevisionID, again.RevisionID)
	}
}
