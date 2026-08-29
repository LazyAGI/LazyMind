package chatagent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type retryingCoreClient struct {
	mu     sync.Mutex
	inputs []map[string]any
	paths  []string
	fail   int
}

type countingRunner struct{ calls int }

type availableRunner struct{ countingRunner }

func (*availableRunner) Availability() (bool, string) { return true, "" }

type togglePolicy struct {
	mu      sync.Mutex
	enabled bool
	changed chan struct{}
}

func newTogglePolicy(enabled bool) *togglePolicy {
	return &togglePolicy{enabled: enabled, changed: make(chan struct{})}
}

func (p *togglePolicy) Enabled(string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.enabled, nil
}

func (p *togglePolicy) Changes() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.changed
}

func (p *togglePolicy) setEnabled(enabled bool) {
	p.mu.Lock()
	p.enabled = enabled
	close(p.changed)
	p.changed = make(chan struct{})
	p.mu.Unlock()
}

type catalogRunner struct{ countingRunner }

func (catalogRunner) Sessions(context.Context) ([]NativeSession, error) {
	return []NativeSession{{
		ThreadID: "thread-1", ProjectKey: "project-1", ProjectName: "LazyRAG",
		DisplayName: "真实会话",
		Turns:       []NativeTurn{{ID: "turn-1", User: "问题", Assistant: "回答"}},
	}}, nil
}

type privacyCatalogRunner struct {
	countingRunner
	calls int
}

func (runner *privacyCatalogRunner) Sessions(context.Context) ([]NativeSession, error) {
	runner.calls++
	return catalogRunner{}.Sessions(context.Background())
}

type blockingCoreClient struct{}

type claimBlockingCoreClient struct{ started chan struct{} }

type pendingMirrorCoreClient struct {
	paths  []string
	inputs []map[string]any
	reject int
}

func (r *countingRunner) Run(context.Context, Run, func(Event) error) error {
	r.calls++
	return nil
}

func (blockingCoreClient) DoJSON(ctx context.Context, _, _ string, _, _ any) error {
	<-ctx.Done()
	return ctx.Err()
}

func (c claimBlockingCoreClient) DoJSON(ctx context.Context, _, _ string, _, _ any) error {
	close(c.started)
	<-ctx.Done()
	return ctx.Err()
}

func (c *pendingMirrorCoreClient) DoJSON(_ context.Context, _, path string, input, output any) error {
	c.paths = append(c.paths, path)
	request := input.(map[string]any)
	c.inputs = append(c.inputs, request)
	if response, ok := output.(*syncSessionCatalogResponse); ok {
		response.Updated = len(request["sessions"].([]NativeSession)) - c.reject
		response.Rejected = c.reject
	}
	return nil
}

func (c *retryingCoreClient) DoJSON(_ context.Context, _, path string, input, output any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	copyInput := make(map[string]any, len(input.(map[string]any)))
	for key, value := range input.(map[string]any) {
		copyInput[key] = value
	}
	c.inputs = append(c.inputs, copyInput)
	c.paths = append(c.paths, path)
	if len(c.inputs) <= c.fail {
		return errors.New("response was lost")
	}
	if response, ok := output.(*syncSessionCatalogResponse); ok {
		response.Updated = len(copyInput["sessions"].([]NativeSession))
	}
	return nil
}

func TestHostSynchronizesProviderNativeSessions(t *testing.T) {
	client := &retryingCoreClient{}
	host := &Host{api: client, runner: &catalogRunner{}, provider: "codex", id: "host-1"}
	if !host.syncSessionCatalog(context.Background()) {
		t.Fatal("session catalog sync failed")
	}
	if len(client.paths) != 1 || client.paths[0] != "/external-chat/providers/codex/sessions:sync" {
		t.Fatalf("paths=%#v", client.paths)
	}
	sessions, ok := client.inputs[0]["sessions"].([]NativeSession)
	if !ok || len(sessions) != 1 || sessions[0].ProjectName != "LazyRAG" || len(sessions[0].Turns) != 1 {
		t.Fatalf("sessions=%#v", client.inputs[0]["sessions"])
	}
	if client.inputs[0]["host_id"] != "host-1" {
		t.Fatalf("host identity=%#v", client.inputs[0])
	}
}

func TestHostDoesNotReadNativeSessionsUntilExecutorIsEnabled(t *testing.T) {
	client := &retryingCoreClient{}
	runner := &privacyCatalogRunner{}
	policy := newTogglePolicy(false)
	host := &Host{
		api: client, runner: runner, policy: policy, provider: "codex", id: "host-1",
	}
	if !host.syncSessionCatalog(context.Background()) {
		t.Fatal("disabled catalog reset failed")
	}
	if runner.calls != 0 {
		t.Fatalf("disabled executor read native sessions %d times", runner.calls)
	}
	if len(client.inputs) != 1 || client.inputs[0]["reset"] != true ||
		client.inputs[0]["session_access_enabled"] != false || len(client.inputs[0]["sessions"].([]NativeSession)) != 0 {
		t.Fatalf("disabled sync payload=%#v", client.inputs)
	}

	policy.setEnabled(true)
	if !host.syncSessionCatalog(context.Background()) {
		t.Fatal("enabled catalog sync failed")
	}
	if runner.calls != 1 {
		t.Fatalf("enabled executor read native sessions %d times", runner.calls)
	}
}

func TestHostDoesNotAcceptPartialNativeCatalogImport(t *testing.T) {
	client := &pendingMirrorCoreClient{reject: 1}
	host := &Host{api: client, runner: &catalogRunner{}, provider: "cursor", id: "host-1"}
	if host.syncSessionCatalog(context.Background()) {
		t.Fatal("partial native catalog import was accepted")
	}
}

func TestHostEventRetryKeepsIdempotencyAndLeaseTokens(t *testing.T) {
	client := &retryingCoreClient{fail: 1}
	host := &Host{api: client, id: "host-1"}
	run := Run{RunID: "run-1", LeaseToken: "lease-1"}
	if err := host.sendEvent(context.Background(), run, Event{Type: "message", Text: "answer"}); err != nil {
		t.Fatalf("send event: %v", err)
	}
	if len(client.inputs) != 2 {
		t.Fatalf("attempts=%d, want 2", len(client.inputs))
	}
	first, second := client.inputs[0], client.inputs[1]
	if first["event_id"] == "" || first["event_id"] != second["event_id"] {
		t.Fatalf("event retry changed id: %#v %#v", first, second)
	}
	if first["lease_token"] != run.LeaseToken || first["host_id"] != host.id {
		t.Fatalf("event lost lease ownership: %#v", first)
	}
}

func TestHostTerminalEventRetriesForTheDeliveryWindow(t *testing.T) {
	client := &retryingCoreClient{fail: 4}
	host := &Host{api: client, id: "host-1"}
	run := Run{RunID: "run-1", LeaseToken: "lease-1"}
	if err := host.sendTerminalEvent(context.Background(), run, Event{Type: "completed"}); err != nil {
		t.Fatalf("send terminal event: %v", err)
	}
	if len(client.inputs) != 5 {
		t.Fatalf("attempts=%d, want 5", len(client.inputs))
	}
	firstID := client.inputs[0]["event_id"]
	for _, input := range client.inputs[1:] {
		if input["event_id"] != firstID {
			t.Fatalf("terminal retry changed event id: %#v", client.inputs)
		}
	}
}

func TestHostFinalizesDurableProviderCheckpointWithoutRunningAgentAgain(t *testing.T) {
	client := &retryingCoreClient{}
	runner := &countingRunner{}
	host := &Host{api: client, runner: runner, id: "host-2"}
	host.execute(context.Background(), Run{
		RunID: "run-1", Action: "finalize", HostID: "host-2", LeaseToken: "lease-2",
	})
	if runner.calls != 0 {
		t.Fatalf("provider runner called %d times", runner.calls)
	}
	if len(client.inputs) != 1 || client.inputs[0]["type"] != "completed" ||
		client.inputs[0]["host_id"] != "host-2" || client.inputs[0]["lease_token"] != "lease-2" {
		t.Fatalf("unexpected finalization event: %#v", client.inputs)
	}
}

func TestHostBoundsBlockedCoreRequestByOperationTimeout(t *testing.T) {
	host := &Host{api: blockingCoreClient{}}
	started := time.Now()
	err := host.doJSON(context.Background(), 20*time.Millisecond, "POST", "/blocked", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked request error=%v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("blocked request exceeded its operation timeout: %s", elapsed)
	}
}

func TestHostPolicyOverridesProviderAvailability(t *testing.T) {
	policy := newTogglePolicy(false)
	host := &Host{runner: &availableRunner{}, provider: "codex", policy: policy}
	host.refreshAvailability()
	if host.ready || host.unavailableReason != executionDisabledReason {
		t.Fatalf("disabled host ready=%v reason=%q", host.ready, host.unavailableReason)
	}
	policy.setEnabled(true)
	host.refreshAvailability()
	if !host.ready || host.unavailableReason != "" {
		t.Fatalf("enabled host ready=%v reason=%q", host.ready, host.unavailableReason)
	}
}

func TestHostPolicyChangeCancelsOutstandingClaim(t *testing.T) {
	policy := newTogglePolicy(true)
	client := claimBlockingCoreClient{started: make(chan struct{})}
	host := &Host{api: client, provider: "cursor", policy: policy}
	result := make(chan struct {
		err     error
		changed bool
	}, 1)
	go func() {
		err, changed := host.claim(context.Background(), policy.Changes(), "/claim", map[string]any{}, &struct{}{})
		result <- struct {
			err     error
			changed bool
		}{err: err, changed: changed}
	}()
	<-client.started
	policy.setEnabled(false)
	select {
	case outcome := <-result:
		if !outcome.changed || !errors.Is(outcome.err, context.Canceled) {
			t.Fatalf("claim err=%v changed=%v", outcome.err, outcome.changed)
		}
	case <-time.After(time.Second):
		t.Fatal("policy change did not cancel claim")
	}
}

func TestHostObservesPolicyChangeBetweenSubscriptionAndClaim(t *testing.T) {
	policy := newTogglePolicy(true)
	changes := policy.Changes()
	policy.setEnabled(false)
	client := claimBlockingCoreClient{started: make(chan struct{})}
	host := &Host{api: client, provider: "codex", policy: policy}
	host.refreshAvailability()
	err, changed := host.claim(context.Background(), changes, "/claim", map[string]any{}, &struct{}{})
	if !changed || !errors.Is(err, context.Canceled) || host.ready {
		t.Fatalf("claim err=%v changed=%v ready=%v", err, changed, host.ready)
	}
}

func TestHeartbeatFailureWindowStaysBelowCoreLease(t *testing.T) {
	const coreLeaseDuration = 30 * time.Second
	if heartbeatRequestTimeout >= leaseLossGrace {
		t.Fatalf("heartbeat request timeout %s must be shorter than loss grace %s", heartbeatRequestTimeout, leaseLossGrace)
	}
	if leaseLossGrace+heartbeatRequestTimeout >= coreLeaseDuration {
		t.Fatalf("heartbeat failure window %s must be shorter than Core lease %s", leaseLossGrace+heartbeatRequestTimeout, coreLeaseDuration)
	}
}
