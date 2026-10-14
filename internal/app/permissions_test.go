package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/google/uuid"
)

func TestStaffRoutesEnforceIndependentCapabilities(t *testing.T) {
	cfg := config.Config{JWTSecret: "test-secret-at-least-32-characters-long", JWTIssuer: "test", JWTAudience: "test", AccessTokenTTL: time.Hour}
	router := New(cfg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, time.Hour, time.Hour)
	for _, tc := range []struct {
		name, role  string
		permissions []string
	}{
		{"blog editor", auth.RoleEditor, []string{auth.PermissionBlogModerator}},
		{"test editor", auth.RoleEditor, []string{auth.PermissionContentEditor}},
		{"combined editor", auth.RoleEditor, []string{auth.PermissionBlogModerator, auth.PermissionContentEditor}},
		{"writer with test access", auth.RoleWriter, []string{auth.PermissionContentEditor}},
		{"editor without access", auth.RoleEditor, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token, _, err := tokens.NewAccessToken(uuid.New(), tc.role, tc.permissions)
			if err != nil {
				t.Fatal(err)
			}
			request := func(method, path string) int {
				r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(`{}`))
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				return w.Code
			}
			if status := request("GET", "/admin/access"); status != http.StatusOK {
				t.Errorf("workspace: %d", status)
			}
			for _, entry := range []struct{ method, path string }{
				{"GET", "/admin/users"}, {"PUT", "/admin/users/invalid"}, {"GET", "/admin/analytics/overview"},
				{"GET", "/admin/ai-providers"}, {"PUT", "/admin/ai-limits"}, {"GET", "/admin/waitlist"},
				{"GET", "/admin/super-admins"}, {"POST", "/admin/super-admins"},
			} {
				if status := request(entry.method, entry.path); status != http.StatusForbidden {
					t.Errorf("system %s %s: %d", entry.method, entry.path, status)
				}
			}
			has := func(permission string) bool {
				for _, p := range tc.permissions {
					if p == permission {
						return true
					}
				}
				return false
			}
			for _, section := range []struct {
				permission string
				paths      []string
			}{
				{auth.PermissionContentEditor, []string{"/admin/reading/materials/invalid", "/admin/listening/tests/invalid", "/admin/writing/materials/invalid", "/admin/speaking/materials/invalid", "/admin/full-mocks/invalid"}},
				{auth.PermissionBlogModerator, []string{"/admin/blog/posts/invalid", "/admin/writers/applications/invalid"}},
			} {
				for _, path := range section.paths {
					status := request("GET", path)
					if has(section.permission) {
						if status == 401 || status == 403 || status >= 500 {
							t.Errorf("allowed %s: %d", path, status)
						}
					} else if status != http.StatusForbidden {
						t.Errorf("denied %s: %d", path, status)
					}
				}
			}
			for _, path := range []string{"/admin/reading/materials/invalid/publish", "/admin/listening/tests/invalid/publish", "/admin/writing/materials/invalid/publish", "/admin/speaking/materials/invalid/publish"} {
				status := request("POST", path)
				if has(auth.PermissionContentEditor) {
					if status == 401 || status == 403 || status >= 500 {
						t.Errorf("editor cannot publish %s: %d", path, status)
					}
				} else if status != http.StatusForbidden {
					t.Errorf("unexpected publication access %s: %d", path, status)
				}
			}
			if !has(auth.PermissionBlogModerator) && tc.role != auth.RoleWriter {
				if status := request("GET", "/blog/posts"); status != 404 {
					t.Errorf("hidden public blog leaked: %d", status)
				}
			}
		})
	}
}
