package health

import (
	"context"
	"net/http"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
)

type Check func(context.Context) error

type Handler struct {
	postgres Check
	redis    Check
	storage  Check
	timeout  time.Duration
}

func NewHandler(postgres, redis Check, optionalStorage ...Check) *Handler {
	handler := &Handler{postgres: postgres, redis: redis, timeout: 2 * time.Second}
	if len(optionalStorage) > 0 {
		handler.storage = optionalStorage[0]
	}
	return handler
}

func (h *Handler) Live(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	details := map[string]string{"postgres": "ok", "redis": "ok"}
	if err := h.postgres(ctx); err != nil {
		details["postgres"] = "unavailable"
	}
	if err := h.redis(ctx); err != nil {
		details["redis"] = "unavailable"
	}
	if h.storage != nil {
		details["object_storage"] = "ok"
		if err := h.storage(ctx); err != nil {
			details["object_storage"] = "unavailable"
		}
	}
	if details["postgres"] != "ok" || details["redis"] != "ok" || details["object_storage"] == "unavailable" {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":       "not_ready",
			"dependencies": details,
		})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"dependencies": details,
	})
}
