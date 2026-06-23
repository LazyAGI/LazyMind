package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"lazymind/core/state"
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
func WriteStatus(ctx context.Context, stateStore state.Store, taskID string, fields map[string]any) error {
	if stateStore == nil {
		return nil
	}
	key := taskStatusKey(taskID)
	if err := stateStore.HSet(ctx, key, fields, taskStatusExpire); err != nil {
		return err
	}
	return nil
}

// ReadStatus returns the status snapshot HASH (empty map if missing).
func ReadStatus(ctx context.Context, stateStore state.Store, taskID string) (map[string]string, error) {
	if stateStore == nil {
		return nil, nil
	}
	return stateStore.HGetAll(ctx, taskStatusKey(taskID))
}

// AppendStreamEvent RPUSHes one Task SSE event JSON onto the stream LIST.
func AppendStreamEvent(ctx context.Context, stateStore state.Store, taskID string, event any) error {
	if stateStore == nil {
		return nil
	}
	bs, err := json.Marshal(event)
	if err != nil {
		return err
	}
	key := taskStreamKey(taskID)
	return stateStore.RPush(ctx, key, bs, taskStreamExpire)
}

// StreamEventsFrom returns raw event JSON strings from offset (0-based) to tail.
func StreamEventsFrom(ctx context.Context, stateStore state.Store, taskID string, from int64) ([]string, error) {
	if stateStore == nil {
		return nil, nil
	}
	return stateStore.LRange(ctx, taskStreamKey(taskID), from, -1)
}

// StreamExists reports whether the stream LIST key still exists (not expired).
func StreamExists(ctx context.Context, stateStore state.Store, taskID string) (bool, error) {
	if stateStore == nil {
		return false, nil
	}
	return stateStore.Exists(ctx, taskStreamKey(taskID))
}
