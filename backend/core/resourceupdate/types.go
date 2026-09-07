package resourceupdate

import (
	"context"
	"encoding/json"
	"time"

	"lazymind/core/algo"
)

type HistoryStats struct {
	UserTurnCount         int      `gorm:"column:user_turn_count"`
	ToolCallCount         int      `gorm:"column:tool_call_count"`
	QualifiedSessionCount int      `gorm:"column:qualified_session_count"`
	QualifiedSessionIDs   []string `gorm:"-"`
	QuantityThreshold     int      `gorm:"-"`
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
	RequestID                      string   `json:"requestid"`
	UserID                         string   `json:"user_id"`
	TriggerReason                  string   `json:"trigger_reason,omitempty"`
	CandidateUserTurnCount         int      `json:"candidate_user_turn_count,omitempty"`
	CandidateToolCallCount         int      `json:"candidate_tool_call_count,omitempty"`
	CandidateQualifiedSessionCount int      `json:"candidate_qualified_session_count,omitempty"`
	QuantityThreshold              int      `json:"quantity_threshold,omitempty"`
	SchedulerPreflightAt           string   `json:"scheduler_preflight_at,omitempty"`
	StartTime                      string   `json:"start_time,omitempty"`
	EndTime                        string   `json:"end_time,omitempty"`
	UserTurnCount                  int      `json:"user_turn_count,omitempty"`
	ToolCallCount                  int      `json:"tool_call_count,omitempty"`
	QualifiedSessionCount          int      `json:"qualified_session_count,omitempty"`
	StartPreflightAt               string   `json:"start_preflight_at,omitempty"`
	StartTriggerReason             string   `json:"start_trigger_reason,omitempty"`
	SessionIDs                     []string `json:"session_ids,omitempty"`
	WindowFrozen                   bool     `json:"window_frozen"`
}

type memoryReviewRequestJSON struct {
	ConversationID             string          `json:"conversation_id"`
	ConversationLastActiveAtMS int64           `json:"conversation_last_active_at_ms"`
	History                    json.RawMessage `json:"history,omitempty"`
}

type skillDraftAutoCommitRequestJSON struct {
	TaskID       string `json:"task_id"`
	DraftVersion int64  `json:"draft_version"`
}

type taskOutcome struct {
	Status       string
	ResultID     string
	ResultJSON   json.RawMessage
	ErrorCode    string
	ErrorMessage string
	Permanent    bool
	Deferred     bool
	RetryAfter   time.Duration
}

type reviewCallers struct {
	Skill               func(context.Context, algo.SkillReviewRequest) (*algo.SkillReviewResponse, int, error)
	Memory              func(context.Context, algo.MemoryReviewRequest) (*algo.MemoryReviewResponse, int, error)
	PreferenceOrganizer func(context.Context, algo.PreferenceOrganizerRequest) (*algo.PreferenceOrganizerResponse, int, error)
}

type clockFunc func() time.Time
