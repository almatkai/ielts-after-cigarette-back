package cache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestDailyLimiterNilClientFailsOpen(t *testing.T) {
	var limiter *DailyLimiter
	res, err := limiter.Allow(context.Background(), "test", "user1", 100)
	if err != nil || !res.Allowed {
		t.Fatalf("expected allowed without error, got res=%v, err=%v", res, err)
	}

	res, err = limiter.Check(context.Background(), "test", "user1", 100)
	if err != nil || !res.Allowed {
		t.Fatalf("expected check allowed without error, got res=%v, err=%v", res, err)
	}
}

func TestDailyLimiterRedis(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	defer client.Close()

	limiter := NewDailyLimiter(client)
	ctx := context.Background()
	user := uuid.NewString()

	// Initial check: has full quota
	chk, err := limiter.Check(ctx, "writing", user, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !chk.Allowed || chk.Current != 0 || chk.Remaining != 2 {
		t.Fatalf("unexpected check result: %+v", chk)
	}

	// First allow
	res1, err := limiter.Allow(ctx, "writing", user, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !res1.Allowed || res1.Current != 1 || res1.Remaining != 1 {
		t.Fatalf("unexpected first allow: %+v", res1)
	}

	// Second allow
	res2, err := limiter.Allow(ctx, "writing", user, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Allowed || res2.Current != 2 || res2.Remaining != 0 {
		t.Fatalf("unexpected second allow: %+v", res2)
	}

	// Third allow -> should be blocked
	res3, err := limiter.Allow(ctx, "writing", user, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res3.Allowed || res3.Current != 2 || res3.Remaining != 0 {
		t.Fatalf("unexpected third allow: %+v", res3)
	}

	// Check should also reflect blocked
	chkAfter, err := limiter.Check(ctx, "writing", user, 2)
	if err != nil {
		t.Fatal(err)
	}
	if chkAfter.Allowed || chkAfter.Remaining != 0 {
		t.Fatalf("unexpected check after limit: %+v", chkAfter)
	}

	// ResetAt should be in the future
	if !res1.ResetAt.After(time.Now()) {
		t.Fatalf("resetAt must be in future: %v", res1.ResetAt)
	}
}
