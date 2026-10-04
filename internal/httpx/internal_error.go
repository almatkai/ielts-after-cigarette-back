package httpx

import (
	"context"
	"log/slog"
	"net/http"
)

const actorKey contextKey = "actor-id"

// WithActor stores an opaque internal ID, never email, tokens or request bodies.
func WithActor(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, actorKey, userID)
}

// InternalError is the single reporting seam for unexpected handler failures.
// A future tracker can be integrated here without coupling handlers to its SDK.
// The cause stays in structured logs; the client receives only requestId.
func InternalError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	actor, _ := r.Context().Value(actorKey).(string)
	logger.ErrorContext(r.Context(), "internal request error",
		"request_id", RequestID(r.Context()), "user_id", actor,
		"method", r.Method, "path", r.URL.Path, "error", err)
	reportError(r.Context(), ErrorEvent{
		Error: err, RequestID: RequestID(r.Context()), ActorID: actor,
		Method: r.Method, Path: r.URL.Path, Origin: "http",
	})
	WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred", nil)
}
