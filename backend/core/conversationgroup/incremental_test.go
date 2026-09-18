package conversationgroup

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

func TestIncrementalBatchAuditResumeAndPartition(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.ConversationOrganizerCandidate{}, &orm.AsyncJob{})
	now := time.Now().UTC()
	until := now.Add(time.Hour)
	job := asyncjob.Job{ID: "j", AttemptCount: 1}
	if err := db.Create(&orm.AsyncJob{ID: job.ID, Status: "running", JobType: organizerJobType, AttemptCount: 1, LockUntil: &until, NextRunAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	snapshot := organizerSnapshot{ID: "r", Conversations: []snapshotConversation{}, Groups: []snapshotGroup{}}
	for i := 0; i < 103; i++ {
		id := fmt.Sprintf("c%03d", i)
		snapshot.Conversations = append(snapshot.Conversations, snapshotConversation{ID: id, Summary: "工作"})
		if err := db.Create(&orm.ConversationOrganizerSnapshotItem{RunID: "r", ConversationID: id, Ordinal: i, Summary: "工作", PreparationStatus: "done"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(snapshot)
	run := orm.ConversationOrganizerRun{ID: "r", Status: "running", JobID: "j", SnapshotHash: "hash", SnapshotJSON: raw, ModelConfigJSON: json.RawMessage(`{}`)}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	auditCalls := 0
	batchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":cancel") {
			fmt.Fprint(w, `{"settled":true}`)
			return
		}
		var request struct {
			Input struct {
				Phase         string                 `json:"phase"`
				Cursor        int                    `json:"cursor"`
				Conversations []snapshotConversation `json:"conversations"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		input := request.Input
		out := organizerStepOutput{Identity: "identity", Processed: len(input.Conversations)}
		if input.Phase == "audit" {
			auditCalls++
			out.Accepted = true
			out.AuditReason = scopeAuditAccepted
			if len(input.Conversations) > 50 {
				t.Error("oversized audit")
			}
		} else {
			batchCalls++
			if batchCalls == 1 {
				for _, item := range input.Conversations {
					out.Assignments = append(out.Assignments, incrementalAssignment{ID: item.ID, GroupID: "g999"})
				}
				json.NewEncoder(w).Encode(map[string]any{"type": "result", "result": organizerTaskResult{Status: "succeeded", Output: out}})
				return
			}
			if input.Cursor == 0 {
				out.Operations = []candidateOperation{{Op: "create", ID: "new_1", Name: "工作", Scope: "处理工作"}}
			} else {
				out.Operations = []candidateOperation{{Op: "update", ID: "g1", Scope: "处理各类工作"}}
			}
			for _, item := range input.Conversations {
				target := "g1"
				if input.Cursor == 0 {
					target = "new_1"
				}
				out.Assignments = append(out.Assignments, incrementalAssignment{ID: item.ID, GroupID: target})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"type": "result", "result": organizerTaskResult{Status: "succeeded", Output: out}})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	var proposal *organizerProposal
	for i := 0; i < 14 && proposal == nil; i++ {
		// Reload from the database on every turn to exercise recovery, not in-memory continuity.
		if err := db.Where("id=?", run.ID).Take(&run).Error; err != nil {
			t.Fatal(err)
		}
		var err error
		proposal, err = runIncrementalStep(t.Context(), db.DB, &run, job, snapshot, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
	}
	if proposal == nil || len(proposal.NewGroups) != 1 || len(proposal.NewGroups[0].ConversationIDs) != 103 || batchCalls != 4 || auditCalls != 3 {
		t.Fatalf("incomplete: proposal=%+v batches=%d audits=%d", proposal, batchCalls, auditCalls)
	}
	var cp map[string]any
	json.Unmarshal(run.CheckpointJSON, &cp)
	if _, ok := cp["assignments"]; ok {
		t.Fatal("historical assignments retained in checkpoint")
	}
}

func TestScopeAuditExhaustionFallsBackWithoutChangingCommittedCandidates(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.ConversationOrganizerCandidate{}, &orm.AsyncJob{})
	now := time.Now().UTC()
	until := now.Add(time.Hour)
	job := asyncjob.Job{ID: "j", AttemptCount: 1}
	if err := db.Create(&orm.AsyncJob{ID: job.ID, Status: "running", JobType: organizerJobType, AttemptCount: 1, LockUntil: &until, NextRunAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	snapshot := organizerSnapshot{ID: "r"}
	for i, id := range []string{"old-1", "old-2", "old-3", "new-1"} {
		conversation := snapshotConversation{ID: id, Title: id, Summary: "邮件任务"}
		snapshot.Conversations = append(snapshot.Conversations, conversation)
		assignment := "cand-a"
		if id == "new-1" {
			assignment = ""
		}
		row := orm.ConversationOrganizerSnapshotItem{RunID: "r", ConversationID: id, Ordinal: i, Title: id, Summary: "邮件任务", PreparationStatus: "done", Assignment: assignment}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	card := directoryCard{ID: "cand-a", Name: "邮件处理", Scope: "处理邮件", Kind: "candidate", Count: 3, Version: 1}
	cardRaw, _ := json.Marshal(card)
	if err := db.Create(&orm.ConversationOrganizerCandidate{RunID: "r", ID: card.ID, Data: cardRaw}).Error; err != nil {
		t.Fatal(err)
	}
	cp := incrementalCheckpoint{GroupIDs: map[string]string{"cand-a": "g1"}, NextGroupNumber: 1, NextOrdinal: 3, Cursor: 3, Version: 1, Identity: "identity", Stage: "organizing", BatchSize: 50}
	cpRaw, _ := json.Marshal(cp)
	snapshotRaw, _ := json.Marshal(snapshot)
	run := orm.ConversationOrganizerRun{ID: "r", Status: "running", JobID: "j", SnapshotHash: "hash", SnapshotJSON: snapshotRaw, CheckpointJSON: cpRaw, ModelConfigJSON: json.RawMessage(`{}`)}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	auditCalls, batchCalls, fallbackCalls := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":cancel") {
			fmt.Fprint(w, `{"settled":true}`)
			return
		}
		var request struct {
			Input struct {
				Phase                      string                  `json:"phase"`
				PreserveExistingCandidates bool                    `json:"preserve_existing_candidates"`
				ScopeChange                incrementalScopeRepair  `json:"scope_change"`
				ScopeRepair                *incrementalScopeRepair `json:"scope_repair"`
				Conversations              []snapshotConversation  `json:"conversations"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		input := request.Input
		out := organizerStepOutput{Identity: "identity", Processed: len(input.Conversations)}
		if input.Phase == "audit" {
			auditCalls++
			if input.ScopeChange.Operation.Op != "update" || len(input.ScopeChange.SourceGroups) != 1 || input.ScopeChange.SourceGroups[0].ID != "g1" {
				t.Errorf("missing audit context: %+v", input.ScopeChange)
			}
			out.AuditReason = scopeAuditCoverageGap
			out.RejectedIDs = []string{"old-1"}
		} else {
			batchCalls++
			if input.PreserveExistingCandidates {
				fallbackCalls++
				if input.ScopeRepair == nil || input.ScopeRepair.Reason != scopeAuditCoverageGap || len(input.ScopeRepair.Rejected) != 1 || input.ScopeRepair.Rejected[0].ID != "old-1" {
					t.Errorf("missing persisted repair evidence: %+v", input.ScopeRepair)
				}
				out.Assignments = []incrementalAssignment{{ID: "new-1", GroupID: "free"}}
			} else {
				out.Operations = []candidateOperation{{Op: "update", ID: "g1", Scope: "仅发送邮件"}}
				out.Assignments = []incrementalAssignment{{ID: "new-1", GroupID: "g1"}}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "result", "result": organizerTaskResult{Status: "succeeded", Output: out}})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	var proposal *organizerProposal
	for i := 0; i < 12 && proposal == nil; i++ {
		if err := db.Where("id=?", run.ID).Take(&run).Error; err != nil {
			t.Fatal(err)
		}
		var err error
		proposal, err = runIncrementalStep(t.Context(), db.DB, &run, job, snapshot, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
	}
	if proposal == nil || auditCalls != 3 || batchCalls != 4 || fallbackCalls != 1 {
		t.Fatalf("proposal=%+v audits=%d batches=%d fallback=%d", proposal, auditCalls, batchCalls, fallbackCalls)
	}
	if len(proposal.NewGroups) != 1 || len(proposal.NewGroups[0].ConversationIDs) != 3 || len(proposal.FreeConversationIDs) != 1 || proposal.FreeConversationIDs[0] != "new-1" {
		t.Fatalf("unexpected partition: %+v", proposal)
	}
	var storedCandidate orm.ConversationOrganizerCandidate
	if err := db.Where("run_id=? AND id=?", run.ID, card.ID).Take(&storedCandidate).Error; err != nil {
		t.Fatal(err)
	}
	if string(storedCandidate.Data) != string(cardRaw) {
		t.Fatalf("committed candidate changed: %s", storedCandidate.Data)
	}
	var storedNew orm.ConversationOrganizerSnapshotItem
	if err := db.Where("run_id=? AND conversation_id=?", run.ID, "new-1").Take(&storedNew).Error; err != nil {
		t.Fatal(err)
	}
	if storedNew.Assignment != "free" {
		t.Fatalf("fallback assignment=%q", storedNew.Assignment)
	}
	var finalCheckpoint incrementalCheckpoint
	if err := json.Unmarshal(run.CheckpointJSON, &finalCheckpoint); err != nil {
		t.Fatal(err)
	}
	if finalCheckpoint.Pending != nil || finalCheckpoint.ScopeRepair != nil || finalCheckpoint.PreserveExistingCandidates || finalCheckpoint.ScopeAuditFailures != 0 {
		t.Fatalf("fallback state was not cleared: %+v", finalCheckpoint)
	}
}

func TestNoSharedScenarioImmediatelyPreservesExistingCandidates(t *testing.T) {
	out := organizerStepOutput{Accepted: false, AuditReason: scopeAuditNoSharedScenario}
	rejected, err := validateScopeAudit(out, []orm.ConversationOrganizerSnapshotItem{{ConversationID: "c1"}})
	if err != nil || len(rejected) != 0 {
		t.Fatalf("semantic rejection should not require rejected members: rejected=%v err=%v", rejected, err)
	}
	cp := incrementalCheckpoint{
		GroupIDs:        map[string]string{"g1": "candidate-1", "new-1": "candidate-2"},
		NextGroupNumber: 2,
		Repair:          2,
	}
	pending := &incrementalPending{Operations: []candidateOperation{
		{Op: "create", ID: "new-1", Name: "混合任务", Scope: "宽泛任务"},
		{Op: "merge", SourceIDs: []string{"g1", "new-1"}, TargetID: "g1", Name: "混合任务", Scope: "宽泛任务"},
	}}
	cp.Pending = pending
	evidence := incrementalScopeRepair{Operation: pending.Operations[1]}
	recordScopeAuditRejection(&cp, pending, map[string]string{"g1": "candidate-1"}, evidence, out.AuditReason, rejected)

	if cp.Pending != nil || !cp.PreserveExistingCandidates || cp.ScopeAuditFailures != 1 || cp.Repair != 0 || cp.Stage != "organizing" {
		t.Fatalf("semantic rejection did not enter conservative mode: %+v", cp)
	}
	if cp.ScopeRepair == nil || cp.ScopeRepair.Reason != scopeAuditNoSharedScenario {
		t.Fatalf("semantic rejection evidence was not retained: %+v", cp.ScopeRepair)
	}
	if _, ok := cp.GroupIDs["new-1"]; ok {
		t.Fatalf("orphan created candidate mapping was retained: %+v", cp.GroupIDs)
	}
	if cp.NextGroupNumber != 2 {
		t.Fatalf("candidate numbering moved backwards: %d", cp.NextGroupNumber)
	}
}

func TestPreservedCandidatesOnlyAllowCreate(t *testing.T) {
	if err := validatePreservedCandidateOperations(true, []candidateOperation{{Op: "create", ID: "cand-new", Name: "新场景", Scope: "新任务"}}); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"rename", "update", "merge"} {
		if err := validatePreservedCandidateOperations(true, []candidateOperation{{Op: op}}); err == nil {
			t.Fatalf("preserve mode accepted %s", op)
		}
	}
}

func TestOrganizerNameLockIsUserScoped(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ConversationOrganizerRun{})
	run := orm.ConversationOrganizerRun{ID: "r", UserID: "u", Status: "running", Stage: "canceling", SnapshotJSON: json.RawMessage(`{}`), ModelConfigJSON: json.RawMessage(`{}`)}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := requireOrganizerNamesUnlocked(db.DB, "u"); err == nil {
		t.Fatal("canceling run released names")
	}
	if err := requireOrganizerNamesUnlocked(db.DB, "other"); err != nil {
		t.Fatal(err)
	}
	db.Model(&run).Update("status", "canceled")
	if err := requireOrganizerNamesUnlocked(db.DB, "u"); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalTransferScalesWithConversationCount(t *testing.T) {
	var measurements []int
	for _, n := range []int{1000, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.ConversationOrganizerCandidate{}, &orm.AsyncJob{})
			now := time.Now().UTC()
			until := now.Add(time.Hour)
			job := asyncjob.Job{ID: "j", AttemptCount: 1}
			if err := db.Create(&orm.AsyncJob{ID: job.ID, Status: "running", JobType: organizerJobType, AttemptCount: 1, LockUntil: &until, NextRunAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			snapshot := organizerSnapshot{ID: "r", Conversations: []snapshotConversation{}, Groups: []snapshotGroup{}}
			rows := []orm.ConversationOrganizerSnapshotItem{}
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("c%05d", i)
				snapshot.Conversations = append(snapshot.Conversations, snapshotConversation{ID: id, Summary: "工作"})
				rows = append(rows, orm.ConversationOrganizerSnapshotItem{RunID: "r", ConversationID: id, Ordinal: i, Summary: "工作", PreparationStatus: "done"})
			}
			if err := db.CreateInBatches(rows, 200).Error; err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(snapshot)
			run := orm.ConversationOrganizerRun{ID: "r", Status: "running", JobID: "j", SnapshotHash: "hash", SnapshotJSON: raw, ModelConfigJSON: json.RawMessage(`{}`)}
			if err := db.Create(&run).Error; err != nil {
				t.Fatal(err)
			}
			totalBytes, calls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ":cancel") {
					fmt.Fprint(w, `{"settled":true}`)
					return
				}
				var body json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				totalBytes += len(body)
				calls++
				var request struct {
					Input struct {
						Conversations []snapshotConversation `json:"conversations"`
					} `json:"input"`
				}
				json.Unmarshal(body, &request)
				items := request.Input.Conversations
				if len(items) > 50 {
					t.Error("unbounded batch")
				}
				out := organizerStepOutput{Identity: "identity", Processed: len(items), Operations: []candidateOperation{}, Assignments: []incrementalAssignment{}}
				for _, item := range items {
					out.Assignments = append(out.Assignments, incrementalAssignment{ID: item.ID, GroupID: "free"})
				}
				json.NewEncoder(w).Encode(map[string]any{"type": "result", "result": organizerTaskResult{Status: "succeeded", Output: out}})
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			var proposal *organizerProposal
			for i := 0; i < n/25+2 && proposal == nil; i++ {
				var err error
				proposal, err = runIncrementalStep(t.Context(), db.DB, &run, job, snapshot, map[string]any{})
				if err != nil {
					t.Fatal(err)
				}
			}
			if proposal == nil || len(proposal.FreeConversationIDs) != n || calls != n/50 {
				t.Fatalf("incomplete %d: calls=%d", n, calls)
			}
			measurements = append(measurements, totalBytes)
			t.Logf("conversations=%d request_bytes=%d model_steps=%d", n, totalBytes, calls)
		})
	}
	if len(measurements) == 2 && measurements[1] > measurements[0]*11 {
		t.Fatalf("superlinear transfer: %v", measurements)
	}
}

func TestCandidateOperationsProtectFormalGroupsAndNames(t *testing.T) {
	for _, op := range []candidateOperation{
		{Op: "rename", ID: "g1", Name: "changed"},
		{Op: "update", ID: "g1", Scope: "changed"},
		{Op: "merge", SourceIDs: []string{"g1", "cand_a"}, TargetID: "cand_a", Name: "merged", Scope: "merged"},
		{Op: "create", ID: "cand_new", Name: " Formal ", Scope: "work"},
		{Op: "rename", ID: "cand_a", Name: " beta "},
	} {
		t.Run(op.Op+op.ID, func(t *testing.T) {
			cards := map[string]directoryCard{
				"g1":     {ID: "g1", Kind: "existing", Name: "Formal", Scope: "work", Version: 1},
				"cand_a": {ID: "cand_a", Kind: "candidate", Name: "Alpha", Scope: "work", Version: 1},
				"cand_b": {ID: "cand_b", Kind: "candidate", Name: "Beta", Scope: "work", Version: 1},
			}
			if _, err := applyCandidateOperation(cards, op); err == nil {
				t.Fatalf("accepted invalid operation: %+v", op)
			}
		})
	}
}

func TestCandidateMergeLimitsSourcesAndMemberCounts(t *testing.T) {
	cards := map[string]directoryCard{}
	sources := make([]string, 0, 5)
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("cand_%d", i)
		cards[id] = directoryCard{ID: id, Kind: "candidate", Name: fmt.Sprintf("group-%d", i), Scope: "work", Count: 5}
		if i <= 5 {
			sources = append(sources, id)
		}
	}
	op := candidateOperation{Op: "merge", SourceIDs: sources, TargetID: sources[0], Name: "merged", Scope: "merged work"}
	affected, err := applyCandidateOperation(cards, op)
	if err != nil || len(affected) != 5 || cards[sources[0]].Count != 25 {
		t.Fatalf("valid merge failed: affected=%v target=%+v err=%v", affected, cards[sources[0]], err)
	}
	if _, err := applyCandidateOperation(cards, candidateOperation{Op: "merge", SourceIDs: []string{sources[0], "cand_6"}, TargetID: sources[0], Name: "again", Scope: "again"}); err == nil {
		t.Fatal("merged target above the member limit remained mergeable")
	}
	base := map[string]directoryCard{
		"one":   {ID: "one", Kind: "candidate", Name: "one", Scope: "work", Count: 1},
		"two":   {ID: "two", Kind: "candidate", Name: "two", Scope: "work", Count: 1},
		"three": {ID: "three", Kind: "candidate", Name: "three", Scope: "work", Count: 1},
	}
	if _, err := applyCandidateOperation(base, candidateOperation{Op: "merge", SourceIDs: []string{"one", "one"}, TargetID: "one", Name: "duplicate", Scope: "work"}); err == nil {
		t.Fatal("merge accepted duplicate sources")
	}
	if _, err := applyCandidateOperation(base, candidateOperation{Op: "merge", SourceIDs: []string{"one", "two"}, TargetID: "three", Name: "outside", Scope: "work"}); err == nil {
		t.Fatal("merge accepted a target outside its sources")
	}
	if _, err := applyCandidateOperation(map[string]directoryCard{
		"large": {ID: "large", Kind: "candidate", Name: "large", Scope: "work", Count: 6},
		"small": {ID: "small", Kind: "candidate", Name: "small", Scope: "work", Count: 1},
	}, candidateOperation{Op: "merge", SourceIDs: []string{"large", "small"}, TargetID: "large", Name: "merged", Scope: "merged"}); err == nil {
		t.Fatal("candidate above the member limit was merged")
	}
	six := append(append([]string{}, sources...), "cand_6")
	if _, err := applyCandidateOperation(map[string]directoryCard{
		"cand_1": {ID: "cand_1", Kind: "candidate", Name: "one", Scope: "work", Count: 1},
		"cand_2": {ID: "cand_2", Kind: "candidate", Name: "two", Scope: "work", Count: 1},
		"cand_3": {ID: "cand_3", Kind: "candidate", Name: "three", Scope: "work", Count: 1},
		"cand_4": {ID: "cand_4", Kind: "candidate", Name: "four", Scope: "work", Count: 1},
		"cand_5": {ID: "cand_5", Kind: "candidate", Name: "five", Scope: "work", Count: 1},
		"cand_6": {ID: "cand_6", Kind: "candidate", Name: "six", Scope: "work", Count: 1},
	}, candidateOperation{Op: "merge", SourceIDs: six, TargetID: "cand_1", Name: "merged", Scope: "merged"}); err == nil {
		t.Fatal("merge accepted more than five sources")
	}
	large := map[string]directoryCard{"large": {ID: "large", Kind: "candidate", Name: "large", Scope: "work", Count: 100}}
	if _, err := applyCandidateOperation(large, candidateOperation{Op: "update", ID: "large", Scope: "updated work"}); err != nil {
		t.Fatalf("large candidate update was rejected: %v", err)
	}
}

func TestScopeRepairProjectionUsesCurrentBatch(t *testing.T) {
	original := incrementalScopeRepair{NewMembers: []snapshotConversation{{ID: "current"}, {ID: "later"}}}
	projected := scopeRepairForRows(original, []orm.ConversationOrganizerSnapshotItem{{ConversationID: "current"}})
	if len(projected.NewMembers) != 1 || projected.NewMembers[0].ID != "current" {
		t.Fatalf("unexpected projected evidence: %+v", projected.NewMembers)
	}
	if len(original.NewMembers) != 2 {
		t.Fatal("persisted repair evidence was mutated")
	}
}

func TestPendingDirectoryCompletedBeforeAssignmentAudit(t *testing.T) {
	for _, scenario := range []string{"later_create", "later_merge", "unknown_target"} {
		t.Run(scenario, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.ConversationOrganizerCandidate{}, &orm.AsyncJob{})
			now := time.Now().UTC()
			until := now.Add(time.Hour)
			job := asyncjob.Job{ID: "j", AttemptCount: 1}
			if err := db.Create(&orm.AsyncJob{ID: "j", Status: "running", JobType: organizerJobType, AttemptCount: 1, LockUntil: &until, NextRunAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			snapshot := organizerSnapshot{ID: "r"}
			for i, id := range []string{"old", "new-b", "new-a"} {
				snapshot.Conversations = append(snapshot.Conversations, snapshotConversation{ID: id, Summary: id})
				assignment := ""
				if i == 0 {
					assignment = "cand_a"
				}
				if err := db.Create(&orm.ConversationOrganizerSnapshotItem{RunID: "r", ConversationID: id, Ordinal: i, Summary: id, PreparationStatus: "done", Assignment: assignment}).Error; err != nil {
					t.Fatal(err)
				}
			}
			card := directoryCard{ID: "cand_a", Name: "邮件", Scope: "读邮件", Kind: "candidate", Count: 1, Version: 1}
			cardRaw, _ := json.Marshal(card)
			if err := db.Create(&orm.ConversationOrganizerCandidate{RunID: "r", ID: card.ID, Data: cardRaw}).Error; err != nil {
				t.Fatal(err)
			}
			pending := &incrementalPending{AuditOrdinal: -1, Operations: []candidateOperation{
				{Op: "update", ID: "cand_a", Scope: "收发邮件"},
				{Op: "create", ID: "cand_b", Name: "附件", Scope: "处理邮件附件"},
			}, Assignments: []incrementalAssignment{{ID: "new-b", GroupID: "cand_b"}, {ID: "new-a", GroupID: "cand_a"}}}
			if scenario == "later_merge" {
				pending.Operations = append(pending.Operations, candidateOperation{Op: "merge", SourceIDs: []string{"cand_a", "cand_b"}, TargetID: "cand_a", Name: "邮件处理", Scope: "收发邮件及附件"})
			}
			if scenario == "unknown_target" {
				pending.Assignments[0].GroupID = "cand_missing"
			}
			cp := incrementalCheckpoint{GroupIDs: map[string]string{"cand_a": "g1", "cand_b": "g2"}, NextGroupNumber: 2, NextOrdinal: 1, Cursor: 1, Version: 1, Identity: "identity", Stage: "organizing", BatchSize: 50, Pending: pending}
			cpRaw, _ := json.Marshal(cp)
			snapshotRaw, _ := json.Marshal(snapshot)
			run := orm.ConversationOrganizerRun{ID: "r", Status: "running", JobID: "j", SnapshotHash: "hash", SnapshotJSON: snapshotRaw, CheckpointJSON: cpRaw, ModelConfigJSON: json.RawMessage(`{}`)}
			if err := db.Create(&run).Error; err != nil {
				t.Fatal(err)
			}
			audits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Input struct {
						Phase         string                 `json:"phase"`
						Scope         string                 `json:"scope"`
						ScopeChange   incrementalScopeRepair `json:"scope_change"`
						Conversations []snapshotConversation `json:"conversations"`
					} `json:"input"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				audits++
				input := request.Input
				wantScope, wantMembers := "收发邮件", 1
				if scenario == "later_merge" {
					wantScope, wantMembers = "收发邮件及附件", 2
				}
				if input.Phase != "audit" || input.Scope != wantScope || len(input.ScopeChange.NewMembers) != wantMembers || len(input.Conversations) != 1 || input.Conversations[0].ID != "old" {
					t.Errorf("unexpected audit: %+v", input)
				}
				if scenario == "later_create" && input.ScopeChange.NewMembers[0].ID != "new-a" {
					t.Error("unrelated new candidate included in audit")
				}
				var stored orm.ConversationOrganizerCandidate
				db.Where("run_id=? AND id=?", "r", "cand_a").Take(&stored)
				if string(stored.Data) != string(cardRaw) {
					t.Error("candidate committed before audit completed")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"type": "result", "result": organizerTaskResult{Status: "succeeded", Output: organizerStepOutput{Identity: "identity", Processed: 1, Accepted: true, AuditReason: scopeAuditAccepted}}})
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			for step := 0; step < 6; step++ {
				_, err := runIncrementalStep(t.Context(), db.DB, &run, job, snapshot, map[string]any{})
				if scenario == "unknown_target" {
					if err == nil || err.Error() != "unknown or cyclic candidate target" || audits != 0 {
						t.Fatalf("invalid target reached audit: err=%v audits=%d", err, audits)
					}
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if run.ProgressCurrent == 3 {
					break
				}
			}
			var assigned int64
			db.Model(&orm.ConversationOrganizerSnapshotItem{}).Where("run_id=? AND ordinal>0 AND assignment<>''", "r").Count(&assigned)
			if scenario == "unknown_target" {
				if assigned != 0 {
					t.Fatal("invalid batch wrote assignments")
				}
			} else if assigned != 2 || run.ProgressCurrent != 3 || audits == 0 {
				t.Fatalf("batch incomplete: assigned=%d progress=%d audits=%d", assigned, run.ProgressCurrent, audits)
			}
			var candidates []orm.ConversationOrganizerCandidate
			if err := db.Where("run_id=?", "r").Find(&candidates).Error; err != nil {
				t.Fatal(err)
			}
			if scenario == "unknown_target" {
				if len(candidates) != 1 || string(candidates[0].Data) != string(cardRaw) {
					t.Fatal("invalid batch changed candidate directory")
				}
			} else {
				cards, err := loadDirectory(db.DB, run.ID, snapshot)
				if err != nil {
					t.Fatal(err)
				}
				target, err := directoryTarget(cards, "cand_b")
				wantTarget, wantCount := "cand_b", 1
				if scenario == "later_merge" {
					wantTarget, wantCount = "cand_a", 3
				}
				if err != nil || target != wantTarget || cards[target].Count != wantCount {
					t.Fatalf("incorrect committed target/count: target=%s cards=%+v err=%v", target, cards, err)
				}
			}
		})
	}
}
