package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/artifact"
	"lazymind/core/common/orm"
)

func TestPublishedPanelFollowsRestoreWhileHistoryStaysPinned(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	db := orm.MigrateTestDB(t, v2PersistModels()...)
	ctx := context.Background()
	id := "cccccccc-cccc-4ccc-8ccc-cccccccccccd"
	var first, last *ConversationArtifactDTO
	for i := 1; i <= 12; i++ {
		dto, err := persistConversationArtifact(ctx, db.DB, "c1", fmt.Sprintf("h%d", i), "u1", &ArtifactCreatedEvent{
			ArtifactID: id, Filename: "report.txt", ContentType: "text", LogicalKey: "report", ReplaceExisting: i > 1,
			Value: json.RawMessage(fmt.Sprintf(`{"text":"version-%d"}`, i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			first = dto
		}
		last = dto
	}
	if last.HistoryID != "h12" {
		t.Fatalf("event lost delivery history: %#v", last)
	}
	svc := artifact.New(db.DB)
	if _, err := svc.RestorePublished(ctx, "u1", first.V2ArtifactID, first.RevisionID, last.HeadVersion); err != nil {
		t.Fatal(err)
	}
	legacy := []ConversationArtifactDTO{*last}
	queries := 0
	count := func(tx *gorm.DB) {
		if !tx.DryRun {
			queries++
		}
	}
	_ = db.Callback().Query().Before("gorm:query").Register("projection-count", count)
	_ = db.Callback().Row().Before("gorm:row").Register("projection-count", count)
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove("projection-count")
		_ = db.Callback().Row().Remove("projection-count")
	})
	panel, err := publishedConversationArtifacts(ctx, db.DB, "u1", "c1", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if queries != 2 {
		t.Fatalf("panel queries=%d, want fixed 2 without revision-chain loads", queries)
	}
	if len(panel) != 1 || panel[0].RevisionID != first.RevisionID || panel[0].RevisionCount != 12 || panel[0].ArtifactID != first.V2ArtifactID || !strings.Contains(string(panel[0].Value), `"version-1"`) {
		t.Fatalf("panel did not follow restored head: %#v", panel)
	}
	deliveries, err := conversationDeliveryArtifacts(ctx, db.DB, "u1", "c1", legacy, panel)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 12 {
		t.Fatalf("deliveries=%d, want all immutable turns", len(deliveries))
	}
	for _, dto := range deliveries {
		if dto.HistoryID == "h12" && (dto.RevisionID != last.RevisionID || !strings.Contains(string(dto.Value), "version-12")) {
			t.Fatalf("restore rewrote historical delivery: %#v", dto)
		}
	}
	foreign, err := publishedConversationArtifacts(ctx, db.DB, "other", "c1", nil)
	if err != nil || len(foreign) != 0 {
		t.Fatalf("cross-owner projection=%#v err=%v", foreign, err)
	}
	if err := artifact.PurgeConversationOwned(db.DB, "u1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	panel, err = publishedConversationArtifacts(ctx, db.DB, "u1", "c1", legacy)
	if err != nil || len(panel) != 0 {
		t.Fatalf("purge resurrected legacy: %#v err=%v", panel, err)
	}
}

func TestOldForkKeepsPinnedReceiptWithoutHistoryBinding(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	db := orm.MigrateTestDB(t, v2PersistModels()...)
	ctx := context.Background()
	svc := artifact.New(db.DB)
	first, err := svc.CommitRevision(ctx, artifact.CommitRequest{OwnerUserID: "u1", LogicalKey: "conv:child:file", Title: "old.txt", ContentType: "text", InlineJSON: []byte(`{"text":"old fork"}`), Bindings: []artifact.BindingSpec{
		{ScopeType: artifact.ScopeConversation, ScopeID: "child", Role: artifact.RoleOutput, FollowHead: true},
		{ScopeType: artifact.ScopeLegacyRow, ScopeID: "old-child-row", Role: artifact.RoleOutput},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CommitRevision(ctx, artifact.CommitRequest{OwnerUserID: "u1", ArtifactID: first.ArtifactID, Title: "new.txt", ContentType: "text", InlineJSON: []byte(`{"text":"new child"}`)}); err != nil {
		t.Fatal(err)
	}
	legacy := []ConversationArtifactDTO{{ArtifactID: "old-child-row", SourceType: "main_chat", HistoryID: "child-history"}}
	panel, err := publishedConversationArtifacts(ctx, db.DB, "u1", "child", legacy)
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := conversationDeliveryArtifacts(ctx, db.DB, "u1", "child", legacy, panel)
	if err != nil || len(deliveries) != 1 || deliveries[0].RevisionID != first.RevisionID || deliveries[0].HistoryID != "child-history" {
		t.Fatalf("old fork receipt missing: %#v %v", deliveries, err)
	}
}

func TestForkSnapshotsEveryDeliveryForReusedLegacyID(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	db := orm.MigrateTestDB(t, append(v2PersistModels(), &orm.ChatHistory{}, &orm.SubAgentTask{}, &orm.SubAgentArtifact{})...)
	ctx := context.Background()
	id := "cccccccc-cccc-4ccc-8ccc-ccccccccccce"
	cutoff := time.Now().UTC().Add(time.Hour)
	histories := []orm.ChatHistory{
		{ID: "h1", ConversationID: "c1", Seq: 1, TimeMixin: orm.TimeMixin{CreateTime: cutoff, UpdateTime: cutoff}},
		{ID: "h2", ConversationID: "c1", Seq: 2, TimeMixin: orm.TimeMixin{CreateTime: cutoff, UpdateTime: cutoff}},
	}
	for i, h := range histories {
		if err := db.Create(&h).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := persistConversationArtifact(ctx, db.DB, "c1", h.ID, "u1", &ArtifactCreatedEvent{ArtifactID: id, Filename: "a.txt", ContentType: "text", LogicalKey: "a", ReplaceExisting: i > 0, Value: json.RawMessage(fmt.Sprintf(`{"text":"v%d"}`, i+1))}); err != nil {
			t.Fatal(err)
		}
	}
	snapshots, err := loadForkArtifacts(ctx, db.DB, orm.Conversation{ID: "c1", BaseModel: orm.BaseModel{CreateUserID: "u1"}}, histories)
	if err != nil || len(snapshots) != 2 {
		t.Fatalf("snapshots=%#v err=%v", snapshots, err)
	}
	if snapshots[0].RevisionID == snapshots[1].RevisionID || !strings.Contains(string(snapshots[0].Value), "v1") || !strings.Contains(string(snapshots[1].Value), "v2") {
		t.Fatalf("fork replaced history with latest: %#v", snapshots)
	}
	copied := []orm.ChatHistory{{ID: "ch1", Result: "file_id:" + id}, {ID: "ch2", Result: "file_id:" + id}}
	rows, err := prepareForkArtifactCopies("u1", "child", histories, copied, snapshots)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(copied[0].Result, rows[0].ID) || !strings.Contains(copied[1].Result, rows[1].ID) {
		t.Fatalf("fork rewrote links to another turn: %#v", copied)
	}
}

func TestPublishedPanelDeduplicatesSeparateDeliveriesAndIncludesV2Only(t *testing.T) {
	t.Setenv("LAZYMIND_ARTIFACT_V2_ENABLED", "true")
	db := orm.MigrateTestDB(t, v2PersistModels()...)
	ctx := context.Background()
	legacy := []ConversationArtifactDTO{}
	for i := 1; i <= 3; i++ {
		dto, err := persistConversationArtifact(ctx, db.DB, "c1", "h1", "u1", &ArtifactCreatedEvent{
			ArtifactID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i), Filename: "report.txt", ContentType: "text", LogicalKey: "report",
			Value: json.RawMessage(fmt.Sprintf(`{"text":"v%d"}`, i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		legacy = append(legacy, *dto)
	}
	panel, err := publishedConversationArtifacts(ctx, db.DB, "u1", "c1", legacy)
	if err != nil || len(panel) != 1 || panel[0].Revision != 3 {
		t.Fatalf("duplicates: %#v err=%v", panel, err)
	}
	panel, err = publishedConversationArtifacts(ctx, db.DB, "u1", "c1", nil)
	if err != nil || len(panel) != 1 {
		t.Fatalf("V2 depended on legacy enumeration: %#v err=%v", panel, err)
	}
}

func TestForkRewritesReferencesToLatestPrecedingDelivery(t *testing.T) {
	source := []orm.ChatHistory{{ID: "h1"}, {ID: "h2"}, {ID: "h3"}, {ID: "h4"}}
	copied := []orm.ChatHistory{{ID: "c1", Result: "file_id:original"}, {ID: "c2", Result: "file_id:original"}, {ID: "c3", Result: "file_id:original"}, {ID: "c4", Result: "file_id:original"}}
	snapshots := []forkArtifactSnapshot{
		{SourceID: "original", RevisionID: "r1", HistoryID: "h1", ContentType: "text", Value: json.RawMessage(`{"text":"v1"}`)},
		{SourceID: "original", RevisionID: "r2", HistoryID: "h3", ContentType: "text", Value: json.RawMessage(`{"text":"v2"}`)},
	}
	rows, err := prepareForkArtifactCopies("u1", "child", source, copied, snapshots)
	if err != nil {
		t.Fatal(err)
	}
	for i, rowIndex := range []int{0, 0, 1, 1} {
		if copied[i].Result != "file_id:"+rows[rowIndex].ID {
			t.Fatalf("history %d has wrong revision: %s", i, copied[i].Result)
		}
	}
}
