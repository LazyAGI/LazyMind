package tree

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	scanstate "github.com/lazymind/scan_control_plane/internal/state"
)

const targetSearchCacheStatePrefix = "scan:binding_target_search_cache:"

type stateTargetSearchCacheStore struct {
	store scanstate.Store
}

type stateTargetSearchCachePayload struct {
	Nodes     []TreeNode `json:"nodes"`
	Status    string     `json:"status,omitempty"`
	Complete  bool       `json:"complete"`
	Truncated bool       `json:"truncated"`
	LastError string     `json:"last_error,omitempty"`
	StaleAt   time.Time  `json:"stale_at,omitempty"`
}

func NewStateTargetSearchCacheStore(store scanstate.Store) TargetSearchCacheStore {
	if store == nil {
		return nil
	}
	return &stateTargetSearchCacheStore{store: store}
}

func (s *stateTargetSearchCacheStore) Get(ctx context.Context, key string) (targetSearchCacheSnapshot, bool, error) {
	if s == nil || s.store == nil {
		return targetSearchCacheSnapshot{}, false, nil
	}
	data, err := s.store.Get(ctx, s.dataKey(key))
	if scanstate.IsMissing(err) {
		locked, lockErr := s.hasLock(ctx, key)
		if lockErr != nil {
			return targetSearchCacheSnapshot{}, false, lockErr
		}
		if locked {
			return targetSearchCacheSnapshot{
				status:   targetSearchCacheStatusBuilding,
				building: true,
			}, true, nil
		}
		return targetSearchCacheSnapshot{}, false, nil
	}
	if err != nil {
		return targetSearchCacheSnapshot{}, false, err
	}
	var payload stateTargetSearchCachePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return targetSearchCacheSnapshot{}, false, err
	}
	stale := !payload.StaleAt.IsZero() && time.Now().After(payload.StaleAt)
	return targetSearchCacheSnapshot{
		nodes:     append([]TreeNode(nil), payload.Nodes...),
		status:    payload.status(),
		complete:  payload.Complete,
		truncated: payload.Truncated,
		lastError: payload.LastError,
		stale:     stale,
		staleAt:   payload.StaleAt,
	}, true, nil
}

func (s *stateTargetSearchCacheStore) Set(ctx context.Context, key string, snapshot targetSearchCacheSnapshot, staleTTL, expireTTL time.Duration) error {
	if s == nil || s.store == nil {
		return nil
	}
	if snapshot.staleAt.IsZero() {
		snapshot.staleAt = time.Now().Add(staleTTL)
	}
	if expireTTL <= 0 {
		expireTTL = targetSearchCacheExpireTTL
	}
	payload := stateTargetSearchCachePayload{
		Nodes:     append([]TreeNode(nil), snapshot.nodes...),
		Status:    snapshot.status,
		Complete:  snapshot.complete,
		Truncated: snapshot.truncated,
		LastError: snapshot.lastError,
		StaleAt:   snapshot.staleAt,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := s.store.Set(ctx, s.dataKey(key), data, expireTTL); err != nil {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.store.Del(releaseCtx, s.lockKey(key))
		return err
	}
	return s.store.Del(ctx, s.lockKey(key))
}

func (s *stateTargetSearchCacheStore) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if s == nil || s.store == nil {
		return false, nil
	}
	lockTTL := ttl
	if lockTTL <= 0 {
		lockTTL = time.Minute
	}
	return s.store.SetNX(ctx, s.lockKey(key), []byte("1"), lockTTL)
}

func (s *stateTargetSearchCacheStore) hasLock(ctx context.Context, key string) (bool, error) {
	return s.store.Exists(ctx, s.lockKey(key))
}

func (p stateTargetSearchCachePayload) status() string {
	switch p.Status {
	case targetSearchCacheStatusMissing, targetSearchCacheStatusBuilding, targetSearchCacheStatusComplete, targetSearchCacheStatusFailed:
		return p.Status
	}
	if p.Complete {
		return targetSearchCacheStatusComplete
	}
	if strings.TrimSpace(p.LastError) != "" {
		return targetSearchCacheStatusFailed
	}
	return targetSearchCacheStatusMissing
}

func (s *stateTargetSearchCacheStore) dataKey(key string) string {
	return targetSearchCacheStatePrefix + targetSearchCacheStorageKey(key) + ":data"
}

func (s *stateTargetSearchCacheStore) lockKey(key string) string {
	return targetSearchCacheStatePrefix + targetSearchCacheStorageKey(key) + ":lock"
}
