package billing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cache"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/redis/go-redis/v9"
)

func TestAssistantRedisQuotaSharedByChatAndStreamAndPaidTransition(t *testing.T) {
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
	pool := testdb.Open(t)
	ctx := context.Background()
	user := testdb.User(t, pool)
	s := NewService(NewPostgresRepository(pool), true, nil, "", cache.NewDailyLimiter(client), nil)
	h := testHandler(s)
	var legacyCalls, success, denied atomic.Int32
	legacy := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { legacyCalls.Add(1); next.ServeHTTP(w, r) })
	}
	handler := h.AssistantLimit(legacy)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := "/api/v1/assistant/chat"
			if i%2 == 0 {
				path += "/stream"
			}
			r := httptest.NewRequest("POST", path, nil).WithContext(auth.WithUser(ctx, user, auth.RoleStudent))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			switch w.Code {
			case 204:
				success.Add(1)
			case 402:
				denied.Add(1)
			default:
				t.Errorf("assistant response: %d %s", w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()
	if success.Load() != 5 || denied.Load() != 7 || legacyCalls.Load() != 0 {
		t.Fatalf("daily quota success=%d denied=%d legacy=%d", success.Load(), denied.Load(), legacyCalls.Load())
	}
	a, err := s.Access(ctx, user)
	if err != nil || a.Assistant.Limit != 5 || a.Assistant.Remaining != 0 {
		t.Fatalf("assistant access: %+v %v", a, err)
	}
	if _, err = s.Grant(ctx, user, user, GrantInput{PlanCode: "self", Source: "ADMIN"}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/assistant/chat/stream", nil).WithContext(auth.WithUser(ctx, user, auth.RoleStudent))
	handler.ServeHTTP(w, r)
	if w.Code != 204 || legacyCalls.Load() != 1 {
		t.Fatalf("paid transition: %d calls=%d", w.Code, legacyCalls.Load())
	}
	// Staff bypass paywall and the paid limiter when paywall is enabled.
	execSQL(t, pool, `UPDATE users SET role='EDITOR' WHERE id=$1`, user)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 204 || legacyCalls.Load() != 1 {
		t.Fatalf("staff: %d %d", w.Code, legacyCalls.Load())
	}
}
