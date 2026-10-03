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

func TestPreviewRoutesRequireAdmin(t *testing.T) {
	cfg := config.Config{JWTSecret: "preview-test-secret-at-least-32-characters", JWTIssuer: "test", JWTAudience: "test", AccessTokenTTL: time.Hour, RefreshTokenTTL: time.Hour}
	router := New(cfg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, time.Hour, time.Hour)
	for _, path := range []string{"/api/v1/admin/reading/materials/not-a-uuid/preview", "/api/v1/admin/listening/tests/not-a-uuid/preview"} {
		for _, role := range []string{"", auth.RoleStudent, auth.RoleEditor, auth.RoleAdmin} {
			t.Run(path+role, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, path, nil)
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
				} else if role == auth.RoleAdmin {
					// Invalid ID reaches the registered handler, without requiring a DB.
					want = http.StatusBadRequest
				}
				if response.Code != want {
					t.Fatalf("status=%d want=%d body=%s", response.Code, want, response.Body.String())
				}
			})
		}
	}
}
