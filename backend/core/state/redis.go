package state

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(client *redis.Client) *RedisStore {
	if client == nil {
		return nil
	}
	return &RedisStore{client: client}
}

func MustRedisFromEnv() *RedisStore {
	return NewRedisStore(MustRedisClientFromEnv())
}

func NewRedisStoreFromURL(raw string) (*RedisStore, error) {
	client, err := NewRedisClientFromURL(raw)
	if err != nil {
		return nil, err
	}
	return NewRedisStore(client), nil
}

func NewRedisClientFromURL(raw string) (*redis.Client, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if u.Scheme != "redis" {
		return nil, fmt.Errorf("redis url scheme must be redis")
	}
	addr := u.Host
	pass, _ := u.User.Password()
	dbIndex := 0
	if p := strings.TrimPrefix(strings.TrimSpace(u.Path), "/"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n >= 0 {
			dbIndex = n
		}
	}
	c := redis.NewClient(&redis.Options{Addr: addr, Password: pass, DB: dbIndex})
	if err := c.Ping(context.Background()).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	return c, nil
}

func MustRedisClientFromEnv() *redis.Client {
	if raw := strings.TrimSpace(os.Getenv("LAZYMIND_REDIS_URL")); raw != "" {
		c, err := NewRedisClientFromURL(raw)
		if err != nil {
			panic(err)
		}
		return c
	}

	addr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	if addr == "" {
		addr = "redis:6379"
	}
	c := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("REDIS_PASSWORD"), DB: 0})
	if err := c.Ping(context.Background()).Err(); err != nil {
		panic(fmt.Errorf("redis ping failed: %w", err))
	}
	return c
}

func (s *RedisStore) HSet(ctx context.Context, key string, fields map[string]any, ttl time.Duration) error {
	if err := s.client.HSet(ctx, key, fields).Err(); err != nil {
		return err
	}
	if ttl > 0 {
		return s.client.Expire(ctx, key, ttl).Err()
	}
	return nil
}

func (s *RedisStore) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return s.client.HGetAll(ctx, key).Result()
}

func (s *RedisStore) HGet(ctx context.Context, key, field string) ([]byte, error) {
	return s.client.HGet(ctx, key, field).Bytes()
}

func (s *RedisStore) HDel(ctx context.Context, key string, fields ...string) error {
	return s.client.HDel(ctx, key, fields...).Err()
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

func (s *RedisStore) RPush(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := s.client.RPush(ctx, key, value).Err(); err != nil {
		return err
	}
	if ttl > 0 {
		return s.client.Expire(ctx, key, ttl).Err()
	}
	return nil
}

func (s *RedisStore) LPush(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := s.client.LPush(ctx, key, value).Err(); err != nil {
		return err
	}
	if ttl > 0 {
		return s.client.Expire(ctx, key, ttl).Err()
	}
	return nil
}

func (s *RedisStore) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return s.client.LRange(ctx, key, start, stop).Result()
}

func (s *RedisStore) LTrim(ctx context.Context, key string, start, stop int64) error {
	return s.client.LTrim(ctx, key, start, stop).Err()
}

func (s *RedisStore) BLPop(ctx context.Context, key string, timeout time.Duration) error {
	_, err := s.client.BLPop(ctx, timeout, key).Result()
	return err
}

func (s *RedisStore) ZAdd(ctx context.Context, key, member string, score float64, ttl time.Duration) error {
	if err := s.client.ZAdd(ctx, key, redis.Z{Score: score, Member: member}).Err(); err != nil {
		return err
	}
	if ttl > 0 {
		return s.client.Expire(ctx, key, ttl).Err()
	}
	return nil
}

func (s *RedisStore) ZRemRangeByScore(ctx context.Context, key string, min, max float64) error {
	return s.client.ZRemRangeByScore(ctx, key, fmt.Sprintf("%f", min), fmt.Sprintf("%f", max)).Err()
}

func (s *RedisStore) ZCard(ctx context.Context, key string) (int64, error) {
	return s.client.ZCard(ctx, key).Result()
}

func (s *RedisStore) SubscribeExpiredKeys(ctx context.Context, onExpired func(key string) error) {
	if s == nil || s.client == nil || onExpired == nil {
		return
	}
	channel := fmt.Sprintf("__keyevent@%d__:expired", s.client.Options().DB)
	for ctx.Err() == nil {
		pubsub := s.client.Subscribe(ctx, channel)
		msgCh := pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				_ = pubsub.Close()
				return
			case msg, ok := <-msgCh:
				if !ok {
					_ = pubsub.Close()
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Second):
					}
					goto reconnect
				}
				_ = onExpired(msg.Payload)
			}
		}
	reconnect:
		continue
	}
}

func (s *RedisStore) Close() error {
	return s.client.Close()
}
