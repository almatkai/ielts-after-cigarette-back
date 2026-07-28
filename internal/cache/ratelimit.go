package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RateLimiter struct {
	client *redis.Client
	script *redis.Script
}

func NewRateLimiter(client *redis.Client) *RateLimiter {
	return &RateLimiter{
		client: client,
		script: redis.NewScript(`
			local count = redis.call("INCR", KEYS[1])
			if count == 1 then
				redis.call("PEXPIRE", KEYS[1], ARGV[1])
			end
			return count
		`),
	}
}

func (l *RateLimiter) Allow(ctx context.Context, key string, limit int64, window time.Duration) (bool, error) {
	count, err := l.script.Run(ctx, l.client, []string{key}, window.Milliseconds()).Int64()
	if err != nil {
		return false, fmt.Errorf("apply rate limit: %w", err)
	}
	return count <= limit, nil
}
