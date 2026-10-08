package guest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/fullmock"
	"github.com/google/uuid"
)

func TestGuestRetakeRequiresLiveCookieAndBudgets(t *testing.T) {
	for _, tc := range []struct {
		name                                            string
		status                                          int
		missing, expired, claimed, blocked, unavailable bool
		err                                             error
	}{
		{name: "allowed", status: 200},
		{name: "missing", missing: true, status: 401},
		{name: "expired", expired: true, status: 403},
		{name: "claimed", claimed: true, status: 403},
		{name: "quota", blocked: true, status: 429},
		{name: "redis", unavailable: true, status: 503},
		{name: "foreign", err: fullmock.ErrSessionNotFound, status: 404},
		{name: "running", err: fullmock.ErrRetakeNotReady, status: 409},
		{name: "racing claim", err: fullmock.ErrGuestUnavailable, status: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testHandler()
			item := Trial{UserID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour)}
			if tc.expired {
				item.ExpiresAt = time.Now().Add(-time.Hour)
			}
			if tc.claimed {
				id := uuid.New()
				item.ClaimedBy = &id
			}
			if !tc.missing {
				h.repo.(*fakeRepo).item = item
			}
			m := h.mocks.(*fakeMock)
			m.retakeErr = tc.err
			budgetCalls := 0
			h.allow = func(_ context.Context, key string, limit int64, ttl time.Duration) (bool, error) {
				budgetCalls++
				if strings.HasPrefix(key, "guest:requests:") {
					if limit != 180 || ttl != time.Minute {
						t.Errorf("unexpected request budget: %s %d %v", key, limit, ttl)
					}
				} else if !strings.HasPrefix(key, "guest:starts:") || limit <= 0 || ttl != 24*time.Hour {
					t.Errorf("unexpected start budget: %s %d %v", key, limit, ttl)
				}
				isStart := strings.HasPrefix(key, "guest:starts:")
				if tc.unavailable && isStart {
					return false, errors.New("redis down")
				}
				return !(tc.blocked && isStart), nil
			}
			r := cookieRequest(http.MethodPost, "/api/v1/guest/start")
			r.Body = io.NopCloser(strings.NewReader(`{"examType":"academic","acceptedTerms":true,"retakeSessionId":"` + uuid.New().String() + `"}`))
			w := httptest.NewRecorder()
			h.Start(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if h.repo.(*fakeRepo).creates != 0 || len(w.Result().Cookies()) != 0 || m.starts != 0 {
				t.Fatal("retake changed identity or started an unrelated mock")
			}
			if tc.status == 200 && (budgetCalls != 3 || m.retakes != 1) {
				t.Fatalf("budgets=%d retakes=%d", budgetCalls, m.retakes)
			}
			if tc.status != 200 && m.retakes != 0 {
				t.Fatal("rejected retake created a session")
			}
		})
	}
}
