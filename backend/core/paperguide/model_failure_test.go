package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"testing"
)

func TestModelFailureCausePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		parent, child, call, want error
		class                     string
	}{
		{"child deadline hides transport", nil, context.DeadlineExceeded, &CallError{Class: "transport"}, ErrModelTimeout, "timeout"},
		{"child deadline hides late success", nil, context.DeadlineExceeded, nil, ErrModelTimeout, "timeout"},
		{"parent canceled wins", context.Canceled, context.DeadlineExceeded, &CallError{Class: "timeout"}, context.Canceled, "canceled"},
		{"active deadline wins", context.DeadlineExceeded, context.DeadlineExceeded, &CallError{Class: "timeout"}, ErrBudget, "budget"},
		{"budget wins", nil, context.DeadlineExceeded, ErrBudget, ErrBudget, "budget"},
		{"epoch wins", nil, context.DeadlineExceeded, ErrConflict, ErrConflict, "conflict"},
		{"lease wins", nil, context.DeadlineExceeded, asyncjob.ErrLeaseLost, asyncjob.ErrLeaseLost, "conflict"},
		{"cancellation cause", nil, nil, context.Canceled, context.Canceled, "canceled"},
		{"success before cleanup", nil, nil, nil, nil, "success"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			class, err := classifyModelCall(tc.parent, tc.child, tc.call)
			if class != tc.class || !errors.Is(err, tc.want) {
				t.Fatalf("%s %v", class, err)
			}
		})
	}
}

func TestStoppedModelCallCannotSpendRecovery(t *testing.T) {
	for _, stop := range []string{"cancel", "epoch", "budget", "lease"} {
		t.Run(stop, func(t *testing.T) {
			d, id := fixture(t)
			snap, _ := BuildSourceSnapshot(d, id)
			repo := Repository{DB: guideDB(t)}
			scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
			model := ModelIdentity{ID: "m", ContextTokens: 128000}
			rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: model})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			want := ErrBudget
			service := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(context.Context, string, map[string]any, map[string]any) (json.RawMessage, error) {
				calls++
				switch stop {
				case "cancel":
					cancel()
					want = context.Canceled
				case "epoch":
					if err := repo.Cancel(t.Context(), scope, rev.ID); err != nil {
						t.Fatal(err)
					}
					want = ErrConflict
				case "budget":
					return nil, ErrBudget
				case "lease":
					want = asyncjob.ErrLeaseLost
					return nil, want
				}
				return nil, &CallError{Class: "timeout"}
			}}
			x := execution{s: service, rev: rev, scope: scope, model: model, snap: snap, f: testFence{repo.DB}, steps: map[string]orm.PaperGuideStep{}}
			var plan ExplainerPlan
			err = x.task(ctx, "fast:plan", "paper.explainer.plan", map[string]any{}, &plan, func() error { return nil })
			if !errors.Is(err, want) || calls != 1 {
				t.Fatalf("stop=%s err=%v want=%v calls=%d", stop, err, want, calls)
			}
			stored, err := repo.Revision(t.Context(), scope, rev.ID)
			if err != nil || stored.CallsUsed != 1 || stored.RecoveryCalls != 0 {
				t.Fatalf("extra attempt: %+v %v", stored, err)
			}
		})
	}
}
