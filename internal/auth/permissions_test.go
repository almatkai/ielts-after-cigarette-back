package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestIndependentAndCombinedPermissions(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		role                     string
		permissions              []string
		blog, content, workspace bool
	}{
		{"admin", RoleAdmin, nil, true, true, true},
		{"legacy editor without permissions", RoleEditor, nil, false, false, true},
		{"blog editor", RoleEditor, []string{PermissionBlogModerator}, true, false, true},
		{"test editor", RoleEditor, []string{PermissionContentEditor}, false, true, true},
		{"combined editor", RoleEditor, []string{PermissionBlogModerator, PermissionContentEditor}, true, true, true},
		{"writer with test access", RoleWriter, []string{PermissionContentEditor}, false, true, true},
		{"student with blog access", RoleStudent, []string{PermissionBlogModerator}, true, false, true},
		{"writer", RoleWriter, nil, false, false, false},
		{"student", RoleStudent, nil, false, false, false},
		{"unknown capability", RoleStudent, []string{"UNKNOWN"}, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := withUserPermissions(context.Background(), uuid.New(), tc.role, tc.permissions)
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
			for _, check := range []struct {
				name    string
				handler http.Handler
				allowed bool
			}{
				{"blog", RequireAnyPermission(PermissionBlogModerator)(next), tc.blog},
				{"content", RequireAnyPermission(PermissionContentEditor)(next), tc.content},
				{"workspace", RequireAdminWorkspace(next), tc.workspace},
				{"users and system", RequireAnyRole(RoleAdmin)(next), tc.role == RoleAdmin},
			} {
				w := httptest.NewRecorder()
				check.handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil).WithContext(ctx))
				want := http.StatusForbidden
				if check.allowed {
					want = http.StatusNoContent
				}
				if w.Code != want {
					t.Errorf("%s: status %d, want %d", check.name, w.Code, want)
				}
			}
			if HasPermission(ctx, PermissionBlogModerator) != tc.blog || HasPermission(ctx, PermissionContentEditor) != tc.content {
				t.Fatal("permission helper disagrees with middleware")
			}
		})
	}
}

func TestPermissionClaimsAndImmediateRevocation(t *testing.T) {
	id := uuid.New()
	state := AccountState{Role: RoleEditor, Permissions: []string{PermissionBlogModerator, PermissionContentEditor}}
	tokens := NewTokenManager("test-secret", "test", "test", time.Hour, time.Hour).WithAccountState(func(uuid.UUID) (AccountState, error) { return state, nil })
	old, _, err := tokens.NewAccessToken(id, RoleEditor, state.Permissions)
	if err != nil {
		t.Fatal(err)
	}
	state.Version++
	state.Permissions = []string{PermissionContentEditor}
	if _, err := tokens.ParseAccessToken(old); err == nil {
		t.Fatal("removed permission still authenticates")
	}
	// Simulate a user view fetched just before access was narrowed. Issuance
	// must use the current DB capabilities with the current token version.
	fresh, _, err := tokens.NewAccessToken(id, RoleEditor, []string{PermissionBlogModerator, PermissionContentEditor})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := tokens.ParseAccessToken(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(claims.Permissions, state.Permissions) {
		t.Fatalf("stale permissions signed: %v", claims.Permissions)
	}
	for _, middleware := range []func(http.Handler) http.Handler{Authenticate(tokens), AuthenticateOptional(tokens)} {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !HasPermission(r.Context(), PermissionContentEditor) || HasPermission(r.Context(), PermissionBlogModerator) {
				t.Fatal("claims not propagated correctly")
			}
			w.WriteHeader(http.StatusNoContent)
		})
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", "Bearer "+fresh)
		w := httptest.NewRecorder()
		middleware(next).ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("authentication: %d", w.Code)
		}
	}
}
