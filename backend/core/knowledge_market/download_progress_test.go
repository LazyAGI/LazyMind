package knowledge_market

import (
	"context"
	"testing"
)

type fakeProgressReporter struct {
	current []int64
	total   []int64
}

func (f *fakeProgressReporter) SetProgress(_ context.Context, current, total int64) error {
	f.current = append(f.current, current)
	f.total = append(f.total, total)
	return nil
}

func (f *fakeProgressReporter) Heartbeat(_ context.Context) error {
	return nil
}

func TestMarketDownloadProgressReport(t *testing.T) {
	reporter := &fakeProgressReporter{}
	progress := newMarketDownloadProgress(context.Background(), reporter)

	progress.Report(50, 100)
	progress.Report(100, 100)

	if len(reporter.current) != 2 {
		t.Fatalf("reports=%d, want 2", len(reporter.current))
	}
	if reporter.current[0] != 50 || reporter.total[0] != 100 {
		t.Fatalf("first report=%d/%d, want 50/100", reporter.current[0], reporter.total[0])
	}
	if reporter.current[1] != 100 || reporter.total[1] != 100 {
		t.Fatalf("last report=%d/%d, want 100/100", reporter.current[1], reporter.total[1])
	}
}

func TestMarketDownloadProgressMonotonic(t *testing.T) {
	reporter := &fakeProgressReporter{}
	progress := newMarketDownloadProgress(context.Background(), reporter)

	progress.Report(50, 100)
	progress.Report(25, 100) // fetch retry restarted: must not move backwards
	progress.Report(75, 100)

	if len(reporter.current) != 2 {
		t.Fatalf("reports=%d, want 2: %v", len(reporter.current), reporter.current)
	}
	if reporter.current[1] != 75 {
		t.Fatalf("last report=%d, want 75", reporter.current[1])
	}
}

func TestMarketDownloadProgressUnknownTotal(t *testing.T) {
	reporter := &fakeProgressReporter{}
	progress := newMarketDownloadProgress(context.Background(), reporter)

	progress.Report(10, -1)
	progress.Report(10, 0)

	if len(reporter.current) != 0 {
		t.Fatalf("unknown total should not report, got %v", reporter.current)
	}
}
