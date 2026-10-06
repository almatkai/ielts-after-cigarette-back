package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/ailimits"
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cache"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestDailyAssistantLimitMiddleware(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	redisClient := redis.NewClient(opts)
	defer redisClient.Close()

	limiter := cache.NewDailyLimiter(redisClient)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	limitsService := ailimits.NewService(nil, limiter, redisClient, ailimits.Limits{
		AssistantLimit:      2,
		GuestAssistantLimit: 1,
	})

	handler := dailyAssistantLimit(limitsService, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Guest test: limit is 1
	guestIP := "198.51.100." + uuid.NewString()[:8]
	req1 := httptest.NewRequest("POST", "/assistant/chat", nil)
	req1.RemoteAddr = guestIP + ":1234"
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first guest request failed: %d", w1.Code)
	}

	// Second guest request should be blocked (limit 1)
	req2 := httptest.NewRequest("POST", "/assistant/chat", nil)
	req2.RemoteAddr = guestIP + ":1234"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for second guest request, got: %d", w2.Code)
	}

	// User test: limit is 2
	userID := uuid.New()
	userCtx := auth.WithUser(context.Background(), userID, "STUDENT")

	for i := 1; i <= 2; i++ {
		req := httptest.NewRequest("POST", "/assistant/chat", nil).WithContext(userCtx)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("user request %d failed: %d", i, w.Code)
		}
	}

	// Third user request should be blocked (limit 2)
	reqUserBlocked := httptest.NewRequest("POST", "/assistant/chat", nil).WithContext(userCtx)
	wUserBlocked := httptest.NewRecorder()
	handler.ServeHTTP(wUserBlocked, reqUserBlocked)
	if wUserBlocked.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for third user request, got: %d", wUserBlocked.Code)
	}

	// Admin test: should bypass limit
	adminCtx := auth.WithUser(context.Background(), userID, "ADMIN")
	reqAdmin := httptest.NewRequest("POST", "/assistant/chat", nil).WithContext(adminCtx)
	wAdmin := httptest.NewRecorder()
	handler.ServeHTTP(wAdmin, reqAdmin)
	if wAdmin.Code != http.StatusOK {
		t.Fatalf("expected admin bypass, got: %d", wAdmin.Code)
	}
}
