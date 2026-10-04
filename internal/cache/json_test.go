package cache

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestJSONRoundTripAndExpiry(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	c, err := NewJSON(url, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	key := "iac:test:" + uuid.NewString()
	defer c.client.Del(ctx, key)
	c.PutMany(ctx, map[string]any{key: map[string]string{"value": "hello"}}, time.Minute)
	if got := c.GetMany(ctx, []string{key, key + ":missing"}); got[key] != `{"value":"hello"}` || len(got) != 1 {
		t.Fatalf("cache: %v", got)
	}
	if ttl := c.client.TTL(ctx, key).Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("TTL: %v", ttl)
	}
	if err := c.client.PExpire(ctx, key, time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if len(c.GetMany(ctx, []string{key})) != 0 {
		t.Fatal("expired key returned")
	}
}

func TestJSONFailOpenAndCircuitBreaker(t *testing.T) {
	// Black-hole a connection to simulate a Redis server that accepts TCP but
	// never answers; an optional cache may not inherit the queue's long timeout.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	c, err := NewJSON("redis://"+listener.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	if len(c.GetMany(context.Background(), []string{"key"})) != 0 {
		t.Fatal("failure was a hit")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cache stalled fallback")
	}
	if c.retryAfter.Load() <= time.Now().UnixNano() {
		t.Fatal("circuit was not opened")
	}
	start = time.Now()
	c.PutMany(context.Background(), map[string]any{"key": "value"}, time.Hour)
	if len(c.GetMany(context.Background(), []string{"key"})) != 0 || time.Since(start) > 50*time.Millisecond {
		t.Fatal("open circuit still accessed Redis")
	}
	var absent *JSON
	absent.PutMany(context.Background(), nil, time.Hour)
	if len(absent.GetMany(context.Background(), []string{"key"})) != 0 {
		t.Fatal("disabled cache returned data")
	}
}
