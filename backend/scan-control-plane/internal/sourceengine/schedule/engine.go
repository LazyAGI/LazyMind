package schedule

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // embed IANA zones for minimal runtime images

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const (
	SyncModeManual    = "manual"
	SyncModeScheduled = "scheduled"
	SyncModeWatch     = "watch"

	TriggerTypeManual    = "manual"
	TriggerTypeScheduled = "scheduled"
	TriggerTypeWatch     = "watch"
	TriggerTypeReconcile = "reconcile"

	watchReconcileInterval = 10 * time.Minute
	scheduleLookaheadDays  = 8
)

type schedulePolicy struct {
	Timezone string         `json:"timezone"`
	Calendar string         `json:"calendar"`
	Rules    []scheduleRule `json:"rules"`
	location *time.Location
}

type scheduleRule struct {
	Days []string `json:"days"`
	Time string   `json:"time"`

	hour   int
	minute int
	second int
}

type Store interface {
	GetBinding(ctx context.Context, sourceID, bindingID string) (store.Binding, error)
	GetSyncRun(ctx context.Context, runID string) (store.SyncRun, error)
	GetSyncCheckpoint(ctx context.Context, bindingID string) (store.SyncCheckpoint, error)
	ListDueSyncCheckpoints(ctx context.Context, now time.Time, limit int) ([]store.SyncCheckpoint, error)
	FinishSyncRun(ctx context.Context, runID, workerID string, finish store.SyncRunFinish) (store.SyncRun, bool, error)
}

type SyncRunQueue interface {
	EnqueueSyncRun(ctx context.Context, run store.SyncRun) (store.SyncRun, bool, error)
}

type CheckpointScheduleEngine struct {
	store        Store
	queue        SyncRunQueue
	tasks        PendingTaskPlanner
	clock        func() time.Time
	newID        func(prefix string) string
	retryBackoff func(retryCount int64) time.Duration
}

type Option func(*CheckpointScheduleEngine)

type PendingTaskPlanner interface {
	GeneratePendingTasks(ctx context.Context, sourceID, bindingID, runID string) error
}

func NewCheckpointScheduleEngine(store Store, queue SyncRunQueue, options ...Option) *CheckpointScheduleEngine {
	if store == nil {
		panic("schedule store is required")
	}
	if queue == nil {
		panic("schedule queue is required")
	}
	e := &CheckpointScheduleEngine{
		store: store,
		queue: queue,
		clock: time.Now,
		newID: func(prefix string) string {
			return prefix + "-" + time.Now().UTC().Format("20060102150405.000000000")
		},
		retryBackoff: func(retryCount int64) time.Duration {
			if retryCount <= 0 {
				retryCount = 1
			}
			delay := time.Duration(retryCount) * time.Minute
			if delay > time.Hour {
				return time.Hour
			}
			return delay
		},
	}
	for _, option := range options {
		option(e)
	}
	return e
}

func WithClock(clock func() time.Time) Option {
	return func(e *CheckpointScheduleEngine) {
		if clock != nil {
			e.clock = clock
		}
	}
}

func WithIDGenerator(newID func(prefix string) string) Option {
	return func(e *CheckpointScheduleEngine) {
		if newID != nil {
			e.newID = newID
		}
	}
}

func WithRetryBackoff(backoff func(retryCount int64) time.Duration) Option {
	return func(e *CheckpointScheduleEngine) {
		if backoff != nil {
			e.retryBackoff = backoff
		}
	}
}

func WithTaskPlanner(planner PendingTaskPlanner) Option {
	return func(e *CheckpointScheduleEngine) {
		e.tasks = planner
	}
}

func (e *CheckpointScheduleEngine) BuildCheckpoint(_ context.Context, binding store.Binding, now time.Time) (store.SyncCheckpoint, error) {
	next, err := nextSyncAt(binding, now)
	if err != nil {
		return store.SyncCheckpoint{}, err
	}
	return store.SyncCheckpoint{
		SourceID:          binding.SourceID,
		BindingID:         binding.BindingID,
		BindingGeneration: binding.BindingGeneration,
		NextSyncAt:        next,
		LastError:         store.JSON{},
		CreatedAt:         now,
		UpdatedAt:         now,
	}, nil
}

func (e *CheckpointScheduleEngine) TriggerInitialSync(ctx context.Context, binding store.Binding) ([]string, error) {
	if binding.SyncMode == SyncModeWatch {
		return nil, nil
	}
	intent, err := e.enqueueBindingRun(ctx, binding, TriggerTypeManual, connector.ScopeTypeFull, nil, "", e.clock().UTC(), nil)
	if err != nil || intent.Run.RunID == "" {
		return nil, err
	}
	return []string{intent.Run.RunID}, nil
}

type ManualSyncRequest struct {
	RequestID string
	SourceID  string
	BindingID string
	ScopeType connector.ScopeType
	ScopeRef  connector.ScopeRef
}

type SyncRunIntent struct {
	Run     store.SyncRun
	Created bool
}

type WatchEventSyncRequest struct {
	Binding    store.Binding
	ObjectKey  string
	Path       string
	EventType  string
	OccurredAt time.Time
	IsDir      bool
}

func (e *CheckpointScheduleEngine) EnqueueWatchEventSync(ctx context.Context, req WatchEventSyncRequest) (SyncRunIntent, error) {
	if req.Binding.SyncMode != SyncModeManual && req.Binding.SyncMode != SyncModeScheduled && req.Binding.SyncMode != SyncModeWatch {
		return SyncRunIntent{}, fmt.Errorf("binding %s sync mode does not support file events", req.Binding.BindingID)
	}
	occurredAt := req.OccurredAt.UTC()
	now := e.clock().UTC()
	if occurredAt.IsZero() {
		occurredAt = now
	}
	runAt := occurredAt
	if runAt.After(now) {
		runAt = now
	}
	scopeRef := connector.ScopeRef{
		"event_type":  req.EventType,
		"occurred_at": occurredAt.Format(time.RFC3339Nano),
	}
	if req.ObjectKey != "" {
		scopeRef["object_key"] = req.ObjectKey
	}
	if req.Path != "" {
		scopeRef["path"] = req.Path
	}
	if req.IsDir {
		scopeRef["is_dir"] = "true"
	}
	return e.enqueueBindingRun(ctx, req.Binding, TriggerTypeWatch, connector.ScopeTypeWatchEvent, scopeRef, "", runAt, &occurredAt)
}

func (e *CheckpointScheduleEngine) EnqueueManualSync(ctx context.Context, req ManualSyncRequest) (SyncRunIntent, error) {
	binding, err := e.store.GetBinding(ctx, req.SourceID, req.BindingID)
	if err != nil {
		return SyncRunIntent{}, err
	}
	scopeType := req.ScopeType
	if scopeType == "" {
		scopeType = connector.ScopeTypeFull
	}
	return e.enqueueBindingRun(ctx, binding, TriggerTypeManual, scopeType, req.ScopeRef, req.RequestID, e.clock().UTC(), nil)
}

func (e *CheckpointScheduleEngine) EnqueueDueSyncRuns(ctx context.Context, limit int) ([]SyncRunIntent, error) {
	now := e.clock().UTC()
	checkpoints, err := e.store.ListDueSyncCheckpoints(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	intents := make([]SyncRunIntent, 0, len(checkpoints))
	for _, checkpoint := range checkpoints {
		binding, err := e.store.GetBinding(ctx, checkpoint.SourceID, checkpoint.BindingID)
		if err != nil {
			return intents, err
		}
		trigger := TriggerTypeScheduled
		if binding.SyncMode == SyncModeWatch {
			trigger = TriggerTypeReconcile
		}
		intent, err := e.enqueueBindingRun(ctx, binding, trigger, connector.ScopeTypeFull, nil, "", now, scheduledFireAt(trigger, checkpoint.NextSyncAt))
		if err != nil {
			return intents, err
		}
		intents = append(intents, intent)
	}
	return intents, nil
}

type FinishRunRequest struct {
	RunID          string
	WorkerID       string
	Status         string
	Cursor         string
	Coverage       store.JSON
	SeenCount      int64
	NewCount       int64
	ModifiedCount  int64
	DeletedCount   int64
	UnchangedCount int64
	ErrorCode      string
	ErrorMessage   string
	NextSyncAt     *time.Time
}

func (e *CheckpointScheduleEngine) FinishRun(ctx context.Context, req FinishRunRequest) (store.SyncRun, bool, error) {
	run, err := e.store.GetSyncRun(ctx, req.RunID)
	if err != nil {
		return store.SyncRun{}, false, err
	}
	binding, err := e.store.GetBinding(ctx, run.SourceID, run.BindingID)
	if err != nil {
		return store.SyncRun{}, false, err
	}
	checkpoint, err := e.store.GetSyncCheckpoint(ctx, run.BindingID)
	if err != nil {
		return store.SyncRun{}, false, err
	}
	finish, err := e.buildFinish(binding, checkpoint, req)
	if err != nil {
		return store.SyncRun{}, false, err
	}
	finished, ok, err := e.store.FinishSyncRun(ctx, req.RunID, req.WorkerID, finish)
	if err != nil || !ok || finished.Status != store.SyncRunStatusSucceeded || e.tasks == nil || !shouldGeneratePendingTasks(finished, binding) {
		return finished, ok, err
	}
	err = e.tasks.GeneratePendingTasks(ctx, finished.SourceID, finished.BindingID, finished.RunID)
	return finished, ok, err
}

func shouldGeneratePendingTasks(run store.SyncRun, binding store.Binding) bool {
	if run.TriggerType == TriggerTypeWatch && run.ScopeType == string(connector.ScopeTypeWatchEvent) {
		return binding.SyncMode == SyncModeWatch
	}
	return true
}

func (e *CheckpointScheduleEngine) enqueueBindingRun(ctx context.Context, binding store.Binding, trigger string, scopeType connector.ScopeType, scopeRef connector.ScopeRef, requestID string, runAt time.Time, scheduledAt *time.Time) (SyncRunIntent, error) {
	run := store.SyncRun{
		RunID:             e.syncRunID(binding, requestID),
		SourceID:          binding.SourceID,
		BindingID:         binding.BindingID,
		BindingGeneration: binding.BindingGeneration,
		TriggerType:       trigger,
		ScheduledFireAt:   scheduledAt,
		ScopeType:         string(scopeType),
		ScopeRef:          scopeJSON(scopeRef),
		Coverage:          store.JSON{},
		Status:            store.SyncRunStatusPending,
		StartedAt:         runAt,
	}
	if requestID != "" {
		if persisted, err := e.store.GetSyncRun(ctx, run.RunID); err == nil {
			return SyncRunIntent{Run: persisted, Created: false}, nil
		} else if store.ErrorCodeOf(err) != store.ErrCodeNotFound {
			return SyncRunIntent{}, err
		}
	}
	persisted, created, err := e.queue.EnqueueSyncRun(ctx, run)
	return SyncRunIntent{Run: persisted, Created: created}, err
}

func (e *CheckpointScheduleEngine) syncRunID(binding store.Binding, requestID string) string {
	if requestID == "" {
		return e.newID("sync-run")
	}
	sum := sha256.Sum256([]byte(binding.SourceID + "\x00" + binding.BindingID + "\x00" + fmt.Sprint(binding.BindingGeneration) + "\x00" + requestID))
	return "sync-run-" + hex.EncodeToString(sum[:12])
}

func (e *CheckpointScheduleEngine) buildFinish(binding store.Binding, checkpoint store.SyncCheckpoint, req FinishRunRequest) (store.SyncRunFinish, error) {
	now := e.clock().UTC()
	status := req.Status
	if status == "" {
		status = store.SyncRunStatusSucceeded
	}
	next := req.NextSyncAt
	if status == store.SyncRunStatusSucceeded && next == nil {
		var err error
		next, err = nextSyncAt(binding, now)
		if err != nil {
			return store.SyncRunFinish{}, err
		}
	}
	if status != store.SyncRunStatusSucceeded && next == nil {
		retryAt := now.Add(e.retryBackoff(checkpoint.RetryCount + 1))
		next = &retryAt
	}
	return store.SyncRunFinish{
		Status:         status,
		Cursor:         req.Cursor,
		NextSyncAt:     next,
		Coverage:       req.Coverage,
		SeenCount:      req.SeenCount,
		NewCount:       req.NewCount,
		ModifiedCount:  req.ModifiedCount,
		DeletedCount:   req.DeletedCount,
		UnchangedCount: req.UnchangedCount,
		ErrorCode:      req.ErrorCode,
		ErrorMessage:   req.ErrorMessage,
		FinishedAt:     now,
	}, nil
}

func nextSyncAt(binding store.Binding, now time.Time) (*time.Time, error) {
	if binding.SyncMode == SyncModeWatch {
		if binding.NextSyncAt != nil && binding.NextSyncAt.After(now) {
			return binding.NextSyncAt, nil
		}
		next := now.Add(watchReconcileInterval)
		return &next, nil
	}
	if binding.SyncMode != SyncModeScheduled {
		return nil, nil
	}
	next, err := NextSyncAt(binding.SchedulePolicy, now)
	if err != nil {
		return nil, err
	}
	return &next, nil
}

func ValidateSchedulePolicy(policy store.JSON) error {
	_, err := parseSchedulePolicy(policy)
	return err
}

func NextSyncAt(policyJSON store.JSON, now time.Time) (time.Time, error) {
	policy, err := parseSchedulePolicy(policyJSON)
	if err != nil {
		return time.Time{}, err
	}
	localNow := now.In(policy.location)
	seen := map[int64]time.Time{}
	var earliest time.Time
	for offset := 0; offset < scheduleLookaheadDays; offset++ {
		day := localNow.AddDate(0, 0, offset)
		for _, rule := range policy.Rules {
			if !ruleMatchesDay(rule, day) {
				continue
			}
			candidate := time.Date(day.Year(), day.Month(), day.Day(), rule.hour, rule.minute, rule.second, 0, policy.location)
			if !candidate.After(localNow) {
				continue
			}
			candidateUTC := candidate.UTC()
			key := candidateUTC.UnixNano()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = candidateUTC
			if earliest.IsZero() || candidateUTC.Before(earliest) {
				earliest = candidateUTC
			}
		}
	}
	if earliest.IsZero() {
		return time.Time{}, fmt.Errorf("schedule policy produced no next sync time within %d days", scheduleLookaheadDays)
	}
	return earliest, nil
}

func parseSchedulePolicy(policyJSON store.JSON) (schedulePolicy, error) {
	if len(policyJSON) == 0 {
		return schedulePolicy{}, fmt.Errorf("schedule_policy is required")
	}
	body, err := json.Marshal(policyJSON)
	if err != nil {
		return schedulePolicy{}, fmt.Errorf("schedule_policy must be valid JSON")
	}
	var policy schedulePolicy
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return schedulePolicy{}, fmt.Errorf("schedule_policy is invalid")
	}
	policy.Timezone = strings.TrimSpace(policy.Timezone)
	if policy.Timezone == "" {
		return schedulePolicy{}, fmt.Errorf("timezone is required")
	}
	loc, err := time.LoadLocation(policy.Timezone)
	if err != nil {
		return schedulePolicy{}, fmt.Errorf("unsupported schedule timezone %q", policy.Timezone)
	}
	policy.location = loc
	policy.Calendar = strings.ToLower(strings.TrimSpace(policy.Calendar))
	if policy.Calendar != "weekly" {
		return schedulePolicy{}, fmt.Errorf("calendar must be weekly")
	}
	if len(policy.Rules) == 0 {
		return schedulePolicy{}, fmt.Errorf("rules must not be empty")
	}
	for i := range policy.Rules {
		rule, err := normalizeScheduleRule(policy.Rules[i])
		if err != nil {
			return schedulePolicy{}, fmt.Errorf("rules[%d].%s", i, err.Error())
		}
		policy.Rules[i] = rule
	}
	return policy, nil
}

func normalizeScheduleRule(rule scheduleRule) (scheduleRule, error) {
	hour, minute, second, err := parsePolicyTime(rule.Time)
	if err != nil {
		return scheduleRule{}, fmt.Errorf("time %s", err.Error())
	}
	if len(rule.Days) == 0 {
		return scheduleRule{}, fmt.Errorf("days must not be empty")
	}
	days := make([]string, 0, len(rule.Days))
	for _, day := range rule.Days {
		normalized := strings.ToLower(strings.TrimSpace(day))
		if !validScheduleDay(normalized) {
			return scheduleRule{}, fmt.Errorf("days contains unsupported value %q", day)
		}
		days = append(days, normalized)
	}
	rule.Days = days
	rule.hour = hour
	rule.minute = minute
	rule.second = second
	return rule, nil
}

func parsePolicyTime(token string) (int, int, int, error) {
	parts := strings.Split(token, ":")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("must use HH:mm:ss")
	}
	hour, err := parseFixedRange(parts[0], 0, 23)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("must use HH:mm:ss")
	}
	minute, err := parseFixedRange(parts[1], 0, 59)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("must use HH:mm:ss")
	}
	second, err := parseFixedRange(parts[2], 0, 59)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("must use HH:mm:ss")
	}
	return hour, minute, second, nil
}

func validScheduleDay(day string) bool {
	switch day {
	case "everyday", "workday", "non_workday", "mon", "tue", "wed", "thu", "fri", "sat", "sun":
		return true
	default:
		return false
	}
}

func ruleMatchesDay(rule scheduleRule, day time.Time) bool {
	for _, selector := range rule.Days {
		if daySelectorMatches(selector, day.Weekday()) {
			return true
		}
	}
	return false
}

func daySelectorMatches(selector string, weekday time.Weekday) bool {
	switch selector {
	case "everyday":
		return true
	case "workday":
		return weekday >= time.Monday && weekday <= time.Friday
	case "non_workday":
		return weekday == time.Saturday || weekday == time.Sunday
	case "mon":
		return weekday == time.Monday
	case "tue":
		return weekday == time.Tuesday
	case "wed":
		return weekday == time.Wednesday
	case "thu":
		return weekday == time.Thursday
	case "fri":
		return weekday == time.Friday
	case "sat":
		return weekday == time.Saturday
	case "sun":
		return weekday == time.Sunday
	default:
		return false
	}
}

func parseFixedRange(token string, min, max int) (int, error) {
	if len(token) != 2 {
		return 0, fmt.Errorf("invalid time token")
	}
	value, err := strconv.Atoi(token)
	if err != nil || value < min || value > max {
		return 0, fmt.Errorf("invalid time token")
	}
	return value, nil
}

func scheduledFireAt(trigger string, nextSyncAt *time.Time) *time.Time {
	if trigger != TriggerTypeScheduled || nextSyncAt == nil {
		return nil
	}
	fireAt := nextSyncAt.UTC()
	return &fireAt
}

func scopeJSON(scopeRef connector.ScopeRef) store.JSON {
	if len(scopeRef) == 0 {
		return store.JSON{}
	}
	out := make(store.JSON, len(scopeRef))
	for key, value := range scopeRef {
		out[key] = value
	}
	return out
}
