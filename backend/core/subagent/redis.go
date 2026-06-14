package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// taskStreamKeyPrefix holds the LIST of Task SSE events for replay + tail.
	taskStreamKeyPrefix = "rag/subagent/stream:%s"
	// taskStatusKeyPrefix holds a HASH snapshot of the latest task status (derived cache).
	taskStatusKeyPrefix = "rag/subagent/status:%s"

	taskStreamExpire = 2 * time.Hour
	taskStatusExpire = 2 * time.Hour
)

func taskStreamKey(taskID string) string { return fmt.Sprintf(taskStreamKeyPrefix, taskID) }
func taskStatusKey(taskID string) string { return fmt.Sprintf(taskStatusKeyPrefix, taskID) }

// WriteStatus upserts the status snapshot HASH (status / progress / current_phase / summary).
func WriteStatus(ctx context.Context, rdb *redis.Client, taskID string, fields map[string]any) error {
	if rdb == nil {
		return nil
	}
	key := taskStatusKey(taskID)
	if err := rdb.HSet(ctx, key, fields).Err(); err != nil {
		return err
	}
	return rdb.Expire(ctx, key, taskStatusExpire).Err()
}

// ReadStatus returns the status snapshot HASH (empty map if missing).
func ReadStatus(ctx context.Context, rdb *redis.Client, taskID string) (map[string]string, error) {
	if rdb == nil {
		return nil, nil
	}
	return rdb.HGetAll(ctx, taskStatusKey(taskID)).Result()
}

// AppendStreamEvent RPUSHes one Task SSE event JSON onto the stream LIST.
func AppendStreamEvent(ctx context.Context, rdb *redis.Client, taskID string, event any) error {
	if rdb == nil {
		return nil
	}
	bs, err := json.Marshal(event)
	if err != nil {
		return err
	}
	key := taskStreamKey(taskID)
	if err := rdb.RPush(ctx, key, bs).Err(); err != nil {
		return err
	}
	return rdb.Expire(ctx, key, taskStreamExpire).Err()
}

// StreamEventsFrom returns raw event JSON strings from offset (0-based) to tail.
func StreamEventsFrom(ctx context.Context, rdb *redis.Client, taskID string, from int64) ([]string, error) {
	if rdb == nil {
		return nil, nil
	}
	return rdb.LRange(ctx, taskStreamKey(taskID), from, -1).Result()
}

// StreamExists reports whether the stream LIST key still exists (not expired).
func StreamExists(ctx context.Context, rdb *redis.Client, taskID string) (bool, error) {
	if rdb == nil {
		return false, nil
	}
	n, err := rdb.Exists(ctx, taskStreamKey(taskID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
