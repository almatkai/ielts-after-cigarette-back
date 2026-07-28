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
	userIDKey authContextKey = "user-id"
	roleKey   authContextKey = "role"
)

func UserID(ctx context.Context) (uuid.UUID, bool) {
	value, ok := ctx.Value(userIDKey).(uuid.UUID)
	return value, ok
}

func Role(ctx context.Context) string {
	value, _ := ctx.Value(roleKey).(string)
	return value
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
			ctx := context.WithValue(r.Context(), userIDKey, userID)
			ctx = context.WithValue(ctx, roleKey, claims.Role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
