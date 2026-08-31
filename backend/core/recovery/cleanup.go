package recovery

import (
	"context"
	"time"

	"gorm.io/gorm"

	"lazymind/core/chat"
	"lazymind/core/log"
	skillv2handler "lazymind/core/skillv2/handler"
	"lazymind/core/workflow"
)

const DefaultCleanupInterval = 24 * time.Hour

// RunCleanup applies the manual permanent-delete domain operation for every
// expired object. Resource classes are isolated and each implementation also
// isolates individual objects.
func RunCleanup(ctx context.Context, db *gorm.DB, now time.Time) {
	type result struct {
		resource string
		purged   int
		failed   int
	}
	results := []result{}
	purged, failed := skillv2handler.PurgeExpiredTrash(ctx, db, now)
	results = append(results, result{"skills", purged, failed})
	purged, failed = chat.PurgeExpiredConversationTrash(ctx, db, now)
	results = append(results, result{"conversations", purged, failed})
	purged, failed = workflow.PurgeExpiredWorkflowTrash(ctx, db, now)
	results = append(results, result{"workflow_drafts", purged, failed})
	for _, item := range results {
		event := log.Logger.Info()
		if item.failed > 0 {
			event = log.Logger.Warn()
		}
		event.Str("resource", item.resource).Int("purged", item.purged).Int("failed", item.failed).
			Msg("recovery retention cleanup completed")
	}
}

// Start executes once during Core startup and then every interval until the
// process context is canceled.
func Start(ctx context.Context, db *gorm.DB, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultCleanupInterval
	}
	go func() {
		RunCleanup(ctx, db, time.Now().UTC())
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				RunCleanup(ctx, db, now.UTC())
			}
		}
	}()
}
