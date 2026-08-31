package knowledge_market

import (
	"testing"

	"lazymind/core/common/orm"
)

func TestOverallPercentDownloadHighResolution(t *testing.T) {
	cases := []struct {
		name        string
		install     *orm.KnowledgeMarketInstall
		job         orm.AsyncJob
		wantStage   string
		wantPercent int64
	}{
		{
			name:        "high resolution start",
			install:     &orm.KnowledgeMarketInstall{InstallState: string(orm.InstallStateDownloading)},
			job:         orm.AsyncJob{Status: "running", ProgressCurrent: 0, ProgressTotal: 100},
			wantStage:   "downloading",
			wantPercent: 0,
		},
		{
			name:        "high resolution middle",
			install:     &orm.KnowledgeMarketInstall{InstallState: string(orm.InstallStateDownloading)},
			job:         orm.AsyncJob{Status: "running", ProgressCurrent: 37, ProgressTotal: 100},
			wantStage:   "downloading",
			wantPercent: 14,
		},
		{
			name:        "high resolution complete",
			install:     &orm.KnowledgeMarketInstall{InstallState: string(orm.InstallStateDownloading)},
			job:         orm.AsyncJob{Status: "running", ProgressCurrent: 100, ProgressTotal: 100},
			wantStage:   "downloading",
			wantPercent: 40,
		},
		{
			name:        "legacy stage marker start",
			install:     &orm.KnowledgeMarketInstall{InstallState: string(orm.InstallStateDownloading)},
			job:         orm.AsyncJob{Status: "running", ProgressCurrent: 0, ProgressTotal: 2},
			wantStage:   "downloading",
			wantPercent: 0,
		},
		{
			name:        "legacy stage marker complete",
			install:     &orm.KnowledgeMarketInstall{InstallState: string(orm.InstallStateDownloading)},
			job:         orm.AsyncJob{Status: "running", ProgressCurrent: 1, ProgressTotal: 2},
			wantStage:   "downloading",
			wantPercent: 40,
		},
		{
			name:        "failed high resolution download stays in download band",
			install:     &orm.KnowledgeMarketInstall{InstallState: string(orm.InstallStateFailed)},
			job:         orm.AsyncJob{Status: "failed", ProgressCurrent: 37, ProgressTotal: 100},
			wantStage:   "failed",
			wantPercent: 14,
		},
		{
			name:        "install failed while job still running is shown failed",
			install:     &orm.KnowledgeMarketInstall{InstallState: string(orm.InstallStateFailed)},
			job:         orm.AsyncJob{Status: "running", ProgressCurrent: 80, ProgressTotal: 100},
			wantStage:   "failed",
			wantPercent: 32,
		},
		{
			name:        "failed legacy download",
			install:     &orm.KnowledgeMarketInstall{InstallState: string(orm.InstallStateFailed)},
			job:         orm.AsyncJob{Status: "failed", ProgressCurrent: 0, ProgressTotal: 2},
			wantStage:   "failed",
			wantPercent: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stage, percent := installStageAndPercent(tc.job, tc.install, parseProgressInfo{})
			if stage != tc.wantStage {
				t.Fatalf("stage=%s, want %s", stage, tc.wantStage)
			}
			if percent != tc.wantPercent {
				t.Fatalf("percent=%d, want %d", percent, tc.wantPercent)
			}
		})
	}
}
