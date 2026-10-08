package cache

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// JSON is an optional, fail-open cache. Never use it for ownership checks,
// mutable job state or the only copy of student answers.
// Use a dedicated client: queue BRPOP needs very different timeouts.
type JSON struct {
	client     *redis.Client
	logger     *slog.Logger
	retryAfter atomic.Int64
}

func NewJSON(redisURL string, logger *slog.Logger) (*JSON, error) {
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	options.DialTimeout = 100 * time.Millisecond
	options.ReadTimeout = 100 * time.Millisecond
	options.WriteTimeout = 100 * time.Millisecond
	options.PoolTimeout = 100 * time.Millisecond
	options.ContextTimeoutEnabled = true
	options.MaxRetries = -1
	return &JSON{client: redis.NewClient(options), logger: logger}, nil
}

func (c *JSON) Close() error { return c.client.Close() }

// GetMany performs one round trip even when a review needs many versions.
func (c *JSON) GetMany(ctx context.Context, keys []string) map[string]string {
	result := make(map[string]string)
	if c == nil || len(keys) == 0 || time.Now().UnixNano() < c.retryAfter.Load() {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	values, err := c.client.MGet(ctx, keys...).Result()
	if err != nil {
		c.failed(ctx, err)
		return result
	}
	for i, value := range values {
		if data, ok := value.(string); ok && len(data) <= 4<<20 {
			result[keys[i]] = data
		}
	}
	return result
}

func (c *JSON) PutMany(ctx context.Context, values map[string]any, ttl time.Duration) {
	if c == nil || len(values) == 0 || time.Now().UnixNano() < c.retryAfter.Load() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	pipe := c.client.Pipeline()
	for key, value := range values {
		data, err := json.Marshal(value)
		if err != nil || len(data) > 4<<20 {
			continue
		}
		pipe.Set(ctx, key, data, ttl)
	}
	if pipe.Len() == 0 {
		return
	}
	if _, err := pipe.Exec(ctx); err != nil {
		c.failed(ctx, err)
	}
}

func (c *JSON) failed(ctx context.Context, err error) {
	if ctx.Err() == context.Canceled {
		return
	}
	now := time.Now().UnixNano()
	previous := c.retryAfter.Load()
	if previous <= now && c.retryAfter.CompareAndSwap(previous, time.Now().Add(30*time.Second).UnixNano()) && c.logger != nil {
		c.logger.Warn("optional Redis cache unavailable; using PostgreSQL", "error", err)
	}
}
