package product

import (
	"encoding/json"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"testing"
	"time"
)

func reviewWorkspace() map[string]any {
	return map[string]any{
		"workspace_id": "workspace",
		"current_run":  map[string]any{"selected_stage": "design", "run_status": "awaiting-stage-confirmation", "hard_stop_policy": "explicit-decision"},
		"decisions":    []any{map[string]any{"decision_id": "D1", "title": "跨租户共享", "value": "share across tenants", "risk": "irreversible", "status": "proposed"}},
	}
}

func TestProductNavigationNeverApprovesDecisions(t *testing.T) {
	for _, tc := range []struct {
		action, stage string
		blocked       bool
	}{
		{"finish", "", false}, {"switch-stage", "design", false},
		{"switch-stage", "prd", true}, {"continue", "prd", true},
	} {
		t.Run(tc.action+tc.stage, func(t *testing.T) {
			workspace := reviewWorkspace()
			before, _ := json.Marshal(workspace)
			err := productRelayDecisionGate(workspace, tc.action, tc.stage)
			if (err != nil) != tc.blocked {
				t.Fatalf("unexpected gate: %v", err)
			}
			after, _ := json.Marshal(workspace)
			if string(before) != string(after) {
				t.Fatal("navigation mutated decisions")
			}
			decision := productCurrentDecisions(workspace)[0]
			decision["deferred"] = true
			if err := productRelayDecisionGate(workspace, tc.action, tc.stage); err != nil {
				t.Fatal(err)
			}
			if decision["status"] != "proposed" {
				t.Fatal("deferral accepted the decision")
			}
		})
	}
}

func seedReviewProduct(t *testing.T) *Repository {
	t.Helper()
	repo := testRepo(t)
	if err := repo.db.AutoMigrate(&orm.WorkflowRevision{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{}, &orm.SubAgentArtifact{}); err != nil {
		t.Fatal(err)
	}
	createTestConversation(t, repo, "conversation", "owner")
	now := time.Now().UTC()
	if err := repo.db.Create(&orm.WorkflowRevision{ID: "revision", CompiledGraph: json.RawMessage(`{"runtime":{"host_extensions":["product-project-v1"]}}`)}).Error; err != nil {
		t.Fatal(err)
	}
	session := orm.WorkflowSession{ID: "session", WorkflowID: "renamed-project", WorkflowRevisionID: "revision", ConversationID: "conversation", Status: "completed", StateVersion: 1, CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}
	if err := repo.db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	content := "Product decision content"
	values := map[string]any{
		"workspace_state": reviewWorkspace(),
		"design_document": map[string]any{"text": content},
		"stage_manifest":  map[string]any{"artifact_id": "artifact", "version": "1.0", "stage": "design", "status": "reviewable", "host_artifact": map[string]any{"slot": "design_document", "content_sha256": requestHash([]byte(content))}},
	}
	for slot, value := range values {
		raw, _ := json.Marshal(value)
		row := orm.WorkflowSlotRevision{ID: slot, SessionID: "session", SlotID: slot, Slot: slot, Revision: 1, Selected: true, ContentSnapshot: raw, Validity: "effective", ChangeSource: "human", CreatedAt: now}
		if err := repo.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestProductFinishPersistsProposedDecisionAndIsIdempotent(t *testing.T) {
	repo := seedReviewProduct(t)
	req := ProductRelayRequest{Action: "finish", ExpectedStateVersion: 1, IdempotencyKey: "finish-once"}
	first, err := repo.RelayProductStage(t.Context(), "owner", "session", req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.RelayProductStage(t.Context(), "owner", "session", req)
	if err != nil || string(first) != string(second) {
		t.Fatalf("retry: %s %v", second, err)
	}
	_, workspace, _, err := repo.productRelayState(t.Context(), "owner", "session")
	if err != nil {
		t.Fatal(err)
	}
	decision := productCurrentDecisions(workspace)[0]
	if decision["status"] != "proposed" || decision["accepted_by"] != nil {
		t.Fatalf("implicit approval: %#v", decision)
	}
}

func TestProductDecisionExplicitApprovalChecksHashAndRestoresAfterRefresh(t *testing.T) {
	repo := seedReviewProduct(t)
	_, digest := productDecisionSnapshot(productCurrentDecisions(reviewWorkspace())[0])
	req := ProductDecisionRequest{Action: "accept", ExpectedStateVersion: 1, ExpectedDecisionHash: "sha256:stale", IdempotencyKey: "accept-once"}
	if _, err := repo.DecideProductDecision(t.Context(), "owner", "session", "D1", req); err == nil {
		t.Fatal("accepted stale hash")
	}
	req.ExpectedDecisionHash = digest
	if _, err := repo.DecideProductDecision(t.Context(), "other-user", "session", "D1", req); err == nil {
		t.Fatal("accepted for another owner")
	}
	first, err := repo.DecideProductDecision(t.Context(), "owner", "session", "D1", req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.DecideProductDecision(t.Context(), "owner", "session", "D1", req)
	if err != nil || string(first) != string(second) {
		t.Fatalf("retry: %s %v", second, err)
	}
	_, workspace, _, err := repo.productRelayState(t.Context(), "owner", "session")
	if err != nil {
		t.Fatal(err)
	}
	decision := productCurrentDecisions(workspace)[0]
	if decision["status"] != "accepted" || decision["accepted_by"] != "owner" {
		t.Fatalf("lost explicit approval: %#v", decision)
	}
	// A later proposal retains the accepted baseline, but must be reviewed anew.
	changed := reviewWorkspace()
	productCurrentDecisions(changed)[0]["value"] = "a different proposal"
	if err := repo.restoreProductDecisionEvents(t.Context(), "owner", orm.WorkflowSession{ID: "session"}, changed); err != nil {
		t.Fatal(err)
	}
	candidate := productDecisionCandidate(productCurrentDecisions(changed)[0])
	if candidate["status"] != "proposed" || candidate["value"] != "a different proposal" {
		t.Fatalf("new content was auto-approved: %#v", candidate)
	}
}

func TestProductLegacyAutoApprovalIsReopened(t *testing.T) {
	workspace := reviewWorkspace()
	decision := productCurrentDecisions(workspace)[0]
	decision["status"], decision["accepted_by"], decision["acceptance_ref"] = "accepted", "product-workflow-default", "old-command"
	productReopenAutomaticDecisions(workspace)
	if productPendingHardStops(workspace) != 1 || decision["accepted_by"] != nil {
		t.Fatalf("still trusted auto acceptance: %#v", decision)
	}
}

func testRepo(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	if err := repo.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{}); err != nil {
		t.Fatal(err)
	}
	return repo
}
func createTestConversation(t *testing.T, repo *Repository, id, owner string) {
	t.Helper()
	if err := repo.db.AutoMigrate(&orm.Conversation{}, &orm.WorkflowSession{}, &orm.TaskCenterTask{}); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Create(&orm.Conversation{ID: id, BaseModel: orm.BaseModel{CreateUserID: owner, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestStageReadyComesFromActualGraphAndBoundInputs(t *testing.T) {
	raw := json.RawMessage(`{"nodes":{"renamed_entry":{"id":"renamed_entry","input_expression":{"material":"brief"}}},"control_edges":[{"from":"__start__","to":"renamed_entry"},{"from":"renamed_entry","to":"__end__"}],"material_producers":{"brief":{"kind":"external"}}}`)
	if ready := initialReady(raw, nil); len(ready) != 0 {
		t.Fatalf("missing input was ready: %v", ready)
	}
	if ready := initialReady(raw, []InputBinding{{ID: "bound", MaterialID: "brief"}}); len(ready) != 1 || ready[0] != "renamed_entry" {
		t.Fatalf("wrong frontier: %v", ready)
	}
}

func TestRelayKeepsPinnedPackageWhenNewHeadExists(t *testing.T) {
	repo := seedReviewProduct(t)
	graph := json.RawMessage(`{"runtime":{"host_extensions":["product-project-v1"]},"nodes":{"renamed_entry":{"id":"renamed_entry"}},"control_edges":[{"from":"__start__","to":"renamed_entry"},{"from":"renamed_entry","to":"__end__"}],"material_producers":{"workspace_seed":{"kind":"external"},"stage_approval":{"kind":"external"},"requested_stage":{"kind":"external"},"upstream_design":{"kind":"external"}}}`)
	if err := repo.db.AutoMigrate(&orm.WorkflowResource{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Create(&orm.WorkflowResource{ID: "resource", WorkflowID: "renamed-project", WorkflowRef: "builtin:renamed-project", HeadRevisionID: "new-head", Status: "active", OwnerUserID: "owner"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Model(&orm.WorkflowRevision{}).Where("id = ?", "revision").Updates(map[string]any{"plugin_resource_id": "resource", "compiled_graph": graph}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Create(&orm.WorkflowRevision{ID: "new-head", WorkflowResourceID: "resource", RevisionNo: 2, CompiledGraph: json.RawMessage(`{"nodes":{}}`)}).Error; err != nil {
		t.Fatal(err)
	}
	raw, err := repo.RelayProductStage(t.Context(), "owner", "session", ProductRelayRequest{Action: "switch-stage", SelectedStage: "design", ExpectedStateVersion: 1, IdempotencyKey: "switch-pinned"})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		SessionID string   `json:"session_id"`
		Ready     []string `json:"ready_steps"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	var next orm.WorkflowSession
	if err := repo.db.First(&next, "id = ?", result.SessionID).Error; err != nil {
		t.Fatal(err)
	}
	if next.WorkflowRevisionID != "revision" {
		t.Fatalf("upgraded implicitly to %s", next.WorkflowRevisionID)
	}
	if len(result.Ready) != 1 || result.Ready[0] != "renamed_entry" {
		t.Fatalf("wrong Ready: %s", raw)
	}
	bindings, err := repo.ListInputBindings(t.Context(), "owner", next.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		if binding.MaterialID == "word_target" {
			t.Fatal("host injected business default")
		}
	}
}
