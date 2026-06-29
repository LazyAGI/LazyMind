package resourceupdate

import (
	"errors"

	"github.com/rs/zerolog"

	appLog "lazymind/core/log"
)

const (
	logEventAutoApplyApplied           = "resource_update.auto_apply.applied"
	logEventAutoApplySkipped           = "resource_update.auto_apply.skipped"
	logEventAutoApplyTaskBlocked       = "resource_update.auto_apply.task_blocked"
	logEventAutoApplyTaskCreated       = "resource_update.auto_apply.task_created"
	logEventAutoEvoScanDone            = "resource_update.auto_evo_enabled.scan_done"
	logEventAutoEvoScanStart           = "resource_update.auto_evo_enabled.scan_start"
	logEventResultExpired              = "resource_update.result.expired"
	logEventResultScanDone             = "resource_update.result_scan.done"
	logEventResultScanSkipped          = "resource_update.result_scan.skipped"
	logEventIdleEventFailed            = "resource_update.idle.event_failed"
	logEventIdleEventRecorded          = "resource_update.idle.event_recorded"
	logEventIdleEventSkipped           = "resource_update.idle.event_skipped"
	logEventIdleEventTriggered         = "resource_update.idle.event_triggered"
	logEventIdleStateCleanupFailed     = "resource_update.idle.state_cleanup_failed"
	logEventIdleFallbackFailed         = "resource_update.idle.fallback_failed"
	logEventIdleExpiredKeyNotifyFailed = "resource_update.idle.expired_key_notify_failed"
	logEventMemoryReviewCallDone       = "resource_update.memory_review.call_done"
	logEventMemoryReviewCallFailed     = "resource_update.memory_review.call_failed"
	logEventMemoryReviewCallStart      = "resource_update.memory_review.call_start"
	logEventMemoryReviewSkipped        = "resource_update.memory_review.call_skipped"
	logEventReviewAccepted             = "resource_update.review.accepted"
	logEventReviewRejected             = "resource_update.review.rejected"
	logEventRuntimeStarted             = "resource_update.runtime.started"
	logEventSchedulerActive            = "resource_update.scheduler.active_task_blocked"
	logEventSchedulerMinInterval       = "resource_update.scheduler.min_interval_blocked"
	logEventSchedulerNotDue            = "resource_update.scheduler.not_due"
	logEventSchedulerSeeded            = "resource_update.scheduler.states_seeded"
	logEventSchedulerSkipped           = "resource_update.scheduler.state_skipped"
	logEventSchedulerSettled           = "resource_update.scheduler.active_task_settled"
	logEventSchedulerTaskCreated       = "resource_update.scheduler.task_created"
	logEventSchedulerTickFailed        = "resource_update.scheduler.tick_failed"
	logEventScannerTickFailed          = "resource_update.scanner.tick_failed"
	logEventSkillReviewCallFailed      = "resource_update.skill_review.call_failed"
	logEventSkillReviewAccepted        = "resource_update.skill_review.call_accepted"
	logEventSkillReviewCallStart       = "resource_update.skill_review.call_start"
	logEventSkillReviewFrozen          = "resource_update.skill_review.window_frozen"
	logEventSkillReviewPreflight       = "resource_update.skill_review.preflight_skipped"
	logEventSkillReviewReused          = "resource_update.skill_review.window_reused"
	logEventWorkerClaimed              = "resource_update.worker.tasks_claimed"
	logEventWorkerFinished             = "resource_update.worker.task_finished"
	logEventWorkerRecovered            = "resource_update.worker.tasks_recovered"
	logEventWorkerTickFailed           = "resource_update.worker.tick_failed"
)

func resourceUpdateInfo(event string) *zerolog.Event {
	return appLog.Logger.Info().
		Str("component", "resource_update").
		Str("event", event)
}

func resourceUpdateWarn(event string, err error) *zerolog.Event {
	logEvent := appLog.Logger.Warn().
		Str("component", "resource_update").
		Str("event", event)
	if err != nil {
		logEvent = logEvent.Err(err)
	}
	return logEvent
}

func reviewSkipReason(err error) string {
	switch {
	case errors.Is(err, errReviewConflict):
		return "review_conflict"
	case errors.Is(err, errReviewNotFound):
		return "review_not_found"
	case errors.Is(err, errReviewInvalid):
		return "review_invalid"
	default:
		return "review_skipped"
	}
}
