package resourceupdate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestOrganizeErrorCategorySurvivesTaskAPIProjection(t *testing.T) {
	var response skillReviewTaskStatusResponse
	applySkillOrganizeDetails(&response, orm.ResourceUpdateTask{TaskType: orm.ResourceUpdateTaskTypeOrganizeSkill}, `{"error":"provider offline","error_code":"skill_organize_model_transport","error_category":"model_transport","failed_stage":"organize_plan"}`)
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["error_code"] != "skill_organize_model_transport" || payload["error_category"] != "model_transport" {
		t.Fatalf("category lost: %s", encoded)
	}
}

func TestCancelledOrganizeTaskOverridesLateStats(t *testing.T) {
	response := skillReviewTaskStatusResponse{Status: "failed", RunStatus: "failed"}
	applySkillOrganizeDetails(&response, orm.ResourceUpdateTask{TaskType: orm.ResourceUpdateTaskTypeOrganizeSkill, Status: orm.ResourceUpdateTaskStatusSkipped, ErrorCode: "skill_organize_cancelled"}, `{"error_code":"skill_organize_model_transport","error_category":"model_transport"}`)
	if response.Status != "cancelled" || response.ErrorCode != "skill_organize_cancelled" {
		t.Fatalf("late result overrode cancellation: %#v", response)
	}
}

func TestCancelledOrganizePendingReviewExposedWithAndWithoutStats(t *testing.T) {
	for _, summary := range []string{"", `{"pending_review":true,"cancellation_details":{"pending_review":true,"rollback_status":"incomplete"}}`} {
		var response skillReviewTaskStatusResponse
		applySkillOrganizeDetails(&response, orm.ResourceUpdateTask{TaskType: orm.ResourceUpdateTaskTypeOrganizeSkill, ErrorCode: "skill_organize_cancelled", ResultJSON: json.RawMessage(`{"cancellation_details":{"pending_review":true,"rollback_status":"incomplete"}}`)}, summary)
		if !response.PendingReview || response.CancellationDetails["rollback_status"] != "incomplete" || response.Status != "cancelled" {
			t.Fatalf("warning lost: %#v", response)
		}
	}
}

func TestOrganizeCancellationStatsDominateDifferentRunIDs(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	insertSkillReviewStats(t, db, map[string]any{"id": "cancelled", "requestid": "org_req", "userid": "u", "status": "cancelled", "started_at": "2026-09-29", "summary": map[string]any{"error_code": "skill_organize_cancelled"}})
	insertSkillReviewStats(t, db, map[string]any{"id": "late", "requestid": "org_req", "userid": "u", "status": "organize_plan", "started_at": "2026-09-30", "summary": map[string]any{}})
	for _, resultID := range []string{"", "late"} {
		row, found, err := findSkillReviewTaskStats(context.Background(), db, "u", orm.ResourceUpdateTask{TaskType: orm.ResourceUpdateTaskTypeOrganizeSkill, ResultID: resultID}, "org_req")
		if err != nil || !found || row.Status != "cancelled" {
			t.Fatalf("cancelled request revived for result %q: %#v %v", resultID, row, err)
		}
	}
}

// TestTaskToResponse maps all ORM task fields to the response DTO.
func TestTaskToResponse(t *testing.T) {
	now := time.Now().UTC()
	started := now.Add(-time.Hour)
	finished := now.Add(-30 * time.Minute)
	task := orm.ResourceUpdateTask{
		ID:             "task-1",
		TaskType:       orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType:   orm.ResourceUpdateResourceTypeSkill,
		UserID:         "user-1",
		ResourceID:     "res-1",
		TriggerType:    orm.ResourceUpdateTriggerTypeScheduled,
		TriggerID:      "trigger-1",
		Status:         orm.ResourceUpdateTaskStatusDone,
		ReviewResultID: "review-1",
		ResultID:       "result-1",
		ErrorCode:      "",
		ErrorMessage:   "",
		AttemptCount:   2,
		NextRunAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
		StartedAt:      &started,
		FinishedAt:     &finished,
	}
	resp := taskToResponse(task)
	if resp.ID != "task-1" {
		t.Fatalf("ID = %q", resp.ID)
	}
	if resp.Status != orm.ResourceUpdateTaskStatusDone {
		t.Fatalf("status = %q", resp.Status)
	}
	if resp.AttemptCount != 2 {
		t.Fatalf("attempts = %d", resp.AttemptCount)
	}
	if resp.StartedAt == nil || !resp.StartedAt.Equal(started) {
		t.Fatal("started_at mismatch")
	}
	if resp.FinishedAt == nil || !resp.FinishedAt.Equal(finished) {
		t.Fatal("finished_at mismatch")
	}
	if resp.ResultID != "result-1" {
		t.Fatalf("result_id = %q", resp.ResultID)
	}
}

// TestTaskToResponseEmptyResultID keeps empty ResultID as-is.
func TestTaskToResponseResultIDFallback(t *testing.T) {
	task := orm.ResourceUpdateTask{
		ID:             "task-2",
		ReviewResultID: "review-only",
		ResultID:       "",
	}
	resp := taskToResponse(task)
	if resp.ResultID != "" {
		t.Fatalf("result_id = %q, want empty", resp.ResultID)
	}
}

// TestSkillResultToResponse maps SkillReviewResult to response DTO.
func TestSkillResultToResponse(t *testing.T) {
	now := time.Now()
	row := SkillReviewResult{
		ID:           "skill-1",
		SkillName:    "test-skill",
		Type:         skillReviewTypePatch,
		ReviewStatus: reviewStatusPending,
		UserID:       "user-1",
		RequestID:    "req-1",
		SkillContent: "---\nname: test\ndescription: desc\n---\n\nbody\n",
		Summary:      "summary",
		Time:         now,
	}
	resp := skillResultToResponse(row)
	if resp.ID != "skill-1" {
		t.Fatalf("ID = %q", resp.ID)
	}
	if resp.SkillName != "test-skill" {
		t.Fatalf("skill_name = %q", resp.SkillName)
	}
	if resp.Type != skillReviewTypePatch {
		t.Fatalf("type = %q", resp.Type)
	}
}

// TestReviewSingleFileFS_ListAll returns SKILL.md entry when file exists.
func TestReviewSingleFileFS_ListAll(t *testing.T) {
	fs := reviewSingleFileFS{content: "# My Skill", exists: true}
	entries, err := fs.ListAll(context.TODO())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Path != "SKILL.md" {
		t.Fatalf("path = %q, want SKILL.md", entries[0].Path)
	}
	if entries[0].Type != "file" {
		t.Fatalf("type = %q, want file", entries[0].Type)
	}
}

// TestReviewSingleFileFS_ListAllEmpty returns nil when file does not exist.
func TestReviewSingleFileFS_ListAllEmpty(t *testing.T) {
	fs := reviewSingleFileFS{content: "", exists: false}
	entries, err := fs.ListAll(context.TODO())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0", len(entries))
	}
}

// TestReviewSingleFileFS_ReadFile returns content bytes.
func TestReviewSingleFileFS_ReadFile(t *testing.T) {
	fs := reviewSingleFileFS{content: "test content"}
	data, err := fs.ReadFile(context.TODO(), "SKILL.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "test content" {
		t.Fatalf("got %q, want test content", string(data))
	}
}
