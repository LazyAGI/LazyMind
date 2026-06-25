package plugin

import (
	"context"
	"path/filepath"
	"testing"

	"lazymind/core/common/orm"
)

// newTestDB creates an in-memory SQLite database and auto-migrates all plugin tables.
func newTestDB(t *testing.T) *orm.DB {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "plugin.db"))
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.SubAgentTask{},
		&orm.SubAgentStep{},
		&orm.SubAgentArtifact{},
		&orm.PluginSession{},
		&orm.PluginSessionStep{},
		&orm.PluginSlotRevision{},
		&orm.PluginStepIntent{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// ──────────────────────────────────────────────
// Session CRUD
// ──────────────────────────────────────────────

func TestCreateSession_Basic(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	s, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID:      "ps-1",
		ConversationID: "conv-1",
		PluginID:       "image-plugin",
		CurrentStepID:  "analyze_subject",
		CreateUserID:   "user-1",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if s.Status != SessionStatusActive {
		t.Fatalf("expected active, got %s", s.Status)
	}
	if s.PluginID != "image-plugin" {
		t.Fatalf("expected image-plugin, got %s", s.PluginID)
	}
}

func TestCreateSession_RejectsSecondActiveSession(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-1", ConversationID: "conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("first session: %v", err)
	}

	// A second active session on the same conversation must be rejected.
	_, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-2", ConversationID: "conv-1", PluginID: "image-plugin",
	})
	if err == nil {
		t.Fatal("expected error for duplicate active session, got nil")
	}
}

func TestCreateSession_AllowsNewSessionAfterCompletion(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-1", ConversationID: "conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("first session: %v", err)
	}
	if err := UpdateSessionStatus(ctx, db.DB, "ps-1", SessionStatusCompleted); err != nil {
		t.Fatalf("complete session: %v", err)
	}

	// A new session is allowed once the previous one is completed.
	_, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-2", ConversationID: "conv-1", PluginID: "image-plugin",
	})
	if err != nil {
		t.Fatalf("second session after completion: %v", err)
	}
}

func TestGetActiveSession(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// No session yet.
	got, err := GetActiveSession(ctx, db.DB, "conv-1")
	if err != nil {
		t.Fatalf("GetActiveSession: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil when no active session")
	}

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-1", ConversationID: "conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err = GetActiveSession(ctx, db.DB, "conv-1")
	if err != nil {
		t.Fatalf("GetActiveSession after create: %v", err)
	}
	if got == nil || got.ID != "ps-1" {
		t.Fatalf("expected ps-1, got %v", got)
	}
}

func TestUpdateSessionCurrentStep(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-1", ConversationID: "conv-1", PluginID: "image-plugin",
		CurrentStepID: "analyze_subject",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := UpdateSessionCurrentStep(ctx, db.DB, "ps-1", "optimize_prompt"); err != nil {
		t.Fatalf("update: %v", err)
	}

	s, err := GetSession(ctx, db.DB, "ps-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if s.CurrentStepID != "optimize_prompt" {
		t.Fatalf("expected optimize_prompt, got %s", s.CurrentStepID)
	}
}

// ──────────────────────────────────────────────
// Session Step CRUD
// ──────────────────────────────────────────────

func TestCreateSessionStep_AttemptIncrement(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-1", ConversationID: "conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}

	att1, _ := NextAttempt(ctx, db.DB, "ps-1", "optimize_prompt")
	if att1 != 1 {
		t.Fatalf("expected attempt 1, got %d", att1)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-1", "optimize_prompt", "task-1", att1); err != nil {
		t.Fatalf("step 1: %v", err)
	}

	att2, _ := NextAttempt(ctx, db.DB, "ps-1", "optimize_prompt")
	if att2 != 2 {
		t.Fatalf("expected attempt 2 on retry, got %d", att2)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-1", "optimize_prompt", "task-2", att2); err != nil {
		t.Fatalf("step 2: %v", err)
	}
}

func TestUpdateStepStatus(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-1", ConversationID: "conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-1", "analyze_subject", "task-a", 1); err != nil {
		t.Fatalf("step: %v", err)
	}

	if err := UpdateStepStatus(ctx, db.DB, "task-a", StepStatusRunning); err != nil {
		t.Fatalf("update running: %v", err)
	}
	step, _ := GetStepByTaskID(ctx, db.DB, "task-a")
	if step.Status != StepStatusRunning {
		t.Fatalf("expected running, got %s", step.Status)
	}

	if err := UpdateStepStatus(ctx, db.DB, "task-a", StepStatusSucceeded); err != nil {
		t.Fatalf("update succeeded: %v", err)
	}
	step, _ = GetStepByTaskID(ctx, db.DB, "task-a")
	if step.Status != StepStatusSucceeded {
		t.Fatalf("expected succeeded, got %s", step.Status)
	}
}

func TestGetLatestStep_ReturnsHighestAttempt(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-1", ConversationID: "conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}

	for i, taskID := range []string{"t-1", "t-2", "t-3"} {
		if _, err := CreateSessionStep(ctx, db.DB, "ps-1", "generate_image", taskID, i+1); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
	}

	step, err := GetLatestStep(ctx, db.DB, "ps-1", "generate_image")
	if err != nil {
		t.Fatalf("GetLatestStep: %v", err)
	}
	if step == nil || step.Attempt != 3 {
		t.Fatalf("expected attempt 3, got %v", step)
	}
}

func TestListSteps_OrderedByCreation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-1", ConversationID: "conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}

	stepIDs := []string{"analyze_subject", "optimize_prompt", "generate_image", "enhance_image"}
	for i, sid := range stepIDs {
		if _, err := CreateSessionStep(ctx, db.DB, "ps-1", sid, "task-"+sid, i+1); err != nil {
			t.Fatalf("step %s: %v", sid, err)
		}
	}

	steps, err := ListSteps(ctx, db.DB, "ps-1")
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	if len(steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(steps))
	}
	for i, s := range steps {
		if s.StepID != stepIDs[i] {
			t.Fatalf("step[%d]: expected %s, got %s", i, stepIDs[i], s.StepID)
		}
	}
}

// ──────────────────────────────────────────────
// Slot Revisions
// ──────────────────────────────────────────────

func TestWriteSlotRevision_Single_DeselectsPrevious(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-1", ConversationID: "conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}

	// Write revision 1.
	r1, err := WriteSlotRevision(ctx, db.DB, "ps-1", "prompt_used", "optimized_prompt", "optimize_prompt", 1, "single", nil)
	if err != nil {
		t.Fatalf("rev1: %v", err)
	}
	if !r1.Selected || r1.Revision != 1 {
		t.Fatalf("rev1: expected selected=true revision=1, got %+v", r1)
	}

	// Write revision 2 (re-run of optimize_prompt).
	r2, err := WriteSlotRevision(ctx, db.DB, "ps-1", "prompt_used", "optimized_prompt", "optimize_prompt", 2, "single", nil)
	if err != nil {
		t.Fatalf("rev2: %v", err)
	}
	if !r2.Selected || r2.Revision != 2 {
		t.Fatalf("rev2: expected selected=true revision=2, got %+v", r2)
	}

	// Revision 1 must now be deselected.
	selected, _ := LoadSelectedSlots(ctx, db.DB, "ps-1")
	if len(selected) != 1 {
		t.Fatalf("expected 1 selected slot, got %d", len(selected))
	}
	if selected[0].Revision != 2 {
		t.Fatalf("expected latest revision selected, got revision %d", selected[0].Revision)
	}
}

func TestWriteSlotRevision_List_AppendsAll(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-1", ConversationID: "conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}

	// Three enhance runs — each appends to the list.
	for i := 1; i <= 3; i++ {
		if _, err := WriteSlotRevision(ctx, db.DB,
			"ps-1", "enhanced_image_output", "enhanced_image_url",
			"enhance_image", i, "list", nil); err != nil {
			t.Fatalf("rev %d: %v", i, err)
		}
	}

	selected, err := LoadSelectedSlots(ctx, db.DB, "ps-1")
	if err != nil {
		t.Fatalf("LoadSelectedSlots: %v", err)
	}
	if len(selected) != 3 {
		t.Fatalf("expected 3 list entries, got %d", len(selected))
	}
	for i, s := range selected {
		if s.ListIndex == nil || *s.ListIndex != i {
			t.Fatalf("entry %d: expected list_index=%d, got %v", i, i, s.ListIndex)
		}
		if !s.Selected {
			t.Fatalf("entry %d: expected selected=true", i)
		}
	}
}

func TestWriteSlotRevision_SingleAndList_Coexist(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-1", ConversationID: "conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}

	// Write a single-cardinality slot (prompt).
	if _, err := WriteSlotRevision(ctx, db.DB,
		"ps-1", "prompt_used", "optimized_prompt", "optimize_prompt", 1, "single", nil); err != nil {
		t.Fatalf("single slot: %v", err)
	}

	// Write two list-cardinality slots (enhanced images).
	for i := 1; i <= 2; i++ {
		if _, err := WriteSlotRevision(ctx, db.DB,
			"ps-1", "enhanced_image_output", "enhanced_image_url",
			"enhance_image", i, "list", nil); err != nil {
			t.Fatalf("list slot %d: %v", i, err)
		}
	}

	selected, _ := LoadSelectedSlots(ctx, db.DB, "ps-1")
	// 1 single + 2 list = 3 selected rows.
	if len(selected) != 3 {
		t.Fatalf("expected 3 total selected rows, got %d", len(selected))
	}
}

func TestWriteSlotRevision_PartialRetry_ReplacesTargetIndex(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-partial", ConversationID: "conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}

	// Write three items (index 0, 1, 2) via normal append.
	for i := 1; i <= 3; i++ {
		if _, err := WriteSlotRevision(ctx, db.DB,
			"ps-partial", "image_gallery", "material_image",
			"collect_materials", i, "list", nil); err != nil {
			t.Fatalf("initial append %d: %v", i, err)
		}
	}

	// Partial retry: replace only index 1.
	idx := 1
	r, err := WriteSlotRevision(ctx, db.DB,
		"ps-partial", "image_gallery", "material_image",
		"collect_materials", 4, "list", &idx)
	if err != nil {
		t.Fatalf("partial retry: %v", err)
	}
	if r.ListIndex == nil || *r.ListIndex != 1 {
		t.Fatalf("expected list_index=1, got %v", r.ListIndex)
	}
	if !r.Selected {
		t.Fatal("new revision must be selected=true")
	}

	// After partial retry: indices 0 and 2 still selected; index 1 has a new selected row.
	selected, err := LoadSelectedSlots(ctx, db.DB, "ps-partial")
	if err != nil {
		t.Fatalf("LoadSelectedSlots: %v", err)
	}
	// We expect 3 selected rows: index 0, index 1 (new), index 2.
	if len(selected) != 3 {
		t.Fatalf("expected 3 selected rows after partial retry, got %d", len(selected))
	}
	// The selected row for index 1 must be the new one (revision 4).
	var idx1Rev int
	for _, s := range selected {
		if s.ListIndex != nil && *s.ListIndex == 1 {
			idx1Rev = s.Revision
		}
	}
	if idx1Rev != 4 {
		t.Fatalf("expected index-1 revision to be 4 (new), got %d", idx1Rev)
	}
}

// ──────────────────────────────────────────────
// LoadSelectedSlots — empty session
// ──────────────────────────────────────────────

func TestLoadSelectedSlots_EmptySession(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-empty", ConversationID: "conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}

	slots, err := LoadSelectedSlots(ctx, db.DB, "ps-empty")
	if err != nil {
		t.Fatalf("LoadSelectedSlots: %v", err)
	}
	if len(slots) != 0 {
		t.Fatalf("expected empty, got %d rows", len(slots))
	}
}
