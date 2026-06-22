package tree

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const targetSearchCacheRedisPrefix = "scan:binding_target_search_cache:"

type redisTargetSearchCacheStore struct {
	client *redis.Client
}

type redisTargetSearchCachePayload struct {
	Nodes     []TreeNode `json:"nodes"`
	Status    string     `json:"status,omitempty"`
	Complete  bool       `json:"complete"`
	Truncated bool       `json:"truncated"`
	LastError string     `json:"last_error,omitempty"`
	StaleAt   time.Time  `json:"stale_at,omitempty"`
}

func NewRedisTargetSearchCacheStore(rawURL string) (TargetSearchCacheStore, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, nil
	}
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(options)
	return &redisTargetSearchCacheStore{client: client}, nil
}

func (s *redisTargetSearchCacheStore) Get(ctx context.Context, key string) (targetSearchCacheSnapshot, bool, error) {
	if s == nil || s.client == nil {
		return targetSearchCacheSnapshot{}, false, nil
	}
	data, err := s.client.Get(ctx, s.dataKey(key)).Bytes()
	if err == redis.Nil {
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
	var payload redisTargetSearchCachePayload
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

func (s *redisTargetSearchCacheStore) Set(ctx context.Context, key string, snapshot targetSearchCacheSnapshot, staleTTL, expireTTL time.Duration) error {
	if s == nil || s.client == nil {
		return nil
	}
	if snapshot.staleAt.IsZero() {
		snapshot.staleAt = time.Now().Add(staleTTL)
	}
	if expireTTL <= 0 {
		expireTTL = targetSearchCacheExpireTTL
	}
	payload := redisTargetSearchCachePayload{
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
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, s.dataKey(key), data, expireTTL)
	pipe.Del(ctx, s.lockKey(key))
	_, err = pipe.Exec(ctx)
	if err != nil {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.client.Del(releaseCtx, s.lockKey(key)).Result()
	}
	return err
}

func (s *redisTargetSearchCacheStore) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if s == nil || s.client == nil {
		return false, nil
	}
	lockTTL := ttl
	if lockTTL <= 0 {
		lockTTL = time.Minute
	}
	return s.client.SetNX(ctx, s.lockKey(key), "1", lockTTL).Result()
}

func (s *redisTargetSearchCacheStore) hasLock(ctx context.Context, key string) (bool, error) {
	n, err := s.client.Exists(ctx, s.lockKey(key)).Result()
	return n > 0, err
}

func (p redisTargetSearchCachePayload) status() string {
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

func (s *redisTargetSearchCacheStore) dataKey(key string) string {
	return targetSearchCacheRedisPrefix + targetSearchCacheRedisKey(key) + ":data"
}

func (s *redisTargetSearchCacheStore) lockKey(key string) string {
	return targetSearchCacheRedisPrefix + targetSearchCacheRedisKey(key) + ":lock"
}
