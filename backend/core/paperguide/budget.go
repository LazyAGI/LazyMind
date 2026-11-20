package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

const (
	MaxCalls         = 24
	maxPlannedCalls  = 14
	maxRecoveryCalls = 2
	MaxActiveMillis  = int64(30 * 60 * 1000)

	localProcessingReserveMillis = int64(120000)
)

func ModelAttemptTimeout(task string) time.Duration {
	if task == "paper.explainer.chapter" {
		return 5 * time.Minute
	}
	return 2 * time.Minute
}

var ErrBudget = errors.New("paper_guide_budget_exhausted")

// Live peers may release their time reservations when their calls finish.
var errAttemptTimeUnavailable = fmt.Errorf("%w: attempt reservation unavailable", ErrBudget)

func stringInt(n int64) string { return strconv.FormatInt(n, 10) }

// Only this execution's live reservations can release capacity. Reservations
// left by an interrupted worker are already part of the durable spend.
type attemptReservations struct {
	mu      sync.Mutex
	active  int
	changed chan struct{}
}

func (r *attemptReservations) reserve(write func() error) (<-chan struct{}, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	err := write()
	if err == errAttemptTimeUnavailable && r.active > 0 {
		return r.changed, nil
	}
	if err == nil {
		if r.active == 0 {
			r.changed = make(chan struct{})
		}
		r.active++
	}
	return nil, err
}

func (r *attemptReservations) finish() {
	r.mu.Lock()
	defer r.mu.Unlock()
	close(r.changed)
	r.active--
	if r.active > 0 {
		r.changed = make(chan struct{})
	}
}

func (r Repository) checkResumeBudget(ctx context.Context, rev orm.PaperGuideRevision) error {
	available := MaxActiveMillis - rev.ActiveMillis - localProcessingReserveMillis
	if available < 0 {
		return ErrBudget
	}

	steps := r.DB.WithContext(ctx).Model(&orm.PaperGuideStep{}).Where("revision_id = ?", rev.ID)
	var planJSON string
	err := steps.Session(&gorm.Session{}).Select("output_json").Where("step_key = ?", "fast:plan").Take(&planJSON).Error
	var required []string
	planned, recovery := 0, 0
	if errors.Is(err, gorm.ErrRecordNotFound) {
		required = []string{"fast:plan"}
		// Every new plan requires at least this many new chapter calls.
		planned = minExplainerChapters
	} else if err != nil {
		return err
	} else {
		var plan ExplainerPlan
		if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
			return ErrEvidence
		}
		for _, chapter := range plan.Chapters {
			required = append(required, "fast:chapter:"+chapter.ID)
		}
	}
	// Read only keys. Model output checkpoints can individually exceed 100 KB.
	var keys []string
	if err := steps.Pluck("step_key", &keys).Error; err != nil {
		return err
	}
	complete, attempted := map[string]bool{}, map[string]bool{}
	for _, key := range keys {
		if request, ok := strings.CutPrefix(key, "request:"); ok {
			if i := strings.LastIndexByte(request, ':'); i >= 0 {
				attempted[request[:i]] = true
			}
		} else {
			complete[key] = true
		}
	}
	for _, key := range required {
		if complete[key] {
			continue
		}
		if attempted[key] {
			recovery++
		} else {
			planned++
		}
	}
	if planned+recovery == 0 {
		return nil
	}
	if available < ModelAttemptTimeout("paper.explainer.chapter").Milliseconds() || rev.CallsUsed+planned+recovery > MaxCalls || rev.PlannedCalls+planned > maxPlannedCalls || rev.RecoveryCalls+recovery > maxRecoveryCalls {
		return ErrBudget
	}
	return nil
}
