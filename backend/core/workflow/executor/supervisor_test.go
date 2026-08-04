package executor

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"lazymind/core/workflow/attempt"
)

type fakeAttempts struct {
	mu         sync.Mutex
	claim      attempt.Claim
	heartbeats int
	progresses []string
	terminals  []string
	results    []string
}

func (f *fakeAttempts) Claim(context.Context, string) (attempt.Claim, error) { return f.claim, nil }
func (f *fakeAttempts) Heartbeat(context.Context, string, string) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeats++
	return time.Now(), nil
}
func (f *fakeAttempts) Progress(_ context.Context, _, _ string, v json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.progresses = append(f.progresses, string(v))
	return nil
}
func (f *fakeAttempts) Complete(_ context.Context, _, _ string, v json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminals = append(f.terminals, "succeeded")
	f.results = append(f.results, string(v))
	return nil
}
func (f *fakeAttempts) Fail(_ context.Context, _, _, code string, v json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminals = append(f.terminals, "failed:"+code)
	f.results = append(f.results, string(v))
	return nil
}
func (f *fakeAttempts) Cancel(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminals = append(f.terminals, "cancelled")
	return nil
}

type fakeLoader struct{ value AttemptContext }

func (f fakeLoader) LoadAttemptContext(context.Context, string) (AttemptContext, error) {
	return f.value, nil
}

type memorySink struct {
	mu     sync.Mutex
	values []Artifact
}

func (s *memorySink) Save(_ context.Context, _ AttemptContext, v Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values = append(s.values, v)
	return nil
}

type fakeAdapter struct {
	delay      time.Duration
	panicValue any
	missing    bool
}

func (f fakeAdapter) BuildRunSpec(_ context.Context, v AttemptContext) (HostRunSpec, error) {
	return HostRunSpec{Attempt: v}, nil
}
func (f fakeAdapter) RunSubAgent(ctx context.Context, _ HostRunSpec, c Callbacks) (Result, error) {
	if f.panicValue != nil {
		panic(f.panicValue)
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	_ = c.Progress(json.RawMessage(`{"progress":50}`))
	result := Result{Summary: "ok", Projection: map[string]any{"state": "done"}}
	if !f.missing {
		a := Artifact{Slot: "report", ContentType: "application/json", Value: json.RawMessage(`{"ok":true}`), Seq: 1}
		_ = c.Artifact(a)
		result.Artifacts = []Artifact{a}
	}
	return result, nil
}
func (fakeAdapter) Cancel(context.Context, string) error { return nil }

func newSupervisor(executor HostExecutor) (*Supervisor, *fakeAttempts, *memorySink) {
	a := &fakeAttempts{claim: attempt.Claim{AttemptID: "a1", LeaseToken: "lease", FencingGeneration: 4}}
	sink := &memorySink{}
	return &Supervisor{Attempts: a, Contexts: fakeLoader{AttemptContext{AttemptID: "a1", SessionID: "s1", StepID: "step", AttemptNo: 1, Operation: "run", RequiredOutputs: []string{"report"}}}, Executor: executor, Artifacts: sink, Config: Config{ExecutorID: "worker", HeartbeatInterval: time.Millisecond}}, a, sink
}

func TestSupervisorHeartbeatCallbacksArtifactsAndSingleTerminal(t *testing.T) {
	s, a, sink := newSupervisor(fakeAdapter{delay: 5 * time.Millisecond})
	result, err := s.ExecuteSync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "ok" || a.heartbeats == 0 || len(a.progresses) != 1 || len(sink.values) != 1 {
		t.Fatalf("incomplete execution: %#v %#v %#v", result, a, sink)
	}
	if !reflect.DeepEqual(a.terminals, []string{"succeeded"}) {
		t.Fatalf("terminals=%v", a.terminals)
	}
}
func TestSupervisorMissingRequiredOutputFails(t *testing.T) {
	s, a, _ := newSupervisor(fakeAdapter{missing: true})
	_, err := s.ExecuteSync(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !reflect.DeepEqual(a.terminals, []string{"failed:REQUIRED_OUTPUT_MISSING"}) {
		t.Fatalf("terminals=%v", a.terminals)
	}
}
func TestSupervisorPanicAlwaysTerminates(t *testing.T) {
	s, a, _ := newSupervisor(fakeAdapter{panicValue: "boom"})
	_, err := s.ExecuteSync(context.Background())
	if err == nil {
		t.Fatal("expected panic error")
	}
	if !reflect.DeepEqual(a.terminals, []string{"failed:EXECUTOR_PANIC"}) {
		t.Fatalf("terminals=%v", a.terminals)
	}
}

func TestSyncAndHandoffHaveEquivalentProjectionArtifactAndTerminal(t *testing.T) {
	syncSupervisor, syncAttempts, syncSink := newSupervisor(fakeAdapter{})
	syncResult, err := syncSupervisor.ExecuteSync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	handoffSupervisor, handoffAttempts, handoffSink := newSupervisor(fakeAdapter{})
	ack, err := handoffSupervisor.Handoff(context.Background())
	if err != nil || !ack.Owned || ack.FencingGeneration != 4 {
		t.Fatalf("ack=%#v err=%v", ack, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		handoffAttempts.mu.Lock()
		done := len(handoffAttempts.terminals) > 0
		handoffAttempts.mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !reflect.DeepEqual(syncAttempts.terminals, handoffAttempts.terminals) || !reflect.DeepEqual(syncSink.values, handoffSink.values) {
		t.Fatalf("not equivalent: sync=%v/%v handoff=%v/%v", syncAttempts.terminals, syncSink.values, handoffAttempts.terminals, handoffSink.values)
	}
	var handoffResult Result
	if err := json.Unmarshal([]byte(handoffAttempts.results[0]), &handoffResult); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(syncResult.Projection, handoffResult.Projection) {
		t.Fatalf("projection differs")
	}
}
func TestCanaryAndRollbackSwitch(t *testing.T) {
	cases := []struct {
		mode            Mode
		percent, bucket int
		schema, want    bool
	}{{ModeLegacy, 100, 0, true, false}, {ModeShadow, 100, 0, true, false}, {ModeCanonical, 0, 0, true, true}, {ModeCanary, 10, 9, true, true}, {ModeCanary, 10, 10, true, false}, {ModeCanonical, 100, 0, false, false}}
	for _, tc := range cases {
		if got := UseCanonical(tc.mode, tc.percent, tc.bucket, tc.schema); got != tc.want {
			t.Errorf("%s got %v want %v", tc.mode, got, tc.want)
		}
	}
}
func TestExecutionErrorHasSingleTerminal(t *testing.T) {
	adapter := errorAdapter{}
	s, a, _ := newSupervisor(adapter)
	_, err := s.ExecuteSync(context.Background())
	if !errors.Is(err, errRun) {
		t.Fatal(err)
	}
	if len(a.terminals) != 1 {
		t.Fatalf("terminals=%v", a.terminals)
	}
}

var errRun = errors.New("run failed")

type errorAdapter struct{}

func (errorAdapter) BuildRunSpec(_ context.Context, v AttemptContext) (HostRunSpec, error) {
	return HostRunSpec{Attempt: v}, nil
}
func (errorAdapter) RunSubAgent(context.Context, HostRunSpec, Callbacks) (Result, error) {
	return Result{}, errRun
}
func (errorAdapter) Cancel(context.Context, string) error { return nil }
