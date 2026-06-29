package resourceupdate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/state"
)

func Start(ctx context.Context, db *gorm.DB, stateStore state.Store, cfg Config) {
	cfg = normalizeConfig(cfg)
	workerID := defaultWorkerID("resourceupdate")
	resourceUpdateInfo(logEventRuntimeStarted).
		Str("worker_id", workerID).
		Bool("state_enabled", stateStore != nil).
		Dur("scheduler_interval", cfg.SchedulerTickInterval).
		Dur("worker_interval", cfg.WorkerInterval).
		Dur("scanner_interval", cfg.ScannerInterval).
		Dur("conversation_idle_seconds", cfg.ConversationIdleSeconds).
		Msg(logEventRuntimeStarted)
	scheduler := NewScheduler(db, cfg, workerID+"-scheduler")
	worker := NewWorker(db, cfg, workerID+"-worker")
	scanner := NewScanner(db, cfg, workerID+"-scanner")
	go runSchedulerLoop(ctx, scheduler, cfg.SchedulerTickInterval)
	go runWorkerLoop(ctx, worker, cfg.WorkerInterval)
	go runScannerLoop(ctx, scanner, cfg.ScannerInterval)
	if stateStore != nil {
		idleProcessor := NewIdleProcessor(db, stateStore, cfg, workerID+"-idle")
		go runIdleFallbackLoop(ctx, idleProcessor, cfg.ConversationIdleFallbackScanInterval)
		if cfg.ConversationIdleEnableExpiredKeyNotify {
			go runIdleExpiredKeyNotifyLoop(ctx, stateStore, idleProcessor)
		}
	}
}

func EnabledFromEnv() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("LAZYMIND_RESOURCE_UPDATE_ENABLED")))
	return v == "1" || v == "true" || v == "yes"
}

func runSchedulerLoop(ctx context.Context, scheduler *Scheduler, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := scheduler.RunOnce(ctx); err != nil {
			resourceUpdateWarn(logEventSchedulerTickFailed, err).
				Msg(logEventSchedulerTickFailed)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runWorkerLoop(ctx context.Context, worker *Worker, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := worker.RunOnce(ctx); err != nil {
			resourceUpdateWarn(logEventWorkerTickFailed, err).
				Msg(logEventWorkerTickFailed)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runScannerLoop(ctx context.Context, scanner *Scanner, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := scanner.RunOnce(ctx); err != nil {
			resourceUpdateWarn(logEventScannerTickFailed, err).
				Msg(logEventScannerTickFailed)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func LogStartup(enabled bool) {
	resourceUpdateInfo(logEventRuntimeStarted).
		Bool("enabled", enabled).
		Msg(fmt.Sprintf("resource update runtime enabled=%v", enabled))
}
