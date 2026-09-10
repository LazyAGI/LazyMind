package chat

import (
	"encoding/json"
	"testing"

	"lazymind/core/common/orm"
)

func TestOrganizerFreezeRejectsStaleProvisionalAndPreservesClosedOpening(t *testing.T) {
	s := openingTestService(t)
	conv := openingTestConversation(t, s, "c1", "title", "auto")
	openingTestInput(t, s, "h1", conv.ID, "帮我优化这个", 1)
	first, err := loadOrganizerOpeningSnapshot(s.db, conv)
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := json.Marshal(first.IDs)
	meta := orm.ConversationOpening{ConversationID: conv.ID, UserID: conv.CreateUserID, Status: "done", IntentStatus: "provisional", Summary: "旧摘要", SourceHash: first.Hash, EvidenceHash: first.Evidence, SourceHistoryIDs: ids, InputJSON: first.Input, SeedRevision: 1}
	if err := s.db.Create(&meta).Error; err != nil {
		t.Fatal(err)
	}
	out, err := (OrganizerOpeningPreparer{}).Freeze(t.Context(), s.db, conv)
	if err != nil || out.Summary != "旧摘要" || out.Reason != "" {
		t.Fatalf("matching provisional must remain eligible: %+v %v", out, err)
	}
	openingTestInput(t, s, "h2", conv.ID, "是 LazyMind 的本地文件检索", 2)
	current, err := loadOrganizerOpeningSnapshot(s.db, conv)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&meta).Updates(map[string]any{"status": "pending", "source_hash": current.Hash, "seed_revision": 2, "job_id": "new-job"}).Error; err != nil {
		t.Fatal(err)
	}
	out, err = (OrganizerOpeningPreparer{}).Freeze(t.Context(), s.db, conv)
	if err != nil {
		t.Fatal(err)
	}
	var frozen frozenOrganizerOpening
	if err := json.Unmarshal(out.Frozen, &frozen); err != nil {
		t.Fatal(err)
	}
	if out.Summary != "" || frozen.JobID != "new-job" || frozen.Snapshot.Hash != current.Hash {
		t.Fatalf("stale summary reused: %+v %+v", out, frozen)
	}
	// A closed opening retains its established intent when unrelated turns are appended.
	if err := s.db.Model(&meta).Updates(map[string]any{"status": "done", "window_closed": true, "intent_status": "ready", "source_hash": first.Hash, "evidence_hash": first.Evidence, "source_history_ids": ids}).Error; err != nil {
		t.Fatal(err)
	}
	out, err = (OrganizerOpeningPreparer{}).Freeze(t.Context(), s.db, conv)
	if err != nil || out.Summary != "旧摘要" {
		t.Fatalf("closed intent lost: %+v %v", out, err)
	}
	if err := s.db.Model(&orm.ChatHistory{}).Where("id=?", "h1").Update("raw_content", "帮我开发新功能").Error; err != nil {
		t.Fatal(err)
	}
	out, err = (OrganizerOpeningPreparer{}).Freeze(t.Context(), s.db, conv)
	if err != nil || out.Summary != "" {
		t.Fatalf("changed evidence reused: %+v %v", out, err)
	}
}
