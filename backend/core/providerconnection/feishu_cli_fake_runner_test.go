package providerconnection

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

type fakeFeishuCLIInvocation struct {
	ProfileDir string
	Args       []string
}

type fakeFeishuCLIResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Err      error
}

type fakeFeishuCLIRunner struct {
	mu      sync.Mutex
	calls   []fakeFeishuCLIInvocation
	results []fakeFeishuCLIResult
}

func (runner *fakeFeishuCLIRunner) queue(results ...fakeFeishuCLIResult) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.results = append(runner.results, results...)
}

func (runner *fakeFeishuCLIRunner) run(ctx context.Context, profileDir string, args ...string) (fakeFeishuCLIResult, error) {
	if err := ctx.Err(); err != nil {
		return fakeFeishuCLIResult{}, err
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.calls = append(runner.calls, fakeFeishuCLIInvocation{
		ProfileDir: profileDir,
		Args:       append([]string(nil), args...),
	})
	if len(runner.results) == 0 {
		return fakeFeishuCLIResult{}, errors.New("fake lark-cli result queue is empty")
	}
	result := runner.results[0]
	runner.results = runner.results[1:]
	return result, result.Err
}

func (runner *fakeFeishuCLIRunner) invocations() []fakeFeishuCLIInvocation {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	result := make([]fakeFeishuCLIInvocation, len(runner.calls))
	for index, call := range runner.calls {
		result[index] = fakeFeishuCLIInvocation{
			ProfileDir: call.ProfileDir,
			Args:       append([]string(nil), call.Args...),
		}
	}
	return result
}

func TestFakeFeishuCLIRunnerKeepsProfileAndArgumentsIsolated(t *testing.T) {
	t.Parallel()
	runner := &fakeFeishuCLIRunner{}
	runner.queue(
		fakeFeishuCLIResult{Stdout: []byte(`{"verification_url":"https://accounts.example.invalid/a"}`)},
		fakeFeishuCLIResult{Stdout: []byte(`{"verification_url":"https://accounts.example.invalid/b"}`)},
	)

	if _, err := runner.run(context.Background(), "/runtime/user-a/connection-a/config", "auth", "login", "--no-wait", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.run(context.Background(), "/runtime/user-b/connection-b/config", "auth", "status", "--json", "--verify"); err != nil {
		t.Fatal(err)
	}

	want := []fakeFeishuCLIInvocation{
		{ProfileDir: "/runtime/user-a/connection-a/config", Args: []string{"auth", "login", "--no-wait", "--json"}},
		{ProfileDir: "/runtime/user-b/connection-b/config", Args: []string{"auth", "status", "--json", "--verify"}},
	}
	if got := runner.invocations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("fake runner invocations = %#v, want %#v", got, want)
	}
}

func TestFakeFeishuCLIRunnerHonorsCanceledContext(t *testing.T) {
	t.Parallel()
	runner := &fakeFeishuCLIRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runner.run(ctx, "/runtime/user-a/connection-a/config", "auth", "status", "--json"); !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context.Canceled", err)
	}
	if got := runner.invocations(); len(got) != 0 {
		t.Fatalf("canceled invocation reached fake process: %#v", got)
	}
}
