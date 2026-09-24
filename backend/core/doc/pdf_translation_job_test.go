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

func TestBuildPDFTranslationDraftPreservesSourceLayoutAndTranslation(t *testing.T) {
	manifest := translationLayoutManifest{Version: 2, Blocks: []translationLayoutBlock{{
		ID: "block-1", Page: 2, PageWidth: 600, PageHeight: 800,
		BBox: []float64{10, 20, 300, 80}, Type: "native_text_block", Text: "Source paragraph",
	}}}
	draft := buildPDFTranslationDraft("artifact-1", "", "zh", manifest, map[string]string{"block-1": "译文段落"})
	if draft.ArtifactID != "artifact-1" || len(draft.Blocks) != 1 {
		t.Fatalf("unexpected draft: %#v", draft)
	}
	block := draft.Blocks[0]
	if block.SourceText != "Source paragraph" || block.TranslatedText != "译文段落" || block.Page != 2 || len(block.BBox) != 4 {
		t.Fatalf("unexpected draft block: %#v", block)
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
