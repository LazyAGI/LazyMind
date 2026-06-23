package state

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	client *redis.Client
}

func NewRedisStoreFromURL(raw string) (*RedisStore, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if u.Scheme != "redis" {
		return nil, fmt.Errorf("redis url scheme must be redis")
	}
	pass, _ := u.User.Password()
	dbIndex := 0
	if p := strings.TrimPrefix(strings.TrimSpace(u.Path), "/"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n >= 0 {
			dbIndex = n
		}
	}
	c := redis.NewClient(&redis.Options{Addr: u.Host, Password: pass, DB: dbIndex})
	if err := c.Ping(context.Background()).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	return &RedisStore{client: c}, nil
}

func (s *RedisStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return s.client.Set(ctx, key, value, ttl).Err()
}

func (s *RedisStore) Get(ctx context.Context, key string) ([]byte, error) {
	return s.client.Get(ctx, key).Bytes()
}

func (s *RedisStore) Del(ctx context.Context, keys ...string) error {
	return s.client.Del(ctx, keys...).Err()
}

func (s *RedisStore) Exists(ctx context.Context, key string) (bool, error) {
	n, err := s.client.Exists(ctx, key).Result()
	return n > 0, err
}

func (s *RedisStore) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return s.client.SetNX(ctx, key, value, ttl).Result()
}

func (s *RedisStore) Close() error {
	return s.client.Close()
}
