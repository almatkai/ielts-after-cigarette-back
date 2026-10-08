package cache

import (
	"context"
	"errors"
	"fmt"
	"time"
	_ "time/tzdata"

	"github.com/redis/go-redis/v9"
)

const TimeZone = "Asia/Almaty"

var location = mustLoadLocation(TimeZone)

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

type DailyLimitResult struct {
	Allowed   bool
	Current   int64
	Limit     int64
	Remaining int64
	ResetAt   time.Time
}

type DailyLimiter struct {
	client      *redis.Client
	allowScript *redis.Script
}

func NewDailyLimiter(client *redis.Client) *DailyLimiter {
	return &DailyLimiter{
		client: client,
		allowScript: redis.NewScript(`
			local current = tonumber(redis.call("GET", KEYS[1]) or "0")
			local limit = tonumber(ARGV[1])
			if current >= limit then
				return {0, current}
			end
			local count = redis.call("INCR", KEYS[1])
			if count == 1 then
				redis.call("PEXPIRE", KEYS[1], ARGV[2])
			end
			return {1, count}
		`),
	}
}

func (d *DailyLimiter) Allow(ctx context.Context, scope, identifier string, limit int64) (DailyLimitResult, error) {
	now := time.Now().In(location)
	dateStr := now.Format("2006-01-02")
	resetAt := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, location)

	if d == nil || d.client == nil || limit <= 0 {
		return DailyLimitResult{
			Allowed:   true,
			Current:   0,
			Limit:     limit,
			Remaining: limit,
			ResetAt:   resetAt,
		}, nil
	}

	key := fmt.Sprintf("daily-limit:%s:%s:%s", scope, identifier, dateStr)
	ttl := 48 * time.Hour

	val, err := d.allowScript.Run(ctx, d.client, []string{key}, limit, ttl.Milliseconds()).Slice()
	if err != nil {
		return DailyLimitResult{}, fmt.Errorf("apply daily limit: %w", err)
	}
	allowedInt, _ := val[0].(int64)
	count, _ := val[1].(int64)
	allowed := allowedInt == 1
	remaining := limit - count
	if remaining < 0 {
		remaining = 0
	}
	return DailyLimitResult{
		Allowed:   allowed,
		Current:   count,
		Limit:     limit,
		Remaining: remaining,
		ResetAt:   resetAt,
	}, nil
}

func (d *DailyLimiter) Check(ctx context.Context, scope, identifier string, limit int64) (DailyLimitResult, error) {
	now := time.Now().In(location)
	dateStr := now.Format("2006-01-02")
	resetAt := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, location)

	if d == nil || d.client == nil || limit <= 0 {
		return DailyLimitResult{
			Allowed:   true,
			Current:   0,
			Limit:     limit,
			Remaining: limit,
			ResetAt:   resetAt,
		}, nil
	}

	key := fmt.Sprintf("daily-limit:%s:%s:%s", scope, identifier, dateStr)
	val, err := d.client.Get(ctx, key).Int64()
	if err != nil && !errors.Is(err, redis.Nil) {
		return DailyLimitResult{}, fmt.Errorf("check daily limit: %w", err)
	}
	remaining := limit - val
	if remaining < 0 {
		remaining = 0
	}
	return DailyLimitResult{
		Allowed:   val < limit,
		Current:   val,
		Limit:     limit,
		Remaining: remaining,
		ResetAt:   resetAt,
	}, nil
}
