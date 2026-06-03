package worker

import "testing"

func TestSourceLimiterHonorsGlobalLimitFirst(t *testing.T) {
	t.Parallel()

	limiter := newSourceLimiter(2, 10)
	if !limiter.tryAcquire("source-a") || !limiter.tryAcquire("source-b") {
		t.Fatalf("expected first two global slots to be acquired")
	}
	if limiter.tryAcquire("source-c") {
		t.Fatalf("global limit should block additional source slots")
	}
	limiter.release("source-a")
	if !limiter.tryAcquire("source-c") {
		t.Fatalf("released global slot should allow another source")
	}
}

func TestSourceLimiterRestrictsSingleSourceWithinGlobalLimit(t *testing.T) {
	t.Parallel()

	limiter := newSourceLimiter(5, 2)
	if !limiter.tryAcquire("source-a") || !limiter.tryAcquire("source-a") {
		t.Fatalf("expected first two source slots to be acquired")
	}
	if limiter.tryAcquire("source-a") {
		t.Fatalf("source limit should block third same-source task")
	}
	if !limiter.tryAcquire("source-b") {
		t.Fatalf("source limit should not block another source while global slots remain")
	}
}
