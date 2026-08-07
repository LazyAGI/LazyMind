package resourceupdate

import (
	"context"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

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
