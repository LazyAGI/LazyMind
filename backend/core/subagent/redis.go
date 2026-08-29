package subagent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"lazymind/core/state"
)

const subagentStreamDirEnv = "LAZYMIND_SUBAGENT_STREAM_DIR"

var (
	localTaskStreamLocks       [64]sync.RWMutex
	localTaskStreamCleanupMu   sync.Mutex
	lastLocalTaskStreamCleanup time.Time
	localTaskStreamsMigrated   sync.Map
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

func useLocalTaskStreamFiles(stateStore state.Store) bool {
	if stateStore == nil {
		return false
	}
	_, ok := stateStore.(*state.SQLiteStore)
	return ok
}

func localTaskStreamDir() string {
	if dir := os.Getenv(subagentStreamDirEnv); dir != "" {
		return dir
	}
	return filepath.Join(filepath.Dir(state.DefaultSQLitePath()), "subagent-streams")
}

func localTaskStreamPath(taskID string) string {
	sum := sha256.Sum256([]byte(taskID))
	return filepath.Join(localTaskStreamDir(), fmt.Sprintf("%x.ndjson", sum))
}

func localTaskStreamLock(taskID string) *sync.RWMutex {
	sum := sha256.Sum256([]byte(taskID))
	return &localTaskStreamLocks[int(sum[0])%len(localTaskStreamLocks)]
}

func cleanupExpiredLocalTaskStreams(now time.Time) {
	dir := localTaskStreamDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := now.Add(-taskStreamExpire)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".ndjson" {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

func maybeCleanupExpiredLocalTaskStreams(now time.Time) {
	localTaskStreamCleanupMu.Lock()
	defer localTaskStreamCleanupMu.Unlock()
	if !lastLocalTaskStreamCleanup.IsZero() && now.Sub(lastLocalTaskStreamCleanup) < time.Minute {
		return
	}
	cleanupExpiredLocalTaskStreams(now)
	lastLocalTaskStreamCleanup = now
}

func readLocalTaskStreamFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(nil, 16*1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func hasStringPrefix(values, prefix []string) bool {
	if len(values) < len(prefix) {
		return false
	}
	for index := range prefix {
		if values[index] != prefix[index] {
			return false
		}
	}
	return true
}

// migrateLegacyLocalTaskStream moves pre-file Local streams out of SQLite on
// first access. Writing the file before deleting the LIST makes an interrupted
// migration retryable; the prefix check prevents duplicate events on retry.
func migrateLegacyLocalTaskStream(ctx context.Context, stateStore state.Store, taskID string) error {
	migrationKey := localTaskStreamDir() + "\x00" + taskID
	if _, migrated := localTaskStreamsMigrated.Load(migrationKey); migrated {
		return nil
	}
	legacy, err := stateStore.LRange(ctx, taskStreamKey(taskID), 0, -1)
	if err != nil {
		return err
	}
	if len(legacy) > 0 {
		dir := localTaskStreamDir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		path := localTaskStreamPath(taskID)
		existing, err := readLocalTaskStreamFile(path)
		if err != nil {
			return err
		}
		if !hasStringPrefix(existing, legacy) {
			existing = append(append(make([]string, 0, len(legacy)+len(existing)), legacy...), existing...)
			temp, err := os.CreateTemp(dir, ".subagent-stream-migration-*")
			if err != nil {
				return err
			}
			tempPath := temp.Name()
			defer os.Remove(tempPath)
			if err := temp.Chmod(0o600); err != nil {
				_ = temp.Close()
				return err
			}
			writer := bufio.NewWriter(temp)
			for _, line := range existing {
				if _, err := writer.WriteString(line + "\n"); err != nil {
					_ = temp.Close()
					return err
				}
			}
			if err := writer.Flush(); err != nil {
				_ = temp.Close()
				return err
			}
			if err := temp.Sync(); err != nil {
				_ = temp.Close()
				return err
			}
			if err := temp.Close(); err != nil {
				return err
			}
			if err := os.Rename(tempPath, path); err != nil {
				return err
			}
		}
	}
	if err := stateStore.Del(ctx, taskStreamKey(taskID)); err != nil {
		return err
	}
	localTaskStreamsMigrated.Store(migrationKey, struct{}{})
	return nil
}

func appendLocalTaskStreamEvent(ctx context.Context, stateStore state.Store, taskID string, bs []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lock := localTaskStreamLock(taskID)
	lock.Lock()
	defer lock.Unlock()
	if err := migrateLegacyLocalTaskStream(ctx, stateStore, taskID); err != nil {
		return err
	}
	dir := localTaskStreamDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	maybeCleanupExpiredLocalTaskStreams(time.Now())
	f, err := os.OpenFile(localTaskStreamPath(taskID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	line := append(append([]byte(nil), bs...), '\n')
	_, err = f.Write(line)
	return err
}

func localTaskStreamEventsFrom(ctx context.Context, stateStore state.Store, taskID string, from int64) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lock := localTaskStreamLock(taskID)
	lock.Lock()
	defer lock.Unlock()
	if err := migrateLegacyLocalTaskStream(ctx, stateStore, taskID); err != nil {
		return nil, err
	}
	path := localTaskStreamPath(taskID)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.ModTime().Before(time.Now().Add(-taskStreamExpire)) {
		_ = os.Remove(path)
		return nil, nil
	}
	lines, err := readLocalTaskStreamFile(path)
	if err != nil {
		return nil, err
	}
	if from >= int64(len(lines)) {
		return nil, nil
	}
	if from < 0 {
		from = 0
	}
	return lines[from:], nil
}

func localTaskStreamExists(ctx context.Context, stateStore state.Store, taskID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	lock := localTaskStreamLock(taskID)
	lock.Lock()
	defer lock.Unlock()
	if err := migrateLegacyLocalTaskStream(ctx, stateStore, taskID); err != nil {
		return false, err
	}
	path := localTaskStreamPath(taskID)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.ModTime().Before(time.Now().Add(-taskStreamExpire)) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		return false, nil
	}
	return info.Mode().IsRegular(), nil
}

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

// AppendStreamEvent appends one Task SSE event to the replay transport. Redis
// uses a LIST; local SQLite mode uses an NDJSON file to avoid writing token
// deltas into core_state.db.
func AppendStreamEvent(ctx context.Context, stateStore state.Store, taskID string, event any) error {
	if stateStore == nil {
		return nil
	}
	bs, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if useLocalTaskStreamFiles(stateStore) {
		return appendLocalTaskStreamEvent(ctx, stateStore, taskID, bs)
	}
	key := taskStreamKey(taskID)
	return stateStore.RPush(ctx, key, bs, taskStreamExpire)
}

// StreamEventsFrom returns raw event JSON strings from offset (0-based) to tail.
func StreamEventsFrom(ctx context.Context, stateStore state.Store, taskID string, from int64) ([]string, error) {
	if stateStore == nil {
		return nil, nil
	}
	if useLocalTaskStreamFiles(stateStore) {
		return localTaskStreamEventsFrom(ctx, stateStore, taskID, from)
	}
	return stateStore.LRange(ctx, taskStreamKey(taskID), from, -1)
}

// StreamExists reports whether the replay stream still exists (not expired).
func StreamExists(ctx context.Context, stateStore state.Store, taskID string) (bool, error) {
	if stateStore == nil {
		return false, nil
	}
	if useLocalTaskStreamFiles(stateStore) {
		return localTaskStreamExists(ctx, stateStore, taskID)
	}
	return stateStore.Exists(ctx, taskStreamKey(taskID))
}
