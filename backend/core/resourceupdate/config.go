package resourceupdate

import "time"

type Stage struct {
	Window    time.Duration
	Interval  time.Duration
	Successes int
}

type Config struct {
	SchedulerTickInterval time.Duration
	SchedulerLockTTL      time.Duration
	SchedulerRetryDelay   time.Duration
	SchedulerBatchSize    int

	MinUserTurns          int
	MinToolTurns          int
	QuantityCheckInterval time.Duration
	MinInterval           time.Duration
	MaxWindow             time.Duration
	Stages                []Stage

	WorkerInterval   time.Duration
	WorkerLockTTL    time.Duration
	WorkerBatchSize  int
	MaxAttempts      int
	RetryBackoffBase time.Duration
	RetryBackoffMax  time.Duration

	ScannerInterval time.Duration

	ConversationIdleSeconds                time.Duration
	ConversationIdleHistoryTTL             time.Duration
	ConversationIdleHistoryMaxMessages     int
	ConversationIdleFallbackScanInterval   time.Duration
	ConversationIdleFallbackBatchSize      int
	ConversationIdleEnableExpiredKeyNotify bool

	conversationIdleEnableExpiredKeyNotifySet bool
}

func DefaultConfig() Config {
	return Config{
		SchedulerTickInterval: 60 * time.Second,
		SchedulerLockTTL:      5 * time.Minute,
		SchedulerRetryDelay:   5 * time.Minute,
		SchedulerBatchSize:    100,

		MinUserTurns:          3,
		MinToolTurns:          8,
		QuantityCheckInterval: 5 * time.Minute,
		MinInterval:           time.Hour,
		MaxWindow:             7 * 24 * time.Hour,
		Stages: []Stage{
			{Window: 3 * time.Hour, Interval: 3 * time.Hour, Successes: 3},
			{Window: 24 * time.Hour, Interval: 24 * time.Hour, Successes: 7},
			{Window: 3 * 24 * time.Hour, Interval: 3 * 24 * time.Hour, Successes: 0},
		},

		WorkerInterval:   2 * time.Second,
		WorkerLockTTL:    5 * time.Minute,
		WorkerBatchSize:  10,
		MaxAttempts:      3,
		RetryBackoffBase: time.Second,
		RetryBackoffMax:  4 * time.Second,

		ScannerInterval: 5 * time.Second,

		ConversationIdleSeconds:                5 * time.Minute,
		ConversationIdleHistoryTTL:             30 * time.Minute,
		ConversationIdleHistoryMaxMessages:     100,
		ConversationIdleFallbackScanInterval:   5 * time.Minute,
		ConversationIdleFallbackBatchSize:      100,
		ConversationIdleEnableExpiredKeyNotify: true,
	}
}

func normalizeConfig(cfg Config) Config {
	def := DefaultConfig()
	if cfg.SchedulerTickInterval <= 0 {
		cfg.SchedulerTickInterval = def.SchedulerTickInterval
	}
	if cfg.SchedulerLockTTL <= 0 {
		cfg.SchedulerLockTTL = def.SchedulerLockTTL
	}
	if cfg.SchedulerRetryDelay <= 0 {
		cfg.SchedulerRetryDelay = def.SchedulerRetryDelay
	}
	if cfg.SchedulerBatchSize <= 0 {
		cfg.SchedulerBatchSize = def.SchedulerBatchSize
	}
	if cfg.MinUserTurns < 0 {
		cfg.MinUserTurns = def.MinUserTurns
	}
	if cfg.MinToolTurns < 0 {
		cfg.MinToolTurns = def.MinToolTurns
	}
	if cfg.QuantityCheckInterval <= 0 {
		cfg.QuantityCheckInterval = def.QuantityCheckInterval
	}
	if cfg.MinInterval <= 0 {
		cfg.MinInterval = def.MinInterval
	}
	if cfg.MaxWindow <= 0 {
		cfg.MaxWindow = def.MaxWindow
	}
	if len(cfg.Stages) == 0 {
		cfg.Stages = def.Stages
	}
	if cfg.WorkerInterval <= 0 {
		cfg.WorkerInterval = def.WorkerInterval
	}
	if cfg.WorkerLockTTL <= 0 {
		cfg.WorkerLockTTL = def.WorkerLockTTL
	}
	if cfg.WorkerBatchSize <= 0 {
		cfg.WorkerBatchSize = def.WorkerBatchSize
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = def.MaxAttempts
	}
	if cfg.RetryBackoffBase <= 0 {
		cfg.RetryBackoffBase = def.RetryBackoffBase
	}
	if cfg.RetryBackoffMax <= 0 {
		cfg.RetryBackoffMax = def.RetryBackoffMax
	}
	if cfg.ScannerInterval <= 0 {
		cfg.ScannerInterval = def.ScannerInterval
	}
	if cfg.ConversationIdleSeconds <= 0 {
		cfg.ConversationIdleSeconds = def.ConversationIdleSeconds
	}
	if cfg.ConversationIdleHistoryTTL <= 0 {
		cfg.ConversationIdleHistoryTTL = def.ConversationIdleHistoryTTL
	}
	if cfg.ConversationIdleHistoryMaxMessages <= 0 {
		cfg.ConversationIdleHistoryMaxMessages = def.ConversationIdleHistoryMaxMessages
	}
	if cfg.ConversationIdleFallbackScanInterval <= 0 {
		cfg.ConversationIdleFallbackScanInterval = def.ConversationIdleFallbackScanInterval
	}
	if cfg.ConversationIdleFallbackBatchSize <= 0 {
		cfg.ConversationIdleFallbackBatchSize = def.ConversationIdleFallbackBatchSize
	}
	if !cfg.conversationIdleEnableExpiredKeyNotifySet {
		cfg.ConversationIdleEnableExpiredKeyNotify = def.ConversationIdleEnableExpiredKeyNotify
	}
	return cfg
}

func (cfg Config) WithConversationIdleExpiredKeyNotify(enabled bool) Config {
	cfg.ConversationIdleEnableExpiredKeyNotify = enabled
	cfg.conversationIdleEnableExpiredKeyNotifySet = true
	return cfg
}
