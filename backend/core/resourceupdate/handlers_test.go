package resourceupdate

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"lazymind/core/algo"
)

// TestSafeSkillCode returns response code or 0 for nil.
func TestSafeSkillCode(t *testing.T) {
	if got := safeSkillCode(nil); got != 0 {
		t.Fatalf("nil got %d, want 0", got)
	}
	if got := safeSkillCode(&algo.SkillReviewResponse{Code: 200}); got != 200 {
		t.Fatalf("got %d, want 200", got)
	}
}

// TestSafeSkillStatus returns status string or empty for nil.
func TestSafeSkillStatus(t *testing.T) {
	if got := safeSkillStatus(nil); got != "" {
		t.Fatalf("nil got %q, want empty", got)
	}
	resp := &algo.SkillReviewResponse{}
	resp.Data.Status = "completed"
	if got := safeSkillStatus(resp); got != "completed" {
		t.Fatalf("got %q, want completed", got)
	}
}

// TestSafeSkillRequestID returns request ID or empty for nil.
func TestSafeSkillRequestID(t *testing.T) {
	if got := safeSkillRequestID(nil); got != "" {
		t.Fatalf("nil got %q, want empty", got)
	}
	resp := &algo.SkillReviewResponse{}
	resp.Data.RequestID = "req-1"
	if got := safeSkillRequestID(resp); got != "req-1" {
		t.Fatalf("got %q, want req-1", got)
	}
}

// TestSafeSkillTaskID returns task ID or empty for nil.
func TestSafeSkillTaskID(t *testing.T) {
	if got := safeSkillTaskID(nil); got != "" {
		t.Fatalf("nil got %q, want empty", got)
	}
	resp := &algo.SkillReviewResponse{}
	resp.Data.TaskID = "task-1"
	if got := safeSkillTaskID(resp); got != "task-1" {
		t.Fatalf("got %q, want task-1", got)
	}
}

// TestSkillReviewResponseStatusAccepted accepts pending, running, completed.
func TestSkillReviewResponseStatusAccepted(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"pending", true},
		{"running", true},
		{"completed", true},
		{"failed", false},
		{"", false},
		{"  pending  ", true},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := skillReviewResponseStatusAccepted(tt.status); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSafeMemoryStatus returns status string or empty for nil.
func TestSafeMemoryStatus(t *testing.T) {
	if got := safeMemoryStatus(nil); got != "" {
		t.Fatalf("nil got %q, want empty", got)
	}
	if got := safeMemoryStatus(&algo.MemoryReviewResponse{Status: "success"}); got != "success" {
		t.Fatalf("got %q, want success", got)
	}
}

// TestSafeMemoryTaskID returns task ID or empty for nil.
func TestSafeMemoryTaskID(t *testing.T) {
	if got := safeMemoryTaskID(nil); got != "" {
		t.Fatalf("nil got %q, want empty", got)
	}
	if got := safeMemoryTaskID(&algo.MemoryReviewResponse{TaskID: "mt-1"}); got != "mt-1" {
		t.Fatalf("got %q, want mt-1", got)
	}
}

// TestDecodeHistory decodes JSON, returns nil for empty, raw string for invalid.
func TestDecodeHistory(t *testing.T) {
	// nil/empty
	if got := decodeHistory(nil); got != nil {
		t.Fatalf("nil got %v, want nil", got)
	}
	if got := decodeHistory(json.RawMessage{}); got != nil {
		t.Fatalf("empty got %v, want nil", got)
	}

	// valid JSON
	valid := json.RawMessage(`[{"role":"user","content":"hello"}]`)
	got := decodeHistory(valid)
	if got == nil {
		t.Fatal("expected non-nil for valid JSON")
	}

	// invalid JSON -> falls back to raw string
	invalid := json.RawMessage(`not json`)
	raw := decodeHistory(invalid)
	if s, ok := raw.(string); !ok || s != "not json" {
		t.Fatalf("invalid json got %v, want raw string", raw)
	}
}

// TestSkillPreflightReason maps known errors to reason strings.
func TestSkillPreflightReason(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{errSkillActiveTaskMismatch, "active_task_mismatch"},
		{errSkillTooFrequent, "min_interval_not_reached"},
		{errSkillInvalidWindow, "invalid_window"},
		{errSkillWindowTooOld, "window_too_old"},
		{errSkillThresholdNotReached, "history_threshold_not_reached"},
		{errors.New("unknown"), "preflight_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := skillPreflightReason(tt.err); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestWorkerStageFor returns correct stage with defaults for edge indices.
func TestWorkerStageFor(t *testing.T) {
	w := &Worker{cfg: DefaultConfig()}

	// Stage 0
	s0 := w.stageFor(0)
	if s0.QuantityThreshold != 5 {
		t.Fatalf("stage 0 threshold = %d, want 5", s0.QuantityThreshold)
	}

	// Negative index -> first stage
	sNeg := w.stageFor(-1)
	if sNeg.QuantityThreshold != 5 {
		t.Fatalf("negative index threshold = %d, want 5", sNeg.QuantityThreshold)
	}

	// Index beyond range -> last stage
	sLast := w.stageFor(100)
	if sLast.QuantityThreshold != 20 {
		t.Fatalf("last stage threshold = %d, want 20", sLast.QuantityThreshold)
	}

	// Empty stages -> first default stage
	w2 := &Worker{cfg: Config{Stages: nil}}
	sDef := w2.stageFor(0)
	if sDef.QuantityThreshold <= 0 {
		t.Fatal("default stage threshold should be > 0")
	}
}

// TestWorkerStageForFillsZeroValues fills zero Window and Interval from config.
func TestWorkerStageForFillsZeroValues(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Stages = []Stage{
		{Window: 0, Interval: 0, QuantityThreshold: 0, Successes: 3},
	}
	w := &Worker{cfg: cfg}
	s := w.stageFor(0)
	if s.Window <= 0 {
		t.Fatal("zero Window should be filled from cfg.MaxWindow")
	}
	if s.Interval <= 0 {
		t.Fatal("zero Interval should be filled from cfg.MinInterval")
	}
	if s.QuantityThreshold <= 0 {
		t.Fatal("zero QuantityThreshold should be filled from defaults")
	}
}

// TestFormatTaskTime formats time in RFC3339Nano.
func TestFormatTaskTime(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	got := formatTaskTime(now)
	if len(got) == 0 {
		t.Fatal("expected non-empty formatted time")
	}
}

// TestParseTaskTime parses RFC3339Nano formatted time strings.
func TestParseTaskTime(t *testing.T) {
	input := "2026-07-30T12:00:00Z"
	parsed, err := parseTaskTime(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Year() != 2026 {
		t.Fatalf("year = %d, want 2026", parsed.Year())
	}

	// Whitespace stripped
	parsed2, err := parseTaskTime("  2026-07-30T12:00:00Z  ")
	if err != nil {
		t.Fatalf("parse with whitespace: %v", err)
	}
	if parsed2.Year() != 2026 {
		t.Fatalf("year = %d, want 2026", parsed2.Year())
	}

	// Invalid format
	_, err = parseTaskTime("not a time")
	if err == nil {
		t.Fatal("expected error for invalid time")
	}
}

// TestTimeOrZero returns zero time for nil pointer.
func TestTimeOrZero(t *testing.T) {
	if got := timeOrZero(nil); !got.IsZero() {
		t.Fatal("expected zero time for nil")
	}
	now := time.Now()
	if got := timeOrZero(&now); !got.Equal(now) {
		t.Fatal("expected same time")
	}
}

// TestAddTimePtr adds duration to time pointer.
func TestAddTimePtr(t *testing.T) {
	// Nil pointer returns nil
	if got := addTimePtr(nil, time.Hour); got != nil {
		t.Fatal("expected nil for nil input")
	}
	// Non-nil adds duration
	now := time.Now()
	got := addTimePtr(&now, time.Hour)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("got %v, want %v", *got, now.Add(time.Hour))
	}
}

// TestIsFrozenSkillTask detects frozen window tasks from request JSON.
func TestIsFrozenSkillTask(t *testing.T) {
	// nil
	if isFrozenSkillTask(nil) {
		t.Fatal("nil should not be frozen")
	}
	// empty
	if isFrozenSkillTask(json.RawMessage{}) {
		t.Fatal("empty should not be frozen")
	}
	// frozen
	frozen := mustMarshal(t, skillGenerateRequestJSON{WindowFrozen: true})
	if !isFrozenSkillTask(frozen) {
		t.Fatal("frozen request should be detected")
	}
	// not frozen
	notFrozen := mustMarshal(t, skillGenerateRequestJSON{WindowFrozen: false})
	if isFrozenSkillTask(notFrozen) {
		t.Fatal("non-frozen request should not be detected")
	}
}

// TestFrozenTaskEnd extracts end time from frozen request JSON.
func TestFrozenTaskEnd(t *testing.T) {
	if got := frozenTaskEnd(nil); !got.IsZero() {
		t.Fatal("nil should return zero time")
	}
	if got := frozenTaskEnd(json.RawMessage{}); !got.IsZero() {
		t.Fatal("empty should return zero time")
	}
	endTime := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	frozen := mustMarshal(t, skillGenerateRequestJSON{WindowFrozen: true, EndTime: formatTaskTime(endTime)})
	got := frozenTaskEnd(frozen)
	if !got.Equal(endTime) {
		t.Fatalf("got %v, want %v", got, endTime)
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
