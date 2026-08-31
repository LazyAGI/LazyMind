package knowledge_market

import (
	"context"
	"time"

	"lazymind/core/asyncjob"
)

const (
	downloadProgressTotal       = int64(100)
	downloadProgressStep        = int64(2)
	downloadProgressMinInterval = time.Second
)

// marketDownloadProgress adapts the download package's byte/percent callback
// into async_jobs progress updates. It throttles writes, keeps the progress
// monotonic across fetch retries, and uses total=100 while the install is in
// the downloading state.
type marketDownloadProgress struct {
	ctx         context.Context
	reporter    asyncjob.Reporter
	lastPercent int64
	lastReport  time.Time
}

func newMarketDownloadProgress(ctx context.Context, reporter asyncjob.Reporter) *marketDownloadProgress {
	if reporter == nil {
		return nil
	}
	return &marketDownloadProgress{ctx: ctx, reporter: reporter}
}

// Report implements download.ProgressFunc for the market install/update
// pipelines. total <= 0 means the total is unknown, so no real progress update
// is written.
func (p *marketDownloadProgress) Report(done, total int64) {
	if p == nil || p.reporter == nil || total <= 0 {
		return
	}
	percent := done * 100 / total
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	// Retries in download.Fetch can restart from zero; never let the visible
	// progress move backwards.
	if percent < p.lastPercent {
		return
	}
	if percent == p.lastPercent && time.Since(p.lastReport) < downloadProgressMinInterval {
		return
	}
	if percent-p.lastPercent < downloadProgressStep &&
		percent < downloadProgressTotal &&
		time.Since(p.lastReport) < downloadProgressMinInterval {
		return
	}

	_ = p.reporter.SetProgress(p.ctx, percent, downloadProgressTotal)
	p.lastPercent = percent
	p.lastReport = time.Now()
}
