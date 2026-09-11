package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"lazymind/core/algo"
	"lazymind/core/common/orm"
)

func TestOrganizerBatchReusesSummaryAndSplitsInvalidIDs(t *testing.T) {
	s := conversationTitleTestService(t)
	meta := orm.ConversationOpening{ConversationID: "cached", UserID: "u", Status: "done", SourceHash: "h", IntentStatus: "ready", Summary: "已有摘要", InputJSON: json.RawMessage(`{}`), SourceHistoryIDs: json.RawMessage(`[]`)}
	if err := s.db.Create(&meta).Error; err != nil {
		t.Fatal(err)
	}
	var sizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Items []algo.ConversationTitleBatchInput `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if r.URL.Path != "/api/conversation/titles:generate" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		items := request.Items
		sizes = append(sizes, len(items))
		result := algo.ConversationTitleBatchResult{Status: "succeeded"}
		for _, input := range items {
			id := input.ID
			if len(items) > 1 {
				id = items[0].ID
			} // A whole invalid batch must be rejected before any mapping is accepted.
			result.Output.Items = append(result.Output.Items, algo.ConversationTitleBatchItem{ID: id, ConversationTitle: algo.ConversationTitle{Title: "标题" + input.ID, Summary: "摘要" + input.ID, IntentStatus: "ready"}})
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	var inputs []json.RawMessage
	for _, id := range []string{"cached", "new1", "new2"} {
		raw, err := json.Marshal(frozenGroupingTitle{ConversationID: id, Snapshot: conversationTitleSnapshot{Hash: "h", Input: json.RawMessage(`{"messages":[{"role":"user","content":"发邮件"}]}`)}})
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, raw)
	}
	results, err := (OrganizerTitlePreparer{}).ResolveBatch(t.Context(), s.db, "u", inputs, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sizes, []int{2, 1, 1}) || len(results) != 3 || results[0].Output.Summary != "已有摘要" || results[1].Output.Summary != "摘要1" || results[2].Output.Summary != "摘要2" {
		t.Fatalf("batch mapping/reuse failed: sizes=%v results=%+v", sizes, results)
	}
}

func TestOrganizerFreezeRejectsStaleProvisionalAndPreservesClosedOpening(t *testing.T) {
	s := conversationTitleTestService(t)
	conv := conversationTitleTestConversation(t, s, "c1", "title", "auto")
	conversationTitleTestInput(t, s, "h1", conv.ID, "帮我优化这个", 1)
	first, err := loadGroupingTitleSnapshot(s.db, conv)
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := json.Marshal(first.IDs)
	meta := orm.ConversationOpening{ConversationID: conv.ID, UserID: conv.CreateUserID, Status: "done", IntentStatus: "provisional", Summary: "旧摘要", SourceHash: first.Hash, EvidenceHash: first.Evidence, SourceHistoryIDs: ids, InputJSON: first.Input, SeedRevision: 1}
	if err := s.db.Create(&meta).Error; err != nil {
		t.Fatal(err)
	}
	out, err := (OrganizerTitlePreparer{}).Freeze(t.Context(), s.db, conv)
	if err != nil || out.Summary != "旧摘要" || out.Reason != "" {
		t.Fatalf("matching provisional must remain eligible: %+v %v", out, err)
	}
	conversationTitleTestInput(t, s, "h2", conv.ID, "是 LazyMind 的本地文件检索", 2)
	current, err := loadGroupingTitleSnapshot(s.db, conv)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&meta).Updates(map[string]any{"status": "pending", "source_hash": current.Hash, "seed_revision": 2, "job_id": "new-job"}).Error; err != nil {
		t.Fatal(err)
	}
	out, err = (OrganizerTitlePreparer{}).Freeze(t.Context(), s.db, conv)
	if err != nil {
		t.Fatal(err)
	}
	var frozen frozenGroupingTitle
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
	out, err = (OrganizerTitlePreparer{}).Freeze(t.Context(), s.db, conv)
	if err != nil || out.Summary != "旧摘要" {
		t.Fatalf("closed intent lost: %+v %v", out, err)
	}
	if err := s.db.Model(&orm.ChatHistory{}).Where("id=?", "h1").Update("raw_content", "帮我开发新功能").Error; err != nil {
		t.Fatal(err)
	}
	out, err = (OrganizerTitlePreparer{}).Freeze(t.Context(), s.db, conv)
	if err != nil || out.Summary != "" {
		t.Fatalf("changed evidence reused: %+v %v", out, err)
	}
}
