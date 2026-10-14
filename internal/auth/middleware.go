package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/google/uuid"
)

type authContextKey string

const (
	userIDKey      authContextKey = "user-id"
	roleKey        authContextKey = "role"
	permissionsKey authContextKey = "permissions"
)

func UserID(ctx context.Context) (uuid.UUID, bool) {
	value, ok := ctx.Value(userIDKey).(uuid.UUID)
	return value, ok
}

func Role(ctx context.Context) string {
	value, _ := ctx.Value(roleKey).(string)
	return value
}

func HasPermission(ctx context.Context, permission string) bool {
	if Role(ctx) == RoleAdmin {
		return true
	}
	permissions, _ := ctx.Value(permissionsKey).([]string)
	for _, value := range permissions {
		if value == permission {
			return true
		}
	}
	return false
}

func WithUser(ctx context.Context, userID uuid.UUID, role string) context.Context {
	ctx = context.WithValue(httpx.WithActor(ctx, userID.String()), userIDKey, userID)
	return context.WithValue(ctx, roleKey, role)
}

func withUserPermissions(ctx context.Context, userID uuid.UUID, role string, permissions []string) context.Context {
	ctx = WithUser(ctx, userID, role)
	return context.WithValue(ctx, permissionsKey, permissions)
}

// RequireAnyPermission grants access to administrators and users assigned one
// of the explicit capabilities.
func RequireAnyPermission(permissions ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(permissions))
	for _, permission := range permissions {
		allowed[permission] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if Role(r.Context()) != RoleAdmin {
				assigned, _ := r.Context().Value(permissionsKey).([]string)
				ok := false
				for _, permission := range assigned {
					if _, ok = allowed[permission]; ok {
						break
					}
				}
				if !ok {
					httpx.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to access this resource", nil)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAdminWorkspace keeps the shared admin shell available to staff while
// each content area remains protected by its own capability middleware.
func RequireAdminWorkspace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := Role(r.Context())
		if role != RoleAdmin && role != RoleEditor && !HasPermission(r.Context(), PermissionBlogModerator) && !HasPermission(r.Context(), PermissionContentEditor) {
			httpx.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to access this resource", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func Authenticate(tokens *TokenManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := strings.TrimSpace(r.Header.Get("Authorization"))
			parts := strings.Fields(header)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid Bearer token is required", nil)
				return
			}
			claims, err := tokens.ParseAccessToken(parts[1])
			if err != nil {
				httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid Bearer token is required", nil)
				return
			}
			userID, _ := uuid.Parse(claims.Subject)
			next.ServeHTTP(w, r.WithContext(withUserPermissions(r.Context(), userID, claims.Role, claims.Permissions)))
		})
	}
}

func AuthenticateOptional(tokens *TokenManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := strings.TrimSpace(r.Header.Get("Authorization"))
			parts := strings.Fields(header)
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				if claims, err := tokens.ParseAccessToken(parts[1]); err == nil {
					userID, _ := uuid.Parse(claims.Subject)
					r = r.WithContext(withUserPermissions(r.Context(), userID, claims.Role, claims.Permissions))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func RequireAnyRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		role = NormalizeRole(role)
		if ValidRole(role) {
			allowed[role] = struct{}{}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := allowed[NormalizeRole(Role(r.Context()))]; !ok {
				httpx.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to access this resource", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
