package resourceupdate

import (
	"context"
	"encoding/json"
	"time"

	"lazymind/core/algo"
)

type HistoryStats struct {
	UserTurnCount         int `gorm:"column:user_turn_count"`
	ToolCallCount         int `gorm:"column:tool_call_count"`
	QualifiedSessionCount int `gorm:"column:qualified_session_count"`
	QuantityThreshold     int `gorm:"-"`
}

type SchedulerTickResult struct {
	SeededStates  int
	ClaimedStates int
	CreatedTasks  int
	SkippedStates int
}

type WorkerRunResult struct {
	Recovered int
	Claimed   int
	Done      int
	Skipped   int
	Retried   int
	Failed    int
}

type skillGenerateRequestJSON struct {
	RequestID                      string `json:"requestid"`
	UserID                         string `json:"user_id"`
	TriggerReason                  string `json:"trigger_reason,omitempty"`
	CandidateUserTurnCount         int    `json:"candidate_user_turn_count,omitempty"`
	CandidateToolCallCount         int    `json:"candidate_tool_call_count,omitempty"`
	CandidateQualifiedSessionCount int    `json:"candidate_qualified_session_count,omitempty"`
	QuantityThreshold              int    `json:"quantity_threshold,omitempty"`
	SchedulerPreflightAt           string `json:"scheduler_preflight_at,omitempty"`
	StartTime                      string `json:"start_time,omitempty"`
	EndTime                        string `json:"end_time,omitempty"`
	UserTurnCount                  int    `json:"user_turn_count,omitempty"`
	ToolCallCount                  int    `json:"tool_call_count,omitempty"`
	QualifiedSessionCount          int    `json:"qualified_session_count,omitempty"`
	StartPreflightAt               string `json:"start_preflight_at,omitempty"`
	StartTriggerReason             string `json:"start_trigger_reason,omitempty"`
	WindowFrozen                   bool   `json:"window_frozen"`
}

type memoryGenerateRequestJSON struct {
	SessionID      string          `json:"session_id"`
	History        json.RawMessage `json:"history,omitempty"`
	Memory         string          `json:"memory,omitempty"`
	User           string          `json:"user,omitempty"`
	Target         string          `json:"target,omitempty"`
	CurrentContent string          `json:"current_content,omitempty"`
}

type taskOutcome struct {
	Status       string
	ResultID     string
	ErrorCode    string
	ErrorMessage string
	Permanent    bool
}

type reviewCallers struct {
	Skill  func(context.Context, algo.SkillReviewRequest) (*algo.SkillReviewResponse, int, error)
	Memory func(context.Context, algo.MemoryReviewRequest) (*algo.MemoryReviewResponse, int, error)
}

type clockFunc func() time.Time
