package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/google/uuid"
)

func TestAIProviderManagementIsAdminOnly(t *testing.T) {
	cfg := config.Config{JWTSecret: "test-secret-at-least-32-characters-long", JWTIssuer: "test", JWTAudience: "test", AccessTokenTTL: time.Hour}
	router := New(cfg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, time.Hour, time.Hour)
	for _, path := range []struct{ method, url string }{{"GET", "/api/v1/admin/ai-providers/routing"}, {"PUT", "/api/v1/admin/ai-providers/routing"}, {"GET", "/api/v1/admin/ai-providers/stats"}, {"POST", "/api/v1/admin/ai-providers/order"}, {"GET", "/api/v1/admin/ai-providers"}, {"POST", "/api/v1/admin/ai-providers"}, {"POST", "/api/v1/admin/ai-providers/test"}, {"PUT", "/api/v1/admin/ai-providers/" + uuid.New().String()}, {"DELETE", "/api/v1/admin/ai-providers/" + uuid.New().String()}, {"POST", "/api/v1/admin/ai-providers/" + uuid.New().String() + "/test"}} {
		for _, role := range []string{"", auth.RoleStudent, auth.RoleEditor} {
			req := httptest.NewRequest(path.method, path.url, nil)
			if role != "" {
				token, _, err := tokens.NewAccessToken(uuid.New(), role)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer "+token)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			want := http.StatusForbidden
			if role == "" {
				want = http.StatusUnauthorized
			}
			if response.Code != want {
				t.Fatalf("%s %s role=%q status=%d expected=%d", path.method, path.url, role, response.Code, want)
			}
		}
	}
	token, _, _ := tokens.NewAccessToken(uuid.New(), auth.RoleAdmin)
	request := httptest.NewRequest("GET", "/api/v1/admin/ai-providers", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("admin cannot list: %d %s", response.Code, response.Body.String())
	}
}
