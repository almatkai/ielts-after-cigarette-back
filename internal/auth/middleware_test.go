package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRequireAnyRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		role       string
		wantStatus int
	}{
		{name: "admin is allowed", role: RoleAdmin, wantStatus: http.StatusNoContent},
		{name: "editor is allowed", role: RoleEditor, wantStatus: http.StatusNoContent},
		{name: "student is forbidden", role: RoleStudent, wantStatus: http.StatusForbidden},
		{name: "missing role is forbidden", wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
			handler := RequireAnyRole(RoleEditor, RoleAdmin)(next)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/access", nil)
			if tt.role != "" {
				request = request.WithContext(context.WithValue(request.Context(), roleKey, tt.role))
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusForbidden && response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("content type = %q", response.Header().Get("Content-Type"))
			}
		})
	}
}

type testAPIKeyValidator struct {
	validKey    string
	userID      uuid.UUID
	role        string
	permissions []string
}

func (v *testAPIKeyValidator) ValidateAPIKey(ctx context.Context, apiKey string) (uuid.UUID, string, []string, error) {
	if apiKey == v.validKey {
		return v.userID, v.role, v.permissions, nil
	}
	return uuid.Nil, "", nil, ErrInvalidCredentials
}

func TestAuthenticate_WithMCPAPIKey(t *testing.T) {
	adminID := uuid.New()
	validator := &testAPIKeyValidator{
		validKey:    "iac_mcp_validkey123",
		userID:      adminID,
		role:        RoleAdmin,
		permissions: []string{PermissionContentEditor},
	}
	tm := NewTokenManager("test-secret-at-least-32-chars-long!", "issuer", "audience", 15*time.Minute, 24*time.Hour)
	tm.WithAPIKeyValidator(validator)

	called := false
	var gotUserID uuid.UUID
	var gotRole string
	var gotHasPerm bool

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		gotUserID, _ = UserID(r.Context())
		gotRole = Role(r.Context())
		gotHasPerm = HasPermission(r.Context(), PermissionContentEditor)
		w.WriteHeader(http.StatusOK)
	})

	handler := Authenticate(tm)(next)

	// 1. Valid key in Authorization: Bearer
	req := httptest.NewRequest(http.MethodGet, "/admin/test", nil)
	req.Header.Set("Authorization", "Bearer iac_mcp_validkey123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !called || gotUserID != adminID || gotRole != RoleAdmin || !gotHasPerm {
		t.Fatalf("context not properly populated from API key")
	}

	// 2. Valid key in X-API-Key
	called = false
	req = httptest.NewRequest(http.MethodGet, "/admin/test", nil)
	req.Header.Set("X-API-Key", "iac_mcp_validkey123")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !called || gotUserID != adminID {
		t.Fatalf("expected user %s, got %s", adminID, gotUserID)
	}

	// 3. Invalid key
	req = httptest.NewRequest(http.MethodGet, "/admin/test", nil)
	req.Header.Set("Authorization", "Bearer iac_mcp_wrongkey")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for invalid key, got %d", rec.Code)
	}
}
