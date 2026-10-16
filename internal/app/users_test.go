package app

import (
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUserManagementRoutesRequireAdmin(t *testing.T) {
	cfg := config.Config{JWTSecret: "test-secret-at-least-32-characters-long", JWTIssuer: "test", JWTAudience: "test", AccessTokenTTL: time.Hour}
	router := New(cfg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, time.Hour, time.Hour)
	for _, role := range []string{"", auth.RoleStudent, auth.RoleWriter, auth.RoleEditor} {
		token, _, _ := tokens.NewAccessToken(uuid.New(), role)
		for _, entry := range []struct{ method, path string }{{"GET", "/users"}, {"GET", "/users/" + uuid.NewString()}, {"PUT", "/users/" + uuid.NewString()}, {"DELETE", "/users/" + uuid.NewString()}, {"POST", "/users/" + uuid.NewString() + "/password"}, {"POST", "/users/" + uuid.NewString() + "/revoke-sessions"}, {"GET", "/users/" + uuid.NewString() + "/attempts"}, {"GET", "/users/" + uuid.NewString() + "/attempts/" + uuid.NewString()}} {
			r := httptest.NewRequest(entry.method, "/api/v1/admin"+entry.path, strings.NewReader(`{}`))
			if role != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			expected := 403
			if role == "" {
				expected = 401
			}
			if w.Code != expected {
				t.Errorf("%s %s role %s: %d", entry.method, entry.path, role, w.Code)
			}
		}
	}
	for _, path := range []string{"/api/v1/blog/posts", "/api/v1/blog/posts/hidden-post"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Errorf("public blog exposed: %s %d", path, w.Code)
		}
	}
}
