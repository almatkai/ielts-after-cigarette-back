package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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
