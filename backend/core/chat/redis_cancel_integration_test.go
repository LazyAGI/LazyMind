package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"lazymind/core/state"
)

type cancelWatchResult struct {
	received bool
	err      error
}

func TestRedisCancelWatcherReleasesConnections(t *testing.T) {
	rawURL := strings.TrimSpace(os.Getenv("TEST_REDIS_URL"))
	if rawURL == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		t.Fatalf("parse TEST_REDIS_URL: %v", err)
	}
	options.PoolSize = 2
	options.PoolTimeout = 250 * time.Millisecond
	options.MaxRetries = 0
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	stateStore := state.NewRedisStore(client)

	pingCtx, pingCancel := context.WithTimeout(context.Background(), time.Second)
	defer pingCancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		t.Fatalf("ping test redis: %v", err)
	}

	prefix := fmt.Sprintf("integration:chat-cancel:%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		keys, _ := client.Keys(cleanupCtx, prefix+"*").Result()
		if len(keys) > 0 {
			_ = client.Del(cleanupCtx, keys...).Err()
		}
	})

	t.Run("sequential watchers", func(t *testing.T) {
		for i := 0; i < 20; i++ {
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan cancelWatchResult, 1)
			go func(historyID string) {
				received, err := watchChatCancelSignal(ctx, stateStore, prefix, historyID)
				result <- cancelWatchResult{received: received, err: err}
			}(fmt.Sprintf("sequential-%d", i))
			time.Sleep(20 * time.Millisecond)
			cancel()
			assertCancelledWatcher(t, result)
			assertRedisPoolIdle(t, client)
		}
	})

	t.Run("concurrent watchers", func(t *testing.T) {
		const watcherCount = 10
		cancels := make([]context.CancelFunc, 0, watcherCount)
		results := make([]chan cancelWatchResult, 0, watcherCount)
		for i := 0; i < watcherCount; i++ {
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan cancelWatchResult, 1)
			cancels = append(cancels, cancel)
			results = append(results, result)
			go func(historyID string) {
				received, err := watchChatCancelSignal(ctx, stateStore, prefix, historyID)
				result <- cancelWatchResult{received: received, err: err}
			}(fmt.Sprintf("concurrent-%d", i))
		}
		time.Sleep(50 * time.Millisecond)
		for _, cancel := range cancels {
			cancel()
		}
		for _, result := range results {
			assertCancelledWatcher(t, result)
		}
		assertRedisPoolIdle(t, client)
	})

	t.Run("real stop signal", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		result := make(chan cancelWatchResult, 1)
		go func() {
			received, err := watchChatCancelSignal(ctx, stateStore, prefix, "signal")
			result <- cancelWatchResult{received: received, err: err}
		}()
		time.Sleep(20 * time.Millisecond)
		if err := setChatCancelSignal(ctx, stateStore, prefix, "signal"); err != nil {
			t.Fatalf("set stop signal: %v", err)
		}
		select {
		case got := <-result:
			if got.err != nil || !got.received {
				t.Fatalf("received=%v err=%v, want true/nil", got.received, got.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("watcher did not receive stop signal")
		}
		assertRedisPoolIdle(t, client)
	})

	t.Run("backend error is not stop", func(t *testing.T) {
		closedClient := redis.NewClient(options)
		closedStore := state.NewRedisStore(closedClient)
		if err := closedClient.Close(); err != nil {
			t.Fatalf("close redis client: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		received, err := watchChatCancelSignal(ctx, closedStore, prefix, "closed")
		if received || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("received=%v err=%v, want false/context deadline exceeded", received, err)
		}
	})

	t.Run("run decisions are atomic and run scoped", func(t *testing.T) {
		decisionCtx := context.Background()
		conversationID := prefix + ":decision"
		historyID := "history"
		failure := &RunTerminal{Status: "failed", Reason: "model_failure", Code: "rate_limited"}
		for _, runID := range []string{"cancel-first", "failure-first", "next-run"} {
			key := runDecisionKey(conversationID, historyID, runID)
			t.Cleanup(func() { _ = client.Del(context.Background(), key).Err() })
		}

		if won, err := claimUserCancelDecision(decisionCtx, stateStore, conversationID, historyID, "cancel-first"); err != nil || !won {
			t.Fatalf("claim cancellation: won=%v err=%v", won, err)
		}
		cancelled := resolveRunTerminal(
			decisionCtx, stateStore, conversationID, historyID, "cancel-first",
			failure, false, "redis_integration_terminal",
		)
		if cancelled.Status != "cancelled" || cancelled.Reason != "user_cancelled" {
			t.Fatalf("cancel-first terminal=%#v", cancelled)
		}

		acceptedFailure := resolveRunTerminal(
			decisionCtx, stateStore, conversationID, historyID, "failure-first",
			failure, false, "redis_integration_terminal",
		)
		if won, err := claimUserCancelDecision(decisionCtx, stateStore, conversationID, historyID, "failure-first"); err != nil || won {
			t.Fatalf("late cancellation: won=%v err=%v", won, err)
		}
		if acceptedFailure.Status != "failed" || acceptedFailure.Reason != "model_failure" {
			t.Fatalf("failure-first terminal=%#v", acceptedFailure)
		}

		nextRun := resolveRunTerminal(
			decisionCtx, stateStore, conversationID, historyID, "next-run",
			failure, false, "redis_integration_terminal",
		)
		if nextRun.Status != "failed" || nextRun.Reason != "model_failure" {
			t.Fatalf("previous run decision leaked into next run: %#v", nextRun)
		}
	})

	assertRedisPoolIdle(t, client)
	requestCtx, requestCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer requestCancel()
	healthKey := prefix + ":health"
	if err := client.Set(requestCtx, healthKey, "ok", time.Minute).Err(); err != nil {
		t.Fatalf("redis set after watcher load: %v", err)
	}
	if value, err := client.Get(requestCtx, healthKey).Result(); err != nil || value != "ok" {
		t.Fatalf("redis get after watcher load value=%q err=%v", value, err)
	}
}

func assertCancelledWatcher(t *testing.T, result <-chan cancelWatchResult) {
	t.Helper()
	select {
	case got := <-result:
		if got.received || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("received=%v err=%v, want false/context canceled", got.received, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled watcher did not exit")
	}
}

func assertRedisPoolIdle(t *testing.T, client *redis.Client) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		stats := client.PoolStats()
		inUse := stats.TotalConns - stats.IdleConns
		if inUse == 0 && stats.PendingRequests == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"redis pool did not return to idle: total=%d idle=%d pending=%d timeouts=%d",
				stats.TotalConns, stats.IdleConns, stats.PendingRequests, stats.Timeouts,
			)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
