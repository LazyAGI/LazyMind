package doc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReplayedTranslationKeepsSharedArtifactFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retained.pdf")
	if err := os.WriteFile(path, []byte("saved"), 0600); err != nil {
		t.Fatal(err)
	}
	old := pdfArtifactRecord{ID: "old", StoredPath: path, SourcePath: path}
	current := pdfArtifactRecord{ID: "new", StoredPath: path, SourcePath: path}
	removeReplacedTranslationFiles([]pdfArtifactRecord{old}, current)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("retry deleted retained artifact", err)
	}
}

func TestCanceledTranslationCannotProducePartialSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := translateUnitsParallel(ctx, []translationWorkUnit{{ID: "one", Text: "hello"}}, 0, 1,
		func(ctx context.Context, text string) (string, error) { return "", ctx.Err() }, func(int64, int64) error { return nil })
	if !errors.Is(err, context.Canceled) || result != nil {
		t.Fatalf("canceled job returned success: %v %v", result, err)
	}
}

func TestTranslationCheckpointSurvivesRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "part")
	calls := 0
	translate := func() (string, error) { calls++; return "完整译文", nil }
	for range 2 {
		got, err := cachedTranslation(path, translate)
		if err != nil || got != "完整译文" {
			t.Fatalf("%q %v", got, err)
		}
	}
	if calls != 1 {
		t.Fatalf("repeated successful request: %d", calls)
	}
}

func TestTranslationCheckpointDoesNotCacheFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "part")
	for _, fn := range []func() (string, error){func() (string, error) { return "", errors.New("429") }, func() (string, error) { return "", nil }} {
		if _, err := cachedTranslation(path, fn); err == nil {
			t.Fatal("accepted failed or empty translation")
		}
	}
	got, err := cachedTranslation(path, func() (string, error) { return "恢复后的译文", nil })
	if err != nil || got != "恢复后的译文" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestRetiredTranslationRemovesCheckpoints(t *testing.T) {
	for _, operation := range []string{"replace", "delete", "replay"} {
		t.Run(operation, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source.pdf")
			if err := os.WriteFile(source, []byte("source"), 0600); err != nil {
				t.Fatal(err)
			}
			checkpoint := filepath.Join(source+".translation-cache", "chunk.txt")
			if _, err := cachedTranslation(checkpoint, func() (string, error) { return "translation", nil }); err != nil {
				t.Fatal(err)
			}
			output := source + ".translated.pdf"
			diagnostics := output + ".diagnostics.json"
			if err := os.WriteFile(diagnostics, []byte("diagnostics"), 0600); err != nil {
				t.Fatal(err)
			}
			old := pdfArtifactRecord{ID: "old", SourcePath: source, StoredPath: output}
			switch operation {
			case "replace":
				removeReplacedTranslationFiles([]pdfArtifactRecord{old}, pdfArtifactRecord{ID: "new", SourcePath: source + ".new"})
			case "delete":
				removeArtifactFiles([]pdfArtifactRecord{old})
			case "replay":
				removeReplacedTranslationFiles([]pdfArtifactRecord{old}, pdfArtifactRecord{ID: "new", SourcePath: source, StoredPath: output})
			}
			_, diagnosticErr := os.Stat(diagnostics)
			if operation == "replay" {
				if diagnosticErr != nil {
					t.Fatalf("shared diagnostics removed: %v", diagnosticErr)
				}
			} else if !os.IsNotExist(diagnosticErr) {
				t.Fatalf("retired diagnostics remain: %v", diagnosticErr)
			}
			_, err := os.Stat(source + ".translation-cache")
			if operation == "replay" {
				if err != nil {
					t.Fatalf("shared checkpoints removed: %v", err)
				}
				if data, err := os.ReadFile(checkpoint); err != nil || string(data) != "translation" {
					t.Fatalf("shared checkpoint lost: %q %v", data, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("retired checkpoint directory remains: %v", err)
			}
		})
	}
}

func TestEmptyArtifactPathsDoNotRemoveUnrelatedSidecars(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, path := range []string{".diagnostics.json", ".translation-cache"} {
		if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	removeArtifactFiles([]pdfArtifactRecord{{}})
	removeReplacedTranslationFiles([]pdfArtifactRecord{{}}, pdfArtifactRecord{})
	for _, path := range []string{".diagnostics.json", ".translation-cache"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
			t.Fatalf("unrelated file removed: %s %v", path, err)
		}
	}
}
