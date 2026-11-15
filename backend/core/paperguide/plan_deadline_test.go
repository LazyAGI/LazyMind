package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
)

type deadlineGuide struct {
	*testing.T
	Service
	scope Scope
	rev   orm.PaperGuideRevision
	snap  GuideSourceSnapshot
	model ModelIdentity
}

func newDeadlineGuide(t *testing.T, extract bool) deadlineGuide {
	t.Helper()
	d, id := fixture(t)
	model := ModelIdentity{ID: "m", ContextTokens: 128000}
	if extract {
		// Force the actual Run entry to select bounded extraction before PLAN.
		for i := 0; i < 30; i++ {
			d.Blocks = append(d.Blocks, pdfir.Block{ID: fmt.Sprintf("text-%d", i), Type: "text", Text: strings.Repeat("Material and conditions. ", 80), Page: 0})
		}
		model.ContextTokens, model.OutputReserveTokens = 48000, 19200
	}
	snap, err := BuildSourceSnapshot(d, id)
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
	repo := Repository{DB: guideDB(t)}
	rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: model})
	if err != nil {
		t.Fatal(err)
	}
	return deadlineGuide{T: t, scope: scope, rev: rev, snap: snap, model: model, Service: Service{
		Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil },
		ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil },
	}}
}

func (h deadlineGuide) run(ctx context.Context) (asyncjob.Result, error) {
	return h.Service.Run(ctx, asyncjob.Job{ID: h.rev.JobID, PayloadJSON: mustJSON(JobPayload{h.rev.ID, h.rev.Epoch})}, testFence{h.Repo.DB})
}

func (h deadlineGuide) row() orm.PaperGuideRevision {
	h.Helper()
	row, err := h.Repo.Revision(context.Background(), h.scope, h.rev.ID)
	if err != nil {
		h.Fatal(err)
	}
	return row
}

func (h deadlineGuide) checkpoints() []orm.PaperGuideStep {
	h.Helper()
	steps, err := h.Repo.Steps(context.Background(), h.rev.ID)
	if err != nil {
		h.Fatal(err)
	}
	return steps
}

func TestRunnerActiveBalanceMustCoverFullAttemptAndLocalReserve(t *testing.T) {
	for _, tc := range []struct {
		task    string
		extract bool
		used    int64
		allowed bool
	}{
		{"paper.guide.plan", false, 1500000, true},
		{"paper.guide.plan", false, 1500001, false},
		{"paper.guide.extract", true, 1560000, true},
		{"paper.guide.extract", true, 1560001, false},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.task, tc.used), func(t *testing.T) {
			h := newDeadlineGuide(t, tc.extract)
			if err := h.Repo.DB.Model(&h.rev).Update("active_millis", tc.used).Error; err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			h.Call = func(_ context.Context, task string, _ map[string]any, _ map[string]any) (json.RawMessage, error) {
				calls++
				row := h.row()
				if task != tc.task || row.ActiveMillis != 1800000 || row.CallsUsed != 1 {
					t.Fatalf("dispatch task=%s calls=%d reserved=%d", task, row.CallsUsed, row.ActiveMillis)
				}
				cancel()
				return nil, context.Canceled
			}
			outcome, err := h.run(ctx)
			steps := h.checkpoints()
			if tc.allowed {
				if err == nil || calls != 1 || len(steps) != 1 || steps[0].OutputJSON != "" {
					t.Fatalf("interrupted dispatch: calls=%d steps=%+v err=%v", calls, steps, err)
				}
			} else if !errors.Is(err, ErrBudget) || outcome.ErrorCode != ErrBudget.Error() || calls != 0 || len(steps) != 0 || h.row().CallsUsed != 0 {
				t.Fatalf("dispatch with insufficient budget: calls=%d steps=%+v err=%v", calls, steps, err)
			}
		})
	}
}

func TestGuideAttemptSettlesAtItsOwnDeadline(t *testing.T) {
	for _, tc := range []struct {
		name        string
		task        string
		timeout     time.Duration
		takes       time.Duration
		providerErr error
		wantClass   string
		wantElapsed int64
	}{
		{"plan_success_after_old_limit", "paper.guide.plan", 180 * time.Second, 150 * time.Second, nil, "success", 150000},
		{"plan_late_success", "paper.guide.plan", 180 * time.Second, 181 * time.Second, nil, "timeout", 180000},
		{"plan_early_provider_timeout", "paper.guide.plan", 180 * time.Second, 3 * time.Second, &CallError{Class: "timeout"}, "timeout", 3000},
		{"extract_late_success", "paper.guide.extract", 120 * time.Second, 121 * time.Second, nil, "timeout", 120000},
		{"section_late_success", "paper.guide.section", 120 * time.Second, 121 * time.Second, nil, "timeout", 120000},
		{"review_late_success", "paper.guide.review", 120 * time.Second, 121 * time.Second, nil, "timeout", 120000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDeadlineGuide(t, false)
			synctest.Test(t, func(t *testing.T) {
				n := 0
				h.Call = func(ctx context.Context, task string, _ map[string]any, _ map[string]any) (json.RawMessage, error) {
					n++
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) != tc.timeout || task != tc.task {
						t.Errorf("dispatch task=%s deadline=%v, want %v", task, time.Until(deadline), tc.timeout)
					}
					row := h.row()
					if want := int64(n-1)*tc.wantElapsed + tc.timeout.Milliseconds(); row.ActiveMillis != want || row.CallsUsed != n {
						t.Errorf("dispatch calls=%d reserved=%d, want calls=%d reserved=%d", row.CallsUsed, row.ActiveMillis, n, want)
					}
					// The fake ignores cancellation and can still return well-formed JSON.
					time.Sleep(tc.takes)
					return json.RawMessage(`{"valid":true}`), tc.providerErr
				}
				x := execution{s: h.Service, scope: h.scope, rev: h.rev, model: h.model, snap: h.snap, f: testFence{h.Repo.DB}, steps: map[string]orm.PaperGuideStep{}}
				var output map[string]any
				err := x.task(t.Context(), "test", tc.task, x.input(h.snap), &output, func() error { return nil })
				row := h.row()
				wantCalls, checkpoints := 2, 0
				var wantErr error = ErrModelTimeout
				if tc.wantClass == "success" {
					wantCalls, checkpoints, wantErr = 1, 1, nil
				}
				if !errors.Is(err, wantErr) || row.CallsUsed != wantCalls || row.RecoveryCalls != wantCalls-1 || row.ActiveMillis != int64(wantCalls)*tc.wantElapsed {
					t.Errorf("settlement calls=%d recovery=%d ms=%d, error=%v", row.CallsUsed, row.RecoveryCalls, row.ActiveMillis, err)
				}
				tickets, saved := 0, 0
				for _, step := range h.checkpoints() {
					if step.StepKey == "test" {
						saved++
						continue
					}
					tickets++
					var marker struct {
						Task       string `json:"task"`
						ErrorClass string `json:"error_class"`
						Elapsed    int64  `json:"elapsed_ms"`
					}
					if err := json.Unmarshal([]byte(step.OutputJSON), &marker); err != nil || marker.Task != tc.task || marker.ErrorClass != tc.wantClass || marker.Elapsed != tc.wantElapsed {
						t.Errorf("settled ticket=%s err=%v", step.OutputJSON, err)
					}
				}
				if tickets != wantCalls || saved != checkpoints {
					t.Errorf("tickets=%d saved=%d, want %d/%d", tickets, saved, wantCalls, checkpoints)
				}
			})
		})
	}
}

func TestRunnerParentDeadlineKeepsUnknownPLANReservation(t *testing.T) {
	h := newDeadlineGuide(t, false)
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		calls := 0
		h.Call = func(ctx context.Context, task string, _ map[string]any, _ map[string]any) (json.RawMessage, error) {
			calls++
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) != 2*time.Second || task != "paper.guide.plan" {
				t.Fatalf("short parent lost: task=%s deadline=%v", task, deadline)
			}
			<-ctx.Done()
			return mustJSON(baselinePlan(h.snap)), nil
		}
		outcome, err := h.run(ctx)
		row, steps := h.row(), h.checkpoints()
		if !errors.Is(err, ErrBudget) || outcome.ErrorCode != ErrBudget.Error() || calls != 1 || row.ActiveMillis != 300000 ||
			len(steps) != 1 || !strings.HasPrefix(steps[0].StepKey, "request:plan:") || steps[0].OutputJSON != "" {
			t.Fatalf("parent cutoff: calls=%d ms=%d steps=%+v error=%v", calls, row.ActiveMillis, steps, err)
		}
	})
}

// A deadline can be observed on the monotonic clock before the goroutine
// that publishes cancellation has run.
type deadlineBeforeSignal struct {
	context.Context
	at time.Time
}

func (ctx deadlineBeforeSignal) Deadline() (time.Time, bool) { return ctx.at, true }

func TestGuideTaskRejectsResultAtDeadlineBeforeCancellationSignal(t *testing.T) {
	h := newDeadlineGuide(t, false)
	synctest.Test(t, func(t *testing.T) {
		ctx := deadlineBeforeSignal{Context: t.Context(), at: time.Now().Add(time.Second)}
		h.Call = func(ctx context.Context, _ string, _ map[string]any, _ map[string]any) (json.RawMessage, error) {
			time.Sleep(time.Second)
			if ctx.Err() != nil {
				t.Fatalf("expected no cancellation signal, got %v", ctx.Err())
			}
			return mustJSON(baselinePlan(h.snap)), nil
		}
		x := execution{s: h.Service, scope: h.scope, rev: h.rev, model: h.model, snap: h.snap, f: testFence{h.Repo.DB}, steps: map[string]orm.PaperGuideStep{}}
		input, source, err := x.planInput(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var plan GuidePlan
		err = x.task(ctx, "plan", "paper.guide.plan", input, &plan, func() error { return ValidatePlan(source, plan) })
		steps := h.checkpoints()
		if row := h.row(); !errors.Is(err, ErrBudget) || row.ActiveMillis != 180000 || len(steps) != 1 || steps[0].OutputJSON != "" {
			t.Fatalf("expired result accepted: error=%v active=%d steps=%+v", err, row.ActiveMillis, steps)
		}
	})
}

func TestRunnerSavesPLANAfterOldDeadlineAndSettlesLocalWork(t *testing.T) {
	h := newDeadlineGuide(t, false)
	synctest.Test(t, func(t *testing.T) {
		checked := false
		h.Authorize = func(context.Context, Scope, bool) error {
			if !checked {
				checked = true
				time.Sleep(time.Second)
			}
			return nil
		}
		planCalls := 0
		h.Call = func(ctx context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
			switch task {
			case "paper.guide.plan":
				planCalls++
				time.Sleep(150 * time.Second)
				if ctx.Err() != nil {
					t.Fatalf("PLAN cutoff before its new deadline: %v", ctx.Err())
				}
				return mustJSON(baselinePlan(h.snap)), nil
			case "paper.guide.section":
				return mustJSON(teachingSection(input["role"].(Role))), nil
			case "paper.guide.review":
				return mustJSON(GuideReview{Findings: []GuideFinding{}}), nil
			default:
				t.Fatalf("unexpected task %s", task)
				return nil, nil
			}
		}
		if _, err := h.run(t.Context()); err != nil {
			t.Fatal(err)
		}
		row := h.row()
		if planCalls != 1 || row.Stage != "completed" || row.CallsUsed != 10 || row.RecoveryCalls != 0 || row.ActiveMillis != 151000 {
			t.Fatalf("PLAN calls=%d stage=%s all calls=%d recovery=%d active=%d", planCalls, row.Stage, row.CallsUsed, row.RecoveryCalls, row.ActiveMillis)
		}
		got, err := h.Revision(t.Context(), h.scope, h.rev.ID, 0)
		if err != nil || got.Plan == nil || len(got.Sections) != 4 {
			t.Fatalf("validated PLAN and reviewed sections not saved: %+v %v", got, err)
		}
	})
}
