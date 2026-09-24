package doc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTranslateChunkWithRetryRecoversTransientFailure(t *testing.T) {
	oldDelay := translationRetryBaseDelay
	translationRetryBaseDelay = time.Millisecond
	t.Cleanup(func() { translationRetryBaseDelay = oldDelay })
	var calls atomic.Int32
	value, err := translateChunkWithRetry(context.Background(), 3, func(context.Context) (string, error) {
		if calls.Add(1) < 3 {
			return "", errors.New("temporary")
		}
		return "translated", nil
	})
	if err != nil || value != "translated" || calls.Load() != 3 {
		t.Fatalf("value=%q calls=%d err=%v", value, calls.Load(), err)
	}
}

func TestTranslateUnitsParallelPreservesOrderAndReportsWeightedProgress(t *testing.T) {
	units := []translationWorkUnit{{ID: "a", Text: "123456"}, {ID: "b", Text: "abcdef"}}
	var active atomic.Int32
	var maximum atomic.Int32
	var progressMu sync.Mutex
	var progress [][2]int64
	result, err := translateUnitsParallel(context.Background(), units, 3, 4, func(_ context.Context, text string) (string, error) {
		current := active.Add(1)
		for {
			old := maximum.Load()
			if current <= old || maximum.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
		return fmt.Sprintf("[%s]", text), nil
	}, func(done, total int64) error {
		progressMu.Lock()
		progress = append(progress, [2]int64{done, total})
		progressMu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if maximum.Load() < 2 {
		t.Fatalf("expected parallel execution, max active=%d", maximum.Load())
	}
	if result["a"] != "[123]\n[456]" || result["b"] != "[abc]\n[def]" {
		t.Fatalf("unexpected ordered result: %#v", result)
	}
	if len(progress) != 4 || progress[len(progress)-1] != [2]int64{12, 12} {
		t.Fatalf("unexpected progress: %#v", progress)
	}
}
