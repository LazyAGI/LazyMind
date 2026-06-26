package state

import (
	"context"
	"time"
)

// Store is the short-lived shared state backend formerly backed only by Redis.
// It intentionally exposes only the operations LazyMind uses.
type Store interface {
	HSet(ctx context.Context, key string, fields map[string]any, ttl time.Duration) error
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	HGet(ctx context.Context, key, field string) ([]byte, error)
	HDel(ctx context.Context, key string, fields ...string) error

	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Get(ctx context.Context, key string) ([]byte, error)
	Del(ctx context.Context, keys ...string) error
	Exists(ctx context.Context, key string) (bool, error)
	SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)

	RPush(ctx context.Context, key string, value []byte, ttl time.Duration) error
	LPush(ctx context.Context, key string, value []byte, ttl time.Duration) error
	LRange(ctx context.Context, key string, start, stop int64) ([]string, error)
	LTrim(ctx context.Context, key string, start, stop int64) error
	BLPop(ctx context.Context, key string, timeout time.Duration) error

	ZAdd(ctx context.Context, key, member string, score float64, ttl time.Duration) error
	ZRemRangeByScore(ctx context.Context, key string, min, max float64) error
	ZCard(ctx context.Context, key string) (int64, error)

	Close() error
}

// ExpiredKeyNotifier is an optional capability for backends that can push
// key-expiry events. Callers must keep fallback scans for backends without it.
type ExpiredKeyNotifier interface {
	SubscribeExpiredKeys(ctx context.Context, onExpired func(key string) error)
}
