package evolution

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

var ApplyRemoveSuggestion func(ctx context.Context, db *gorm.DB, suggestion orm.ResourceSuggestion) error

const (
	ResourceTypeSkill          = "skill"
	ResourceTypeMemory         = "memory"
	ResourceTypeUserPreference = "user_preference"

	SkillNodeTypeParent = "parent"
	SkillNodeTypeChild  = "child"

	SuggestionActionCreate = "create"
	SuggestionActionModify = "modify"
	SuggestionActionRemove = "remove"

	SuggestionStatusPendingReview = "pending_review"
	SuggestionStatusAccepted      = "accepted"
	SuggestionStatusRejected      = "rejected"
	SuggestionStatusInvalid       = "invalid"
	SuggestionStatusApplied       = "applied"
	SuggestionStatusDiscarded     = "discarded"
	SuggestionStatusNone          = "none"

	UpdateStatusUpToDate = "up_to_date"

	AutoEvoApplyStatusIdle    = "idle"
	AutoEvoApplyStatusRunning = "running"
	AutoEvoApplyStatusFailed  = "failed"
)

type ChatResourceContext struct {
	DisabledTools      []string
	AvailableSkills    []string
	Memory             string
	UserPreference     string
	UsePersonalization bool
}

type SuggestionPayload struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Reason  string `json:"reason,omitempty"`
}

type RecordedSuggestion struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	InvalidReason string `json:"invalid_reason,omitempty"`
}

func AcceptedSuggestionStatuses() []string {
	return []string{SuggestionStatusAccepted}
}

func VisibleSuggestionStatuses() []string {
	return []string{
		SuggestionStatusPendingReview,
		SuggestionStatusAccepted,
	}
}

func CanonicalSuggestionStatus(status string) string {
	switch status {
	case SuggestionStatusPendingReview:
		return SuggestionStatusPendingReview
	case SuggestionStatusAccepted:
		return SuggestionStatusAccepted
	default:
		return SuggestionStatusNone
	}
}

func MergeSuggestionStatus(current, candidate string) string {
	current = CanonicalSuggestionStatus(current)
	candidate = CanonicalSuggestionStatus(candidate)
	if current == SuggestionStatusPendingReview || candidate == SuggestionStatusPendingReview {
		return SuggestionStatusPendingReview
	}
	if current == SuggestionStatusAccepted || candidate == SuggestionStatusAccepted {
		return SuggestionStatusAccepted
	}
	return SuggestionStatusNone
}

func NormalizeAutoEvoApplyStatus(status string) string {
	switch strings.TrimSpace(status) {
	case AutoEvoApplyStatusRunning:
		return AutoEvoApplyStatusRunning
	case AutoEvoApplyStatusFailed:
		return AutoEvoApplyStatusFailed
	default:
		return AutoEvoApplyStatusIdle
	}
}
